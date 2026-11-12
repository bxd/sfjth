package smth

// WAP 数据源：直接调用 wap.newsmth.net 的 JSON API。
// 与 Web 数据源（解析 nForum HTML）相比，wap 用结构化 JSON，速度快、字段稳定，
// 因此作为「分类讨论区 / 个人定制区 / 选择阅读讨论区」等列表数据的首选通道；
// 失败时上层 Client 再回落到 web。
//
// 已确认的接口（2026-11，游客/登录后实测）：
//   GET /wap/api/section/all                       分区列表 {sections:[{id,name,description}]}
//   GET /wap/api/section/subs?id=<hash>            某分区的版面 [{id,name,title,todayPostCount,...}]
//   GET /wap/api/profile/favorite/boards/1/999     我的收藏 {favBoards:[{board:{...}}]}（需登录）
//   GET /wap/api/search/board?keyword=<kw>         版面搜索 [{...}]
//   GET /wap/api/hot/ten                           十大 {topics:[...]}
//
// 认证：站点前端把 kbs-key/kbs-info 存在 Cookie 里，请求自动带上；本客户端把
// 配置里保存的 Cookie 字符串原样带到每个 wap 请求即可，实测 web 登录的 Cookie
// 在 wap 站同样有效。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const wapBaseURL = "https://wap.newsmth.net"

// WapClient 是 wap JSON API 数据源。
type WapClient struct {
	Base   string
	Cookie string
	HTTP   *http.Client

	// 主题列表里的首帖附件字段比文章详情接口更新：实测同一篇文章在
	// /board/topic/list 里给的是 file.mysmth.net/attachment/...，
	// 而 /topic/loadArticlesByMode 只给已经 502 的 exp.newsmth.net 地址。
	// 读正文时把列表里见过的候选地址合并回正文附件，避免漏图。
	attachMu        sync.RWMutex
	attachByArticle map[string][]Attachment // key 为首帖文章 id（wap hash）
}

// NewWapClient 创建 wap 数据源。
func NewWapClient(cfg Config) *WapClient {
	return &WapClient{
		Base:            wapBaseURL,
		Cookie:          cfg.Cookie,
		HTTP:            &http.Client{Timeout: 15 * time.Second},
		attachByArticle: map[string][]Attachment{},
	}
}

// wapResp 是所有 wap 接口的外层包装：code=1 表示成功，data 为实际负载。
type wapResp struct {
	Code    int             `json:"code"`
	KbsCode int             `json:"kbsCode"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// get 请求一个 wap 接口并解包 data。非 200 / code!=1 都返回 error。
func (w *WapClient) get(path string, query url.Values) (json.RawMessage, error) {
	u := strings.TrimRight(w.Base, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", w.Base+"/")
	if w.Cookie != "" {
		req.Header.Set("Cookie", w.Cookie)
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wap HTTP %d", resp.StatusCode)
	}
	var r wapResp
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("wap 响应解析失败：%w", err)
	}
	if r.Code != 1 {
		return nil, fmt.Errorf("wap 返回 code=%d：%s", r.Code, r.Message)
	}
	return r.Data, nil
}

// wapSection 是 section/all 的单个分区。
type wapSection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// wapBoard 是 section/subs 与 favBoards 里的版面对象。两处字段一致。
type wapBoard struct {
	ID             string `json:"id"`             // 32 位 hash，wap 内部标识
	Name           string `json:"name"`           // 英文版名，如 NewExpress（跨站通用）
	Title          string `json:"title"`          // 中文版名
	SectionID      string `json:"sectionId"`      // 所属分区 hash
	TodayPostCount int    `json:"todayPostCount"` // 今日发帖
	AccessScore    int    `json:"accessScore"`
	IsFavorite     int    `json:"isFavorite"`
}

// Sections 从 wap 读取分区列表。
func (w *WapClient) Sections() ([]Section, error) {
	data, err := w.get("/wap/api/section/all", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Sections []wapSection `json:"sections"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	res := make([]Section, 0, len(out.Sections))
	for _, s := range out.Sections {
		if s.ID == "" || s.Name == "" {
			continue
		}
		res = append(res, Section{ID: s.ID, Name: s.Name, Desc: s.Description})
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("wap 分区列表为空")
	}
	return res, nil
}

