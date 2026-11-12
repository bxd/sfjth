package smth

// Web 数据源：模拟浏览器访问 nForum / 手机版页面，解析 GBK/UTF-8 HTML。
// 这是第三方客户端目前唯一稳定可用的通道 —— 官方开放平台 API 需要审批 APP Client，
// 而 hotfix 在 NewSoftware 版也建议桌面客户端「用 webview + 解析 web 站」。
//
// 页面结构取样（2026-11）：
//   /nForum/section/<id>          版面列表，<td class="title_1">…title_7
//   /nForum/board/<board>?p=<n>   主题列表，<td class="title_8">…title_12
//   /nForum/article/<board>/<id>?p=<n>  文章正文（游客会遇 Read verification）
//   /nForum/login                 登录（现带极验验证码，需浏览器登录后导入 Cookie）

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// ErrGuestChallenge 表示游客读帖验证码（登录后即消失）。
var ErrGuestChallenge = errors.New("站点要求游客读帖验证码：请在设置中登录或导入浏览器 Cookie（或用系统浏览器登录后复制 Cookie）")

// WebClient 是带 CookieJar 的 Web 数据源。
type WebClient struct {
	Base   string
	Cookie string
	User   string
	HTTP   *http.Client
}

// NewWebClient 创建 Web 数据源。
func NewWebClient(cfg Config) *WebClient {
	jar, _ := cookiejar.New(nil)
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://www.newsmth.net"
	}
	w := &WebClient{
		Base:   base,
		Cookie: cfg.Cookie,
		User:   cfg.User,
		HTTP:   &http.Client{Jar: jar, Timeout: 15 * time.Second},
	}
	if cfg.Cookie != "" {
		if u, err := url.Parse(base); err == nil {
			jar.SetCookies(u, parseCookieHeader(cfg.Cookie))
		}
	}
	return w
}

func parseCookieHeader(raw string) []*http.Cookie {
	out := []*http.Cookie{}
	for _, part := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[0] != "" {
			out = append(out, &http.Cookie{Name: kv[0], Value: kv[1]})
		}
	}
	return out
}

// Cookies 合并 jar 里的新会话与已保存的 Cookie 字符串（jar 优先）。
// 登录成功后用它把会话落盘，下次启动即可免密复用。
func (w *WebClient) Cookies() string {
	parts := []string{}
	seen := map[string]bool{}
	if u, err := url.Parse(w.Base); err == nil && w.HTTP != nil && w.HTTP.Jar != nil {
		for _, ck := range w.HTTP.Jar.Cookies(u) {
			if ck.Name == "" || seen[ck.Name] {
				continue
			}
			seen[ck.Name] = true
			parts = append(parts, ck.Name+"="+ck.Value)
		}
	}
	for _, ck := range parseCookieHeader(w.Cookie) {
		if ck.Name == "" || seen[ck.Name] {
			continue
		}
		seen[ck.Name] = true
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}

// Get 抓取一个页面并统一转成 UTF-8 字符串。
func (w *WebClient) Get(path string) (string, error) {
	if !strings.HasPrefix(path, "http") {
		path = w.Base + "/" + strings.TrimLeft(path, "/")
	}
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	w.setHeaders(req)
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	s := decodeBody(b, resp.Header.Get("Content-Type"))
	if isChallenge(s) {
		return "", ErrGuestChallenge
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, limit(stripTags(s), 100))
	}
	return s, nil
}

// PostForm 提交表单（登录、发文、回帖、发信）。referer 可选，回帖接口要求
// 带上具体文章页，否则站点可能按来源拒绝。
func (w *WebClient) PostForm(path string, form url.Values, referer ...string) (string, error) {
	if !strings.HasPrefix(path, "http") {
		path = w.Base + "/" + strings.TrimLeft(path, "/")
	}
	req, err := http.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	w.setHeaders(req)
	if len(referer) > 0 && referer[0] != "" {
		req.Header.Set("Referer", referer[0])
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	s := decodeBody(b, resp.Header.Get("Content-Type"))
	if isChallenge(s) {
		return "", ErrGuestChallenge
	}
	return s, nil
}

func (w *WebClient) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Referer", w.Base+"/nForum/")
	if ck := w.Cookies(); ck != "" {
		req.Header.Set("Cookie", ck)
	}
}

