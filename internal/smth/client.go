package smth

// Client 是数据层门面：把三种数据源（demo / web / openapi）统一成 term 界面
// 需要的结构，并叠加 smthflow 风格的「丝滑」能力：
//   * 内存缓存 + 后台预取（翻页/进帖零等待）
//   * 阅读记忆（记住读到第几楼，再进帖自动续读）
//   * 已读置灰 + 未读新回帖数
//   * 已访问链接着色

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client 统一数据入口。
type Client struct {
	mu       sync.Mutex
	store    *Store
	cache    *Cache
	web      *WebClient
	wap      *WapClient
	api      *OpenAPI
	apiRead  *apiRead
	start    time.Time
	boards   map[string]Board
	lastMsg  string
	prefetch map[string]bool
	// wap 文章列表用 hash 标识主题，但前端绑定/阅读记忆用数字 ID。
	// 这里记录「派生数字 ID -> wap hash」，让 Thread() 能透明地走 wap 读取。
	wapHashByID map[int64]string
	// wap 发帖附件的上传令牌与版面 hash 按会话缓存，确保上传与最终发文用同一个 token。
	wapUploadBoard string
	wapUploadHash  string
	wapUploadToken string
}

// NewClient 创建门面。
func NewClient() *Client {
	s := NewStore()
	c := &Client{
		store:       s,
		cache:       NewCache(),
		start:       time.Now(),
		boards:      map[string]Board{},
		prefetch:    map[string]bool{},
		wapHashByID: map[int64]string{},
	}
	c.rebuild()
	return c
}

func (c *Client) rebuild() {
	cfg := c.store.Config()
	c.web = NewWebClient(cfg)
	c.wap = NewWapClient(cfg)
	c.api = NewOpenAPI(cfg, func(token string) { c.store.SetAccessToken(token) })
	c.apiRead = newAPIRead(c.api)
	for _, b := range demoBoards() {
		c.boards[b.ID] = b
	}
}

// Store 暴露存储（只读用途）。
func (c *Client) Store() *Store { return c.store }

// Config 返回当前配置。
func (c *Client) Config() Config { return c.store.Config() }

// SaveConfig 更新配置并重建数据源。
func (c *Client) SaveConfig(patchJSON string) (Config, error) {
	cfg, err := c.store.Patch(patchJSON)
	if err != nil {
		return Config{}, err
	}
	c.mu.Lock()
	c.rebuild()
	c.cache.Clear()
	c.lastMsg = "设置已保存，数据源：" + cfg.Source
	c.mu.Unlock()
	return cfg, nil
}

// SetSource 快速切换数据源。
func (c *Client) SetSource(source string) (Config, error) {
	return c.SaveConfig(fmt.Sprintf(`{"source":%q}`, source))
}

// SetCookie 导入浏览器 Cookie。
func (c *Client) SetCookie(cookie string) error {
	c.store.SetCookie(cookie)
	c.mu.Lock()
	c.rebuild()
	c.cache.Clear()
	c.lastMsg = "Cookie 已导入"
	c.mu.Unlock()
	return nil
}

// Login 兼容旧调用：按配置里的通道（默认 auto）真实登录。
func (c *Client) Login(user, pass string) (string, error) {
	out, err := c.LoginWith(c.Config().LoginMode, user, pass, "")
	if err != nil {
		return "", err
	}
	if !out.OK {
		return "", errors.New(out.Message)
	}
	return out.User, nil
}

// LoginWith 用指定的 web / wap / auto 通道真实登录水木服务器。
// auto 会依次尝试两条通道，任一成功即落盘会话；失败时返回逐通道结果，
// 登录屏据此提示是验证码拦截还是密码错误。密码只在内存中用于本次请求。
func (c *Client) LoginWith(mode, user, pass, captchaJSON string) (LoginOutcome, error) {
	user = strings.TrimSuffix(strings.TrimSpace(user), ".")
	if user == "" || pass == "" {
		return LoginOutcome{}, errors.New("请输入用户名与密码")
	}
	cap, err := parseLoginCaptcha(captchaJSON)
	if err != nil {
		return LoginOutcome{}, err
	}
	mode = normalizeLoginMode(mode)
	cfg := c.Config()
	tr := newLoginTransport(defaultLoginEndpoints(cfg.BaseURL), cfg.Cookie)
	tr.seed()

	out := LoginOutcome{Attempts: []LoginAttempt{}, Channel: mode}
	run := func(ch string) LoginAttempt {
		if ch == LoginWap {
			return tr.wapLogin(user, pass, cap)
		}
		return tr.webLogin(user, pass, cap)
	}
	switch mode {
	case LoginWeb:
		out.Attempts = append(out.Attempts, run(LoginWeb))
	case LoginWap:
		out.Attempts = append(out.Attempts, run(LoginWap))
	default:
		a := run(LoginWeb)
		out.Attempts = append(out.Attempts, a)
		if !a.OK {
			out.Attempts = append(out.Attempts, run(LoginWap))
		}
	}
	for _, a := range out.Attempts {
		out.CaptchaRequired = out.CaptchaRequired || a.CaptchaRequired
		if a.OK {
			out.OK = true
			out.Channel = a.Channel
			out.User = a.User
			break
		}
	}
	if !out.OK {
		msgs := make([]string, 0, len(out.Attempts))
		for _, a := range out.Attempts {
			msgs = append(msgs, a.Channel+": "+a.Message)
		}
		out.Message = strings.Join(msgs, " | ")
		return out, nil
	}
	name := out.User
	if name == "" {
		name = user
	}
	ck := tr.Cookies()
	if ck != "" {
		c.store.SetCookie(ck)
	}
	c.store.SetUser(name)
	// 只保存代号与偏好，绝不保存密码；登录成功即切到真实数据源。
	patch := fmt.Sprintf(`{"last_user_id":%q,"remember_id":true,"login_mode":%q,"source":"web"}`, name, mode)
	_, _ = c.store.Patch(patch)
	c.mu.Lock()
	c.rebuild()
	c.cache.Clear()
	c.lastMsg = "登录成功（" + out.Channel + "）：" + name
	c.mu.Unlock()
	out.Message = c.lastMsg
	return out, nil
}

