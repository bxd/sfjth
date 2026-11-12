package main

// 登录弹窗：在主窗口底部滑出一个独立 WebView，加载水木真实登录页。
//
// 站点两端都开了极验，纯 HTTP 模拟登录无法过验证码。这里让用户直接在
// 真实页面里完成验证，再把 WebView 里的会话 Cookie 回传给 Go 侧。
//
// Windows 版 Wails 3 不会把运行时脚本注入外部 URL 窗口，所以不能用 ExecJS
// 操作外部页面。这里改为：本地 HTTP 服务提供「同源外壳页」和「反向代理」。
//
// 代理采用双端口同源方案：web、wap 各监听一个本地端口，端口下所有路径按前缀
// 分流——站点页面转发目标站，静态资源（static.mysmth.net）转发静态域名。HTML
// 里的静态资源 URL 被改写成同源 /__static/ 路径，确保站点脚本/CSS 能加载执行，
// 登录框才会被站点脚本渲染出来、极验才能用。外壳页与登录页同端口同源，父页
// JS 才能预填表单、裁剪页面、读取 Cookie。

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type loginPopup struct {
	app         *application.App
	mu          sync.Mutex
	listeners   map[string]net.Listener
	ports       map[string]int
	secret      string
	win         *application.WebviewWindow
	onDone      func(cookie, channel, user string)
	onCancel    func()
	doneOnce    *sync.Once // 登录成功只回传一次
	delivered   bool       // onDone 是否已真正投递（区别于 Once 被提前消耗）
	sessMu      sync.Mutex
	sessCookies map[string]string // 代理过程中累积的原始会话 Cookie（含方括号名）
}

func newLoginPopup(app *application.App, onDone func(cookie, channel, user string), onCancel func()) *loginPopup {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &loginPopup{
		app:         app,
		secret:      hex.EncodeToString(b),
		listeners:   map[string]net.Listener{},
		ports:       map[string]int{},
		doneOnce:    &sync.Once{},
		sessCookies: map[string]string{},
		onDone:      onDone,
		onCancel:    onCancel,
	}
}

func (p *loginPopup) base(kind string) string {
	return fmt.Sprintf("http://127.0.0.1:%d", p.ports[kind])
}

func (p *loginPopup) callbackURL(kind string, values url.Values) string {
	values.Set("t", p.secret)
	return p.base(kind) + "/done?" + values.Encode()
}

func (p *loginPopup) shellURL(mode, user, pass string) string {
	kind := "web"
	if mode == "wap" {
		kind = "wap"
	}
	q := url.Values{}
	q.Set("mode", mode)
	q.Set("u", user)
	q.Set("p", pass)
	q.Set("done", p.callbackURL(kind, url.Values{}))
	q.Set("cancel", p.callbackURL(kind, url.Values{"cancel": {"1"}}))
	q.Set("s", p.secret)
	return p.base(kind) + "/__shell?" + q.Encode()
}

func (p *loginPopup) ensureServer() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.listeners) > 0 {
		return nil
	}
	targets := map[string]string{
		"web": "https://www.newsmth.net",
		"wap": "https://wap.newsmth.net",
	}
	for kind, target := range targets {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		p.listeners[kind] = ln
		p.ports[kind] = ln.Addr().(*net.TCPAddr).Port

		mux := http.NewServeMux()
		mux.HandleFunc("/__shell", p.handleShell)
		mux.HandleFunc("/done", p.handleCallback)
		mux.Handle("/", p.reverseProxy(target))
		go func(l net.Listener, h http.Handler) { _ = http.Serve(l, h) }(ln, mux)
	}
	return nil
}

// ---------------------------------------------------------------- 反向代理

var bracketCookies = map[string]string{
	"main[UTMPUSERID]": "sfjth_main_UTMPUSERID",
	"main[UTMPKEY]":    "sfjth_main_UTMPKEY",
	"main[UTMPNUM]":    "sfjth_main_UTMPNUM",
}

