package smth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store 负责本地持久化：配置、Cookie、收藏、已读进度、时间线计数。
// 所有数据放在 %APPDATA%\sfjth\ 下，不写注册表，绿色可迁移。
type Store struct {
	mu   sync.RWMutex
	dir  string
	cfg  Config
	read map[string]int // key: "board/threadID" -> 已读到第几楼
	seen map[string]int // key: "board/threadID" -> 上次看到的回复总数
}

// DefaultConfig 返回出厂配置。默认 demo 数据源，保证首次启动即可完整体验。
func DefaultConfig() Config {
	return Config{
		Source:       "demo",
		BaseURL:      "https://www.newsmth.net",
		RedirectURI:  "http://localhost/sfjth/oauth",
		TerminalCols: 80,
		TerminalRows: 24,
		FontSize:     20,
		ColorScheme:  "classic",
		Favorites:    []string{"NewExpress", "Programming", "ITExpress", "Family", "AutoWorld", "Joke"},
		ShowReadGray: true,
		FoldQuotes:   true,
		Preload:      true,
		MouseEnabled: true,
		LastUserID:   "",
		RememberID:   true,
		AutoLogin:    true,
		Big5:         false,
		LoginMode:    LoginAuto,
	}
}

// NewStore 创建（必要时新建）数据目录并载入已有配置。
func NewStore() *Store {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	dir = filepath.Join(dir, "sfjth")
	_ = os.MkdirAll(dir, 0o755)

	s := &Store{
		dir:  dir,
		cfg:  DefaultConfig(),
		read: map[string]int{},
		seen: map[string]int{},
	}
	s.load()
	return s
}

// Dir 返回数据目录，供设置界面显示。
func (s *Store) Dir() string { return s.dir }

func (s *Store) load() {
	if b, err := os.ReadFile(filepath.Join(s.dir, "config.json")); err == nil {
		var c Config
		if json.Unmarshal(b, &c) == nil {
			// 兼容旧版配置：新增的 bool 默认 true，旧文件里没有这两个键时补上。
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(b, &raw)
			if _, ok := raw["remember_id"]; !ok {
				c.RememberID = true
			}
			if _, ok := raw["auto_login"]; !ok {
				c.AutoLogin = true
			}
			if c.BaseURL == "" {
				c.BaseURL = "https://www.newsmth.net"
			}
			if c.TerminalCols == 0 {
				c.TerminalCols = 80
			}
			if c.TerminalRows == 0 {
				c.TerminalRows = 24
			}
			if c.FontSize == 0 {
				c.FontSize = 20
			}
			if c.Source == "" {
				c.Source = "demo"
			}
			if c.LoginMode == "" {
				c.LoginMode = LoginAuto
			}
			s.cfg = c
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.dir, "progress.json")); err == nil {
		var p struct {
			Read map[string]int `json:"read"`
			Seen map[string]int `json:"seen"`
		}
		if json.Unmarshal(b, &p) == nil {
			if p.Read != nil {
				s.read = p.Read
			}
			if p.Seen != nil {
				s.seen = p.Seen
			}
		}
	}
}

// Config 返回配置副本。
func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// configPatch 用指针区分「没传」和「传了零值」。
// 之前用值类型 Config 接收补丁时，前端只改 client_id 这样的局部设置
// 会把 remember_id / preload 等 bool 一起清零，这里彻底修掉。
type configPatch struct {
	Source       *string   `json:"source"`
	BaseURL      *string   `json:"base_url"`
	Cookie       *string   `json:"cookie"`
	User         *string   `json:"user"`
	ClientID     *string   `json:"client_id"`
	ClientSecret *string   `json:"client_secret"`
	ClientSign   *string   `json:"client_signature"`
	RedirectURI  *string   `json:"redirect_uri"`
	AccessToken  *string   `json:"access_token"`
	LastUserID   *string   `json:"last_user_id"`
	LoginMode    *string   `json:"login_mode"`
	TerminalCols *int      `json:"terminal_cols"`
	TerminalRows *int      `json:"terminal_rows"`
	FontSize     *int      `json:"font_size"`
	ColorScheme  *string   `json:"color_scheme"`
	Favorites    *[]string `json:"favorites"`
	ShowReadGray *bool     `json:"show_read_gray"`
	FoldQuotes   *bool     `json:"fold_quotes"`
	Preload      *bool     `json:"preload"`
	MouseEnabled *bool     `json:"mouse_enabled"`
	RememberID   *bool     `json:"remember_id"`
	AutoLogin    *bool     `json:"auto_login"`
	Big5         *bool     `json:"big5"`
}