func decodeBody(b []byte, contentType string) string {
	lower := strings.ToLower(contentType)
	if strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8") {
		return string(b)
	}
	// nForum 页面声明 <meta charset="GBK">。
	if strings.Contains(lower, "gbk") || strings.Contains(lower, "gb2312") ||
		strings.Contains(strings.ToLower(string(b[:min(600, len(b))])), "charset=gbk") {
		r := transform.NewReader(strings.NewReader(string(b)), simplifiedchinese.GBK.NewDecoder())
		out, err := io.ReadAll(r)
		if err == nil {
			return string(out)
		}
	}
	return string(b)
}

func isChallenge(s string) bool {
	return strings.Contains(s, "guest-read-challenge") || strings.Contains(s, "Read verification")
}

// Login 尝试 Web 登录。当前站点登录带极验验证码，程序化登录可能失败，
// 此时给出可操作的提示（浏览器登录 -> 复制 Cookie -> 设置里导入）。
func (w *WebClient) Login(user, pass string) (string, error) {
	if user == "" || pass == "" {
		return "", errors.New("请输入用户名与密码")
	}
	form := url.Values{}
	form.Set("id", user)
	form.Set("passwd", pass)
	form.Set("CookieDate", "0")
	before := w.Cookies()
	body, err := w.PostForm("/nForum/login", form)
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(body)
	if strings.Contains(lower, "captcha") || strings.Contains(body, "验证码") ||
		strings.Contains(body, "登录失败") || strings.Contains(body, "密码错误") ||
		strings.Contains(body, "用户不存在") || strings.Contains(lower, `"ajax_code":"0005"`) {
		return "", errors.New("站点启用了登录验证码：请用系统浏览器登录水木，再在设置里按 C 导入 Cookie（按 O 打开原站）")
	}
	// 拿到新的会话 Cookie，或首页能识别出登录用户，都算成功。
	if ck := w.Cookies(); ck != "" && ck != before {
		w.Cookie = ck
		w.User = user
		return user, nil
	}
	if name, e := w.Ping(); e == nil && name != "" && name != "guest" {
		w.Cookie = w.Cookies()
		w.User = user
		return user, nil
	}
	return "", fmt.Errorf("站点未返回登录会话（多半是验证码拦截）：请用浏览器登录后按 C 导入 Cookie。响应片段：%s",
		limit(stripTags(body), 120))
}

// Ping 检查当前会话是否可用。
func (w *WebClient) Ping() (string, error) {
	body, err := w.Get("/nForum/")
	if err != nil {
		return "", err
	}
	// 登录后页面会出现用户 ID。
	if m := regexp.MustCompile(`u_login_out|退出登录|注销`).FindString(body); m != "" {
		if w.User == "" {
			return "已登录", nil
		}
		return w.User, nil
	}
	return "guest", nil
}

// Sections 从首页/分区页解析分区列表；失败时回落到水木经典十大分区。
func (w *WebClient) Sections() ([]Section, error) {
	body, err := w.Get("/nForum/")
	if err != nil {
		return classicSections(), nil
	}
	seen := map[string]bool{}
	out := []Section{}
	re := regexp.MustCompile(`href="/nForum/(?:section|fav)/([A-Za-z0-9_]+)"[^>]*>([^<]{2,30})</a>`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		id, name := m[1], strings.TrimSpace(html.UnescapeString(m[2]))
		if seen[id] || name == "" {
			continue
		}
		seen[id] = true
		out = append(out, Section{ID: id, Name: name, Desc: "分类讨论区"})
	}
	if len(out) == 0 {
		return classicSections(), nil
	}
	return out, nil
}