func safeCookieName(name string) string {
	if v, ok := bracketCookies[name]; ok {
		return v
	}
	return name
}

func originalCookieName(name string) string {
	for orig, safe := range bracketCookies {
		if name == safe {
			return orig
		}
	}
	return name
}

// staticHosts 是被改写为同源 /__static/ 的静态资源域名。
var staticHosts = []string{
	"static.mysmth.net",
	"static.newsmth.net",
	"cdn.newsmth.net",
}

// newProxy 建一个反向代理：去掉 stripPrefix 后转发到 target，并统一处理响应头。
// rewriteStatic 为真时对 HTML 里的静态资源域名做同源改写。
func (p *loginPopup) newProxy(targetRaw, stripPrefix string, rewriteStatic bool) http.Handler {
	target, _ := url.Parse(targetRaw)
	rp := httputil.NewSingleHostReverseProxy(target)
	baseDirector := rp.Director
	rp.Director = func(r *http.Request) {
		baseDirector(r)
		if stripPrefix != "" && strings.HasPrefix(r.URL.Path, stripPrefix) {
			r.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, stripPrefix)
		}
		r.Host = target.Host
		r.Header.Set("Origin", targetRaw)
		r.Header.Set("Referer", targetRaw+"/")
		// 强制明文，便于改写 HTML 后重设 Content-Length。
		r.Header.Del("Accept-Encoding")
		// 浏览器侧用安全名字存 Cookie，转发给站点时还原成带方括号的名字。
		cks := r.Header.Values("Cookie")
		r.Header.Del("Cookie")
		for _, line := range cks {
			parts := strings.Split(line, ";")
			for i, part := range parts {
				kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
				if len(kv) == 2 {
					name := strings.TrimSpace(kv[0])
					orig := originalCookieName(name)
					parts[i] = orig + "=" + kv[1]
					// 站点带着什么凭据访问接口，就把它当作当前会话的一部分。
					// WAP 的 token 由前端 JS 写入且可能限定 /wap 路径，
					// 父页读不到，但代理能从请求头可靠捕获。
					if rewriteStatic && orig != "sfjth_login_ok" {
						p.sessMu.Lock()
						if p.sessCookies == nil {
							p.sessCookies = map[string]string{}
						}
						p.sessCookies[orig] = kv[1]
						p.sessMu.Unlock()
					}
				}
			}
			r.Header.Add("Cookie", strings.Join(parts, "; "))
		}
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		// iframe 与外壳同源，站点自身的 X-Frame-Options/CSP 已不适用。
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Content-Security-Policy-Report-Only")

		var cookies []string
		for _, h := range resp.Header.Values("Set-Cookie") {
			parts := strings.Split(h, ";")
			kv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
			if len(kv) != 2 {
				continue
			}
			name, val := strings.TrimSpace(kv[0]), kv[1]

			// 累积原始（带方括号）Cookie，供登录成功后回传给 Go 校验会话。
			// 删除型 Cookie（Max-Age=0 / 过期）从累积里移除。
			lowAll := strings.ToLower(h)
			p.sessMu.Lock()
			if p.sessCookies == nil {
				p.sessCookies = map[string]string{}
			}
			if strings.Contains(lowAll, "max-age=0") ||
				strings.Contains(lowAll, "expires=thu, 01 jan 1970") {
				delete(p.sessCookies, originalCookieName(name))
			} else {
				p.sessCookies[originalCookieName(name)] = val
			}
			p.sessMu.Unlock()

			out := []string{safeCookieName(name) + "=" + val}
			for _, part := range parts[1:] {
				low := strings.ToLower(strings.TrimSpace(part))
				if strings.HasPrefix(low, "domain=") || low == "httponly" || low == "secure" {
					continue
				}
				out = append(out, strings.TrimSpace(part))
			}
			cookies = append(cookies, strings.Join(out, "; "))
		}
		resp.Header.Del("Set-Cookie")
		for _, c := range cookies {
			resp.Header.Add("Set-Cookie", c)
		}

		// 识别登录成功：比外壳页猜 Cookie 名/路径可靠得多。两条互补信号：
		//  1) 登录接口成功响应体：web ajax_login.json {"ajax_st":1}、wap sign-in {"code":1}
		//  2) 会话 Cookie 从 guest 变成真实用户：wap SPA 新版登录走 /token，接口名不固定，
		//     但登录成功一定会把 main[UTMPUSERID] 从 guest 改成真实 ID。
		if rewriteStatic && resp.Request != nil {
			if resp.Request.Method == http.MethodPost && resp.Body != nil {
				if ch := loginSuccessChannel(resp.Request.URL.Path); ch != "" {
					body, err := io.ReadAll(resp.Body)
					if err == nil {
						_ = resp.Body.Close()
						resp.Body = io.NopCloser(bytes.NewReader(body))
						resp.ContentLength = int64(len(body))
						if loginSuccessBody(ch, body) {
							// 一次性标记：外壳页据此确认登录成功，即使站点把
							// token 延后写入 JS Cookie，也能带 Cookie 走 /done。
							resp.Header.Add("Set-Cookie", "sfjth_login_ok=1; Path=/; Max-Age=120")
							p.completeLogin(ch)
						}
					}
				}
			}
			// Cookie 信号： UA 变量 user 已从 guest 变为真实 ID。
			p.sessMu.Lock()
			uid := p.sessCookies["main[UTMPUSERID]"]
			p.sessMu.Unlock()
			if isRealUser(uid) {
				ch := "web"
				if strings.Contains(resp.Request.URL.Host, "wap.") {
					ch = "wap"
				}
				p.completeLogin(ch)
			}
		}

		if loc := resp.Header.Get("Location"); loc != "" {
			resp.Header.Set("Location", relativeLocation(loc))
		}

		if rewriteStatic && resp.Body != nil &&
			strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			_ = resp.Body.Close()
			s := rewriteStaticURLs(string(body))
			resp.Body = io.NopCloser(strings.NewReader(s))
			resp.ContentLength = int64(len(s))
			resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(s)))
		}
		return nil
	}
	return rp
}