// Boards 读取某分区下的版面。section 是 wap 的分区 hash；为空时读「社区管理」。
func (w *WapClient) Boards(section string) ([]Board, error) {
	if section == "" || section == "all" || section == "0" {
		// 没有指定分区：取第一个分区的版面，保证有内容可显示。
		secs, err := w.Sections()
		if err != nil || len(secs) == 0 {
			return nil, err
		}
		section = secs[0].ID
	}
	q := url.Values{"id": {section}}
	data, err := w.get("/wap/api/section/subs", q)
	if err != nil {
		return nil, err
	}
	var out struct {
		Boards []wapBoard `json:"boards"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	res := make([]Board, 0, len(out.Boards))
	for _, b := range out.Boards {
		res = append(res, wapToBoard(b))
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("wap 分区 %s 没有版面", section)
	}
	return res, nil
}

// Favorites 从 wap 读取「我的收藏」（个人定制区）。需要有效登录 Cookie。
func (w *WapClient) Favorites() ([]Board, error) {
	data, err := w.get("/wap/api/profile/favorite/boards/1/999", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		FavBoards []struct {
			Board wapBoard `json:"board"`
		} `json:"favBoards"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	res := make([]Board, 0, len(out.FavBoards))
	for _, f := range out.FavBoards {
		if f.Board.Name == "" {
			continue
		}
		res = append(res, wapToBoard(f.Board))
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("wap 收藏夹为空")
	}
	return res, nil
}

// SearchBoards 按关键词搜索版面（选择阅读讨论区用）。
func (w *WapClient) SearchBoards(keyword string) ([]Board, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, fmt.Errorf("关键词为空")
	}
	q := url.Values{"keyword": {keyword}}
	data, err := w.get("/wap/api/search/board", q)
	if err != nil {
		return nil, err
	}
	var raw []wapBoard
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	res := make([]Board, 0, len(raw))
	for _, b := range raw {
		if b.Name == "" {
			continue
		}
		res = append(res, wapToBoard(b))
	}
	return res, nil
}

// wapToBoard 把 wap 版面对象归一化成统一 Board。
// 关键：ID 用「英文版名」而不是 wap hash —— web 数据源的 Threads/Posts 也用英文版名，
// 这样 wap 读到的列表点进去走 web 回落时，URL/版名无需转换。
func wapToBoard(b wapBoard) Board {
	name := strings.TrimSpace(b.Name)
	cn := strings.TrimSpace(b.Title)
	if cn == "" {
		cn = name
	}
	return Board{
		ID:      name,
		Name:    cn,
		Section: b.SectionID,
		Desc:    fmt.Sprintf("今日 %d", b.TodayPostCount),
		Today:   b.TodayPostCount,
		Kind:    "board",
	}
}

// parseWAPID 把 wap 字符串 id 里的数字部分转 int64，供需要数字 id 的结构使用。
// wap 的文章 id 多为 32 位 hash（无纯数字），此时返回 0 并由调用方决定是否回落 web。
func parseWAPID(s string) int64 {
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return 0
}

// hashID 把 wap 的 32 位 hash 确定性地映射为 int64，供统一结构里的数字 ID 使用。
// 关键：结果必须落在 JavaScript 的安全整数范围内（< 2^53），否则前端 Number(id)
// 会丢精度，回传 BbsService.Thread 时无法匹配到原始 hash。故只取 13 个十六进制
// 字符（52 位）并保留符号位为 0；约 4.5e15 的取值空间对单屏主题足够，碰撞可忽略。
func hashID(hash string) int64 {
	if n := parseWAPID(hash); n > 0 && n < 1<<52 {
		return n
	}
	h := strings.ToLower(strings.TrimSpace(hash))
	if len(h) > 13 {
		h = h[:13]
	}
	if h == "" {
		return 0
	}
	n, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return 0
	}
	return int64(n & 0x1fffffffffffff)
}