// SessionInfo 返回 term 进站登录屏需要的会话信息。
func (c *Client) SessionInfo() SessionInfo {
	cfg := c.store.Config()
	c.mu.Lock()
	msg := c.lastMsg
	c.mu.Unlock()
	return SessionInfo{
		User:       cfg.User,
		LastUserID: cfg.LastUserID,
		HasSession: cfg.Cookie != "" || cfg.AccessToken != "",
		Source:     cfg.Source,
		AutoLogin:  cfg.AutoLogin,
		RememberID: cfg.RememberID,
		Big5:       cfg.Big5,
		LoginMode:  normalizeLoginMode(cfg.LoginMode),
		Message:    msg,
		// 经典进站画面的统计行；站点无常驻统计接口，保留 term 原样数值。
		Online: 69878, MaxOnline: 406798, Guests: 64029,
	}
}

// GuestLogin 以 guest 试用身份进入，不联网、不写会话。
func (c *Client) GuestLogin() (Result, error) {
	c.store.SetUser("")
	c.mu.Lock()
	c.lastMsg = "以 guest 身份试用"
	c.mu.Unlock()
	return Result{OK: true, Message: "以 guest 身份试用（离线演示数据）"}, nil
}

// CheckSession 校验已保存的 Cookie / access_token，供登录屏免密继续。
func (c *Client) CheckSession() (SessionCheck, error) {
	cfg := c.store.Config()
	if cfg.Source == "openapi" && cfg.AccessToken != "" {
		if _, err := c.api.Profile(); err != nil {
			return SessionCheck{Valid: false, Message: "开放平台 token 已失效：" + err.Error()}, nil
		}
		u := cfg.User
		if u == "" {
			u = cfg.LastUserID
		}
		return SessionCheck{Valid: true, User: u, Message: "开放平台会话有效"}, nil
	}
	if cfg.Cookie == "" {
		return SessionCheck{Valid: false, Message: "没有已保存的会话"}, nil
	}
	// 真实验证：两端各自的会话接口都试一次，任一有效即可继续。
	tr := newLoginTransport(defaultLoginEndpoints(cfg.BaseURL), cfg.Cookie)
	webName, webErr := tr.webSession()
	if webErr == nil && webName != "" {
		return SessionCheck{Valid: true, User: webName, Message: "Web 会话有效"}, nil
	}
	wapName, wapErr := tr.wapSession()
	if wapErr == nil {
		if wapName == "" {
			wapName = cfg.User
			if wapName == "" {
				wapName = cfg.LastUserID
			}
		}
		if wapName != "" {
			return SessionCheck{Valid: true, User: wapName, Message: "WAP 会话有效"}, nil
		}
	}
	msg := "会话已过期，请重新输入密码"
	if webErr != nil {
		msg += "（web: " + webErr.Error() + "）"
	}
	if wapErr != nil {
		msg += "（wap: " + wapErr.Error() + "）"
	}
	return SessionCheck{Valid: false, Message: msg}, nil
}

// Logout 先通知站点登出，再清除本地登录态。
func (c *Client) Logout() error {
	cfg := c.Config()
	if cfg.Cookie != "" {
		tr := newLoginTransport(defaultLoginEndpoints(cfg.BaseURL), cfg.Cookie)
		_, _, _ = tr.request(http.MethodGet, desktopUA, tr.ep.web+"/nForum/user/ajax_logout.json", nil,
			map[string]string{"X-Requested-With": "XMLHttpRequest", "Referer": tr.ep.web + "/nForum/"})
	}
	c.store.SetUser("")
	c.store.SetCookie("")
	c.store.SetAccessToken("")
	c.mu.Lock()
	c.rebuild()
	c.cache.Clear()
	c.lastMsg = "已退出登录"
	c.mu.Unlock()
	return nil
}

// OAuthURL 返回开放平台授权页地址。
func (c *Client) OAuthURL() string {
	return c.api.AuthorizeURL(fmt.Sprintf("sfjth-%d", time.Now().Unix()))
}

// OAuthCode 用 code 换取 access_token。
func (c *Client) OAuthCode(code string) error {
	if err := c.api.Exchange(code); err != nil {
		return err
	}
	c.mu.Lock()
	c.lastMsg = "开放平台授权成功"
	c.mu.Unlock()
	return nil
}

// APICall 直接调用官方 API（供高级用户/调试）。
func (c *Client) APICall(method string, paramsJSON string) (string, error) {
	params := map[string]string{}
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			return "", err
		}
	}
	out, err := c.api.Call(method, params)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// note 记录一条显示在底栏的提示。
func (c *Client) note(msg string) {
	c.mu.Lock()
	c.lastMsg = msg
	c.mu.Unlock()
}

// Status 返回运行时状态。
func (c *Client) Status(board string) Status {
	cfg := c.store.Config()
	c.mu.Lock()
	msg := c.lastMsg
	c.mu.Unlock()
	user := cfg.User
	if user == "" {
		user = "guest"
	}
	return Status{
		Source:    cfg.Source,
		User:      user,
		Online:    cfg.Source != "demo" && (cfg.Cookie != "" || cfg.AccessToken != ""),
		Board:     board,
		Message:   msg,
		Uptime:    int64(time.Since(c.start).Seconds()),
		UnreadAll: c.UnreadAll(),
		Wiki:      "https://wiki.open.newsmth.net/",
	}
}

