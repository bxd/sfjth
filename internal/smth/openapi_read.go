package smth

// 本文件把「水木社区开放平台」返回的实体映射成 term 界面需要的结构。
// 字段严格对照官方文档：
//
//   https://wiki.open.newsmth.net/entity/section   id name desc code
//   https://wiki.open.newsmth.net/entity/board     bid id manager name section current_users total ...
//   https://wiki.open.newsmth.net/entity/thread    id board_id subject time count author_id last_user_id last_time article
//   https://wiki.open.newsmth.net/entity/article   id board_id thread_id author_id time subject body
//   https://wiki.open.newsmth.net/method/board/load_section      -> sections[]
//   https://wiki.open.newsmth.net/method/board/read_section      -> boards[]
//   https://wiki.open.newsmth.net/method/board/load_boards       -> list[]
//   https://wiki.open.newsmth.net/method/profile/load_favorites  -> favorites[]
//   https://wiki.open.newsmth.net/method/timeline/boards_threads -> threads[] / boards[] / page
//
// 官方平台是「时间线」模型：没有按版面列主题、按主题读回复的读取方法。
// 因此 timeline/boards_threads（article=1&body=1）返回的原文正文会被缓存下来，
// 供「阅读文章」界面使用；需要完整同主题串或任意版面浏览时请切到 web 数据源。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// apiEntry 缓存时间线里取到的主题原文。
type apiEntry struct {
	subject string
	posts   []Post
}

// apiRead 是官方 API 的读取层，带轻量缓存（时间线原文 + 版面信息 + 翻页游标）。
type apiRead struct {
	o       *OpenAPI
	mu      sync.Mutex
	entries map[string]apiEntry // "board/threadID" -> 原文
	boards  map[string]Board    // 时间线 boards[] 里出现过的版面
	from    map[string]int64    // "board:page" -> 下一页的 from（timeline_id）
}

func newAPIRead(o *OpenAPI) *apiRead {
	return &apiRead{
		o:       o,
		entries: map[string]apiEntry{},
		boards:  map[string]Board{},
		from:    map[string]int64{},
	}
}

// Board 返回时间线里登记过的版面信息。
func (a *apiRead) Board(id string) (Board, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.boards[id]
	return b, ok
}

// Sections 按 board/load_section 读取传统分区。
func (a *apiRead) Sections() ([]Section, error) {
	out, err := a.o.Call(MethodBoardLoadSection, nil)
	if err != nil {
		return nil, err
	}
	raw := objList(out["sections"])
	if len(raw) == 0 {
		return nil, errors.New("开放平台未返回 sections（检查 APP 是否具备 read_board 权限）")
	}
	list := make([]Section, 0, len(raw))
	for _, m := range raw {
		list = append(list, Section{
			ID:    fieldStr(m, "id"),
			Name:  fieldStr(m, "name"),
			Desc:  fieldStr(m, "desc"),
			Total: int(fieldInt(m, "total")),
		})
	}
	return list, nil
}

// Boards 按 board/read_section（指定分区）或 board/load_boards（全部）读取版面。
func (a *apiRead) Boards(section string) ([]Board, error) {
	var (
		out map[string]any
		err error
	)
	if section != "" && section != "all" && section != "0" {
		out, err = a.o.Call(MethodBoardReadSection, map[string]string{
			"section_id": section, "group_id": "0",
		})
	} else {
		out, err = a.o.Call(MethodBoardLoadBoards, map[string]string{"group_id": "0"})
	}
	if err != nil {
		return nil, err
	}
	raw := objList(out["boards"])
	if len(raw) == 0 {
		raw = objList(out["list"])
	}
	if len(raw) == 0 {
		return nil, errors.New("开放平台未返回 boards / list（检查 APP 是否具备 read_board 权限）")
	}
	list := make([]Board, 0, len(raw))
	for _, m := range raw {
		list = append(list, a.toBoard(m))
	}
	return list, nil
}

// Favorites 按 profile/load_favorites 读取个人定制区（group_id=0 为根目录）。
func (a *apiRead) Favorites() ([]Board, error) {
	out, err := a.o.Call(MethodLoadFavorites, map[string]string{"group_id": "0"})
	if err != nil {
		return nil, err
	}
	raw := objList(out["favorites"])
	list := make([]Board, 0, len(raw))
	for _, m := range raw {
		list = append(list, a.toBoard(m))
	}
	return list, nil
}

