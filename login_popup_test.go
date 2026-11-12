package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoginShellHTML(t *testing.T) {
	web := loginShellHTML("web", "bld", `p"&ss`, "http://127.0.0.1:1/done?t=x", "http://127.0.0.1:1/done?t=x&cancel=1")
	for _, want := range []string{`"web"`, `"bld"`, `/nForum/#!mainpage`, `#u_login_form`, `#u_login_id`, `#u_login_passwd`, `sfjth_main_UTMPUSERID`, `下次自动登录`} {
		if !strings.Contains(web, want) {
			t.Fatalf("web 外壳缺少 %q", want)
		}
	}
	for _, bad := range []string{"__CHANNEL__", "__START_PATH__", "__USER__", "__PASS__", "__CB_DONE__", "__CB_CANCEL__"} {
		if strings.Contains(web, bad) {
			t.Fatalf("web 外壳残留占位符 %q", bad)
		}
	}
	if !strings.Contains(web, `p\"&ss`) {
		t.Fatal("密码没有做 JS 字符串转义")
	}

	wap := loginShellHTML("wap", "bld", "pass", "http://127.0.0.1:1/done?t=x", "http://127.0.0.1:1/done?t=x&cancel=1")
	for _, want := range []string{`"wap"`, `src="/login"`, `水木账号`, `密码`, `geetest_login`} {
		if !strings.Contains(wap, want) {
			t.Fatalf("wap 外壳缺少 %q", want)
		}
	}
}

// 真正的双通道登录页：web 走 nForum 侧栏登录框，wap 走手机站 /login。
func TestLoginEntries(t *testing.T) {
	web := loginShellHTML("web", "", "", "", "")
	if !strings.Contains(web, `src="/nForum/#!mainpage"`) {
		t.Fatal("web 应加载 nForum 页面（带 hash 防跳回主站）以取得侧栏登录框")
	}
	wap := loginShellHTML("wap", "", "", "", "")
	if !strings.Contains(wap, `src="/login"`) {
		t.Fatal("wap 应加载 https://wap.newsmth.net/login")
	}
}