// boardHash 把「英文版名」换成 wap 的 32 位 hash。wap 的 /board/topic/list 只认 hash，
// 而统一结构里 Board.ID 存的是英文版名（跨站通用）。search/board 会返回 id=hash。
// 先按英文版名精确匹配，其次按中文名匹配。
func (w *WapClient) boardHash(board string) (string, error) {
	board = strings.TrimSpace(board)
	if board == "" {
		return "", fmt.Errorf("版名为空")
	}
	if isWAPHash(board) {
		return board, nil
	}
	data, err := w.get("/wap/api/search/board", url.Values{"keyword": {board}})
	if err != nil {
		return "", err
	}
	var list []wapBoard
	if err := json.Unmarshal(data, &list); err != nil {
		return "", err
	}
	for _, b := range list {
		if strings.EqualFold(b.Name, board) {
			return b.ID, nil
		}
	}
	for _, b := range list {
		if b.Title == board {
			return b.ID, nil
		}
	}
	return "", fmt.Errorf("wap 找不到版面 %s 的 hash", board)
}

// wapAttachment 是 wap JSON 里的附件对象。实测 exp CDN 可能 502，KS3 地址稳定，
// 因此同时保留多个候选地址，由 AttachmentFetch 依次尝试。
type wapAttachment struct {
	ID     string `json:"id"`
	Hash   string `json:"hash"` // KS3 侧 hash，与 ID 不同；web 取同一附件时会用到
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	URL    string `json:"url"`
	CDNURL string `json:"cdnUrl"`
	KS3URL string `json:"ks3Url"`
	Serial int    `json:"serial"`
}

func wapToAttachment(a wapAttachment) Attachment {
	urls := []string{}
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			u = strings.TrimRight(wapBaseURL, "/") + "/" + strings.TrimLeft(u, "/")
		}
		seen[u] = true
		urls = append(urls, u)
	}
	// 实测 exp.newsmth.net 的 CDN 地址偶发 502，KS3 地址稳定，优先 KS3。
	add(a.KS3URL)
	add(a.CDNURL)
	add(a.URL)
	if a.ID != "" {
		// 官方 wap 主题列表用的是 file.mysmth.net，文章详情接口有时只给 exp；
		// 两个域名都保留，任一可用即可取到同一个附件。
		add("https://file.mysmth.net/attachment/" + a.ID)
		add("https://exp.newsmth.net/attachment/" + a.ID)
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		name = "attachment-" + a.ID
	}
	out := Attachment{
		ID: a.ID, Name: name, Type: a.Type, Size: a.Size, URLs: urls,
	}
	if len(urls) > 0 {
		out.URL = urls[0]
	}
	out.Kind = AttachmentKind(out.Name, out.Type)
	return out
}

// rememberArticleAttachments 缓存主题列表里见到的首帖附件。
// 列表接口与正文接口的 cdnUrl 可能不同，缓存后读正文时可合并候选地址。
func (w *WapClient) rememberArticleAttachments(keys []string, raw []wapAttachment, board, subject string) {
	if len(raw) == 0 || w.attachByArticle == nil {
		return
	}
	items := make([]Attachment, 0, len(raw))
	for _, r := range raw {
		a := wapToAttachment(r)
		a.Board, a.Subject = board, subject
		items = append(items, a)
	}
	w.attachMu.Lock()
	defer w.attachMu.Unlock()
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		w.attachByArticle[key] = mergeAttachmentCandidates(w.attachByArticle[key], items)
	}
}