// Threads 按 timeline/boards_threads 读取驻版主题时间线。
// page 用官方 /pager 的 from 游标实现：from 取上一页最后一条的 timeline_id，size<=50。
func (a *apiRead) Threads(board string, page int) ([]Thread, int, error) {
	if page < 1 {
		page = 1
	}
	const size = 40
	from := a.fromFor(board, page)
	out, err := a.o.Call(MethodBoardsThreads, map[string]string{
		"article": "1",
		"body":    "1",
		"from":    strconv.FormatInt(from, 10),
		"size":    strconv.Itoa(size),
	})
	if err != nil {
		return nil, 0, err
	}
	for _, m := range objList(out["boards"]) {
		b := a.toBoard(m)
		if b.ID != "" {
			a.mu.Lock()
			a.boards[b.ID] = b
			a.mu.Unlock()
		}
	}

	raw := objList(out["threads"])
	items := make([]Thread, 0, len(raw))
	var lastTimeline int64
	for _, m := range raw {
		if tl := fieldInt(m, "timeline_id"); tl > lastTimeline {
			lastTimeline = tl
		}
		bid := fieldStr(m, "board_id")
		if board != "" && !strings.EqualFold(bid, board) {
			continue
		}
		id := fieldInt(m, "id")
		subject := fieldStr(m, "subject")
		author := fieldStr(m, "author_id")
		if author == "" {
			author = fieldStr(fieldMap(m, "author"), "id")
		}
		t := Thread{
			ID:       id,
			BoardID:  bid,
			Subject:  subject,
			Author:   author,
			Date:     apiDate(fieldInt(m, "time")),
			Time:     fieldInt(m, "time"),
			Count:    int(fieldInt(m, "count")),
			LastUser: fieldStr(m, "last_user_id"),
			Pages:    1,
		}
		if art := fieldMap(m, "article"); art != nil {
			p := a.toPost(m, art)
			if p.Body == "" {
				p.Body = "（本主题原文正文未返回，可切到 web 数据源阅读完整同主题串）"
			}
			a.remember(bid, id, subject, []Post{p})
		} else {
			a.remember(bid, id, subject, nil)
		}
		items = append(items, t)
	}
	pages := page
	if len(raw) >= size && lastTimeline > 0 {
		a.setFrom(board, page+1, lastTimeline)
		pages = page + 1
	}
	return items, pages, nil
}

// Posts 返回时间线里缓存的主题原文。
func (a *apiRead) Posts(board string, id int64) ([]Post, string, error) {
	key := board + "/" + strconv.FormatInt(id, 10)
	a.mu.Lock()
	e, ok := a.entries[key]
	a.mu.Unlock()
	if !ok || len(e.posts) == 0 {
		return nil, "", errors.New("该主题不在当前开放平台时间线缓存中：官方 wiki 未提供按版面读取同主题文章的方法，请切到 web 数据源（Cookie）浏览版面")
	}
	return e.posts, e.subject, nil
}

// ------------------------------------------------------------------ 内部工具

func (a *apiRead) remember(board string, id int64, subject string, posts []Post) {
	a.mu.Lock()
	a.entries[board+"/"+strconv.FormatInt(id, 10)] = apiEntry{subject: subject, posts: posts}
	a.mu.Unlock()
}

func (a *apiRead) fromFor(board string, page int) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.from[board+":"+strconv.Itoa(page)]
}

func (a *apiRead) setFrom(board string, page int, id int64) {
	a.mu.Lock()
	a.from[board+":"+strconv.Itoa(page)] = id
	a.mu.Unlock()
}

func (a *apiRead) toBoard(m map[string]any) Board {
	// board 对象的 id 是英文版名（字符串）；group/目录对象的 id 是数字。
	_, stringID := m["id"].(string)
	kind := "board"
	if m["id"] != nil && !stringID {
		kind = "dir"
	}
	b := Board{
		ID:       fieldStr(m, "id"),
		Name:     fieldStr(m, "name"),
		Section:  fieldStr(m, "section"),
		Manager:  fieldStr(m, "manager"),
		Desc:     fieldStr(m, "desc"),
		Users:    int(fieldInt(m, "current_users")),
		Articles: int(fieldInt(m, "total")),
		Kind:     kind,
	}
	if b.Name == "" {
		b.Name = b.ID
	}
	if b.Desc == "" {
		b.Desc = "版面 " + b.ID
	}
	return b
}

func (a *apiRead) toPost(thread, art map[string]any) Post {
	sec := fieldInt(art, "time")
	bid := fieldStr(art, "board_id")
	if bid == "" {
		bid = fieldStr(thread, "board_id")
	}
	subject := fieldStr(art, "subject")
	if subject == "" {
		subject = fieldStr(thread, "subject")
	}
	author := fieldStr(art, "author_id")
	if author == "" {
		author = fieldStr(fieldMap(art, "author"), "id")
	}
	if author == "" {
		author = fieldStr(thread, "author_id")
	}
	return Post{
		ID:       fieldInt(art, "id"),
		BoardID:  bid,
		ThreadID: fieldInt(art, "thread_id"),
		Floor:    1,
		Author:   author,
		Subject:  subject,
		Date:     apiDate(sec),
		Time:     sec,
		From:     "开放平台",
		Body:     fieldStr(art, "body"),
	}
}

func apiDate(sec int64) string {
	if sec <= 0 {
		return "--:--"
	}
	t := time.Unix(sec, 0)
	if time.Since(t) > 365*24*time.Hour {
		return t.Format("06-01-02")
	}
	return t.Format("01-02 15:04")
}

func objList(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func fieldMap(m map[string]any, k string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[k].(map[string]any)
	return v
}

func fieldStr(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func fieldInt(m map[string]any, k string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case json.Number:
		n, _ := v.Int64()
		return n
	case bool:
		if v {
			return 1
		}
	}
	return 0
}
