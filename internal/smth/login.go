package smth

// 真实进站登录：同时支持 Web(nForum) 与 WAP(手机站) 两条通道。
//
// 通道来源（2026-11 实测，见 docs/API.md）：
//   Web: POST https://www.newsmth.net/nForum/user/ajax_login.json
//        form: id / passwd / CookieDate，header: X-Requested-With: XMLHttpRequest
//        返回 GBK JSON {"ajax_st":1,"ajax_code":"0005","ajax_msg":"操作成功"}
//        会话校验: GET /nForum/user/ajax_session.json -> {"id":"<userid>"}
//   WAP: POST https://wap.newsmth.net/wap/authorize/sign-in
//        JSON: username / password(encodeURIComponent) + 极验字段
//        返回 {"code":1,...}；会话校验: GET /wap/api/profile
//
// 站点当前对两条通道都开了极验。程序化登录遇到验证码时，返回明确的
// captcha_required，并允许从登录屏 F3 粘贴浏览器取得的验证令牌继续；
// F4 可一键清除误填的令牌。令牌支持 JSON、key=value 或单个 ticket 字符串。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 登录通道。
const (
	LoginAuto = "auto" // 先 web，失败再 wap；两条都真实请求
	LoginWeb  = "web"
	LoginWap  = "wap"
)

// 登录验证码令牌（从浏览器开发者工具复制，或站点临时关闭验证码时留空）。
type LoginCaptcha struct {
	Ticket        string `json:"ticket"`
	Randstr       string `json:"randstr"`
	AppID         string `json:"appid"`
	CaptchaID     string `json:"captcha_id"`
	LotNumber     string `json:"lot_number"`
	CaptchaOutput string `json:"captcha_output"`
	PassToken     string `json:"pass_token"`
	GenTime       string `json:"gen_time"`
}

// LoginAttempt 是单条通道的尝试结果。
type LoginAttempt struct {
	Channel         string `json:"channel"`
	OK              bool   `json:"ok"`
	User            string `json:"user"`
	Code            string `json:"code"`
	Message         string `json:"message"`
	CaptchaRequired bool   `json:"captcha_required"`
}

// LoginOutcome 是登录屏展示的最终结果（不含 Cookie）。
type LoginOutcome struct {
	OK              bool           `json:"ok"`
	User            string         `json:"user"`
	Channel         string         `json:"channel"`
	Message         string         `json:"message"`
	CaptchaRequired bool           `json:"captcha_required"`
	Attempts        []LoginAttempt `json:"attempts"`
}

// loginEndpoints 便于测试时替换成本地 httptest 服务。
type loginEndpoints struct {
	web string
	wap string
}

func defaultLoginEndpoints(base string) loginEndpoints {
	if base == "" {
		base = "https://www.newsmth.net"
	}
	return loginEndpoints{web: strings.TrimRight(base, "/"), wap: "https://wap.newsmth.net"}
}

// loginTransport 带独立 CookieJar，登录成功后把所有 newsmth 域的 Cookie 合并回配置。
type loginTransport struct {
	ep   loginEndpoints
	http *http.Client
	// 水木用 main[UTMPUSERID] 这类带方括号的 Cookie 名，Go 的 cookiejar
	// 会按 RFC 判定为非法并丢弃，所以这里自己解析 Set-Cookie 并手工带上。
	raw map[string]string
}

func newLoginTransport(ep loginEndpoints, savedCookie string) *loginTransport {
	jar, _ := cookiejar.New(nil)
	t := &loginTransport{
		ep: ep, http: &http.Client{Jar: jar, Timeout: 15 * time.Second},
		raw: map[string]string{},
	}
	for _, ck := range parseCookieHeader(savedCookie) {
		if ck.Name != "" {
			t.raw[ck.Name] = ck.Value
		}
	}
	return t
}

const (
	desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	mobileUA  = "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36"
)