// Boards 解析某个分区下的版面。
func (w *WebClient) Boards(section string) ([]Board, error) {
	if section == "" {
		section = "1"
	}
	body, err := w.Get("/nForum/section/" + url.PathEscape(section))
	if err != nil {
		return nil, err
	}
	out := []Board{}
	rowRe := regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	linkRe := regexp.MustCompile(`href="/nForum/(board|section)/([^"?]+)"[^>]*>([^<]*)</a>`)
	numRe := regexp.MustCompile(`class="title_[4567][^"]*"[^>]*>\s*([\d,]+)\s*<`)
	managerRe := regexp.MustCompile(`(?s)class="title_2"[^>]*>(.*?)</td>`)
	for _, row := range rowRe.FindAllStringSubmatch(body, -1) {
		tr := row[1]
		lm := linkRe.FindStringSubmatch(tr)
		if lm == nil {
			continue
		}
		b := Board{
			ID:      lm[2],
			Name:    strings.TrimSpace(html.UnescapeString(lm[3])),
			Section: section,
			Kind:    lm[1],
		}
		if bm := managerRe.FindStringSubmatch(tr); bm != nil {
			b.Manager = strings.TrimSpace(stripTags(bm[1]))
		}
		nums := numRe.FindAllStringSubmatch(tr, -1)
		vals := []int{}
		for _, n := range nums {
			v, _ := strconv.Atoi(strings.ReplaceAll(n[1], ",", ""))
			vals = append(vals, v)
		}
		if len(vals) >= 4 {
			b.Users, b.Today, b.Topics, b.Articles = vals[0], vals[1], vals[2], vals[3]
		}
		if link := regexp.MustCompile(`class="title_3"[^>]*>(.*?)</td>`).FindStringSubmatch(tr); link != nil {
			b.Desc = strings.TrimSpace(stripTags(link[1]))
		}
		out = append(out, b)
	}
	return out, nil
}

// Threads 解析版面主题列表。
func (w *WebClient) Threads(board string, page int) ([]Thread, int, error) {
	if page < 1 {
		page = 1
	}
	body, err := w.Get(fmt.Sprintf("/nForum/board/%s?p=%d", url.PathEscape(board), page))
	if err != nil {
		return nil, 1, err
	}
	out := []Thread{}
	rowRe := regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	artRe := regexp.MustCompile(`/nForum/article/[^"/]+/(\d+)`)
	titleRe := regexp.MustCompile(`(?s)class="title_9"[^>]*>\s*<a[^>]*>(.*?)</a>`)
	dateRe := regexp.MustCompile(`(?s)class="title_10"[^>]*>(.*?)</td>`)
	authorRe := regexp.MustCompile(`(?s)class="title_12"[^>]*>(.*?)</td>`)
	for _, row := range rowRe.FindAllStringSubmatch(body, -1) {
		tr := row[1]
		tm := titleRe.FindStringSubmatch(tr)
		am := artRe.FindStringSubmatch(tr)
		if tm == nil || am == nil {
			continue
		}
		id, _ := strconv.ParseInt(am[1], 10, 64)
		subject := strings.TrimSpace(stripTags(tm[1]))
		if subject == "" {
			continue
		}
		t := Thread{
			ID: id, BoardID: board, Subject: subject,
			Flag: " ", Count: 1, Pages: 1,
		}
		if dm := dateRe.FindStringSubmatch(tr); dm != nil {
			t.Date = strings.TrimSpace(stripTags(dm[1]))
		}
		if um := authorRe.FindStringSubmatch(tr); um != nil {
			t.Author = strings.TrimSpace(strings.Trim(stripTags(um[1]), "| 　"))
		}
		if strings.Contains(tr, "ico-pos-article-top") {
			t.Flag = "◆"
		} else if strings.Contains(tr, "ico-pos-article-light") {
			t.Flag = "★"
		}
		t.Attach = strings.Contains(tr, "ico-pos-article-attach")
		// threads-tab 中的最大页码即同主题页数。
		pages := 1
		for _, pm := range regexp.MustCompile(`\?p=(\d+)"[^>]*>\d+</a>`).FindAllStringSubmatch(tr, -1) {
			if v, _ := strconv.Atoi(pm[1]); v > pages {
				pages = v
			}
		}
		t.Pages = pages
		t.Count = pages * 10
		out = append(out, t)
	}
	totalPages := parseTotalPages(body)
	return out, totalPages, nil
}