// EnrichAttachments 把主题列表里见过的附件候选地址合并回正文附件。
// 只补 URL，不改变楼层；前端拿到的仍是同一组附件，只是多了可尝试的地址。
func (w *WapClient) EnrichAttachments(posts []Post) {
	if w == nil || len(posts) == 0 {
		return
	}
	w.attachMu.RLock()
	defer w.attachMu.RUnlock()
	if len(w.attachByArticle) == 0 {
		return
	}
	for i := range posts {
		cached := w.attachByArticle[posts[i].HashID]
		if len(cached) == 0 {
			continue
		}
		posts[i].Attachments = mergeAttachmentCandidates(posts[i].Attachments, cached)
	}
}

// mergeAttachmentCandidates 按附件 ID/文件名合并两批附件，去重 URL，
// 并保证新地址排在旧地址前面（列表接口通常比详情接口更新）。
func mergeAttachmentCandidates(dst, fresh []Attachment) []Attachment {
	if len(fresh) == 0 {
		return dst
	}
	out := make([]Attachment, 0, len(dst)+len(fresh))
	out = append(out, fresh...)
	for _, d := range dst {
		idx := -1
		for i, o := range out {
			sameID := d.ID != "" && o.ID == d.ID
			// wap 的 hash ID 与 web 的 "web-<文章号>-<附件号>" 不同，但文件名和
			// 字节数一致时就是同一个附件（t1.png 380648B），要合并候选地址。
			sameFile := d.Name != "" && o.Name != "" && strings.EqualFold(d.Name, o.Name) &&
				(d.Size == 0 || o.Size == 0 || d.Size == o.Size)
			if sameID || sameFile {
				idx = i
				break
			}
		}
		if idx < 0 {
			out = append(out, d)
			continue
		}
		merged := out[idx]
		seen := map[string]bool{}
		urls := []string{}
		add := func(u string) {
			u = strings.TrimSpace(u)
			if u == "" || seen[u] {
				return
			}
			seen[u] = true
			urls = append(urls, u)
		}
		for _, u := range append(append([]string{}, merged.URLs...), merged.URL, d.URL) {
			add(u)
		}
		for _, u := range d.URLs {
			add(u)
		}
		merged.URLs = urls
		if len(urls) > 0 {
			merged.URL = urls[0]
		}
		if merged.Name == "" {
			merged.Name = d.Name
		}
		if merged.Type == "" {
			merged.Type = d.Type
		}
		if merged.Kind == "" {
			merged.Kind = d.Kind
		}
		if merged.Board == "" {
			merged.Board = d.Board
		}
		if merged.Subject == "" {
			merged.Subject = d.Subject
		}
		out[idx] = merged
	}
	return out
}

// wapTopic 是 /board/topic/list 返回的单个主题。
type wapTopic struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Availables int    `json:"availables"` // 可操作楼层数（近似回复数）
	FlushTime  int64  `json:"flushTime"`  // 最后回复时间(ms)
	LastOrder  int    `json:"lastArticleOrder"`
	FirstID    string `json:"firstArticleId"`
	LastID     string `json:"lastArticleId"`
	Board      struct {
		Name string `json:"name"`
	} `json:"board"`
	Article struct {
		ID      string `json:"id"`
		Account struct {
			Name string `json:"name"`
			Nick string `json:"nick"`
		} `json:"account"`
		PostTime    int64           `json:"postTime"`
		Attachments []wapAttachment `json:"attachments"`
	} `json:"article"`
}

