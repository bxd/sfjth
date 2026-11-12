package smth

// 附件的地址归一化、类型识别、受限代理下载与内联预览。
// wap 接口给出的 cdnUrl 偶发 502，ks3Url 稳定，因此统一保留多个候选地址并逐个尝试。
// 前端不直接访问外网：所有附件字节经本文件按白名单代理，既带上站点 Cookie，
// 又避免把 Cookie 暴露给任意 URL。

import (
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	maxInlineSmall = 12 << 20 // 图片 / 文本 / HTML / PDF 内联上限
	maxInlineMedia = 48 << 20 // 音频 / 视频内联上限
	maxDownload    = 512 << 20
)

// AttachmentPreview 是给前端渲染附件预览的结果。
type AttachmentPreview struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
	DataURL  string `json:"data_url,omitempty"`
	Inline   bool   `json:"inline"`    // true 表示 data_url 可直接用于 img/audio/video/iframe
	TooLarge bool   `json:"too_large"` // 超过内联上限，前端改为「用系统程序打开」
}

// AttachmentKind 按 MIME / 扩展名把附件归入前端可预览的类型。
func AttachmentKind(name, mimeType string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	mt := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch {
	case strings.HasPrefix(mt, "image/") || contains(ext, "jpg", "jpeg", "png", "gif", "bmp", "webp", "svg"):
		return "image"
	case strings.HasPrefix(mt, "audio/") || contains(ext, "mp3", "wav", "m4a", "aac", "ogg", "flac", "wma"):
		return "audio"
	case strings.HasPrefix(mt, "video/") || contains(ext, "mp4", "mkv", "avi", "mov", "webm", "wmv", "flv"):
		return "video"
	case mt == "application/pdf" || ext == "pdf":
		return "pdf"
	case mt == "text/html" || contains(ext, "html", "htm"):
		return "html"
	case strings.HasPrefix(mt, "text/") || contains(ext, "txt", "md", "log", "ini", "cfg", "json", "xml", "csv", "go", "py", "js", "ts", "css", "c", "h", "cpp", "java", "sh", "sql"):
		return "text"
	case contains(ext, "doc", "docx", "rtf", "odt", "wps"):
		return "doc"
	case contains(ext, "xls", "xlsx", "ods", "et"):
		return "excel"
	default:
		return "file"
	}
}

func contains(v string, list ...string) bool {
	for _, s := range list {
		if v == s {
			return true
		}
	}
	return false
}

// allowedAttachmentURL 只允许水木及 KS3 的附件路径，防止前端借代理探测内网。
func allowedAttachmentURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	okHost := host == "newsmth.net" || strings.HasSuffix(host, ".newsmth.net") ||
		host == "mysmth.net" || strings.HasSuffix(host, ".mysmth.net") ||
		host == "ksyun.com" || strings.HasSuffix(host, ".ksyun.com")
	if !okHost {
		return false
	}
	p := strings.ToLower(u.Path)
	okPath := strings.HasPrefix(p, "/attachment/") || strings.HasPrefix(p, "/file/") ||
		strings.HasPrefix(p, "/nforum/attachment/") || strings.HasPrefix(p, "/nforum/att/")
	if !okPath {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == ".." || seg == "." {
			return false
		}
	}
	return true
}

// attachmentHashRe 从各种附件地址里抽出 32 位十六进制附件 hash。
var attachmentHashRe = regexp.MustCompile(`([0-9a-fA-F]{32})`)