// webArticleAnchorRe 定义在 attachment.go：正文页各楼层的锚点。
var webArticleReplyRe = regexp.MustCompile(`(?i)/article/[^"'\s<>]*?/post/(\d+)`)

// parseWebArticleIDs 从正文页抽取「楼层 -> 真实文章 ID」。
// nForum 每层以 <a name="aN"> 开头（N = 楼层 - 1，分页后仍是全局楼层），
// 后面紧跟该层的回复链接 /nForum/article/<board>/post/<文章ID>。
// wap 数据层只有 32 位 hash，回帖时必须换成这里的数字 ID。
func parseWebArticleIDs(body string) map[int]int64 {
	out := map[int]int64{}
	locs := webArticleAnchorRe.FindAllStringSubmatchIndex(body, -1)
	for i, loc := range locs {
		n, err := strconv.Atoi(body[loc[2]:loc[3]])
		if err != nil {
			continue
		}
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		m := webArticleReplyRe.FindStringSubmatch(body[loc[1]:end])
		if m == nil {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil && id > 0 {
			out[n+1] = id
		}
	}
	return out
}

// ArticleIDByFloor 取指定楼层的真实文章 ID，用于回复特定楼层。
func (w *WebClient) ArticleIDByFloor(board string, threadID int64, floor int) (int64, error) {
	if floor < 1 || threadID <= 0 {
		return 0, fmt.Errorf("楼层或主题 ID 无效")
	}
	page := (floor-1)/10 + 1
	body, err := w.Get(fmt.Sprintf("/nForum/article/%s/%d?p=%d", url.PathEscape(board), threadID, page))
	if err != nil {
		return 0, err
	}
	if id := parseWebArticleIDs(body)[floor]; id > 0 {
		return id, nil
	}
	return 0, fmt.Errorf("未能定位第 %d 楼的回复地址", floor)
}

// Posts 解析文章正文页。优先按「发信人 / 标题 / 发信站」文本切分，
// 兼容 nForum 与手机版两种排版。
func (w *WebClient) Posts(board string, id int64, page int) ([]Post, string, int, int, error) {
	path := fmt.Sprintf("/nForum/article/%s/%d?p=%d", url.PathEscape(board), id, page)
	body, err := w.Get(path)
	if err != nil {
		return nil, "", 1, 0, err
	}
	text := htmlToText(body)
	articleIDs := parseWebArticleIDs(body)
	subject := ""
	if sm := regexp.MustCompile(`标\s*题:\s*([^\r\n]+)`).FindStringSubmatch(text); sm != nil {
		subject = strings.TrimSpace(sm[1])
	}
	pages := parseTotalPages(body)
	posts := []Post{}
	re := regexp.MustCompile(`(?s)发信人:\s*(\S+)\s*(?:\(([^)]*)\))?[^\r\n]*\r?\n`)
	locs := re.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := text[loc[0]:end]
		head := re.FindStringSubmatch(block)
		floor := (page-1)*10 + i + 1
		p := Post{
			ID: id, BoardID: board, ThreadID: id,
			Floor:  floor,
			Author: head[1], Nick: head[2],
			Subject: subject,
		}
		if realID := articleIDs[floor]; realID > 0 {
			p.ID = realID
		}
		if sm := regexp.MustCompile(`标\s*题:\s*([^\r\n]+)`).FindStringSubmatch(block); sm != nil {
			p.Subject = strings.TrimSpace(sm[1])
		}
		if dm := regexp.MustCompile(`发信站:\s*([^\r\n]+)`).FindStringSubmatch(block); dm != nil {
			p.Date = strings.TrimSpace(dm[1])
			p.From = p.Date
		}
		if idx := strings.Index(block, "发信站:"); idx >= 0 {
			if nl := strings.Index(block[idx:], "\n"); nl >= 0 {
				p.Body = strings.TrimSpace(block[idx+nl+1:])
			}
		}
		p.Body = strings.TrimSuffix(p.Body, "※ 来源:·水木社区")
		posts = append(posts, p)
	}
	if len(posts) == 0 {
		clean := strings.TrimSpace(text)
		if clean == "" {
			clean = "（本页没有可解析的正文，站点结构可能已变化。可在设置里切换数据源或用 O 打开原站。）"
		}
		posts = append(posts, Post{
			ID: id, BoardID: board, ThreadID: id, Floor: (page-1)*10 + 1,
			Author: "guest", Subject: subject, Date: time.Now().Format("2006-01-02"),
			Body: limit(clean, 4000),
		})
	}
	return posts, subject, pages, pages * 10, nil
}

