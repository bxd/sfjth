// Package smth 实现 sfjth 的数据层：官方开放平台 API、Web 数据源回退、
// 本地缓存 / 预取 / 阅读进度（"flow" 式流畅阅读）以及离线演示数据。
package smth

// Section 对应 term 主菜单「分类讨论区」中的一个分区。
type Section struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Desc  string `json:"desc"`
	Total int    `json:"total"`
}

// Board 对应一个版面。
type Board struct {
	ID       string `json:"id"`       // 英文版名，如 NewExpress
	Name     string `json:"name"`     // 中文版名
	Section  string `json:"section"`  // 所属分区
	Manager  string `json:"manager"`  // 版主
	Desc     string `json:"desc"`     // 版面说明 / 最新主题
	Users    int    `json:"users"`    // 在线人数
	Today    int    `json:"today"`    // 今日新帖
	Topics   int    `json:"topics"`   // 主题数
	Articles int    `json:"articles"` // 文章数
	Kind     string `json:"kind"`     // board | dir
}

// Attachment 是文章里的一个附件。URLs 按可用顺序保存（wap 实测 KS3 可达、
// exp CDN 偶发 502），预览/打开时依次尝试。
type Attachment struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	URL  string   `json:"url"`            // 首选绝对地址
	URLs []string `json:"urls,omitempty"` // 备用地址，按优先级排列
	Type string   `json:"type"`           // MIME，如 image/jpeg
	Size int64    `json:"size"`           // 字节数
	Kind string   `json:"kind"`           // image/audio/video/pdf/text/doc/excel/html/file

	// 用于最终兜底：wap 接口给的 cdnUrl 可能整体 502，此时按版面+主题
	// 去 web 版正文里找 static.mysmth.net/nForum/att/... 的真实附件地址。
	Board   string `json:"board,omitempty"`
	Subject string `json:"subject,omitempty"`
}

// Thread 是版面文章列表中的一行（同主题）。
type Thread struct {
	ID       int64  `json:"id"`
	HashID   string `json:"hash_id"` // wap 数据源里的 32 位 hash；数字 ID 由它确定性派生
	BoardID  string `json:"board_id"`
	Subject  string `json:"subject"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Time     int64  `json:"time"`
	Count    int    `json:"count"`  // 同主题文章数 / 回复数
	Pages    int    `json:"pages"`  // 分页页数
	Flag     string `json:"flag"`   // 置顶/精华标记，如 ◆ ★
	Attach   bool   `json:"attach"` // term 列表在标题前显示 @ 号
	Read     bool   `json:"read"`
	Unread   int    `json:"unread"` // 未读新回帖数（smthflow 风格）
	LastUser string `json:"last_user"`
}

// Post 是一篇文章（楼层）。
type Post struct {
	ID          int64        `json:"id"`
	HashID      string       `json:"hash_id"` // wap 文章 hash
	BoardID     string       `json:"board_id"`
	ThreadID    int64        `json:"thread_id"`
	Floor       int          `json:"floor"`
	Author      string       `json:"author"`
	Nick        string       `json:"nick"`
	Subject     string       `json:"subject"`
	Date        string       `json:"date"`
	Time        int64        `json:"time"`
	From        string       `json:"from"`
	Body        string       `json:"body"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Mail 站内信。
type Mail struct {
	ID      int64  `json:"id"`
	Key     string `json:"key,omitempty"` // wap 消息 id（字符串 hash）
	Box     string `json:"box,omitempty"` // web 信箱类型（inbox/outbox/deleted），读详情时用
	From    string `json:"from"`
	To      string `json:"to"`
	Title   string `json:"title"`
	Date    string `json:"date"`
	Time    int64  `json:"time"`
	Unread  bool   `json:"unread"`
	Content string `json:"content"`
}

// TopItem 十大话题。
type TopItem struct {
	Rank    int    `json:"rank"`
	BoardID string `json:"board_id"`
	Board   string `json:"board"`
	Title   string `json:"title"`
	Author  string `json:"author"`
	Count   int    `json:"count"`
	ID      int64  `json:"id"`
}

// Config 是持久化配置。
type Config struct {
	Source       string   `json:"source"` // demo | web | openapi
	BaseURL      string   `json:"base_url"`
	Cookie       string   `json:"cookie"`
	User         string   `json:"user"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	ClientSign   string   `json:"client_signature"`
	RedirectURI  string   `json:"redirect_uri"`
	AccessToken  string   `json:"access_token"`
	TerminalCols int      `json:"terminal_cols"`
	TerminalRows int      `json:"terminal_rows"`
	FontSize     int      `json:"font_size"`
	ColorScheme  string   `json:"color_scheme"`
	Favorites    []string `json:"favorites"`
	ShowReadGray bool     `json:"show_read_gray"`
	FoldQuotes   bool     `json:"fold_quotes"`
	Preload      bool     `json:"preload"`
	MouseEnabled bool     `json:"mouse_enabled"`

	// 登录屏相关：记住代号但不保存密码；会话复用；BIG5 标记。
	LastUserID string `json:"last_user_id"`
	RememberID bool   `json:"remember_id"`
	AutoLogin  bool   `json:"auto_login"`
	Big5       bool   `json:"big5"`
	LoginMode  string `json:"login_mode"` // auto | web | wap
}

// SessionInfo 是 term 进站登录屏需要的会话信息。
type SessionInfo struct {
	User       string `json:"user"`
	LastUserID string `json:"last_user_id"`
	HasSession bool   `json:"has_session"`
	Source     string `json:"source"`
	AutoLogin  bool   `json:"auto_login"`
	RememberID bool   `json:"remember_id"`
	Big5       bool   `json:"big5"`
	LoginMode  string `json:"login_mode"`
	Message    string `json:"message"`
	Online     int    `json:"online"`
	MaxOnline  int    `json:"max_online"`
	Guests     int    `json:"guests"`
}

// SessionCheck 是「复用已保存会话」的校验结果。
type SessionCheck struct {
	Valid   bool   `json:"valid"`
	User    string `json:"user"`
	Message string `json:"message"`
}

// Status 是前端顶栏/底栏需要的运行时状态。
type Status struct {
	Source    string `json:"source"`
	User      string `json:"user"`
	Online    bool   `json:"online"`
	Board     string `json:"board"`
	Message   string `json:"message"`
	Uptime    int64  `json:"uptime"`
	UnreadAll int    `json:"unread_all"`
	Wiki      string `json:"wiki"`
}

// ThreadsResult 版面列表页返回值。
type ThreadsResult struct {
	Board Board    `json:"board"`
	Items []Thread `json:"items"`
	Page  int      `json:"page"`
	Pages int      `json:"pages"`
	Mode  string   `json:"mode"`
}

// ThreadResult 文章阅读页返回值。
type ThreadResult struct {
	Board   Board  `json:"board"`
	Subject string `json:"subject"`
	Items   []Post `json:"items"`
	Page    int    `json:"page"`
	Pages   int    `json:"pages"`
	Total   int    `json:"total"`
	Resume  int    `json:"resume"` // 上次阅读到的楼层，用于「阅读记忆」
}

// MailResult 信箱列表。
type MailResult struct {
	Items []Mail `json:"items"`
	Page  int    `json:"page"`
	Pages int    `json:"pages"`
}

// SearchResult 搜索结果。
type SearchResult struct {
	Items []Thread `json:"items"`
	Query string   `json:"query"`
	Scope string   `json:"scope"`
}

// Result 通用操作结果。
type Result struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	ID      int64  `json:"id"`
}