// Threads 读取某版的主题列表（文章列表）。board 为英文版名或 wap hash。
func (w *WapClient) Threads(board string, page int) ([]Thread, int, error) {
	hash, err := w.boardHash(board)
	if err != nil {
		return nil, 1, err
	}
	if page < 1 {
		page = 1
	}
	data, err := w.get("/wap/api/board/topic/list", url.Values{
		"id":                 {hash},
		"isOrderByFlushTime": {"0"},
		"page":               {strconv.Itoa(page)},
	})
	if err != nil {
		return nil, 1, err
	}
	var out struct {
		Pager struct {
			Total int `json:"total"`
			Size  int `json:"size"`
			Page  int `json:"page"`
		} `json:"pager"`
		Topics []wapTopic `json:"topics"`
		Tops   []wapTopic `json:"tops"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, 1, err
	}
	pages := 1
	if out.Pager.Size > 0 && out.Pager.Total > 0 {
		pages = (out.Pager.Total + out.Pager.Size - 1) / out.Pager.Size
	}
	// 置顶主题只在首页出现，排在前面并标记 ◆（term 的置顶符号）。
	res := make([]Thread, 0, len(out.Tops)+len(out.Topics))
	addTopic := func(t wapTopic, flag string) {
		subject := strings.TrimSpace(t.Subject)
		if subject == "" {
			return
		}
		author := t.Article.Account.Name
		if author == "" {
			author = t.Article.Account.Nick
		}
		ms := t.FlushTime
		if ms == 0 {
			ms = t.Article.PostTime
		}
		count := t.Availables
		if count < 1 {
			count = 1
		}
		webBoard := board
		if t.Board.Name != "" {
			webBoard = t.Board.Name
		}
		w.rememberArticleAttachments([]string{t.FirstID, t.Article.ID}, t.Article.Attachments, webBoard, subject)
		res = append(res, Thread{
			ID: hashID(t.ID), HashID: t.ID, BoardID: board, Subject: subject,
			Author: author, Time: ms, Date: formatMS(ms),
			Count: count, Flag: flag, Attach: len(t.Article.Attachments) > 0,
		})
	}
	if page == 1 {
		for _, t := range out.Tops {
			addTopic(t, "◆")
		}
	}
	for _, t := range out.Topics {
		addTopic(t, " ")
	}
	return res, pages, nil
}

// formatMS 把毫秒时间戳格式化为 term 风格的 MM-DD HH:MM。
func formatMS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.Unix(ms/1000, 0).Format("01-02 15:04")
}

// wapArticle 是 loadArticlesByMode 返回的一层文章。
type wapArticle struct {
	ID          string          `json:"id"`
	Subject     string          `json:"subject"`
	PostTime    int64           `json:"postTime"`
	TopicOrder  int             `json:"topicOrder"`
	Body        string          `json:"body"`
	Attachments []wapAttachment `json:"attachments"`
	Account     struct {
		Name string `json:"name"`
		Nick string `json:"nick"`
	} `json:"account"`
}

// Posts 读取一个主题的文章（按 wap topic hash）。mode=1 表示按楼层正序。
func (w *WapClient) Posts(board, topicHash string, page int) ([]Post, string, int, int, error) {
	if page < 1 {
		page = 1
	}
	path := fmt.Sprintf("/wap/api/topic/loadArticlesByMode/%s/1/%d/20", topicHash, page)
	data, err := w.get(path, nil)
	if err != nil {
		return nil, "", 1, 0, err
	}
	var out struct {
		Pager struct {
			TotalPages int `json:"totalPages"`
			TotalItems int `json:"totalItems"`
		} `json:"pager"`
		Topic struct {
			Subject string `json:"subject"`
		} `json:"topic"`
		Articles []wapArticle `json:"articles"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, "", 1, 0, err
	}
	pages := out.Pager.TotalPages
	if pages < 1 {
		pages = 1
	}
	res := make([]Post, 0, len(out.Articles))
	for i, a := range out.Articles {
		floor := a.TopicOrder
		if floor < 1 {
			floor = (page-1)*20 + i + 1
		}
		attachments := make([]Attachment, 0, len(a.Attachments))
		for _, at := range a.Attachments {
			att := wapToAttachment(at)
			att.Board = board
			att.Subject = strings.TrimSpace(a.Subject)
			attachments = append(attachments, att)
		}
		res = append(res, Post{
			ID: hashID(a.ID), HashID: a.ID, BoardID: board,
			ThreadID: hashID(topicHash), Floor: floor,
			Author: a.Account.Name, Nick: a.Account.Nick,
			Subject: strings.TrimSpace(a.Subject), Date: formatMS(a.PostTime),
			Time: a.PostTime, Body: stripTags(a.Body), Attachments: attachments,
		})
	}
	return res, out.Topic.Subject, pages, out.Pager.TotalItems, nil
}