// UnreadAll 汇总未读数。
func (c *Client) UnreadAll() int {
	n := 0
	for _, b := range demoBoards() {
		for _, t := range demoThreads(b.ID) {
			n += c.store.Unread(t.BoardID, t.ID, t.Count)
		}
	}
	return n
}

// Sections 分区列表。真实数据优先走 wap JSON API，失败回落 web HTML 解析。
func (c *Client) Sections() ([]Section, error) {
	switch c.Config().Source {
	case "demo":
		return demoSections(), nil
	case "openapi":
		// 官方 wiki: board/load_section -> sections[]
		return c.apiRead.Sections()
	default:
		if list, err := c.wap.Sections(); err == nil && len(list) > 0 {
			return list, nil
		}
		return c.web.Sections()
	}
}

// Boards 版面列表。真实数据优先 wap JSON API，失败回落 web HTML 解析。
// 注意两站分区 ID 不同：wap 用 hash、web 用数字/英文，前端传入的 section 原样
// 交给当前选中的通道；wap 失败回落 web 时，若 section 是 wap hash（32 位十六进制），
// web 无法识别，改为拉取 web 的默认分区，避免出现空列表。
func (c *Client) Boards(section string) ([]Board, error) {
	source := c.Config().Source
	key := "boards:" + section + ":" + source
	if v, ok := c.cache.Get(key); ok {
		return v.([]Board), nil
	}
	var out []Board
	var err error
	switch source {
	case "demo":
		out = demoBoards()
		if section != "" && section != "all" && section != "0" {
			filtered := []Board{}
			for _, b := range out {
				if b.Section == section || b.Kind == "dir" {
					filtered = append(filtered, b)
				}
			}
			out = filtered
		}
	case "openapi":
		// 官方 wiki: board/read_section -> boards[] / board/load_boards -> list[]
		out, err = c.apiRead.Boards(section)
		if err != nil {
			return nil, err
		}
	default:
		out, err = c.wap.Boards(section)
		if err != nil || len(out) == 0 {
			// 回落到 web：wap 的分区 hash 在 web 站无效，改用默认分区。
			webSection := section
			if isWAPHash(section) {
				webSection = ""
			}
			out, err = c.web.Boards(webSection)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(out) == 0 && source != "openapi" {
		out = demoBoards()
	}
	for _, b := range out {
		c.boards[b.ID] = b
	}
	c.cache.Put(key, out, 5*time.Minute)
	return out, nil
}

// isWAPHash 判断是否为 wap 的 32 位十六进制分区 id。
func isWAPHash(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, ch := range s {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

// Favorites 收藏版面（个人定制区）。
// 真实数据优先从 wap 在线读取「我的收藏」——web 源下原本只有本地收藏，
// 这次改成服务器真实数据；wap 失败（未登录/网络）再回落 openapi 或本地收藏。
func (c *Client) Favorites() ([]Board, error) {
	source := c.Config().Source
	if source == "demo" {
		fav := c.store.Favorites()
		out := make([]Board, 0, len(fav))
		for _, id := range fav {
			if b, ok := c.boards[id]; ok {
				out = append(out, b)
			} else {
				out = append(out, demoBoardByID(id))
			}
		}
		return out, nil
	}
	// 一级：wap 在线收藏（真实服务器数据）
	if list, err := c.wap.Favorites(); err == nil && len(list) > 0 {
		for _, b := range list {
			c.boards[b.ID] = b
		}
		return list, nil
	}
	// 二级：openapi 官方收藏
	if source == "openapi" {
		if list, err := c.apiRead.Favorites(); err == nil && len(list) > 0 {
			return list, nil
		}
	}
	// 三级：本地收藏兜底，保证个人定制区始终可进入
	c.note("在线收藏不可用，显示本地收藏（登录后可同步服务器）")
	fav := c.store.Favorites()
	out := make([]Board, 0, len(fav))
	for _, id := range fav {
		if b, ok := c.boards[id]; ok {
			out = append(out, b)
		} else {
			out = append(out, demoBoardByID(id))
		}
	}
	return out, nil
}

// SearchBoards 按关键词搜索版面（选择阅读讨论区用）。真实数据优先 wap，
// 失败回落 web 的全部版列表本地过滤；demo 源直接用演示版列表。
func (c *Client) SearchBoards(keyword string) ([]Board, error) {
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return nil, nil
	}
	if c.Config().Source == "web" {
		if list, err := c.wap.SearchBoards(kw); err == nil && len(list) > 0 {
			return list, nil
		}
	}
	// 回落：取全部版列表在本地按中英文名过滤。
	all, err := c.Boards("")
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(kw)
	out := []Board{}
	for _, b := range all {
		if strings.Contains(strings.ToLower(b.ID), lower) || strings.Contains(strings.ToLower(b.Name), lower) {
			out = append(out, b)
		}
	}
	return out, nil
}

// ToggleFavorite 切换收藏。
func (c *Client) ToggleFavorite(board string) bool {
	return c.store.ToggleFavorite(board)
}

// TopTen 当日十大。
func (c *Client) TopTen() ([]TopItem, error) {
	key := "topten:" + c.Config().Source
	if v, ok := c.cache.Get(key); ok {
		return v.([]TopItem), nil
	}
	var out []TopItem
	if c.Config().Source == "demo" || c.Config().Source == "openapi" {
		// 官方 API 无「十大」方法；openapi 下直接用本地演示数据，避免误触 Web 游客验证码。
		out = demoTopTen()
	} else {
		var err error
		out, err = c.web.TopTen()
		if err != nil || len(out) == 0 {
			out = demoTopTen()
		}
	}
	c.cache.Put(key, out, 2*time.Minute)
	return out, nil
}

// Threads 版面主题列表（含已读/未读标记）。
func (c *Client) Threads(board string, page int) (ThreadsResult, error) {
	key := fmt.Sprintf("threads:%s:%d:%s", board, page, c.Config().Source)
	var items []Thread
	pages := 1
	if v, ok := c.cache.Get(key); ok {
		r := v.(ThreadsResult)
		c.mu.Lock()
		for _, t := range r.Items {
			if t.HashID != "" {
				c.wapHashByID[t.ID] = t.HashID
			}
		}
		c.mu.Unlock()
		return c.decorateThreads(r), nil
	}
	switch c.Config().Source {
	case "demo":
		items = demoThreads(board)
		pages = 1
	case "openapi":
		// 官方 wiki: timeline/boards_threads（article=1&body=1），以 timeline_id 作 from 游标。
		// 注意这是「驻版主题时间线」，只覆盖已驻版面；任意版面浏览请用 web 数据源。
		var err error
		items, pages, err = c.apiRead.Threads(board, page)
		if err != nil {
			return ThreadsResult{}, err
		}
		if len(items) == 0 {
			c.note("当前时间线没有该版主题（官方 API 为驻版时间线模型），可切 web 数据源")
		}
	default:
		// wap 优先：JSON 结构化、字段稳定；失败回落 web（nForum HTML 解析）。
		items, pages = nil, 1
		got := false
		if wapItems, wapPages, werr := c.wap.Threads(board, page); werr == nil && len(wapItems) > 0 {
			items, pages, got = wapItems, wapPages, true
			c.mu.Lock()
			for _, t := range wapItems {
				if t.HashID != "" {
					c.wapHashByID[t.ID] = t.HashID
				}
			}
			c.mu.Unlock()
		} else if werr != nil {
			c.note("wap 主题列表失败：" + werr.Error() + "，改用 web")
		}
		if !got {
			var err error
			items, pages, err = c.web.Threads(board, page)
			if err != nil {
				return ThreadsResult{}, err
			}
		}
	}
	// term 列表是「上旧下新」：最新的文章在最下面，光标默认停在那里。
	// wap/nForum 接口返回的是「新→旧」，这里统一反转成「旧→新」，
	// 并把置顶/公告（◆）移到末尾（term 下公告紧随普通文章之后显示在底部）。
	items = termThreadOrder(items)
	res := ThreadsResult{
		Board: c.ensureBoard(board), Items: items, Page: page, Pages: pages, Mode: "最新回复",
	}
	c.cache.Put(key, res, 90*time.Second)
	if c.Config().Preload && page < pages && c.Config().Source != "openapi" {
		c.prefetchThreads(board, page+1)
	}
	return c.decorateThreads(res), nil
}

// termThreadOrder 把「新→旧」的接口结果转成 term 的「旧→新」顺序：最新主题在末尾，
// 置顶/公告（Flag=◆）沉到最底部。稳定排序保证同序主题相对顺序不变。
func termThreadOrder(items []Thread) []Thread {
	out := make([]Thread, 0, len(items))
	for _, t := range items {
		if t.Flag == "◆" {
			continue
		}
		out = append(out, t)
	}
	// 反转普通主题：接口是新→旧，term 要旧→新。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	for _, t := range items {
		if t.Flag == "◆" {
			out = append(out, t)
		}
	}
	return out
}

func (c *Client) decorateThreads(r ThreadsResult) ThreadsResult {
	for i := range r.Items {
		t := &r.Items[i]
		t.Read = c.store.IsRead(t.BoardID, t.ID)
		t.Unread = c.store.Unread(t.BoardID, t.ID, t.Count)
		if t.Unread == 0 && !t.Read {
			// 首次见到的主题，全部回帖都算「未读」过于吵闹，只提示有新帖。
			t.Unread = 0
		}
	}
	return r
}

func (c *Client) prefetchThreads(board string, page int) {
	if c.Config().Source == "openapi" {
		return // 官方时间线按 timeline_id 分页，不走 Web 预取
	}
	key := fmt.Sprintf("threads:%s:%d:%s", board, page, c.Config().Source)
	c.mu.Lock()
	if c.prefetch[key] {
		c.mu.Unlock()
		return
	}
	c.prefetch[key] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.prefetch, key)
			c.mu.Unlock()
		}()
		if _, ok := c.cache.Get(key); ok {
			return
		}
		items, pages, err := c.wap.Threads(board, page)
		if err != nil || len(items) == 0 {
			items, pages, err = c.web.Threads(board, page)
		}
		if err != nil {
			return
		}
		items = termThreadOrder(items)
		c.mu.Lock()
		for _, t := range items {
			if t.HashID != "" {
				c.wapHashByID[t.ID] = t.HashID
			}
		}
		c.mu.Unlock()
		c.cache.Put(key, ThreadsResult{Board: c.board(board), Items: items, Page: page, Pages: pages}, 90*time.Second)
	}()
}