func (t *loginTransport) request(method, ua, target string, body io.Reader, headers map[string]string) ([]byte, string, error) {
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if h := t.cookieHeader(target); h != "" {
		req.Header.Set("Cookie", h)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	for _, sc := range resp.Header.Values("Set-Cookie") {
		kv := strings.SplitN(strings.SplitN(sc, ";", 2)[0], "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			continue
		}
		name := strings.TrimSpace(kv[0])
		if strings.Contains(strings.ToLower(sc), "max-age=0") {
			delete(t.raw, name)
			continue
		}
		t.raw[name] = kv[1]
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode >= 400 {
		return b, ct, fmt.Errorf("HTTP %d: %s", resp.StatusCode, limit(stripTags(decodeBody(b, ct)), 100))
	}
	return b, ct, nil
}

// cookieHeader 合并 jar 与手工保存的 Cookie，按名字排序保证请求可重复。
func (t *loginTransport) cookieHeader(target string) string {
	m := map[string]string{}
	if u, err := url.Parse(target); err == nil && t.http.Jar != nil {
		for _, ck := range t.http.Jar.Cookies(u) {
			if ck.Name != "" {
				m[ck.Name] = ck.Value
			}
		}
	}
	for k, v := range t.raw {
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, "; ")
}

// Cookies 汇总两个域名下的会话 Cookie（包括方括号名字那类）。
func (t *loginTransport) Cookies() string {
	return t.cookieHeader(t.ep.web)
}

// seed 预先访问登录页，拿到站点初始 Cookie（部分接口要求先有会话）。
func (t *loginTransport) seed() {
	_, _, _ = t.request(http.MethodGet, desktopUA, t.ep.web+"/nForum/", nil, nil)
	_, _, _ = t.request(http.MethodGet, mobileUA, t.ep.wap+"/login", nil, nil)
}

// webLogin 走 zSMTH 同款「简单登录」接口。
func (t *loginTransport) webLogin(user, pass string, cap LoginCaptcha) LoginAttempt {
	a := LoginAttempt{Channel: LoginWeb}
	form := url.Values{}
	form.Set("id", user)
	form.Set("passwd", pass)
	form.Set("CookieDate", "2")
	setIf := func(k, v string) {
		if v != "" {
			form.Set(k, v)
		}
	}
	setIf("ticket", cap.Ticket)
	setIf("randstr", cap.Randstr)
	setIf("captcha_id", cap.CaptchaID)
	setIf("lot_number", cap.LotNumber)
	setIf("captcha_output", cap.CaptchaOutput)
	setIf("pass_token", cap.PassToken)
	setIf("gen_time", cap.GenTime)

	b, ct, err := t.request(http.MethodPost, desktopUA, t.ep.web+"/nForum/user/ajax_login.json",
		strings.NewReader(form.Encode()), map[string]string{
			"Content-Type":     "application/x-www-form-urlencoded",
			"X-Requested-With": "XMLHttpRequest",
			"Referer":          t.ep.web + "/nForum/",
			"Accept":           "application/json, text/javascript, */*; q=0.01",
		})
	if err != nil {
		a.Message = "Web 请求失败：" + err.Error()
		return a
	}
	var r struct {
		AjaxST int    `json:"ajax_st"`
		Code   string `json:"ajax_code"`
		Msg    string `json:"ajax_msg"`
	}
	if err := json.Unmarshal([]byte(decodeBody(b, ct)), &r); err != nil {
		a.Message = "Web 返回无法解析：" + err.Error()
		return a
	}
	a.Code = r.Code
	a.Message = r.Msg
	if r.AjaxST != 1 {
		a.CaptchaRequired = captchaMessage(r.Msg) || r.Code == "1103"
		if a.Message == "" {
			a.Message = "Web 登录失败（ajax_code=" + r.Code + "）"
		}
		if a.CaptchaRequired {
			a.Message = "Web 通道需要验证码：" + a.Message
		}
		return a
	}
	name, err := t.webSession()
	if err != nil {
		a.Message = "Web 登录成功但会话校验失败：" + err.Error()
		return a
	}
	a.OK = true
	a.User = name
	a.Message = "Web 登录成功，会话有效"
	return a
}

// wapLogin 走新版手机站 SPA 自己的 sign-in 接口。
func (t *loginTransport) wapLogin(user, pass string, cap LoginCaptcha) LoginAttempt {
	a := LoginAttempt{Channel: LoginWap}
	payload := map[string]string{
		"username": user,
		// 手机站前端提交前做了一次 encodeURIComponent。
		"password": uriComponent(pass),
	}
	put := func(k, v string) {
		if v != "" {
			payload[k] = v
		}
	}
	put("ticket", cap.Ticket)
	put("randstr", cap.Randstr)
	put("appid", cap.AppID)
	put("captcha_id", cap.CaptchaID)
	put("lot_number", cap.LotNumber)
	put("captcha_output", cap.CaptchaOutput)
	put("pass_token", cap.PassToken)
	put("gen_time", cap.GenTime)
	if cap.Ticket == "" && cap.LotNumber == "" {
		put("type", "2") // 极验 4 的登录场景，SPA 默认也会带上
	}
	put("client", "wap")
	buf, _ := json.Marshal(payload)

	b, ct, err := t.request(http.MethodPost, mobileUA, t.ep.wap+"/wap/authorize/sign-in",
		bytes.NewReader(buf), map[string]string{
			"Content-Type": "application/json",
			"Origin":       t.ep.wap,
			"Referer":      t.ep.wap + "/login",
			"Accept":       "application/json, text/plain, */*",
		})
	if err != nil {
		a.Message = "WAP 请求失败：" + err.Error()
		return a
	}
	var r struct {
		Code    int    `json:"code"`
		KbsCode int    `json:"kbsCode"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(decodeBody(b, ct)), &r); err != nil {
		a.Message = "WAP 返回无法解析：" + err.Error()
		return a
	}
	a.Code = fmt.Sprintf("%d/%d", r.Code, r.KbsCode)
	a.Message = r.Message
	if r.Code != 1 {
		a.CaptchaRequired = r.KbsCode == 9 || captchaMessage(r.Message)
		if a.Message == "" {
			a.Message = "WAP 登录失败（code=" + a.Code + "）"
		}
		if a.CaptchaRequired {
			a.Message = "WAP 通道需要验证码：" + a.Message
		}
		return a
	}
	name, err := t.wapSession()
	if err != nil {
		a.Message = "WAP 登录成功但会话校验失败：" + err.Error()
		return a
	}
	a.OK = true
	a.User = name
	a.Message = "WAP 登录成功，会话有效"
	return a
}

// webSession 用官方 nForum 会话接口确认确实登录，而不是只看登录返回。
func (t *loginTransport) webSession() (string, error) {
	b, ct, err := t.request(http.MethodGet, desktopUA, t.ep.web+"/nForum/user/ajax_session.json", nil,
		map[string]string{"X-Requested-With": "XMLHttpRequest", "Referer": t.ep.web + "/nForum/"})
	if err != nil {
		return "", err
	}
	var r struct {
		ID       string `json:"id"`
		UserName string `json:"user_name"`
	}
	if err := json.Unmarshal([]byte(decodeBody(b, ct)), &r); err != nil {
		return "", err
	}
	if r.ID == "" || strings.EqualFold(r.ID, "guest") {
		return "", errors.New("会话仍为 guest")
	}
	return r.ID, nil
}

// wapSession 用手机站 /wap/api/profile 校验会话。
func (t *loginTransport) wapSession() (string, error) {
	b, ct, err := t.request(http.MethodGet, mobileUA, t.ep.wap+"/wap/api/profile", nil,
		map[string]string{"Referer": t.ep.wap + "/"})
	if err != nil {
		return "", err
	}
	var r struct {
		Code     int    `json:"code"`
		Message  string `json:"message"`
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal([]byte(decodeBody(b, ct)), &r); err != nil {
		return "", err
	}
	if r.Code == 403 || strings.Contains(r.Message, "未登陆") || strings.Contains(r.Message, "未登录") {
		return "", errors.New("会话无效：" + r.Message)
	}
	if r.ID != "" {
		return r.ID, nil
	}
	if r.Username != "" {
		return r.Username, nil
	}
	// 部分版本 profile 只返回 code=0 不给 id；Cookie 本身即为凭据。
	if r.Code == 0 {
		return "", nil
	}
	return "", errors.New("手机站会话返回异常：" + r.Message)
}

func captchaMessage(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "验证码") || strings.Contains(s, "验证失败") ||
		strings.Contains(s, "无效的验证") || strings.Contains(s, "captcha") ||
		strings.Contains(s, "verification")
}

// uriComponent 等价于 JavaScript 的 encodeURIComponent（空格是 %20 而非 +）。
func uriComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("-_.!~*'()", rune(c)) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// parseLoginCaptcha 宽容解析验证码令牌。
//
// 站点令牌可能是：完整 JSON、浏览器 Network 里复制的 key=value 串，
// 或用户只粘了一个 ticket 字符串。任何一种都不应拦住真正的登录请求：
// 以前严格 JSON 解析会让误输入的字符（例如单独的 "o"）直接报
// "invalid character ... looking for beginning of value"，用户根本到不了
// 服务器校验那一步。现在能识别字段就映射字段，识别不了就整串当 ticket
// 透传，由服务器给出真实结果。
func parseLoginCaptcha(raw string) (LoginCaptcha, error) {
	var c LoginCaptcha
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return c, nil
	}

	// 1) JSON（含单引号、尾随逗号等浏览器复制时常见的变体）。
	if m, ok := decodeCaptchaJSON(raw); ok && applyCaptchaFields(&c, m, 0) {
		return c, nil
	}

	// 2) key=value / query string / 逗号分隔的松散格式。
	if m := parseLooseCaptchaPairs(raw); len(m) > 0 {
		anyMap := make(map[string]any, len(m))
		for k, v := range m {
			anyMap[k] = v
		}
		if applyCaptchaFields(&c, anyMap, 0) {
			return c, nil
		}
	}

	// 3) 无法识别结构：按单个 ticket 透传，不再本地报错。
	c.Ticket = raw
	return c, nil
}

// decodeCaptchaJSON 尝试解析 JSON 对象，并兼容单引号与尾随逗号。
func decodeCaptchaJSON(raw string) (map[string]any, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return m, true
	}
	fixed := strings.ReplaceAll(raw, "'", `"`)
	for strings.Contains(fixed, `,}`) {
		fixed = strings.ReplaceAll(fixed, `,}`, `}`)
	}
	for strings.Contains(fixed, `,]`) {
		fixed = strings.ReplaceAll(fixed, `,]`, `]`)
	}
	if err := json.Unmarshal([]byte(fixed), &m); err == nil {
		return m, true
	}
	return nil, false
}

// parseLooseCaptchaPairs 解析 ticket=xxx&randstr=yyy 这类无 JSON 包装的串。
func parseLooseCaptchaPairs(raw string) map[string]string {
	m := map[string]string{}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '&' || r == ';' || r == ',' || r == '\n' || r == '\r'
	})
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		k := strings.Trim(strings.TrimSpace(kv[0]), `"'`)
		v := strings.Trim(strings.TrimSpace(kv[1]), `"'`)
		if decoded, err := url.QueryUnescape(v); err == nil {
			v = decoded
		}
		if k != "" {
			m[k] = v
		}
	}
	return m
}