// PostArticle 发表 / 回复文章。replyID 为要回复的那一层的真实文章 ID；
// 0 表示发表新主题。站点实际提交到 ajax_post.json，表单里的 id 即回复目标。
func (w *WebClient) PostArticle(board, subject, content string, replyID int64) error {
	form := url.Values{}
	form.Set("subject", subject)
	form.Set("content", content)
	form.Set("signature", "0")
	form.Set("id", strconv.FormatInt(replyID, 10))
	path := "/nForum/article/" + url.PathEscape(board) + "/ajax_post.json"
	referer := w.Base + "/nForum/article/" + url.PathEscape(board)
	if replyID > 0 {
		referer += "/post/" + strconv.FormatInt(replyID, 10)
	} else {
		referer += "/post"
	}
	body, err := w.PostForm(path, form, referer)
	if err != nil {
		return err
	}
	var reply struct {
		Status  int    `json:"ajax_st"`
		Code    string `json:"ajax_code"`
		Message string `json:"ajax_msg"`
		List    []struct {
			Text string `json:"text"`
			URL  string `json:"url"`
		} `json:"list"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err == nil {
		if reply.Status != 1 {
			msg := reply.Message
			if msg == "" {
				msg = "站点拒绝了该请求"
			}
			return fmt.Errorf("发文失败：%s", limit(msg, 120))
		}
		// 站点对「未过验证码 / 无发文本权限」的提交会返回 ajax_st:1 的假成功：
		// list 里唯一的文章链接是 /article/<board>/11 这种占位符，正文实际未落库。
		// 真实成功的链接指向一篇新文章号（6 位以上）。据此识别，避免误报成功。
		if isFakePostSuccess(reply.List) {
			return fmt.Errorf("站点未接受本次发文（可能需要网页验证或未开通发文权限），请在网页发帖弹窗中完成")
		}
		return nil
	}
	if strings.Contains(body, "错误") || strings.Contains(body, "失败") || strings.Contains(body, "验证码") {
		return fmt.Errorf("发文失败：%s", limit(stripTags(body), 120))
	}
	return nil
}

// isFakePostSuccess 判断 ajax_post.json 的成功响应是不是站点给的「假成功」。
// 假成功只有一个指向 .../11 的占位链接；真实成功给出新建文章的真实 ID。
func isFakePostSuccess(list []struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}) bool {
	for _, it := range list {
		m := regexp.MustCompile(`/article/[^/]+/(\d+)`).FindStringSubmatch(it.URL)
		if len(m) == 2 {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			// 真实文章号至少 6 位；11 是站点占位符。
			return id < 100000
		}
	}
	// 没有任何文章链接也可疑：真实成功至少返回「查看文章」链接。
	return true
}

// ClearAttachments 清空当前会话在该版的临时附件列表，避免上次残留的附件被带进新文章。
func (w *WebClient) ClearAttachments(board string) {
	path := "/nForum/att/" + url.PathEscape(board) + "/ajax_list.json"
	body, err := w.Get(path)
	if err != nil {
		return
	}
	var items []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &items); err != nil {
		return
	}
	ref := w.Base + "/nForum/article/" + url.PathEscape(board) + "/post"
	for _, it := range items {
		if it.Name == "" {
			continue
		}
		u := w.Base + "/nForum/att/" + url.PathEscape(board) + "/ajax_delete.json?name=" + url.QueryEscape(it.Name)
		req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(""))
		if err != nil {
			continue
		}
		w.setHeaders(req)
		req.Header.Set("Referer", ref)
		resp, err := w.HTTP.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
}

// UploadAttachment 上传一个附件到该版的临时附件区，返回 [upload=N][/upload] 标记。
// 水木附件接口 ajax_add.json 不需要验证码，程序可直接上传；N 是本次会话内的序号。
func (w *WebClient) UploadAttachment(board, filename string, data []byte) (string, error) {
	path := "/nForum/att/" + url.PathEscape(board) + "/ajax_add.json?name=" + url.QueryEscape(filename)
	u := w.Base + path
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(string(data)))
	if err != nil {
		return "", err
	}
	w.setHeaders(req)
	// 站点 PLUpload 的 html5 runtime 会带这个头；不带时服务端直接返回 404 空响应，
	// 客户端只能报「响应无法解析」（实测 2026-11）。
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Referer", w.Base+"/nForum/article/"+url.PathEscape(board)+"/post")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	s := decodeBody(b, resp.Header.Get("Content-Type"))
	var out struct {
		No      int    `json:"no"`
		Name    string `json:"name"`
		Status  int    `json:"ajax_st"`
		Message string `json:"ajax_msg"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return "", fmt.Errorf("上传附件失败：响应无法解析")
	}
	if out.Status != 1 || out.No <= 0 {
		msg := out.Message
		if msg == "" {
			msg = "站点拒绝"
		}
		return "", fmt.Errorf("上传附件失败：%s", limit(msg, 80))
	}
	return fmt.Sprintf("[upload=%d][/upload]", out.No), nil
}