// webAttachmentURLs 为同一个附件生成 web 主站（nForum）候选地址。
// wap 与 web 的附件 ID 就是同一串 32 位 hash，换域名/路径即可互相取同一附件；
// 当 wap 给的 KS3/CDN/wap 地址都失败时，用这些 web 地址再试，提升成功率。
func webAttachmentURLs(a Attachment) []string {
	hashes := []string{}
	seenHash := map[string]bool{}
	addHash := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if len(h) == 32 && !seenHash[h] && attachmentHashRe.MatchString(h) {
			seenHash[h] = true
			hashes = append(hashes, h)
		}
	}
	addHash(a.ID)
	// 地址里出现的 hash 也要收集：KS3 用的是独立的 hash（与 a.ID 不同），
	// web 站有时按 KS3 hash 索引，两种都试。
	for _, u := range append(append([]string{}, a.URLs...), a.URL) {
		for _, m := range attachmentHashRe.FindAllString(u, -1) {
			addHash(m)
		}
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(u string) {
		if u != "" && !seen[u] && allowedAttachmentURL(u) {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, h := range hashes {
		// web 主站 nForum 的附件路径；/file/ 是老版下载路径，一并兜底。
		add("https://www.newsmth.net/attachment/" + h)
		add("https://www.newsmth.net/nForum/attachment/" + h)
	}
	return out
}

func attachmentCandidates(a Attachment) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(u string) {
		if u == "" || seen[u] || !allowedAttachmentURL(u) {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	for _, u := range a.URLs {
		add(u)
	}
	add(a.URL)
	// wap 地址全部失败后，再用 web 主站地址取同一个附件。
	for _, u := range webAttachmentURLs(a) {
		add(u)
	}
	return out
}

// webAttRefRe 匹配 web 正文里 nForum 的附件链接：
// //static.mysmth.net/nForum/att/NewSoftware/295666/579
var webAttRefRe = regexp.MustCompile(`(?i)(?:https?:)?//static\.mysmth\.net/nForum/att/([A-Za-z0-9_.-]+)/(\d+)/(\d+)`)

// webStaticAttachmentURLs 是最后一层兜底：wap 接口给出的 cdnUrl 有可能整体 502，
// 但 web 版正文仍嵌着 static.mysmth.net/nForum/att/... 的真实附件地址。
// 用版面名+主题标题在 web 版面列表里找到数字文章号，再从正文提取附件链接。
func webStaticAttachmentURLs(a Attachment, cookie string) []string {
	if a.Board == "" || a.Subject == "" {
		return nil
	}
	aid := webArticleIDBySubject(a.Board, a.Subject, cookie)
	if aid == "" {
		return nil
	}
	u := "https://www.newsmth.net/nForum/article/" + url.PathEscape(a.Board) + "/" + aid
	body, err := fetchGBPage(u, cookie, "https://www.newsmth.net/nForum/")
	if err != nil {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, m := range webAttRefRe.FindAllStringSubmatch(body, -1) {
		if !strings.EqualFold(m[1], a.Board) || m[2] != aid {
			continue
		}
		link := "https://static.mysmth.net/nForum/att/" + m[1] + "/" + m[2] + "/" + m[3]
		if !seen[link] {
			seen[link] = true
			out = append(out, link)
		}
	}
	return out
}

// webArticleAnchorRe 把 web 正文页切成一层一层的文章块。
var webArticleAnchorRe = regexp.MustCompile(`(?i)<a name="a(\d+)"></a>`)

// webArticleBlock 取指定楼层（从 1 开始）的 HTML 片段，避免把别的楼层的图片
// 误并到当前层；找不到锚点时退回整页（首帖）。
func webArticleBlock(body string, floor int) string {
	locs := webArticleAnchorRe.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		return body
	}
	target := floor - 1
	if target < 0 {
		target = 0
	}
	for i, loc := range locs {
		idx, err := strconv.Atoi(body[loc[2]:loc[3]])
		if err != nil || idx != target {
			continue
		}
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		return body[loc[1]:end]
	}
	return body
}

// webAttachmentsForArticle 从 web 正文里读出某一层的全部附件。
// wap 接口有时只报第一个附件（如首帖 t1.png），其余图片只在 web 正文里，
// 因此这里按楼层把 static.mysmth.net/nForum/att/... 全部提取出来。
func webAttachmentsForArticle(board, subject, cookie string, floor int) []Attachment {
	aid := webArticleIDBySubject(board, subject, cookie)
	if aid == "" {
		return nil
	}
	u := "https://www.newsmth.net/nForum/article/" + url.PathEscape(board) + "/" + aid
	body, err := fetchGBPage(u, cookie, "https://www.newsmth.net/nForum/")
	if err != nil {
		return nil
	}
	segment := webArticleBlock(body, floor)
	out := []Attachment{}
	seen := map[string]bool{}
	for _, m := range webAttRefRe.FindAllStringSubmatch(segment, -1) {
		if !strings.EqualFold(m[1], board) || m[2] != aid || seen[m[3]] {
			continue
		}
		seen[m[3]] = true
		link := "https://static.mysmth.net/nForum/att/" + m[1] + "/" + m[2] + "/" + m[3]
		name, typ, size, err := webAttachmentMeta(link, cookie)
		if err != nil {
			continue
		}
		out = append(out, Attachment{
			ID: "web-" + aid + "-" + m[3], Name: name, Type: typ, Size: size,
			Kind: AttachmentKind(name, typ), URL: link, URLs: []string{link},
			Board: board, Subject: subject,
		})
	}
	return out
}

// webAttachmentMeta 用 HEAD 从静态附件响应头取文件名、MIME 与字节数。
func webAttachmentMeta(raw, cookie string) (string, string, int64, error) {
	req, err := http.NewRequest(http.MethodHead, raw, nil)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", "https://www.newsmth.net/nForum/")
	host := strings.ToLower(req.URL.Hostname())
	if (strings.HasSuffix(host, "newsmth.net") || strings.HasSuffix(host, "mysmth.net")) && cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	name := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			name = params["filename"]
		}
		if name == "" {
			if m := regexp.MustCompile(`(?i)filename\*?=(?:"([^"]+)"|([^;]+))`).FindStringSubmatch(cd); len(m) > 0 {
				name = strings.TrimSpace(m[1])
				if name == "" {
					name = strings.TrimSpace(m[2])
				}
			}
		}
	}
	if name == "" {
		name = "attachment-" + raw[strings.LastIndex(raw, "/")+1:]
	}
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return name, resp.Header.Get("Content-Type"), size, nil
}

// webArticleIDBySubject 从 web 版面列表按标题反查数字文章号（nForum 附件路径需要它）。
func webArticleIDBySubject(board, subject, cookie string) string {
	want := normalizeSubject(subject)
	if want == "" {
		return ""
	}
	re := regexp.MustCompile(`(?i)/nForum/article/` + regexp.QuoteMeta(board) + `/(\d+)"[^>]*>([^<]*)</a>`)
	for page := 1; page <= 3; page++ {
		u := "https://www.newsmth.net/nForum/board/" + url.PathEscape(board)
		if page > 1 {
			u += fmt.Sprintf("?p=%d", page)
		}
		body, err := fetchGBPage(u, cookie, "https://www.newsmth.net/nForum/")
		if err != nil {
			return ""
		}
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			got := normalizeSubject(m[2])
			if got == want || (len(got) > 4 && strings.Contains(want, got)) ||
				(len(want) > 4 && strings.Contains(got, want)) {
				return m[1]
			}
		}
	}
	return ""
}