// 真实请求一次两端登录页，确认反向代理与 Cookie 改写没有把页面打坏。
func TestLoginProxySmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网冒烟测试")
	}
	p := &loginPopup{secret: "smoke", listeners: map[string]net.Listener{}, ports: map[string]int{}}
	if err := p.ensureServer(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	cases := []struct {
		kind string
		path string
		want string
	}{
		{"web", "/nForum/", "login"},
		{"wap", "/login", "app"},
	}
	for _, tc := range cases {
		resp, err := client.Get(p.base(tc.kind) + tc.path)
		if err != nil {
			t.Fatalf("%s 代理请求失败：%v", tc.kind, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
			t.Fatalf("%s 代理返回 %d：%s", tc.kind, resp.StatusCode, string(b[:min(len(b), 200)]))
		}
		if !strings.Contains(strings.ToLower(string(b)), tc.want) {
			t.Fatalf("%s 页面不像水木登录页：%s", tc.kind, string(b[:min(len(b), 300)]))
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestCookieNameMapping(t *testing.T) {
	if got := safeCookieName("main[UTMPUSERID]"); got != "sfjth_main_UTMPUSERID" {
		t.Fatalf("安全 Cookie 名错误：%s", got)
	}
	if got := originalCookieName("sfjth_main_UTMPKEY"); got != "main[UTMPKEY]" {
		t.Fatalf("原始 Cookie 名还原错误：%s", got)
	}
	if got := originalCookieName("other"); got != "other" {
		t.Fatalf("普通 Cookie 不应被改写：%s", got)
	}
}

func TestRewriteStaticURLs(t *testing.T) {
	in := `<link href="//static.mysmth.net/nForum/css/x.css"><script src="https://static.mysmth.net/nForum/js/p.js"></script>`
	out := rewriteStaticURLs(in)
	for _, want := range []string{`/__static/static.mysmth.net/nForum/css/x.css`, `/__static/static.mysmth.net/nForum/js/p.js`} {
		if !strings.Contains(out, want) {
			t.Fatalf("静态 URL 未改写为 %q：%s", want, out)
		}
	}
}

// 登录接口成功响应必须能被代理直接识别，避免依赖外壳 JS 猜 Cookie 名/路径
// （wap 登录成功回到 /login 时旧逻辑会漏判，导致不跳主菜单）。
func TestLoginSuccessDetection(t *testing.T) {
	cases := []struct {
		channel string
		path    string
		body    string
		want    bool
	}{
		{"web", "/nForum/user/ajax_login.json", `{"ajax_st":1,"ajax_code":"0005"}`, true},
		{"web", "/nForum/user/ajax_login.json", `{"ajax_st":0,"ajax_code":"1103"}`, false},
		{"wap", "/wap/authorize/sign-in", `{"code":1,"message":"ok"}`, true},
		{"wap", "/wap/authorize/sign-in", `{"code":0,"kbsCode":9}`, false},
	}
	for _, c := range cases {
		if got := loginSuccessChannel(c.path); got != c.channel {
			t.Fatalf("loginSuccessChannel(%q) = %q, want %q", c.path, got, c.channel)
		}
		if got := loginSuccessBody(c.channel, []byte(c.body)); got != c.want {
			t.Fatalf("loginSuccessBody(%q, %s) = %v, want %v", c.channel, c.body, got, c.want)
		}
	}
	// code:10/11 不能被误判为成功（边界）。
	if loginSuccessBody("wap", []byte(`{"code":10}`)) {
		t.Fatal("code:10 不应判为登录成功")
	}
}

func TestIsRealUser(t *testing.T) {
	for _, v := range []string{"guest", "GUEST", "undefined", "", "  "} {
		if isRealUser(v) {
			t.Fatalf("isRealUser(%q) 应为 false", v)
		}
	}
	for _, v := range []string{"bld", "Allen", " user123 "} {
		if !isRealUser(v) {
			t.Fatalf("isRealUser(%q) 应为 true", v)
		}
	}
}

// 用假站点验证：wap 登录成功响应触发 onDone，并把真实 Set-Cookie 回传。
func TestProxyCapturesLoginCookies(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "wap_token=abc123; Path=/")
		w.Header().Add("Set-Cookie", "main[UTMPUSERID]=bld; Path=/")
		_, _ = w.Write([]byte(`{"code":1}`))
	}))
	defer stub.Close()

	got := make(chan string, 2)
	p := &loginPopup{doneOnce: &sync.Once{}, sessCookies: map[string]string{},
		onDone: func(cookie, channel, user string) { got <- channel + "|" + cookie }}
	h := p.newProxy(stub.URL, "", true)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/wap/authorize/sign-in", nil))
	select {
	case g := <-got:
		if !strings.Contains(g, "wap|") || !strings.Contains(g, "wap_token=abc123") ||
			!strings.Contains(g, "main[UTMPUSERID]=bld") {
			t.Fatalf("回传的 Cookie 不完整：%s", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("登录成功响应未触发 onDone")
	}
}

// 登录成功响应要写一次性标记，供外壳页在 Cookie 延后落地时确认成功。
func TestProxyMarksLoginSuccess(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1}`))
	}))
	defer stub.Close()
	p := &loginPopup{doneOnce: &sync.Once{}, sessCookies: map[string]string{}}
	rr := httptest.NewRecorder()
	p.newProxy(stub.URL, "", true).ServeHTTP(rr,
		httptest.NewRequest(http.MethodPost, "/wap/authorize/sign-in", nil))
	got := strings.Join(rr.Header().Values("Set-Cookie"), "; ")
	if !strings.Contains(got, "sfjth_login_ok=1") {
		t.Fatalf("成功响应缺少一次性标记：%s", got)
	}
	if strings.Contains(stripLoginMarker("token=abc; sfjth_login_ok=1"), "sfjth_login_ok") {
		t.Fatal("一次性标记未从会话 Cookie 中剔除")
	}
}

// WAP token 由前端 JS 写入且可能限定 /wap 路径，父页读不到；
// 代理必须从站点请求的 Cookie 头里收集，才能保证 /done 回传的会话完整。
func TestProxyCapturesRequestCookies(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer stub.Close()
	p := &loginPopup{doneOnce: &sync.Once{}, sessCookies: map[string]string{}}
	req := httptest.NewRequest(http.MethodGet, "/wap/api/profile", nil)
	req.Header.Set("Cookie", "token=abc123; main[UTMPUSERID]=bld")
	p.newProxy(stub.URL, "", true).ServeHTTP(httptest.NewRecorder(), req)
	got := p.collectedCookie()
	for _, want := range []string{"token=abc123", "main[UTMPUSERID]=bld"} {
		if !strings.Contains(got, want) {
			t.Fatalf("请求 Cookie 未被收集：%s（缺 %s）", got, want)
		}
	}
}

// 静态资源前缀必须带前导斜杠剥离，否则站点 JS 404（登录按钮无反应的根因）。
func TestStaticProxyStripsPrefix(t *testing.T) {
	var gotPath string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("OK"))
	}))
	defer stub.Close()
	p := &loginPopup{doneOnce: &sync.Once{}, sessCookies: map[string]string{}}
	h := p.newProxy(stub.URL, "/__static/static.mysmth.net/", false)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/__static/static.mysmth.net/nForum/js/pack.js", nil))
	if gotPath != "/nForum/js/pack.js" {
		t.Fatalf("静态前缀未正确剥离，站点 JS 会 404：got %q", gotPath)
	}
}

// 站点可能先返回登录成功响应、随后才由 JS 写入 token Cookie。
// 此时空 Cookie 不能消耗 doneOnce，否则后续 /done 回调会被误判为重复。
func TestEmptyCookieDoesNotConsumeDone(t *testing.T) {
	got := make(chan string, 2)
	p := &loginPopup{doneOnce: &sync.Once{}, sessCookies: map[string]string{},
		onDone: func(cookie, channel, user string) { got <- channel + "|" + cookie }}

	p.completeLogin("wap")
	select {
	case g := <-got:
		t.Fatalf("空 Cookie 不应触发投递：%s", g)
	default:
	}

	p.sessMu.Lock()
	p.sessCookies["token"] = "abc123"
	p.sessMu.Unlock()
	p.completeLogin("wap")
	select {
	case g := <-got:
		if g != "wap|token=abc123" {
			t.Fatalf("Cookie 到齐后应触发投递，got %q", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Cookie 到齐后未触发 onDone")
	}
}

// 外壳携带 Cookie 回传 /done 时，即使代理层先前空跑过，也必须完成投递。
func TestCallbackDeliversAfterEmptyProxyDetection(t *testing.T) {
	got := make(chan string, 2)
	p := &loginPopup{secret: "s", doneOnce: &sync.Once{}, sessCookies: map[string]string{},
		onDone: func(cookie, channel, user string) { got <- channel + "|" + cookie }}
	p.completeLogin("wap") // 空 Cookie：不能消耗 Once

	rr := httptest.NewRecorder()
	p.handleCallback(rr, httptest.NewRequest(http.MethodGet,
		"/done?t=s&c=token%3Dabc123&ch=wap&u=bld", nil))
	select {
	case g := <-got:
		if g != "wap|token=abc123" {
			t.Fatalf("/done 回传未投递：%q", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("/done 回传未触发 onDone")
	}
	if !strings.Contains(rr.Body.String(), "window.close()") {
		t.Fatal("回执页缺少自动关闭兜底")
	}

	// 第二次回调不应重复投递。
	p.handleCallback(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet,
		"/done?t=s&c=token%3Dabc123&ch=wap", nil))
	select {
	case g := <-got:
		t.Fatalf("重复回传不应再次投递：%s", g)
	default:
	}
}

func TestCallbackWithoutCookieDoesNotClose(t *testing.T) {
	got := make(chan string, 1)
	p := &loginPopup{secret: "s", doneOnce: &sync.Once{}, sessCookies: map[string]string{},
		onDone: func(cookie, channel, user string) { got <- cookie }}
	rr := httptest.NewRecorder()
	p.handleCallback(rr, httptest.NewRequest(http.MethodGet, "/done?t=s&ch=wap", nil))
	select {
	case g := <-got:
		t.Fatalf("无 Cookie 不应投递：%s", g)
	default:
	}
	if !strings.Contains(rr.Body.String(), "未获取到会话 Cookie") {
		t.Fatal("无 Cookie 时应给出重试提示")
	}
	if strings.Contains(rr.Body.String(), "window.close()") {
		t.Fatal("无 Cookie 时不能自动关闭窗口")
	}
}

// 前端事件之外必须有可轮询的结果通道，避免 WebView2 事件丢失导致不跳屏。
func TestPopupStatePolling(t *testing.T) {
	s := NewBbsService()
	s.popupGen = 7
	s.popupState = popupState{Generation: 7, Status: "open"}
	s.MarkPopupDone("success", "登录成功：bld", "bld", "wap")
	raw, err := s.LoginPopupState()
	if err != nil {
		t.Fatal(err)
	}
	var st popupState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	if st.Generation != 7 || st.Status != "success" || st.User != "bld" || st.Channel != "wap" {
		t.Fatalf("轮询状态不正确：%+v", st)
	}
}

// 构造函数必须保存 onDone 回调：此前漏掉赋值，导致代理识别成功却无人投递，
// 弹窗关闭后主菜单永远不跳。所有其它测试都用结构体字面量，覆盖不到这里。
func TestNewLoginPopupSavesCallbacks(t *testing.T) {
	got := make(chan string, 1)
	p := newLoginPopup(nil,
		func(cookie, channel, user string) { got <- "done|" + channel + "|" + cookie },
		func() { got <- "cancel" })
	if p.onDone == nil {
		t.Fatal("newLoginPopup 未保存 onDone 回调")
	}
	if p.onCancel == nil {
		t.Fatal("newLoginPopup 未保存 onCancel 回调")
	}
	p.sessMu.Lock()
	p.sessCookies["token"] = "abc123"
	p.sessMu.Unlock()
	p.completeLogin("wap")
	select {
	case g := <-got:
		if g != "done|wap|token=abc123" {
			t.Fatalf("构造函数的 onDone 未被调用：%q", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("构造函数的 onDone 未被调用")
	}
}

func TestRelativeLocation(t *testing.T) {
	cases := map[string]string{
		"https://www.newsmth.net/nForum/mainpage": "/nForum/mainpage",
		"http://www.mysmth.net/nForum/":           "/nForum/",
		"//wap.newsmth.net/":                      "/",
		"/nForum/index":                           "/nForum/index",
	}
	for in, want := range cases {
		if got := relativeLocation(in); got != want {
			t.Fatalf("relativeLocation(%q) = %q, want %q", in, got, want)
		}
	}
}