// readPost 读取单帖正文：wap 列表来的主题（数字 ID 有 hash 映射）优先走 wap，
// 其余（web 列表的数字 ID、直接输入的版名+ID）走 nForum。失败时回落 web。
func (c *Client) readPost(board string, id int64, page int) ([]Post, string, int, int, error) {
	c.mu.Lock()
	hash := c.wapHashByID[id]
	c.mu.Unlock()
	if hash != "" {
		items, subject, pages, total, err := c.wap.Posts(board, hash, page)
		if err == nil && len(items) > 0 {
			// 主题列表里的首帖附件地址（如 file.mysmth.net）比正文接口更新，
			// 合并候选地址后再交给前端，避免正文详情只给已失效的 exp CDN。
			c.wap.EnrichAttachments(items)
			// wap 有时只报第一个附件；web 正文里嵌着该层全部附件，一并补齐。
			c.enrichWebAttachments(items)
			// wap 的楼层 ID 是 hash，回帖要用 web 页上的真实文章 ID。
			c.enrichArticleIDs(board, id, items)
			return items, subject, pages, total, nil
		}
		if err != nil {
			c.note("wap 文章读取失败：" + err.Error() + "，改用 web")
		}
	}
	items, subject, pages, total, err := c.web.Posts(board, id, page)
	return items, subject, pages, total, err
}