// applyCaptchaFields 把字段名映射到 LoginCaptcha；支持嵌套一层 data 对象。
func applyCaptchaFields(c *LoginCaptcha, m map[string]any, depth int) bool {
	if depth > 3 {
		return false
	}
	found := false
	for k, v := range m {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			if setCaptchaField(c, k, s) {
				found = true
			}
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			if applyCaptchaFields(c, nested, depth+1) {
				found = true
			}
			continue
		}
		if setCaptchaField(c, k, fmt.Sprint(v)) {
			found = true
		}
	}
	return found
}

func setCaptchaField(c *LoginCaptcha, key, value string) bool {
	k := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(key))
	switch k {
	case "ticket", "cticket":
		c.Ticket = value
	case "randstr", "randomstr":
		c.Randstr = value
	case "appid":
		c.AppID = value
	case "captchaid":
		c.CaptchaID = value
	case "lotnumber":
		c.LotNumber = value
	case "captchaoutput":
		c.CaptchaOutput = value
	case "passtoken":
		c.PassToken = value
	case "gentime":
		c.GenTime = value
	default:
		return false
	}
	return true
}

func normalizeLoginMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case LoginWeb:
		return LoginWeb
	case LoginWap:
		return LoginWap
	default:
		return LoginAuto
	}
}
