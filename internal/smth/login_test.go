package smth

// 用本地假服务器验证「真实 HTTP 交互」的请求字段与双通道回退，
// 不访问外网、不触碰用户配置目录。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testLoginSite struct {
	web     *httptest.Server
	wap     *httptest.Server
	webFail bool
	wapFail bool
	webForm map[string]string
	wapJSON map[string]string
}

func newTestLoginSite() *testLoginSite {
	s := &testLoginSite{}
	s.web = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nForum/":
			w.WriteHeader(http.StatusOK)
		case "/nForum/user/ajax_login.json":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = r.ParseForm()
			s.webForm = map[string]string{}
			for k, v := range r.Form {
				s.webForm[k] = v[0]
			}
			w.Header().Set("Content-Type", "application/json;charset=UTF-8")
			if s.webFail || s.webForm["passwd"] != "good" {
				_, _ = w.Write([]byte(`{"ajax_st":0,"ajax_code":"1103","ajax_msg":"验证码错误"}`))
				return
			}
			// Go 的 http.SetCookie 会拒绝带方括号的名字，真实站点是原样下发的。
			w.Header().Add("Set-Cookie", "main[UTMPUSERID]="+s.webForm["id"]+"; Path=/")
			_, _ = w.Write([]byte(`{"ajax_st":1,"ajax_code":"0005","ajax_msg":"操作成功"}`))
		case "/nForum/user/ajax_session.json":
			w.Header().Set("Content-Type", "application/json;charset=UTF-8")
			// 方括号 Cookie 名 Go 的 r.Cookie 会拒收，这里按真实站点那样解析原始头。
			ck := ""
			for _, part := range strings.Split(r.Header.Get("Cookie"), ";") {
				kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
				if len(kv) == 2 && kv[0] == "main[UTMPUSERID]" {
					ck = kv[1]
				}
			}
			if ck == "" || ck == "guest" {
				_, _ = w.Write([]byte(`{"id":"guest"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"` + ck + `","user_name":"测试用户"}`))
		case "/nForum/user/ajax_logout.json":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	s.wap = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.WriteHeader(http.StatusOK)
		case "/wap/authorize/sign-in":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			s.wapJSON = payload
			w.Header().Set("Content-Type", "application/json")
			if s.wapFail || payload["password"] != uriComponent("good") {
				_, _ = w.Write([]byte(`{"code":0,"kbsCode":9,"message":"无效的验证"}`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "token", Value: "wap-session", Path: "/"})
			_, _ = w.Write([]byte(`{"code":1,"kbsCode":0,"message":"成功"}`))
		case "/wap/api/profile":
			w.Header().Set("Content-Type", "application/json")
			if _, err := r.Cookie("token"); err != nil {
				_, _ = w.Write([]byte(`{"code":403,"message":"未登陆"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"id":"wapuser"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return s
}

func (s *testLoginSite) close() {
	s.web.Close()
	s.wap.Close()
}

func (s *testLoginSite) transport() *loginTransport {
	tr := newLoginTransport(loginEndpoints{web: s.web.URL, wap: s.wap.URL}, "")
	tr.seed()
	return tr
}

func TestWebLoginRealRequest(t *testing.T) {
	s := newTestLoginSite()
	defer s.close()
	tr := s.transport()
	a := tr.webLogin("tester", "good", LoginCaptcha{})
	if !a.OK {
		t.Fatalf("web 登录应成功，实际：%+v", a)
	}
	if a.User != "tester" {
		t.Fatalf("会话校验应返回 tester，实际 %q", a.User)
	}
	if s.webForm["id"] != "tester" || s.webForm["passwd"] != "good" || s.webForm["CookieDate"] != "2" {
		t.Fatalf("web 表单字段不正确：%#v", s.webForm)
	}
	if !strings.Contains(tr.Cookies(), "main[UTMPUSERID]=tester") {
		t.Fatalf("登录会话没有进入 CookieJar：%s", tr.Cookies())
	}
}

func TestWapLoginRealRequest(t *testing.T) {
	s := newTestLoginSite()
	defer s.close()
	tr := s.transport()
	a := tr.wapLogin("tester", "good", LoginCaptcha{})
	if !a.OK {
		t.Fatalf("wap 登录应成功，实际：%+v", a)
	}
	if s.wapJSON["username"] != "tester" || s.wapJSON["password"] != "good" {
		t.Fatalf("wap JSON 字段不正确：%#v", s.wapJSON)
	}
	if s.wapJSON["client"] != "wap" {
		t.Fatalf("wap 客户端标记缺失：%#v", s.wapJSON)
	}
	if !strings.Contains(tr.Cookies(), "token=wap-session") {
		t.Fatalf("wap 会话没有进入 CookieJar：%s", tr.Cookies())
	}
}

func TestAutoFallbackAndCaptcha(t *testing.T) {
	s := newTestLoginSite()
	defer s.close()
	s.webFail = true
	tr := s.transport()
	w := tr.webLogin("tester", "good", LoginCaptcha{})
	if w.OK || !w.CaptchaRequired {
		t.Fatalf("web 失败应标记验证码：%+v", w)
	}
	a := tr.wapLogin("tester", "good", LoginCaptcha{})
	if !a.OK {
		t.Fatalf("web 失败后 wap 应成功：%+v", a)
	}
	// 两条通道都失败时，两个结果都要能被登录屏展示。
	s.wapFail = true
	tr2 := s.transport()
	w2, a2 := tr2.webLogin("tester", "good", LoginCaptcha{}), tr2.wapLogin("tester", "good", LoginCaptcha{})
	if w2.CaptchaRequired != true || a2.CaptchaRequired != true {
		t.Fatalf("双通道验证码标记不正确：%+v / %+v", w2, a2)
	}
}

func TestCaptchaTokenForwarded(t *testing.T) {
	s := newTestLoginSite()
	defer s.close()
	tr := s.transport()
	cap := LoginCaptcha{Ticket: "tk", Randstr: "rs", LotNumber: "lot", CaptchaOutput: "out", PassToken: "pt", GenTime: "gt"}
	a := tr.webLogin("tester", "bad", cap)
	if a.OK {
		t.Fatal("错误密码不应登录成功")
	}
	if s.webForm["ticket"] != "tk" || s.webForm["lot_number"] != "lot" || s.webForm["pass_token"] != "pt" {
		t.Fatalf("验证码令牌没有转发给 web：%#v", s.webForm)
	}
	a = tr.wapLogin("tester", "bad", cap)
	if s.wapJSON["ticket"] != "tk" || s.wapJSON["client"] != "wap" {
		t.Fatalf("验证码令牌没有转发给 wap：%#v", s.wapJSON)
	}
}

func TestURIComponent(t *testing.T) {
	if got := uriComponent("a b+c/d@e"); got != "a%20b%2Bc%2Fd%40e" {
		t.Fatalf("encodeURIComponent 等价实现错误：%s", got)
	}
}

// 误填/复制来的令牌不能拦住真正的登录请求。
func TestParseLoginCaptchaLoose(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want LoginCaptcha
	}{
		{name: "empty", raw: "  ", want: LoginCaptcha{}},
		{
			name: "json",
			raw:  `{"ticket":"tk","randstr":"rs","lot_number":"lot","pass_token":"pt"}`,
			want: LoginCaptcha{Ticket: "tk", Randstr: "rs", LotNumber: "lot", PassToken: "pt"},
		},
		{
			name: "single quote json",
			raw:  `{'ticket':'tk','randstr':'rs',}`,
			want: LoginCaptcha{Ticket: "tk", Randstr: "rs"},
		},
		{
			name: "query string",
			raw:  "ticket=tk&randstr=rs&lot_number=lot",
			want: LoginCaptcha{Ticket: "tk", Randstr: "rs", LotNumber: "lot"},
		},
		{
			name: "comma separated",
			raw:  "ticket=tk,randstr=rs",
			want: LoginCaptcha{Ticket: "tk", Randstr: "rs"},
		},
		{
			name: "nested data",
			raw:  `{"data":{"lot_number":"lot","captcha_output":"out"}}`,
			want: LoginCaptcha{LotNumber: "lot", CaptchaOutput: "out"},
		},
		{
			name: "plain token",
			raw:  "o",
			want: LoginCaptcha{Ticket: "o"},
		},
		{
			name: "broken json becomes ticket",
			raw:  `{"ticket":"tk"`,
			want: LoginCaptcha{Ticket: `{"ticket":"tk"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLoginCaptcha(tc.raw)
			if err != nil {
				t.Fatalf("宽容解析不应报错：%v", err)
			}
			if got != tc.want {
				t.Fatalf("解析结果不符：got %+v, want %+v", got, tc.want)
			}
		})
	}
}