// rewriteStaticURLs 把 HTML/CSS 里指向静态域名的绝对/协议相对 URL 改成同源路径。
func rewriteStaticURLs(s string) string {
	for _, host := range staticHosts {
		for _, p := range []string{"https://" + host, "http://" + host, "//" + host} {
			s = strings.ReplaceAll(s, p, "/__static/"+host)
		}
	}
	return s
}

// reverseProxy 按前缀分流：/__static/<host>/... 转发静态域名，其余转发站点。
func (p *loginPopup) reverseProxy(targetRaw string) http.Handler {
	site := p.newProxy(targetRaw, "", true)
	staticProxies := map[string]http.Handler{}
	for _, host := range staticHosts {
		// stripPrefix 必须与 r.URL.Path 一致带前导斜杠，否则永远匹配不上，
		// 静态 JS 会被请求成 https://static.host/__static/host/... → 404，
		// 站点脚本不执行：登录表单不渲染、click 处理未绑定、极验弹窗不创建。
		staticProxies[host] = p.newProxy("https://"+host, "/__static/"+host+"/", false)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__static/") {
			rest := strings.TrimPrefix(r.URL.Path, "/__static/")
			host := strings.SplitN(rest, "/", 2)[0]
			if h, ok := staticProxies[host]; ok {
				h.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		site.ServeHTTP(w, r)
	})
}