// SendMail 发送站内信。
func (w *WebClient) SendMail(to, title, content string) error {
	form := url.Values{}
	form.Set("userid", to)
	form.Set("title", title)
	form.Set("content", content)
	_, err := w.PostForm("/nForum/mail/send", form)
	return err
}

// MailBox 读取 web 信箱列表。nForum 的邮件行是表格结构：
// <tr class="no-read"><td><input name="m_2"...><td><a>发信人</a><td><a>标题</a><td>时间</tr>
// no-read 表示未读；详情地址是 /nForum/mail/<box>/<id>.json。
func (w *WebClient) MailBox(box string, page int) ([]Mail, error) {
	path := fmt.Sprintf("/nForum/mail/%s?p=%d", url.PathEscape(box), page)
	body, err := w.Get(path)
	if err != nil {
		return []Mail{}, nil // 信箱属于隐私接口，失败时安静降级
	}
	rowRe := regexp.MustCompile(`(?is)<tr([^>]*)>.*?name="m_(\d+)".*?</td>\s*<td[^>]*>\s*<a[^>]*>(.*?)</a>\s*</td>\s*<td[^>]*>\s*<a[^>]*>(.*?)</a>\s*</td>\s*<td[^>]*>(.*?)</td>`)
	out := []Mail{}
	for _, m := range rowRe.FindAllStringSubmatch(body, -1) {
		id, _ := strconv.ParseInt(m[2], 10, 64)
		if id < 0 {
			continue
		}
		out = append(out, Mail{
			ID: id, Box: box,
			From:   strings.TrimSpace(stripTags(m[3])),
			Title:  strings.TrimSpace(stripTags(m[4])),
			Date:   strings.TrimSpace(stripTags(m[5])),
			Unread: strings.Contains(strings.ToLower(m[1]), "no-read"),
		})
	}
	return out, nil
}