// Patch 只合并补丁里显式出现的字段并持久化。
func (s *Store) Patch(patchJSON string) (Config, error) {
	var p configPatch
	if err := json.Unmarshal([]byte(patchJSON), &p); err != nil {
		return Config{}, fmt.Errorf("配置不是合法 JSON: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	str := func(v *string, dst *string) {
		if v != nil {
			*dst = *v
		}
	}
	num := func(v *int, dst *int) {
		if v != nil && *v > 0 {
			*dst = *v
		}
	}
	flag := func(v *bool, dst *bool) {
		if v != nil {
			*dst = *v
		}
	}
	if p.Source != nil {
		switch *p.Source {
		case "demo", "web", "openapi":
			s.cfg.Source = *p.Source
		default:
			return Config{}, fmt.Errorf("未知数据源 %q（可用 demo / web / openapi）", *p.Source)
		}
	}
	str(p.BaseURL, &s.cfg.BaseURL)
	str(p.Cookie, &s.cfg.Cookie)
	str(p.User, &s.cfg.User)
	str(p.ClientID, &s.cfg.ClientID)
	str(p.ClientSecret, &s.cfg.ClientSecret)
	str(p.ClientSign, &s.cfg.ClientSign)
	str(p.RedirectURI, &s.cfg.RedirectURI)
	str(p.AccessToken, &s.cfg.AccessToken)
	str(p.LastUserID, &s.cfg.LastUserID)
	if p.LoginMode != nil {
		s.cfg.LoginMode = normalizeLoginMode(*p.LoginMode)
	}
	str(p.ColorScheme, &s.cfg.ColorScheme)
	num(p.TerminalCols, &s.cfg.TerminalCols)
	num(p.TerminalRows, &s.cfg.TerminalRows)
	num(p.FontSize, &s.cfg.FontSize)
	if p.Favorites != nil {
		s.cfg.Favorites = *p.Favorites
	}
	flag(p.ShowReadGray, &s.cfg.ShowReadGray)
	flag(p.FoldQuotes, &s.cfg.FoldQuotes)
	flag(p.Preload, &s.cfg.Preload)
	flag(p.MouseEnabled, &s.cfg.MouseEnabled)
	flag(p.RememberID, &s.cfg.RememberID)
	flag(p.AutoLogin, &s.cfg.AutoLogin)
	flag(p.Big5, &s.cfg.Big5)
	s.flushLocked()
	return s.cfg, nil
}

// SetUser 记录当前登录用户。
func (s *Store) SetUser(user string) {
	s.mu.Lock()
	s.cfg.User = user
	s.flushLocked()
	s.mu.Unlock()
}

// SetCookie 记录 Web 会话 Cookie。
func (s *Store) SetCookie(cookie string) {
	s.mu.Lock()
	s.cfg.Cookie = cookie
	s.flushLocked()
	s.mu.Unlock()
}

// SetAccessToken 记录开放平台 token。
func (s *Store) SetAccessToken(token string) {
	s.mu.Lock()
	s.cfg.AccessToken = token
	s.flushLocked()
	s.mu.Unlock()
}

// Favorites 返回收藏版面。
func (s *Store) Favorites() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.cfg.Favorites))
	copy(out, s.cfg.Favorites)
	return out
}

// ToggleFavorite 切换收藏，返回是否已收藏。
func (s *Store) ToggleFavorite(board string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, b := range s.cfg.Favorites {
		if b == board {
			s.cfg.Favorites = append(s.cfg.Favorites[:i], s.cfg.Favorites[i+1:]...)
			s.flushLocked()
			return false
		}
	}
	s.cfg.Favorites = append(s.cfg.Favorites, board)
	s.flushLocked()
	return true
}

func progressKey(board string, id int64) string {
	return board + "/" + jsonNumber(id)
}

func jsonNumber(n int64) string {
	return strconv.FormatInt(n, 10)
}

// ReadFloor 返回上次阅读到的楼层（阅读记忆）。
func (s *Store) ReadFloor(board string, id int64) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.read[progressKey(board, id)]
}

// SeenCount 返回上次看到的回复总数，用于计算未读。
func (s *Store) SeenCount(board string, id int64) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seen[progressKey(board, id)]
}

// MarkRead 记录阅读进度与已见回复数。
func (s *Store) MarkRead(board string, id int64, floor, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := progressKey(board, id)
	if floor > s.read[k] {
		s.read[k] = floor
	}
	if count > s.seen[k] {
		s.seen[k] = count
	}
	s.saveProgressLocked()
}

// Unread 计算未读回复数。
func (s *Store) Unread(board string, id int64, count int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := s.seen[progressKey(board, id)]
	if count > seen {
		return count - seen
	}
	return 0
}

// IsRead 是否读过。
func (s *Store) IsRead(board string, id int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.read[progressKey(board, id)]
	return ok
}

func (s *Store) flushLocked() {
	b, _ := json.MarshalIndent(s.cfg, "", "  ")
	_ = os.WriteFile(filepath.Join(s.dir, "config.json"), b, 0o600)
}

func (s *Store) saveProgressLocked() {
	b, _ := json.Marshal(struct {
		Read map[string]int `json:"read"`
		Seen map[string]int `json:"seen"`
	}{s.read, s.seen})
	_ = os.WriteFile(filepath.Join(s.dir, "progress.json"), b, 0o600)
}

// cacheEntry 是内存缓存项。
type cacheEntry struct {
	value   any
	expires time.Time
}

// Cache 是一个带 TTL 的轻量内存缓存，用于「丝滑」翻页与预取。
type Cache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
}

// NewCache 创建缓存。
func NewCache() *Cache { return &Cache{m: map[string]cacheEntry{}} }

// Get 取缓存。
func (c *Cache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expires) {
		delete(c.m, key)
		return nil, false
	}
	return e.value, true
}

// Put 写缓存，ttl 为零表示 3 分钟。
func (c *Cache) Put(key string, value any, ttl time.Duration) {
	if ttl <= 0 {
		ttl = 3 * time.Minute
	}
	c.mu.Lock()
	c.m[key] = cacheEntry{value: value, expires: time.Now().Add(ttl)}
	c.mu.Unlock()
}

// Clear 清空缓存。
func (c *Cache) Clear() {
	c.mu.Lock()
	c.m = map[string]cacheEntry{}
	c.mu.Unlock()
}

// DeletePrefix 删除所有以 prefix 开头的缓存项。发文后用它精确失效正文/列表缓存。
func (c *Cache) DeletePrefix(prefix string) {
	c.mu.Lock()
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			delete(c.m, k)
		}
	}
	c.mu.Unlock()
}