// enrichWebAttachments 把 web 正文里该层的全部附件并进帖子。
// wap 接口常只列第一个附件（如首帖 t1.png），其余 t2~t6 只在 web 正文里。
func (c *Client) enrichWebAttachments(posts []Post) {
	for i := range posts {
		if len(posts[i].Attachments) == 0 {
			continue
		}
		board, subject := posts[i].Attachments[0].Board, posts[i].Attachments[0].Subject
		if board == "" || subject == "" {
			continue
		}
		web := webAttachmentsForArticle(board, subject, c.Config().Cookie, posts[i].Floor)
		if len(web) == 0 {
			continue
		}
		posts[i].Attachments = mergeAttachmentCandidates(posts[i].Attachments, web)
	}
}

// enrichArticleIDs 用 web 正文页把 wap 派生出来的楼层 ID 换成真实文章 ID。
// 回帖接口要求精确到楼层，hash 派生的 ID 在 web 上并不存在。
func (c *Client) enrichArticleIDs(board string, id int64, posts []Post) {
	pages := map[int]bool{}
	for _, p := range posts {
		if p.Floor > 0 {
			pages[(p.Floor-1)/10+1] = true
		}
	}
	for page := range pages {
		body, err := c.web.Get(fmt.Sprintf("/nForum/article/%s/%d?p=%d", url.PathEscape(board), id, page))
		if err != nil {
			continue
		}
		ids := parseWebArticleIDs(body)
		for i := range posts {
			if real := ids[posts[i].Floor]; real > 0 {
				posts[i].ID = real
			}
		}
	}
}

// InvalidatePostCache 发文/回帖成功后让正文与讨论区列表重新拉取。
// 缓存默认 3 分钟，不清掉的话前端「刷新」仍会拿到发表前的旧数据。
func (c *Client) InvalidatePostCache(board string, threadID int64) {
	c.cache.DeletePrefix(fmt.Sprintf("post:%s:%d:", board, threadID))
	c.cache.DeletePrefix(fmt.Sprintf("threads:%s:", board))
}

// Thread 文章正文（带阅读记忆）。
func (c *Client) Thread(board string, id int64, page int) (ThreadResult, error) {
	key := fmt.Sprintf("post:%s:%d:%d:%s", board, id, page, c.Config().Source)
	subject := demoSubject(board, id, "")
	if v, ok := c.cache.Get(key); ok {
		res := v.(ThreadResult)
		res.Resume = c.store.ReadFloor(board, id)
		return res, nil
	}
	var items []Post
	pages, total := 1, 0
	switch c.Config().Source {
	case "demo":
		items = demoPosts(board, id, page, subject)
		pages = 4
		total = 32
	case "openapi":
		// 官方 wiki 没有「按主题读同主题文章」的方法；使用时间线里缓存的原文。
		var err error
		var s string
		items, s, err = c.apiRead.Posts(board, id)
		if err != nil {
			return ThreadResult{}, err
		}
		if s != "" {
			subject = s
		}
		pages, total = 1, len(items)
	default:
		var s string
		var err error
		items, s, pages, total, err = c.readPost(board, id, page)
		if err != nil {
			return ThreadResult{}, err
		}
		if s != "" {
			subject = s
		}
		if subject == "" {
			subject = demoSubject(board, id, "")
		}
	}
	res := ThreadResult{
		Board: c.ensureBoard(board), Subject: subject, Items: items,
		Page: page, Pages: pages, Total: total,
		Resume: c.store.ReadFloor(board, id),
	}
	c.cache.Put(key, res, 3*time.Minute)
	// 预取下一页，翻页无等待（官方时间线只有原文，无需预取）。
	if c.Config().Preload && page < pages && c.Config().Source != "openapi" {
		c.prefetchPost(board, id, page+1, subject)
	}
	return res, nil
}

func (c *Client) prefetchPost(board string, id int64, page int, subject string) {
	key := fmt.Sprintf("post:%s:%d:%d:%s", board, id, page, c.Config().Source)
	c.mu.Lock()
	if c.prefetch[key] {
		c.mu.Unlock()
		return
	}
	c.prefetch[key] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.prefetch, key)
			c.mu.Unlock()
		}()
		if _, ok := c.cache.Get(key); ok {
			return
		}
		var items []Post
		if c.Config().Source == "demo" {
			items = demoPosts(board, id, page, subject)
		} else {
			var err error
			items, _, _, _, err = c.web.Posts(board, id, page)
			if err != nil {
				return
			}
		}
		c.cache.Put(key, ThreadResult{Board: c.board(board), Subject: subject, Items: items, Page: page}, 3*time.Minute)
	}()
}

// Prefetch 手动预取若干主题（smthflow 式顺滑：光标移动时后台提前取好正文）。
func (c *Client) Prefetch(board string, ids []int64) {
	if c.Config().Source == "demo" || c.Config().Source == "openapi" || !c.Config().Preload {
		return
	}
	for _, id := range ids {
		id := id
		key := fmt.Sprintf("post:%s:%d:%d:%s", board, id, 1, c.Config().Source)
		c.mu.Lock()
		busy := c.prefetch[key]
		c.mu.Unlock()
		if busy {
			continue
		}
		go func() {
			if _, ok := c.cache.Get(key); ok {
				return
			}
			items, subject, pages, total, err := c.readPost(board, id, 1)
			if err != nil {
				return
			}
			c.cache.Put(key, ThreadResult{Board: c.board(board), Subject: subject, Items: items, Page: 1, Pages: pages, Total: total}, 3*time.Minute)
		}()
	}
}

