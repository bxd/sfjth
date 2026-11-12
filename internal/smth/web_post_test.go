package smth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseWebArticleIDsByFloor(t *testing.T) {
	body := `<div class="b-content">
<a name="a0"></a><div class="a-wrap"><a href="/nForum/article/NewSoftware/post/295666" class="a-post">回复</a></div>
<a name="a1"></a><div class="a-wrap"><a href="/nForum/article/NewSoftware/post/295676" class="a-post">回复</a></div>
<a name="a2"></a><div class="a-wrap"><a href="/nForum/article/NewSoftware/post/295677" class="a-post">回复</a></div>
</div>`
	ids := parseWebArticleIDs(body)
	want := map[int]int64{1: 295666, 2: 295676, 3: 295677}
	for floor, id := range want {
		if ids[floor] != id {
			t.Fatalf("floor %d: got %d, want %d", floor, ids[floor], id)
		}
	}
}

func TestPlausibleWebArticleID(t *testing.T) {
	cases := map[int64]bool{
		0: false, -1: false,
		295666: true, 109970: true,
		47765785:         true,  // 站点现在的真实文章号已是 8 位
		1581158902619142: false, // wap hash 派生的长数字不是 web 文章号
		1000000000000:    false,
	}
	for id, want := range cases {
		if got := plausibleWebArticleID(id); got != want {
			t.Fatalf("plausibleWebArticleID(%d) = %v, want %v", id, got, want)
		}
	}
}

func TestPostArticleRejectsFakeSuccess(t *testing.T) {
	// 站点对未过验证码的提交返回 ajax_st:1，但 list 只有 .../11 占位链接。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"list":[{"text":"主题:X","url":"/article/Test/11"},{"text":"水木社区","url":"/mainpage"}],"ajax_st":1,"ajax_code":"0005","ajax_msg":"操作成功"}`))
	}))
	defer srv.Close()
	w := NewWebClient(Config{BaseURL: srv.URL})
	err := w.PostArticle("Test", "X", "content", 0)
	if err == nil || !strings.Contains(err.Error(), "未接受") {
		t.Fatalf("假成功应被识别为失败，got %v", err)
	}
}

func TestPostArticleAcceptsRealSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"list":[{"text":"主题:X","url":"/article/Test/967207"}],"ajax_st":1,"ajax_code":"0005","ajax_msg":"操作成功"}`))
	}))
	defer srv.Close()
	w := NewWebClient(Config{BaseURL: srv.URL})
	if err := w.PostArticle("Test", "X", "content", 0); err != nil {
		t.Fatalf("真实成功不应报错，got %v", err)
	}
}

func TestUploadAttachmentMark(t *testing.T) {
	var requestedWith string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/ajax_add.json") {
			t.Errorf("上传路径错误: %s", r.URL.Path)
		}
		requestedWith = r.Header.Get("X-Requested-With")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"no":3,"name":"a.png","size":10,"ajax_st":1,"ajax_code":"0005","ajax_msg":"操作成功"}`))
	}))
	defer srv.Close()
	w := NewWebClient(Config{BaseURL: srv.URL})
	mark, err := w.UploadAttachment("Test", "a.png", []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if mark != "[upload=3][/upload]" {
		t.Fatalf("upload 标记错误: %q", mark)
	}
	// 站点 PLUpload 必须带 X-Requested-With，否则真实服务端返回 404 空响应。
	if requestedWith != "XMLHttpRequest" {
		t.Fatalf("上传缺少 X-Requested-With: %q", requestedWith)
	}
}

func TestWapUploadUsesFileEndpoint(t *testing.T) {
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		_ = r.ParseMultipartForm(1 << 20)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"data":[{"key":"abc.png","name":"abc.png"}],"message":"操作成功"}`))
	}))
	defer srv.Close()
	client := &WapClient{Base: srv.URL, HTTP: srv.Client(), attachByArticle: map[string][]Attachment{}}
	name, err := client.UploadAttachment("boardhash", "token123", "a.png", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	if name != "abc.png" {
		t.Fatalf("上传返回文件名错误: %q", name)
	}
	if lastPath != "/wap/api/file/upload/boardhash/token123" {
		t.Fatalf("上传路径错误: %q", lastPath)
	}
}

func TestWapPublishPayloadAndFakeSuccess(t *testing.T) {
	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		if strings.HasSuffix(r.URL.Path, "/wap/api/topic/publish") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":1,"data":{"id":"abc"},"kbsCode":0,"message":"操作成功"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"无效的验证"}`))
	}))
	defer srv.Close()
	w := &WapClient{Base: srv.URL, HTTP: srv.Client(), attachByArticle: map[string][]Attachment{}}
	c := WapCaptcha{LotNumber: "lot", CaptchaOutput: "out", PassToken: "pass", GenTime: "1", CaptchaID: WapCaptchaID()}
	if _, err := w.Publish("boardhash", "标题", "正文", "tok", c); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"boardId=boardhash", "subject=%E6%A0%87%E9%A2%98", "body=%E6%AD%A3%E6%96%87", "uploadToken=tok", "lot_number=lot", "pass_token=pass", "type=3", "client=wap", "captcha_id=ade4a85345062fda4657d64aa3206cba", "t="} {
		if !strings.Contains(lastBody, want) {
			t.Fatalf("wap 发文参数缺少 %s: %s", want, lastBody)
		}
	}
	// 没过验证的提交必须明确失败，不能被当作成功。
	if _, err := w.Publish("boardhash", "标题", "正文", "", WapCaptcha{}); err == nil {
		t.Fatal("空验证码应失败")
	}
}