// relativeLocation 把指向水木各域名的绝对/协议相对跳转改写成站点内相对路径。
func relativeLocation(loc string) string {
	hosts := []string{"www.newsmth.net", "www.mysmth.net", "wap.newsmth.net", "m.newsmth.net"}
	for _, scheme := range []string{"https://", "http://"} {
		for _, h := range hosts {
			if strings.HasPrefix(loc, scheme+h) {
				if u, err := url.Parse(loc); err == nil {
					return u.RequestURI()
				}
			}
		}
	}
	if strings.HasPrefix(loc, "//") {
		if u, err := url.Parse("https:" + loc); err == nil {
			for _, h := range hosts {
				if u.Host == h {
					return u.RequestURI()
				}
			}
		}
	}
	return loc
}

// ---------------------------------------------------------------- 登录成功识别

// loginSuccessChannel 判断代理到的请求是哪个登录接口的成功候选路径。
func loginSuccessChannel(path string) string {
	p := strings.ToLower(path)
	if strings.HasSuffix(p, "/nforum/user/ajax_login.json") {
		return "web"
	}
	if strings.HasSuffix(p, "/wap/authorize/sign-in") {
		return "wap"
	}
	return ""
}

// web  ajax_login.json 成功是 GBK JSON：{"ajax_st":1,"ajax_code":"0005",...}
// wap  sign-in 成功是 UTF-8 JSON：{"code":1,...}
var (
	reWebSuccess = regexp.MustCompile(`"ajax_st"\s*:\s*1`)
	reWapSuccess = regexp.MustCompile(`"code"\s*:\s*1\b`)
	// 代理写的一次性成功标记，不能混进最终保存的站点会话 Cookie。
	reLoginMarker = regexp.MustCompile(`(?:^|;\s*)sfjth_login_ok=[^;]*`)
)

func stripLoginMarker(cookie string) string {
	return strings.TrimSpace(reLoginMarker.ReplaceAllString(cookie, ""))
}

func loginSuccessBody(channel string, body []byte) bool {
	if channel == "web" {
		return reWebSuccess.Match(body)
	}
	return reWapSuccess.Match(body)
}

// isRealUser 判断 main[UTMPUSERID] 是否已是登录后的真实用户（初始为 guest）。
func isRealUser(uid string) bool {
	uid = strings.TrimSpace(uid)
	return uid != "" && !strings.EqualFold(uid, "guest") && !strings.EqualFold(uid, "undefined")
}

// collectedCookie 汇总代理过程中捕获到的原始会话 Cookie。
func (p *loginPopup) collectedCookie() string {
	p.sessMu.Lock()
	parts := make([]string, 0, len(p.sessCookies))
	for k, v := range p.sessCookies {
		parts = append(parts, k+"="+v)
	}
	p.sessMu.Unlock()
	return strings.Join(parts, "; ")
}

func (p *loginPopup) setDelivered() {
	p.sessMu.Lock()
	p.delivered = true
	p.sessMu.Unlock()
}

func (p *loginPopup) isDelivered() bool {
	p.sessMu.Lock()
	v := p.delivered
	p.sessMu.Unlock()
	return v
}

// completeLogin 从代理累积的真实会话 Cookie 回传给 Go。
//
// 注意：doneOnce 只能在「真的拿到 Cookie 并完成投递」后消耗。站点可能在
// 登录响应里不写 Cookie，改由前端 JS 稍后写入；若此时先消耗 Once，后续
// /done 回调携带 Cookie 再来时就会被误判为重复而丢弃，表现为弹窗不关、
// 主界面不跳。没 Cookie 时直接返回，等外壳回调补交即可。
func (p *loginPopup) completeLogin(channel string) {
	cookie := stripLoginMarker(p.collectedCookie())
	diagLog("proxy", "completeLogin ch=%s cookieLen=%d onDone=%v", channel, len(cookie), p.onDone != nil)
	if cookie == "" || p.onDone == nil {
		return
	}
	p.doneOnce.Do(func() {
		p.setDelivered()
		go func() {
			p.closeWindow()
			p.onDone(cookie, channel, "")
		}()
	})
}

// ---------------------------------------------------------------- 回调