// MarkRead 记录阅读进度。
func (c *Client) MarkRead(board string, id int64, floor, count int) {
	c.store.MarkRead(board, id, floor, count)
}

// Post 发表 / 回复。
func (c *Client) Post(board, subject, content string, replyID int64) (Result, error) {
	if c.Config().Source == "demo" {
		return Result{OK: true, Message: "【演示模式】文章已保存到本地演示数据（未发送到水木）", ID: time.Now().Unix()}, nil
	}
	if subject == "" || content == "" {
		return Result{}, fmt.Errorf("标题与正文不能为空")
	}
	var err error
	if c.Config().Source == "openapi" {
		if replyID > 0 {
			_, err = c.api.Reply(board, replyID, subject, content)
		} else {
			_, err = c.api.Post(board, subject, content)
		}
	} else {
		err = c.web.PostArticle(board, subject, content, replyID)
	}
	if err != nil {
		return Result{}, err
	}
	c.cache.Clear()
	return Result{OK: true, Message: "发表成功（Ctrl+S 发送，Esc 放弃）"}, nil
}

// MailBox 信箱。
// plausibleWebArticleID 区分 web 的真实文章号（目前已涨到 8 位）与 wap 从 hash 派生的 16 位数字。
// 长数字不能直接发给 ajax_post.json，否则站点报「错误的 Re 文编号」。
func plausibleWebArticleID(id int64) bool { return id > 0 && id < 1000000000000 }

// resolveWebThreadID 得到主题在 web 上的真实数字文章号。
// wap 优先的数据源会把 hash 派生 ID 传给前端；这里先用合法范围内的 ID 试探，
// 再用主题标题在 web 版面列表反查，保证回帖不会用错编号。
func (c *Client) resolveWebThreadID(board, subject string, fallback int64) int64 {
	if plausibleWebArticleID(fallback) {
		if id, err := c.web.ArticleIDByFloor(board, fallback, 1); err == nil && plausibleWebArticleID(id) {
			return fallback
		}
	}
	// 用户可能改过回帖标题，优先用 wap 主题里的原始标题反查。
	lookup := subject
	c.mu.Lock()
	hash := c.wapHashByID[fallback]
	c.mu.Unlock()
	if hash != "" {
		if _, wsub, _, _, err := c.wap.Posts(board, hash, 1); err == nil && wsub != "" {
			lookup = wsub
		}
	}
	if sid := webArticleIDBySubject(board, lookup, c.Config().Cookie); sid != "" {
		if id, err := strconv.ParseInt(sid, 10, 64); err == nil && plausibleWebArticleID(id) {
			return id
		}
	}
	return 0
}

// resolveReplyArticleID 解析「回复某楼」时 web 表单 id 应填的真实文章号。
// wap 的 hash 派生 ID 不能直接用，先反查主题的真实 web 文章号，再按楼层映射。
func (c *Client) resolveReplyArticleID(board, subject string, threadID int64, floor int) (int64, error) {
	webID := c.resolveWebThreadID(board, subject, threadID)
	if webID <= 0 {
		return 0, fmt.Errorf("未能定位主题的 web 文章号，请刷新文章后重试")
	}
	if floor > 1 {
		articleID, _ := c.web.ArticleIDByFloor(board, webID, floor)
		if !plausibleWebArticleID(articleID) {
			return 0, fmt.Errorf("未能定位第 %d 楼的回复地址，请刷新文章后重试", floor)
		}
		return articleID, nil
	}
	// 首帖的文章号就是主题号。
	return webID, nil
}

// ResolvePostTarget 供发帖弹窗使用：返回网页发帖页 URL 应带的回复文章号。
// 新建主题返回 0。
func (c *Client) ResolvePostTarget(board, subject string, threadID int64, floor int) (int64, error) {
	if c.Config().Source == "demo" || c.Config().Source == "openapi" || floor <= 0 {
		return 0, nil
	}
	return c.resolveReplyArticleID(board, subject, threadID, floor)
}

// Reply 回复主题中的某一楼。前端只传主题 ID 和楼层，真实文章 ID 在服务端解析，
// 避免 wap 的 hash 派生 ID 被误当成 web 文章 ID。
func (c *Client) Reply(board, subject, content string, threadID int64, floor int) (Result, error) {
	if c.Config().Source == "demo" {
		return Result{OK: true, Message: "【演示模式】回帖已保存到本地（未发送到水木）", ID: time.Now().Unix()}, nil
	}
	if subject == "" || content == "" {
		return Result{}, fmt.Errorf("标题与正文不能为空")
	}
	if c.Config().Source == "openapi" {
		if _, err := c.api.Reply(board, threadID, subject, content); err != nil {
			return Result{}, err
		}
		c.cache.Clear()
		return Result{OK: true, Message: "回复成功"}, nil
	}
	articleID, err := c.resolveReplyArticleID(board, subject, threadID, floor)
	if err != nil {
		return Result{}, err
	}
	if err := c.web.PostArticle(board, subject, content, articleID); err != nil {
		return Result{}, err
	}
	c.cache.Clear()
	return Result{OK: true, Message: fmt.Sprintf("已回复第 %d 楼", floor)}, nil
}

// WapPostTarget 是 wap 发文需要的两个 hash：版面 hash 与所回复楼层的文章 hash。
type WapPostTarget struct {
	BoardHash   string
	ArticleHash string // 新主题为空；回帖时为所回复楼层
}