// fetchGBPage 读取 web 版 GB18030 页面并转成 UTF-8 字符串。
func fetchGBPage(raw, cookie, referer string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0")
	req.Header.Set("Accept", "text/html,*/*")
	req.Header.Set("Referer", referer)
	host := strings.ToLower(req.URL.Hostname())
	if (strings.HasSuffix(host, "newsmth.net") || strings.HasSuffix(host, "mysmth.net")) && cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(simplifiedchinese.GB18030.NewDecoder().Reader(resp.Body), 4<<20))
	return string(b), err
}

// normalizeSubject 去掉 Re / 回复前缀与多余空白，便于 wap 与 web 标题对照。
func normalizeSubject(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	for _, p := range []string{"Re:", "RE:", "re:", "回复:", "回复："} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimSpace(strings.TrimPrefix(s, p))
			break
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// fetchAttachment 从候选地址下载；KS3 不需要 Cookie，水木域名才带。
// 常规候选全部失败后，再按版面+标题去 web 正文找真实附件地址兜底。
func fetchAttachment(a Attachment, cookie string, max int64) ([]byte, string, error) {
	urls := attachmentCandidates(a)
	var last error
	try := func(raw string) ([]byte, string, error) {
		if !allowedAttachmentURL(raw) {
			return nil, "", fmt.Errorf("附件地址不在允许范围内")
		}
		u, _ := url.Parse(raw)
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) sfjth/0.1")
		req.Header.Set("Accept", "*/*")
		// web 主站地址需要 web 来源的 Referer，wap/KS3 用 wap 来源即可。
		referer := "https://wap.newsmth.net/"
		if strings.Contains(u.Host, "www.newsmth.net") || strings.Contains(u.Host, "www.mysmth.net") ||
			strings.Contains(u.Host, "static.mysmth.net") {
			referer = "https://www.newsmth.net/nForum/"
		}
		req.Header.Set("Referer", referer)
		host := strings.ToLower(u.Hostname())
		if strings.HasSuffix(host, "newsmth.net") || strings.HasSuffix(host, "mysmth.net") {
			if cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
		}
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, max+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, "", readErr
		}
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		contentType := resp.Header.Get("Content-Type")
		// wap/web 站点对不存在的附件路径会用 200 返回 SPA 首页或论坛错误页，
		// 不能当成附件字节，否则前端会把 HTML 当图片预览。两类都要挡住：
		// ① 水木错误页特征；② 返回类型与附件自身类型明显不符（如 t1.png 返回 text/html）。
		if isHTMLResponse(contentType, data) {
			return nil, "", fmt.Errorf("地址返回 HTML 错误页（非附件）")
		}
		if !attachmentContentTypeOK(a, contentType) {
			return nil, "", fmt.Errorf("地址返回内容类型 %q 与附件 %q 不符", contentType, a.Name)
		}
		if int64(len(data)) > max {
			return data[:max], contentType, fmt.Errorf("附件超过 %d 字节", max)
		}
		return data, contentType, nil
	}
	for _, raw := range urls {
		data, ct, err := try(raw)
		if err == nil {
			return data, ct, nil
		}
		last = err
	}
	// 最后兜底：web 正文里的 nForum/att 静态地址。正文可能含多张图，
	// 优先取字节数与附件声明一致的那个；都不一致时退回第一个可下载的图片。
	var fallback []byte
	var fallbackCT string
	for _, raw := range webStaticAttachmentURLs(a, cookie) {
		data, ct, err := try(raw)
		if err != nil {
			last = err
			continue
		}
		if a.Size > 0 && int64(len(data)) != a.Size {
			if fallback == nil {
				fallback, fallbackCT = data, ct
			}
			continue
		}
		return data, ct, nil
	}
	if fallback != nil {
		return fallback, fallbackCT, nil
	}
	if last == nil {
		last = fmt.Errorf("附件地址不在允许范围内")
	}
	return nil, "", last
}

