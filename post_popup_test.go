package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"sfjth/internal/smth"
)

func TestPostShellHTMLVerifiesWithWap(t *testing.T) {
	html := postShellHTML("ade4a85345062fda4657d64aa3206cba", "http://x/done", "http://x/done?cancel=1", "Test · Re: hi")
	for _, want := range []string{
		"initGeetest4", "ade4a85345062fda4657d64aa3206cba", "getValidate",
		`captcha_id: "ade4a85345062fda4657d64aa3206cba"`, "client: 'wap'",
		"http://x/done", "http://x/done?cancel=1", "Test · Re: hi",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("外壳页缺少 %q: %s", want, html[:200])
		}
	}
	// 旧 web 发帖弹窗的预填逻辑必须彻底移除，避免再次出现空白验证窗。
	for _, bad := range []string{"__START_PATH__", "post_subject", "ajax_post.json", "sfjth_post_ok"} {
		if strings.Contains(html, bad) {
			t.Fatalf("外壳页残留旧逻辑 %q", bad)
		}
	}
	for _, ph := range []string{"__CAPTCHA_ID__", "__CB_DONE__", "__CB_CANCEL__", "__LABEL__"} {
		if strings.Contains(html, ph) {
			t.Fatalf("占位符未替换: %s", ph)
		}
	}
}

func TestWapCaptchaRoundTrip(t *testing.T) {
	in := smth.WapCaptcha{LotNumber: "lot", CaptchaOutput: "out", PassToken: "pass", GenTime: "123", CaptchaID: smth.WapCaptchaID(), Client: "wap"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out smth.WapCaptcha
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("验证码结果回传变形: %+v", out)
	}
}

func TestPostCallbackPageEscapes(t *testing.T) {
	rr := httptest.NewRecorder()
	postCallbackPage(rr, "发表失败：a<b>&c", false)
	body := rr.Body.String()
	if !strings.Contains(body, "a&lt;b&gt;&amp;c") {
		t.Fatalf("回调页未转义: %s", body)
	}
	if !strings.Contains(body, "window.close()") {
		t.Fatal("失败页也应提供关闭入口")
	}
}