func TestPostArticleUsesAjaxEndpointAndChecksStatus(t *testing.T) {
	var lastPath, lastForm, lastReferer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		lastReferer = r.Header.Get("Referer")
		_ = r.ParseForm()
		lastForm = r.Form.Encode()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"ajax_st":0,"ajax_code":"0100","ajax_msg":"请先登录"}`))
	}))
	defer srv.Close()

	w := NewWebClient(Config{BaseURL: srv.URL})
	err := w.PostArticle("NewSoftware", "Re: 测试", "内容", 295676)
	if err == nil || !strings.Contains(err.Error(), "请先登录") {
		t.Fatalf("应返回站点错误消息，got %v", err)
	}
	if lastPath != "/nForum/article/NewSoftware/ajax_post.json" {
		t.Fatalf("提交路径错误: %q", lastPath)
	}
	if !strings.Contains(lastForm, "id=295676") || !strings.Contains(lastForm, "content=%E5%86%85%E5%AE%B9") {
		t.Fatalf("表单缺少回复目标或正文: %q", lastForm)
	}
	if !strings.HasSuffix(lastReferer, "/post/295676") {
		t.Fatalf("Referer 应指向所回复楼层: %q", lastReferer)
	}
}

func TestInvalidatePostCache(t *testing.T) {
	c := &Client{cache: NewCache()}
	c.cache.Put("post:Test:123:1:web", ThreadResult{}, 0)
	c.cache.Put("threads:Test:1:web", ThreadsResult{}, 0)
	c.cache.Put("post:Other:123:1:web", ThreadResult{}, 0)
	c.InvalidatePostCache("Test", 123)
	if _, ok := c.cache.Get("post:Test:123:1:web"); ok {
		t.Fatal("正文缓存未失效")
	}
	if _, ok := c.cache.Get("threads:Test:1:web"); ok {
		t.Fatal("列表缓存未失效")
	}
	if _, ok := c.cache.Get("post:Other:123:1:web"); !ok {
		t.Fatal("误删了其它主题的缓存")
	}
}

func TestWapArticleHashByFloorReturnsArticleID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/loadArticlesByMode/") {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"data":{"articles":[{"id":"art-one","topicOrder":1,"topicId":"topic-one"}]}}`))
	}))
	defer srv.Close()
	cl := &WapClient{Base: srv.URL, HTTP: srv.Client(), attachByArticle: map[string][]Attachment{}}
	got, err := cl.wapArticleHashByFloor("topic-one", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != "art-one" {
		t.Fatalf("一楼回帖应使用文章 id，got %q", got)
	}
}

func TestWebMailBoxAndDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/nForum/mail/inbox":
			_, _ = io.WriteString(w, `<div class="mail-list"><form><table class="m-table">
<tr class="no-read"><td><input type="checkbox" name="m_2" class="mail-item"/></td><td><a href="/u/a">deliver</a></td><td><a href="/nForum/mail/inbox/2.json">[系统] 积分变更</a></td><td>2026-10-20 11:28:39</td></tr>
<tr><td><input type="checkbox" name="m_1" class="mail-item"/></td><td><a href="/u/b">SYSOP</a></td><td><a href="/nForum/mail/inbox/1.json">Welcome</a></td><td>2026-09-20 17:07:37</td></tr>
</table></form></div>`)
		case "/nForum/mail/inbox/2.json":
			w.Header().Set("Content-Type", "application/json;charset=utf-8")
			_, _ = io.WriteString(w, `{"num":"2","content":"积分：<br />1184","ajax_st":1}`)
		default:
			t.Errorf("未预期路径: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	cl := NewWebClient(Config{BaseURL: srv.URL})
	mails, err := cl.MailBox("inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(mails) != 2 || mails[0].ID != 2 || mails[0].From != "deliver" || !mails[0].Unread || mails[0].Box != "inbox" {
		t.Fatalf("信箱解析错误: %+v", mails)
	}
	if mails[1].Unread {
		t.Fatalf("已读邮件被误标为未读: %+v", mails[1])
	}
	body, err := cl.MailDetail("inbox", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "1184") || !strings.Contains(body, "积分") {
		t.Fatalf("详情正文错误: %q", body)
	}
}