// WapPostTarget 解析 wap 发文的真实目标 ID。前端传来的 threadID 可能是 wap hash
// 派生的长数字，必须换回站点原始 hash，否则 wap API 不认。
func (c *Client) WapPostTarget(board, subject string, threadID int64, floor int) (WapPostTarget, error) {
	boardHash, err := c.wap.wapBoardHash(board)
	if err != nil {
		return WapPostTarget{}, err
	}
	out := WapPostTarget{BoardHash: boardHash}
	if floor <= 0 {
		return out, nil
	}
	c.mu.Lock()
	topicHash := c.wapHashByID[threadID]
	c.mu.Unlock()
	if topicHash == "" && subject != "" {
		topicHash, _ = c.wap.TopicHashBySubject(board, subject)
	}
	if topicHash == "" {
		return out, fmt.Errorf("未能定位主题的 wap 编号，请从文章列表重新进入后重试")
	}
	articleHash, err := c.wap.wapArticleHashByFloor(topicHash, floor)
	if err != nil {
		return out, err
	}
	out.ArticleHash = articleHash
	return out, nil
}

// ensureWapUpload 准备本次发帖的版面 hash 与上传 token；同一版复用，避免
// 附件传到 A token、发文提交 B token 导致附件丢失。
func (c *Client) ensureWapUpload(board string) (string, string, error) {
	c.mu.Lock()
	if c.wapUploadBoard == board && c.wapUploadHash != "" && c.wapUploadToken != "" {
		h, t := c.wapUploadHash, c.wapUploadToken
		c.mu.Unlock()
		return h, t, nil
	}
	c.mu.Unlock()
	hash, err := c.wap.wapBoardHash(board)
	if err != nil {
		return "", "", err
	}
	token, err := c.wap.UploadToken()
	if err != nil {
		return "", "", err
	}
	c.mu.Lock()
	c.wapUploadBoard, c.wapUploadHash, c.wapUploadToken = board, hash, token
	c.mu.Unlock()
	return hash, token, nil
}

// WapUploadToken 取当前会话的 wap 附件上传令牌。
func (c *Client) WapUploadToken(board string) (string, error) {
	_, token, err := c.ensureWapUpload(board)
	return token, err
}

// WapUploadAttachment 上传附件到 wap 临时区，返回站点文件名。
func (c *Client) WapUploadAttachment(board, filename string, data []byte) (string, error) {
	hash, token, err := c.ensureWapUpload(board)
	if err != nil {
		return "", err
	}
	return c.wap.UploadAttachment(hash, token, filename, data)
}

// WapPublish / WapReply 用 wap JSON API 发文，captcha 来自验证窗口。
func (c *Client) WapPublish(t WapPostTarget, subject, body, token string, captcha WapCaptcha) (string, error) {
	return c.wap.Publish(t.BoardHash, subject, body, token, captcha)
}

func (c *Client) WapReply(t WapPostTarget, body, token string, captcha WapCaptcha) (string, error) {
	return c.wap.Reply(t.ArticleHash, body, token, captcha)
}

// ClearPostAttachments 清空当前会话在指定版的临时附件，发帖前调用，避免旧附件混入。
func (c *Client) ClearPostAttachments(board string) {
	if c.Config().Source == "demo" {
		return
	}
	c.web.ClearAttachments(board)
}

// UploadPostAttachment 上传一个附件到水木临时附件区，返回可插入正文的 [upload=N][/upload] 标记。
func (c *Client) UploadPostAttachment(board, filename string, data []byte) (string, error) {
	if c.Config().Source == "demo" {
		return "[upload=1][/upload]", nil
	}
	return c.web.UploadAttachment(board, filename, data)
}

// MailBox 信箱。
func (c *Client) MailBox(box string, page int) (MailResult, error) {
	if c.Config().Source == "demo" {
		return MailResult{Items: demoMails(box), Page: page, Pages: 1}, nil
	}
	items, err := c.web.MailBox(box, page)
	if err != nil {
		return MailResult{}, err
	}
	return MailResult{Items: items, Page: page, Pages: 1}, nil
}

// MailDetail 读取 web 信件正文；wap 会话仍走 WapMessages。
func (c *Client) MailDetail(box string, id int64) (string, error) {
	return c.web.MailDetail(box, id)
}

// WapConversations 收件箱会话列表。
func (c *Client) WapConversations(page int) (MessageResult, error) { return c.wap.Conversations(page) }

// WapMessages 读取某个会话的消息。
func (c *Client) WapMessages(speakerID string, page int) (MailResult, error) {
	items, err := c.wap.Messages(speakerID, page)
	if err != nil {
		return MailResult{}, err
	}
	return MailResult{Items: items, Page: page, Pages: 1}, nil
}

// WapNotifies 读取一类提醒。
func (c *Client) WapNotifies(kind, page int) (NotifyResult, error) { return c.wap.Notifies(kind, page) }

// NotifyTarget 是一条提醒在版面里的落点：提醒项本身就是版面上的一篇文章，
// 跳过去要用英文版名 + 主题 hash 派生的数字主题号 + 文章所在楼层。
type NotifyTarget struct {
	Board      string `json:"board"`       // 英文版名
	BoardTitle string `json:"board_title"` // 中文版名
	ThreadID   int64  `json:"thread_id"`   // 前端用的数字主题号（由 topic hash 派生）
	Floor      int    `json:"floor"`       // 0 表示没查到，退化为只进主题
	Subject    string `json:"subject"`
}