func (p *loginPopup) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("t") != p.secret {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cookie := q.Get("c")
	channel := q.Get("ch")
	user := q.Get("u")
	cancelled := q.Get("cancel") == "1"
	cookie = stripLoginMarker(cookie)
	if cookie == "" {
		// 外壳 JS 读不到 HttpOnly Cookie 时，退回到代理层累积的会话 Cookie。
		cookie = stripLoginMarker(p.collectedCookie())
	}

	delivered := p.isDelivered()
	diagLog("callback", "hit ch=%s cookieLen=%d delivered=%v cancel=%v", channel, len(cookie), delivered, cancelled)
	if cancelled {
		writeCallbackPage(w, "已取消登录，正在返回 sfjth ...", true)
		go func() {
			p.closeWindow()
			if p.onCancel != nil {
				p.onCancel()
			}
			application.Get().Event.Emit("sfjth:login-cancelled")
		}()
		return
	}
	if cookie == "" && !delivered {
		// 没拿到任何会话凭据不能关窗，否则前端永远等不到成功/失败事件。
		writeCallbackPage(w, "未获取到会话 Cookie，请关闭窗口后重试", false)
		return
	}
	writeCallbackPage(w, "登录信息已收到，正在返回 sfjth ...", true)
	if delivered {
		// 代理层已投递成功，这里只负责把残留的回调页关掉。
		p.closeWindow()
		return
	}
	p.doneOnce.Do(func() {
		p.setDelivered()
		p.closeWindow()
		if p.onDone != nil {
			p.onDone(cookie, channel, user)
		}
	})
}

// writeCallbackPage 输出回执页。autoClose 时用 window.close() 兜底：
// Wails 的窗口关闭走事件循环，极端情况下 Go 侧 Close() 尚未生效，
// 页面自己关掉可以避免用户看到页面永久停在回执文字上。
func writeCallbackPage(w http.ResponseWriter, message string, autoClose bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	js := ""
	if autoClose {
		js = `<script>setTimeout(function(){try{window.close()}catch(e){}},320)</script>`
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><style>body{margin:0;background:#0b0b0b;color:#7fdbff;font:14px/1.6 Consolas,monospace;display:flex;align-items:center;justify-content:center;height:100vh}</style><div>%s</div>%s`,
		templateEsc(message), js)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func templateEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func (p *loginPopup) closeWindow() {
	p.mu.Lock()
	w := p.win
	p.win = nil
	p.mu.Unlock()
	if w != nil {
		w.Close()
	}
}

// ---------------------------------------------------------------- 打开弹窗

// Open 打开登录弹窗。mode 为 auto 时默认走 web（nForum 侧栏登录框）。
func (p *loginPopup) Open(mode, user, pass string) (string, error) {
	if err := p.ensureServer(); err != nil {
		return "", err
	}
	if mode != "wap" {
		mode = "web"
	}
	p.closeWindow()

	// 每次打开都是一次全新的登录尝试：重置成功标志与累积的会话 Cookie。
	p.doneOnce = &sync.Once{}
	p.sessMu.Lock()
	p.delivered = false
	p.sessCookies = map[string]string{}
	p.sessMu.Unlock()

	popupHeight := 270
	if mode == "wap" {
		popupHeight = 520
	}
	width, x, y := 760, 0, 0
	if main, ok := p.app.Window.GetByName("main"); ok {
		b := main.Bounds()
		width = b.Width - 40
		if width > 760 {
			width = 760
		}
		if width < 520 {
			width = 520
		}
		x = b.X + (b.Width-width)/2
		y = b.Y + b.Height - popupHeight
	}

	w := p.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "sfjth-login-popup",
		Title:            "水木社区登录 - sfjth",
		URL:              p.shellURL(mode, user, pass),
		Width:            width,
		Height:           popupHeight,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
		BackgroundColour: application.NewRGB(255, 255, 255),
		Hidden:           true,
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: false,
		},
	})
	p.mu.Lock()
	p.win = w
	p.mu.Unlock()
	w.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		p.mu.Lock()
		if p.win == w {
			p.win = nil
		}
		p.mu.Unlock()
	})

	// 从底部滑入：先放在窗口下沿之外，再逐帧上移到最终位置。
	w.SetPosition(x, y+popupHeight/2)
	w.Show()
	go func() {
		steps := 10
		for i := steps; i >= 0; i-- {
			w.SetPosition(x, y+(popupHeight/2)*i/steps)
			time.Sleep(18 * time.Millisecond)
		}
	}()
	label := map[string]string{"web": "Web", "wap": "WAP"}[mode]
	return fmt.Sprintf("已打开%s登录页，请在弹窗中完成验证", label), nil
}