// MailDetail 读取 web 信件正文（GBK JSON，content 是 HTML 片段）。
// 站点的 .json 详情接口必须带 X-Requested-With，否则直接 404 空响应。
func (w *WebClient) MailDetail(box string, id int64) (string, error) {
	u := w.Base + fmt.Sprintf("/nForum/mail/%s/%d.json", url.PathEscape(box), id)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	w.setHeaders(req)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", w.Base+"/nForum/mail/"+url.PathEscape(box))
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := decodeBody(b, resp.Header.Get("Content-Type"))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r struct {
		Content string `json:"content"`
		AjaxSt  int    `json:"ajax_st"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return "", err
	}
	if r.AjaxSt != 0 && r.AjaxSt != 1 {
		return "", fmt.Errorf("读取信件失败")
	}
	return htmlToText(r.Content), nil
}

// TopTen 读取当日十大（首页 hot 区块）。
func (w *WebClient) TopTen() ([]TopItem, error) {
	body, err := w.Get("/nForum/")
	if err != nil {
		return nil, err
	}
	text := htmlToText(body)
	re := regexp.MustCompile(`(?m)^\s*(\d{1,2})[\.、]\s*(.+?)\s+\[([^\]]+)\]\s*$`)
	out := []TopItem{}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		rank, _ := strconv.Atoi(m[1])
		out = append(out, TopItem{Rank: rank, Title: strings.TrimSpace(m[2]), Board: m[3]})
	}
	return out, nil
}

// ---------- HTML 工具 ----------

func parseTotalPages(s string) int {
	max := 1
	for _, m := range regexp.MustCompile(`[?&]p=(\d+)`).FindAllStringSubmatch(s, -1) {
		v, _ := strconv.Atoi(m[1])
		if v > max && v < 10000 {
			max = v
		}
	}
	return max
}

var (
	brRe   = regexp.MustCompile(`(?i)<\s*br\s*/?\s*>`)
	tagRe  = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe   = regexp.MustCompile(`[ \t]+`)
	lineRe = regexp.MustCompile(`\n{3,}`)
	// Go 的 RE2 不支持反向引用 \1，不能用 <(script|style)...></\1>。
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
)

func stripTags(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`(?i)</(p|div|tr|li|h[1-6]|pre)>`).ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

func htmlToText(s string) string {
	s = scriptStyleRe.ReplaceAllString(s, "")
	s = html.UnescapeString(stripTags(s))
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = wsRe.ReplaceAllString(s, " ")
	s = lineRe.ReplaceAllString(s, "\n\n")
	return s
}

func classicSections() []Section {
	return []Section{
		{ID: "0", Name: "本站系统", Desc: "系统公告与服务"},
		{ID: "1", Name: "国内院校", Desc: "兄弟院校版面"},
		{ID: "2", Name: "休闲娱乐", Desc: "影视音乐游戏"},
		{ID: "3", Name: "五湖四海", Desc: "各地风土人情"},
		{ID: "4", Name: "电脑技术", Desc: "软硬件与网络"},
		{ID: "5", Name: "学术科学", Desc: "理工与人文"},
		{ID: "6", Name: "艺术文化", Desc: "文学艺术摄影"},
		{ID: "7", Name: "体育运动", Desc: "球类与健身"},
		{ID: "8", Name: "财经时事", Desc: "经济与新闻"},
		{ID: "9", Name: "生活百态", Desc: "衣食住行情感"},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