// NotifyTarget 把一条提醒定位到版面上的真实文章。
// 派生出的数字主题号会登记进 wapHashByID，Thread/回帖才能透明地走 wap 读取
// （否则前端拿数字号去 web 找文章会 404）。
func (c *Client) NotifyTarget(board, boardTitle, topicHash, articleHash, subject string) (NotifyTarget, error) {
	board = strings.TrimSpace(board)
	if board == "" {
		return NotifyTarget{}, fmt.Errorf("这条提醒没有版面信息，无法跳转")
	}
	if topicHash == "" {
		return NotifyTarget{}, fmt.Errorf("这条提醒没有主题编号，无法跳转")
	}
	id := hashID(topicHash)
	out := NotifyTarget{Board: board, BoardTitle: boardTitle, ThreadID: id, Subject: subject}
	c.mu.Lock()
	c.wapHashByID[id] = topicHash
	c.mu.Unlock()
	floor, err := c.wap.ArticleFloor(topicHash, articleHash)
	if err != nil {
		c.note("提醒定位楼层失败：" + err.Error() + "，直接进入主题")
		return out, nil
	}
	out.Floor = floor
	return out, nil
}

// WapMarkNotifyRead 标记一条提醒已读。
func (c *Client) WapMarkNotifyRead(id string) error { return c.wap.MarkNotifyRead(id) }

// WapMarkNotifyReadAll 标记一类提醒全部已读。
func (c *Client) WapMarkNotifyReadAll(kind int) error { return c.wap.MarkNotifyReadAll(kind) }

// WapMarkMessageRead 标记一个会话已读。
func (c *Client) WapMarkMessageRead(speakerID string) error { return c.wap.MarkMessageRead(speakerID) }

// WapSendMessage 用 wap 接口发送站内信。
func (c *Client) WapSendMessage(to, body string) error { return c.wap.SendMessage(to, body) }

// WapReminders 汇总四类红点数量。
func (c *Client) WapReminders() (ReminderState, error) {
	if c.Config().Source == "demo" {
		return ReminderState{}, nil
	}
	return c.wap.Reminders()
}

// SendMail 发信。
func (c *Client) SendMail(to, title, content string) (Result, error) {
	if c.Config().Source == "demo" {
		return Result{OK: true, Message: "【演示模式】信件已保存到本地演示信箱"}, nil
	}
	if err := c.web.SendMail(to, title, content); err != nil {
		return Result{}, err
	}
	return Result{OK: true, Message: "信件已发出"}, nil
}

// Search 搜索（本地索引 + Web 回退）。
func (c *Client) Search(keyword, scope string) (SearchResult, error) {
	items := demoSearch(keyword, scope)
	add := func(ts []Thread) {
		for _, t := range ts {
			if containsFold(t.Subject, keyword) || containsFold(t.Author, keyword) {
				items = append(items, t)
			}
		}
	}
	switch c.Config().Source {
	case "openapi":
		// 官方平台无全站搜索方法，只能在驻版时间线里过滤。
		ts, _, err := c.apiRead.Threads("", 1)
		if err == nil {
			add(ts)
		} else {
			c.note("开放平台时间线读取失败：" + err.Error())
		}
	case "web":
		for _, b := range c.boards {
			ts, _, err := c.web.Threads(b.ID, 1)
			if err != nil {
				continue
			}
			add(ts)
		}
	}
	items = c.decorateThreads(ThreadsResult{Items: items}).Items
	return SearchResult{Items: items, Query: keyword, Scope: scope}, nil
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	return len(s) >= len(sub) && (stringsContainsFold(s, sub))
}

func stringsContainsFold(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && (indexOfFold(s, sub) >= 0)
}

func indexOfFold(s, sub string) int {
	// 简单的大小写不敏感包含判断，避免额外依赖。
	ls, lp := []rune(toLower(s)), []rune(toLower(sub))
	for i := 0; i+len(lp) <= len(ls); i++ {
		match := true
		for j := range lp {
			if ls[i+j] != lp[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := []rune(s)
	for i, r := range b {
		if r >= 'A' && r <= 'Z' {
			b[i] = r + 32
		}
	}
	return string(b)
}

func (c *Client) board(id string) Board {
	if b, ok := c.boards[id]; ok {
		return b
	}
	// term 里用户常用小写直接输入版名（newsoftware），而服务器返回的英文版名
	// 是 NewSoftware。大小写不一致时也要命中已缓存的中文名。
	for key, b := range c.boards {
		if strings.EqualFold(key, id) {
			return b
		}
	}
	if b, ok := c.apiRead.Board(id); ok {
		return b
	}
	return demoBoardByID(id)
}

// ensureBoard 保证版面信息里有真正的中文版名。
// 直接从「选择阅读讨论区」输入英文版名时不会经过版面列表，boards 缓存里
// 只有 demo 兜底（Name 等于 ID），此时按版名问一次 wap 搜索接口，把服务器
// 返回的 title（中文版名）补进缓存。查不到时保持原样，不影响正文读取。
func (c *Client) ensureBoard(id string) Board {
	id = strings.TrimSpace(id)
	if id == "" {
		return Board{}
	}
	if b := c.board(id); b.Name != "" && b.Name != b.ID {
		return b
	}
	if c.Config().Source == "demo" {
		return c.board(id)
	}
	if list, err := c.wap.SearchBoards(id); err == nil {
		for _, b := range list {
			if strings.EqualFold(b.ID, id) {
				c.mu.Lock()
				c.boards[b.ID] = b
				c.boards[id] = b
				c.mu.Unlock()
				return b
			}
		}
	}
	// wap 不可用时（未登录等），再从 web/openapi 的版列表本地匹配。
	if all, err := c.Boards(""); err == nil {
		for _, b := range all {
			if strings.EqualFold(b.ID, id) && b.Name != "" && b.Name != b.ID {
				c.mu.Lock()
				c.boards[id] = b
				c.mu.Unlock()
				return b
			}
		}
	}
	return c.board(id)
}

// Wiki 官方文档地址。
const Wiki = "https://wiki.open.newsmth.net/"
