package main

// 发帖/回帖验证窗口。
//
// 水木 web/wap 发文都必须过验证码；web 的 ajax_post.json 即使 ajax_st:1 也可能是
// 不落库的假成功，wap 的 JSON 发文接口则在未验证时明确返回 code=0「无效的验证」。
// 因此这里改为：
//   1) 本地同源代理加载 wap 的脚本环境（只为拿到站点自带的极验 4 初始化能力）；
//   2) 窗口只显示极验按钮，用户点一下完成验证；
//   3) Go 侧拿验证结果直接调用 wap 的 /api/topic/publish 或 /api/topic/reply；
//   4) 成功后关闭窗口并通知前端刷新正文/列表。
// 附件在打开窗口前已上传到 wap 临时区，正文带着 uploadToken 一起提交。

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"sfjth/internal/smth"
)

type postPopup struct {
	app       *application.App
	client    *smth.Client
	mu        sync.Mutex
	listener  net.Listener
	port      int
	secret    string
	win       *application.WebviewWindow
	cookie    string
	onSuccess func(articleURL, message string)
	onCancel  func()

	doneOnce sync.Once
	gen      int64
	genMu    sync.Mutex

	req    PostRequest
	target smth.WapPostTarget
}

func newPostPopup(app *application.App, client *smth.Client, onSuccess func(string, string), onCancel func()) *postPopup {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &postPopup{app: app, client: client, secret: hex.EncodeToString(b), onSuccess: onSuccess, onCancel: onCancel}
}

func (p *postPopup) base() string { return fmt.Sprintf("http://127.0.0.1:%d", p.port) }

func (p *postPopup) ensureServer() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	p.listener = ln
	p.port = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/__shell", p.handleShell)
	mux.HandleFunc("/done", p.handleCallback)
	mux.Handle("/", p.reverseProxy())
	go func(l net.Listener, h http.Handler) { _ = http.Serve(l, h) }(ln, mux)
	return nil
}

// reverseProxy 把 wap 脚本环境代理到同源。wap 的静态资源本来就在 /static 下，
// 不需要像 nForum 那样改写 static.mysmth.net；只需解除 iframe 限制并注入 Cookie。
func (p *postPopup) reverseProxy() http.Handler {
	target, _ := url.Parse("https://wap.newsmth.net")
	rp := httputil.NewSingleHostReverseProxy(target)
	baseDirector := rp.Director
	rp.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = target.Host
		r.Header.Del("Accept-Encoding")
		if ck := p.cookie; ck != "" {
			r.Header.Set("Cookie", ck)
		}
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Content-Security-Policy-Report-Only")
		return nil
	}
	return rp
}

// PostRequest 描述一次发帖/回帖请求。BoardHash/ArticleHash 由 Client 解析后填入。
type PostRequest struct {
	Board       string
	Subject     string
	Content     string
	ThreadID    int64
	Floor       int
	IsReply     bool
	BoardHash   string
	ArticleHash string
	UploadToken string
}

// Open 打开验证窗口。
func (p *postPopup) Open(cookie string, req PostRequest) (string, error) {
	if err := p.ensureServer(); err != nil {
		return "", err
	}
	p.mu.Lock()
	p.closeWindowLocked()
	p.cookie = cookie
	p.req = req
	p.doneOnce = sync.Once{}
	p.mu.Unlock()
	p.genMu.Lock()
	p.gen++
	gen := p.gen
	p.genMu.Unlock()

	q := url.Values{}
	q.Set("s", p.secret)
	q.Set("captcha_id", smth.WapCaptchaID())
	q.Set("done", p.base()+"/done?t="+p.secret)
	q.Set("cancel", p.base()+"/done?t="+p.secret+"&cancel=1")
	q.Set("label", fmt.Sprintf("%s · %s", req.Board, firstLine(req.Subject)))

	popupHeight := 520
	width, x, y := 700, 0, 0
	if main, ok := p.app.Window.GetByName("main"); ok {
		b := main.Bounds()
		width = b.Width - 80
		if width > 700 {
			width = 700
		}
		if width < 460 {
			width = 460
		}
		x = b.X + (b.Width-width)/2
		y = b.Y + (b.Height-popupHeight)/2
	}

	w := p.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "sfjth-post-popup",
		Title:            "发表验证 - sfjth",
		URL:              p.base() + "/__shell?" + q.Encode(),
		Width:            width,
		Height:           popupHeight,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    false,
		BackgroundColour: application.NewRGB(255, 255, 255),
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
	w.SetPosition(x, y)
	w.Show()
	return fmt.Sprintf("已打开发表验证窗口，gen=%d", gen), nil
}