// AttachmentPreview 读取附件并生成 data URL。非内联类型（doc/excel）不下载，
// 由前端显示文件信息并提供「用系统程序打开」。
func (c *Client) AttachmentPreview(a Attachment) (AttachmentPreview, error) {
	out := AttachmentPreview{Name: a.Name, Kind: a.Kind, Mime: a.Type, Size: a.Size, URL: a.URL}
	if out.Kind == "" {
		out.Kind = AttachmentKind(a.Name, a.Type)
	}
	if !inlineKind(out.Kind) {
		return out, nil
	}
	max := int64(maxInlineSmall)
	if out.Kind == "audio" || out.Kind == "video" {
		max = maxInlineMedia
	}
	if a.Size > 0 && a.Size > max {
		out.TooLarge = true
		return out, nil
	}
	data, contentType, err := fetchAttachment(a, c.Config().Cookie, max)
	if err != nil {
		// 超限是可预期的降级：仍把已读到的前 max 字节丢弃，让前端提示改开系统程序。
		if strings.Contains(err.Error(), "超过") {
			out.TooLarge = true
			return out, nil
		}
		return out, err
	}
	if ct := strings.TrimSpace(strings.Split(contentType, ";")[0]); ct != "" && ct != "application/octet-stream" {
		out.Mime = ct
	}
	if out.Mime == "" {
		out.Mime = mime.TypeByExtension(filepath.Ext(a.Name))
	}
	if out.Mime == "" {
		out.Mime = "application/octet-stream"
	}
	out.DataURL = "data:" + out.Mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	out.Inline = true
	return out, nil
}