// ---------------------------------------------------------------- 外壳页面

func (p *loginPopup) handleShell(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	html := loginShellHTML(
		q.Get("mode"), q.Get("u"), q.Get("p"),
		q.Get("done"), q.Get("cancel"),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

// loginShellHTML 是登录弹窗的本地同源外壳：顶栏 + iframe。
// iframe 指向同端口的反向代理，父页因此可以直接操作表单与 Cookie。
func loginShellHTML(mode, user, pass, cbDone, cbCancel string) string {
	// web 用 nForum 侧栏登录框（带 hash 防止跳回主站首页）；wap 用手机站 /login。
	startPath := "/nForum/#!mainpage"
	if mode == "wap" {
		startPath = "/login"
	}
	repl := strings.NewReplacer(
		"__CHANNEL__", jsQuote(mode),
		"__START_PATH__", startPath,
		"__USER__", jsQuote(user),
		"__PASS__", jsQuote(pass),
		"__CB_DONE__", jsQuote(cbDone),
		"__CB_CANCEL__", jsQuote(cbCancel),
	)
	return repl.Replace(`<!doctype html>
<html><head><meta charset="utf-8"><title>水木社区登录 - sfjth</title>
<style>
html,body{margin:0;height:100%;overflow:hidden;background:#eef2f7;font:13px/1.4 Consolas,"Microsoft YaHei",monospace}
#bar{height:30px;background:#0b3d91;color:#fff;display:flex;align-items:center;justify-content:space-between;padding:0 12px;box-sizing:border-box}
#bar button{background:transparent;border:1px solid #9fc;color:#fff;padding:2px 10px;cursor:pointer}
#site{width:100%;height:calc(100% - 30px);border:0;display:block;background:#fff}
</style></head>
<body>
<div id="bar"><span id="tip">sfjth · 账号密码已预填，完成验证后自动返回</span><button id="close">关闭</button></div>
<iframe id="site" src="__START_PATH__"></iframe>
<script>
(function () {
  var channel = __CHANNEL__, user = __USER__, pass = __PASS__;
  var cbDone = __CB_DONE__, cbCancel = __CB_CANCEL__;
  var frame = document.getElementById('site');
  var done = false;
  // 清掉上次登录残留的成功标记，避免新窗口刚打开就误判。
  document.cookie = 'sfjth_login_ok=; Max-Age=0; Path=/';
  document.getElementById('close').onclick = function () { location.replace(cbCancel); };

  function setVal(el, v) {
    if (!el) return false;
    el.disabled = false;
    el.value = v;
    try {
      el.dispatchEvent(new Event('input', {bubbles: true}));
      el.dispatchEvent(new Event('change', {bubbles: true}));
      el.dispatchEvent(new Event('blur', {bubbles: true}));
    } catch (e) {}
    return true;
  }
  function enable(el) { try { if (el) el.disabled = false; } catch (e) {} }
  function style(doc, sel, css) {
    try { doc.querySelectorAll(sel).forEach(function (el) { el.style.cssText = css; }); } catch (e) {}
  }
  function hide(doc, sel) { style(doc, sel, 'display:none !important'); }
  function originalCookie(c) {
    return c
      .replace(/sfjth_main_UTMPUSERID/g, 'main[UTMPUSERID]')
      .replace(/sfjth_main_UTMPKEY/g, 'main[UTMPKEY]')
      .replace(/sfjth_main_UTMPNUM/g, 'main[UTMPNUM]');
  }
  function cookieUser(c) {
    var m = c.match(/(?:^|;\s*)main\[UTMPUSERID\]=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function cookieOK(doc) {
    var c = doc.cookie || '';
    // 代理识别到登录接口成功响应后写的一次性标记，最可靠。
    if (/(?:^|;\s*)sfjth_login_ok=1(?:;|$)/.test(c)) return true;
    var u = cookieUser(originalCookie(c));
    if (channel === 'web') {
      return !!u && u.toLowerCase() !== 'guest';
    }
    var hasAuth = /(?:^|;\s*)(token|wap_token|access_token|main\[UTMPUSERID\]|sfjth_main_UTMPUSERID)=/i.test(c);
    return hasAuth && !/\/login/.test(doc.location.pathname);
  }
  function succeed(doc) {
    if (done) return;
    done = true;
    var c = originalCookie(doc.cookie || '').replace(/(?:^|;\s*)sfjth_login_ok=[^;]*/, '');
    var u = cookieUser(c);
    var v = new URLSearchParams({c: c, ch: channel, u: u});
    location.replace(cbDone + '&' + v.toString());
  }

  // web：nForum 侧栏登录框。表单通常在 <script id="tmpl_u_login"> 模板里，
  // 站点脚本会渲染它；若脚本尚未渲染，这里主动注入模板内容作为兜底。
  function setupWeb(doc) {
    var box = doc.getElementById('u_login');
    if (!box) return false;
    var form = doc.querySelector('#u_login_form, #u_login form');
    if (!form) {
      var tpl = doc.getElementById('tmpl_u_login');
      if (!tpl) return false;
      try { box.innerHTML = tpl.innerHTML; }
      catch (e) { box.innerHTML = tpl.textContent || tpl.innerText || ''; }
      form = doc.querySelector('#u_login_form, #u_login form');
    }
    if (!form || !doc.querySelector('#u_login_id, input[name="id"]')) return false;

    // 每次都回填（安全）：站点脚本若重渲染表单，密码不会丢。
    var idEl = doc.querySelector('#u_login_id, input[name="id"]');
    var pwdEl = doc.querySelector('#u_login_passwd, input[name="passwd"]');
    var ok = setVal(idEl, user) && setVal(pwdEl, pass);
    form.querySelectorAll('input,select,button').forEach(function (el) { enable(el); });
    var remember = doc.querySelector('#u_login_cookie');
    if (remember) { remember.checked = true; enable(remember); }

    // 布局裁剪只做一次：定时器每 300ms 都会调用 setup，若反复裁剪，
    // 会把站点刚显示的极验弹窗再次隐藏，导致点“登录”像没反应。
    if (!doc.__sfjthCropped) {
      doc.__sfjthCropped = true;
      // #dialog 是极验弹窗宿主（站点脚本点击登录时显示），绝不能隐藏。
      var menu = doc.getElementById('menu');
      var anchor = menu || box;
      var keep = {dialog: 1, geetest_login: 1};
      Array.prototype.forEach.call(doc.body.children, function (el) {
        if (el === anchor || keep[el.id]) return;
        el.style.setProperty('display', 'none', 'important');
      });
      var dlg = doc.getElementById('dialog');
      if (dlg) {
        // 交还显隐控制权给站点脚本；提高层级确保浮在压缩后的 body 之上。
        dlg.style.setProperty('display', 'none', '');
        dlg.style.zIndex = '999999';
      }
      if (menu) {
        menu.classList.remove('m-hide');
        Array.prototype.forEach.call(menu.children, function (el) {
          if (el !== box) el.style.setProperty('display', 'none', 'important');
        });
        menu.style.cssText = 'display:block !important;position:static;float:none;margin:34px auto 0;padding:0;width:230px;background:#fff;border:0;box-shadow:none';
      }
      box.style.cssText = 'display:block;margin:0;padding:0;width:230px';
      // 按截图纵向排列：帐号 / 密码 / 下次自动登录 / 登录。
      style(doc, '#u_login .u-login-input', 'display:flex;align-items:center;gap:8px;margin:0 0 12px');
      style(doc, '#u_login .u-login-input span', 'width:40px;font:14px/1.4 "Microsoft YaHei",sans-serif;color:#111');
      style(doc, '#u_login .u-login-input input', 'flex:1;width:150px;height:26px;padding:2px 8px;border:1px solid #7a9bc0;border-radius:5px;box-sizing:border-box;background:#fff');
      style(doc, '#u_login .u-login-check', 'display:flex;align-items:center;gap:6px;margin:0 0 12px;color:#111;font:14px/1.4 "Microsoft YaHei",sans-serif');
      style(doc, '#u_login .u-login-op', 'margin:0');
      style(doc, '#u_login .u-login-op input', 'height:28px;padding:0 18px;border:1px solid #6f97c4;border-radius:5px;background:linear-gradient(#f7fbff,#dce9f8);color:#0b3d91;font:14px/1 "Microsoft YaHei",sans-serif;cursor:pointer');
      doc.body.style.cssText = 'margin:0;background:#fff;overflow:hidden';
    }
    document.getElementById('tip').textContent = 'sfjth · 账号密码已填好，点“登录”完成极验后自动返回';
    return ok;
  }

  // wap：保留手机站原生登录表单与验证码区域。
  function setupWap(doc) {
    var u = null, p = null;
    doc.querySelectorAll('input').forEach(function (el) {
      var ph = el.getAttribute('placeholder') || '';
      if (!u && /水木账号|用户ID|账号/.test(ph)) u = el;
      if (!p && /密码/.test(ph)) p = el;
    });
    if (!u) u = doc.querySelector('input[name="username"],input[type="text"]');
    if (!p) p = doc.querySelector('input[name="password"],input[type="password"]');
    var ok = setVal(u, user) && setVal(p, pass);
    hide(doc, 'img,.ns-logo,.ns-bottom-box,.ns-footer,.ns-regist-button,.ns-description,.ns-description__content,.ns-agreement,.ns-tip,[class*="agreement"],[class*="footer"],[class*="logo"]');
    style(doc, '.ns-login', 'margin:0 auto;padding:16px 18px;max-width:420px;background:#fff');
    style(doc, '.ns-input', 'margin:12px 0');
    style(doc, '.ns-button', 'width:100%');
    var gee = doc.getElementById('geetest_login');
    if (gee) gee.style.cssText = 'margin:12px 0';
    // 让登录表单占满宽度并居中，去掉横向滚动。
    style(doc, '#app', 'max-width:420px;margin:0 auto;overflow:hidden');
    doc.documentElement.style.cssText = 'overflow-x:hidden';
    doc.body.style.cssText = 'margin:0;background:#f2f4f7;overflow-x:hidden;overflow-y:auto';
    return ok;
  }

  function setup() {
    if (done) return;
    var doc;
    try { doc = frame.contentDocument; } catch (e) { return; }
    if (!doc || !doc.body || doc.readyState !== 'complete') return;
    if (channel === 'web') setupWeb(doc); else setupWap(doc);
  }
  frame.addEventListener('load', setup);
  var timer = setInterval(function () {
    if (done) { clearInterval(timer); return; }
    setup();
    try {
      var doc = frame.contentDocument;
      if (doc && cookieOK(doc)) succeed(doc);
    } catch (e) {}
  }, 300);
})();
</script></body></html>`)
}

func jsQuote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSpace(b.String())
}