func (p *postPopup) handleShell(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("s") != p.secret {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	html := postShellHTML(q.Get("captcha_id"), q.Get("done"), q.Get("cancel"), q.Get("label"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (p *postPopup) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("t") != p.secret {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Query().Get("cancel") == "1" {
		p.doneOnce.Do(func() {
			go func() {
				p.closeWindow()
				if p.onCancel != nil {
					p.onCancel()
				}
				application.Get().Event.Emit("sfjth:post-cancelled", map[string]any{})
			}()
		})
		postCallbackPage(w, "已取消发表，正在返回 sfjth ...", true)
		return
	}

	var captcha smth.WapCaptcha
	raw := r.URL.Query().Get("captcha")
	if err := json.Unmarshal([]byte(raw), &captcha); err != nil {
		postCallbackPage(w, "验证结果无效，请重试", false)
		return
	}
	p.mu.Lock()
	req, target, client := p.req, p.target, p.client
	p.mu.Unlock()
	var (
		articleURL string
		err        error
	)
	if req.IsReply {
		articleURL, err = client.WapReply(target, req.Content, req.UploadToken, captcha)
	} else {
		articleURL, err = client.WapPublish(target, req.Subject, req.Content, req.UploadToken, captcha)
	}
	if err != nil {
		postCallbackPage(w, "发表失败："+err.Error(), false)
		return
	}
	p.doneOnce.Do(func() {
		go func() {
			p.closeWindow()
			if p.onSuccess != nil {
				p.onSuccess(articleURL, "发表成功（已通过 wap 验证）")
			}
		}()
	})
	postCallbackPage(w, "文章已发布，正在返回 sfjth ...", true)
}

func (p *postPopup) setTarget(t smth.WapPostTarget) {
	p.mu.Lock()
	p.target = t
	p.mu.Unlock()
}

func (p *postPopup) closeWindow() {
	p.mu.Lock()
	p.closeWindowLocked()
	p.mu.Unlock()
}

func (p *postPopup) closeWindowLocked() {
	if p.win != nil {
		p.win.Close()
		p.win = nil
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return s
}

// postCallbackPage 写一个自动关闭的回执页；fail 时给用户重试入口。
func postCallbackPage(w http.ResponseWriter, message string, autoClose bool) {
	script := "window.close();"
	if !autoClose {
		script = "document.getElementById('msg').textContent=" + jsQuote(message) + ";"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><style>body{font:14px Consolas,monospace;background:#fff;color:#222;padding:24px}</style><div id="msg">`+
		html.EscapeString(message)+`</div><button onclick="window.close()">关闭窗口</button><script>`+script+`</script>`)
}

func postShellHTML(captchaID, cbDone, cbCancel, label string) string {
	repl := strings.NewReplacer(
		"__CAPTCHA_ID__", jsQuote(captchaID),
		"__CB_DONE__", jsQuote(cbDone),
		"__CB_CANCEL__", jsQuote(cbCancel),
		"__LABEL__", jsQuote(label),
	)
	return repl.Replace(`<!doctype html>
<html><head><meta charset="utf-8"><title>发表验证 - sfjth</title>
<style>
html,body{margin:0;height:100%;overflow:hidden;background:#fff;font:13px/1.5 Consolas,"Microsoft YaHei",monospace;color:#222}
#bar{height:40px;background:#0b3d91;color:#fff;display:flex;align-items:center;justify-content:space-between;padding:0 14px;box-sizing:border-box}
#bar button{background:transparent;border:1px solid #9fc;color:#fff;padding:3px 12px;cursor:pointer}
#site{width:100%;height:calc(100% - 88px);border:0;display:block;background:#fff;opacity:0;transition:opacity .2s}
#tip{height:48px;line-height:48px;text-align:center;color:#555}
</style></head><body>
<div id="bar"><span id="label">__LABEL__</span><span>请点一下下方验证按钮<button id="close">取消</button></span></div>
<iframe id="site" src="/post"></iframe>
<div id="tip">验证完成后自动发表，无需其它操作</div>
<script>
(function () {
  var frame = document.getElementById('site');
  var cbDone = __CB_DONE__, cbCancel = __CB_CANCEL__;
  document.getElementById('close').onclick = function () { location.replace(cbCancel); };

  frame.addEventListener('load', function () {
    var win, doc;
    try { win = frame.contentWindow; doc = frame.contentDocument; } catch (e) { return; }
    if (!win || !doc || !doc.body) return;
    // wap 页面的脚本已经加载完成，initGeetest4 挂在 iframe 的 window 上。
    // 清掉页面只保留验证容器，让用户看到的是一个干净的验证窗口。
    doc.body.innerHTML = '<div id="sfjth-gt" style="position:absolute;left:50%;top:30%;transform:translate(-50%,-50%)"></div>';
    doc.body.style.background = '#fff';
    var tries = 0;
    var timer = setInterval(function () {
      tries++;
      if (typeof win.initGeetest4 === 'function') {
        clearInterval(timer);
        win.initGeetest4({
          captchaId: __CAPTCHA_ID__,
          product: 'float',
          rem: '0.75',
          nativeButton: {width: '280px'}
        }, function (obj) {
          var host = doc.getElementById('sfjth-gt');
          if (!host) return;
          obj.appendTo(host);
          frame.style.opacity = '1';
          obj.onSuccess(function () {
            var v = obj.getValidate() || {};
            // wap 前端的真实 geetestResult 还包含 captcha_id 与 client 两个字段，
            // 服务端校验必须拿到 captcha_id，否则会报「请求无效」。
            location.replace(cbDone + '&captcha=' + encodeURIComponent(JSON.stringify({
              lot_number: v.lot_number,
              captcha_output: v.captcha_output,
              pass_token: v.pass_token,
              gen_time: v.gen_time,
              captcha_id: __CAPTCHA_ID__,
              client: 'wap'
            })));
          });
        });
      } else if (tries > 100) {
        clearInterval(timer);
        doc.getElementById('sfjth-gt').textContent = '验证组件加载失败，请关闭窗口重试';
        frame.style.opacity = '1';
      }
    }, 120);
  });
})();
</script></body></html>`)
}