func inlineKind(kind string) bool {
	return contains(kind, "image", "audio", "video", "pdf", "text", "html")
}

// attachmentContentTypeOK 检查响应 Content-Type 与附件声明的类型是否相容。
// 站点 SPA 对未知路径会 200 返回 text/html 首页；若不加这层校验，
// 图片附件会被当成 data:text/html 交给 <img>，表现为“预览空白但不报错”。
func attachmentContentTypeOK(a Attachment, contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if ct == "" || ct == "application/octet-stream" {
		return true
	}
	kind := a.Kind
	if kind == "" {
		kind = AttachmentKind(a.Name, a.Type)
	}
	switch {
	case strings.HasPrefix(ct, "image/"):
		return kind == "image"
	case strings.HasPrefix(ct, "audio/"):
		return kind == "audio"
	case strings.HasPrefix(ct, "video/"):
		return kind == "video"
	case ct == "application/pdf":
		return kind == "pdf"
	case strings.Contains(ct, "html") || ct == "text/plain":
		return kind == "html" || kind == "text"
	default:
		// 其余二进制类型交给扩展名/文件名判断，不在这里武断拒绝。
		return true
	}
}

// isHTMLResponse 判断响应是否是水木返回的 HTML 错误页，而非真正的附件字节。
// web 域名在附件不存在/失效时会用 200 返回一个论坛错误模板；若把它当附件，
// 前端预览会显示乱码。这里只按「水木错误页特征」识别，避免误杀正常的 html 附件。
// 只检查开头 8KB，避免把大附件全文转成字符串。
func isHTMLResponse(contentType string, data []byte) bool {
	ct := strings.ToLower(contentType)
	if !strings.Contains(ct, "html") && !strings.Contains(ct, "text/plain") {
		return false
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	s := string(head)
	markers := []string{
		"论坛错误信息", // nForum 错误页
		"找不到指定文件",
		"产生错误的可能原因",
		"blabla",               // 部分老版错误页
		"window.location.href", // 错误页的跳转脚本
	}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// DownloadAttachment 把附件落到本地临时目录，返回文件路径，供系统程序打开。
func (c *Client) DownloadAttachment(a Attachment) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "sfjth", "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := safeAttachmentName(a.Name)
	if name == "" {
		name = "attachment-" + a.ID
	}
	path := filepath.Join(dir, name)
	// 同名附件（不同 ID）不覆盖；用 ID 后缀区分。
	if _, err := os.Stat(path); err == nil {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		path = filepath.Join(dir, base+"-"+safeAttachmentName(a.ID)+ext)
	}
	data, _, err := fetchAttachment(a, c.Config().Cookie, maxDownload)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func safeAttachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ". ")
	if out == "" || out == "attachment-" {
		return ""
	}
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// humanSize 供前端展示，避免前端再做单位换算。
func HumanSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	if n < 1<<20 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
