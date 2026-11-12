package smth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// 离线解析测试：校验 wapToBoard 把英文版名写进 ID（跨站回落 URL 不换的关键）。
func TestWapToBoardUsesEnglishNameAsID(t *testing.T) {
	b := wapToBoard(wapBoard{
		ID:             "c8d614e56acb8a192ec4af8b375a5eea",
		Name:           "NewExpress",
		Title:          "水木特快",
		TodayPostCount: 12,
	})
	if b.ID != "NewExpress" {
		t.Fatalf("Board.ID 应为英文版名，便于跨站回落，got %q", b.ID)
	}
	if b.Name != "水木特快" {
		t.Fatalf("Board.Name 应为中文版名，got %q", b.Name)
	}
	if b.Today != 12 {
		t.Fatalf("今日发帖数错误：%d", b.Today)
	}
}

func TestIsWAPHash(t *testing.T) {
	if !isWAPHash("c8d614e56acb8a192ec4af8b375a5eea") {
		t.Fatal("32 位十六进制应判为 wap hash")
	}
	for _, s := range []string{"", "1", "NewExpress", "xyz", strings.Repeat("g", 32)} {
		if isWAPHash(s) {
			t.Fatalf("%q 不应判为 wap hash", s)
		}
	}
}

// hashID 必须稳定、为正、且落在 JS 安全整数范围内（前端 Number(id) 不丢精度）。
func TestHashIDStableAndJSSafe(t *testing.T) {
	const h = "dc0129b4430e033c8e5662ae3b246e75"
	a := hashID(h)
	b := hashID(h)
	if a <= 0 || a != b {
		t.Fatalf("hashID 不稳定或非正：%d vs %d", a, b)
	}
	if a >= 1<<53 {
		t.Fatalf("hashID 超出 JS 安全整数范围：%d", a)
	}
	if hashID(strings.ToUpper(h)) != a {
		t.Fatal("hashID 应忽略大小写")
	}
	if hashID("12345") != 12345 {
		t.Fatal("纯数字 id 应原样返回")
	}
}

// wap 附件的 KS3 地址要排在 CDN 前面（实测 exp CDN 偶发 502），
// 并且相对 url 要补全为绝对地址，否则前端代理会拒绝。
func TestWapToAttachmentPrefersKS3(t *testing.T) {
	raw := `{"id":"5cff0545910a825dfa98a1ec2644ed89","name":"photo.jpg","type":"image/jpeg","size":101626,"url":"attachment/5cff0545910a825dfa98a1ec2644ed89","cdnUrl":"https://exp.newsmth.net/attachment/5cff0545910a825dfa98a1ec2644ed89","ks3Url":"https://ks3-cn-beijing.ksyun.com/attachment/45b39d62a07541174c94ffe834b90301"}`
	var wa wapAttachment
	if err := json.Unmarshal([]byte(raw), &wa); err != nil {
		t.Fatal(err)
	}
	a := wapToAttachment(wa)
	if a.Kind != "image" || a.URL != wa.KS3URL {
		t.Fatalf("附件类型/首选地址错误：kind=%q url=%q", a.Kind, a.URL)
	}
	if len(a.URLs) != 4 || !strings.HasPrefix(a.URLs[2], "https://wap.newsmth.net/") ||
		!strings.HasPrefix(a.URLs[3], "https://file.mysmth.net/") {
		t.Fatalf("候选地址应含 KS3/CDN/补全后的相对地址/file 兜底，got %v", a.URLs)
	}
}

// 主题列表给出的附件地址比正文详情更新：合并后正文附件必须带上 file.mysmth.net 候选。
func TestWapThreadListAttachmentURLsMergedIntoPost(t *testing.T) {
	const aid = "6a8d0e955cb1f525fd2d111952ebca87"
	w := &WapClient{attachByArticle: map[string][]Attachment{}}
	w.rememberArticleAttachments([]string{"first-article-hash"}, []wapAttachment{{
		ID:     aid,
		Name:   "t1.png",
		Type:   "image/png",
		CDNURL: "https://file.mysmth.net/attachment/" + aid,
		URL:    "attachment/" + aid,
	}}, "NewSoftware", "我把FQTerm重写并魔改了")
	posts := []Post{{
		HashID: "first-article-hash",
		Attachments: []Attachment{{
			ID: aid, Name: "t1.png", Type: "image/png", Kind: "image",
			URL:  "https://exp.newsmth.net/attachment/" + aid,
			URLs: []string{"https://exp.newsmth.net/attachment/" + aid},
		}},
	}}
	w.EnrichAttachments(posts)
	if len(posts[0].Attachments) != 1 {
		t.Fatalf("合并后不应产生重复附件：%v", posts[0].Attachments)
	}
	joined := strings.Join(append([]string{posts[0].Attachments[0].URL}, posts[0].Attachments[0].URLs...), "\n")
	if !strings.Contains(joined, "https://file.mysmth.net/attachment/"+aid) {
		t.Fatalf("正文附件缺少主题列表提供的 file 地址：%v", posts[0].Attachments[0].URLs)
	}
	if !strings.Contains(joined, "https://exp.newsmth.net/attachment/"+aid) {
		t.Fatalf("正文详情原有的 exp 地址不应丢失：%v", posts[0].Attachments[0].URLs)
	}
}

// wap 地址失败时要能生成 web 主站地址取同一个附件；ID 与 KS3 hash 两种都要覆盖。
func TestWebAttachmentFallbackURLs(t *testing.T) {
	a := Attachment{
		ID:  "5bf1d49cc83cae01b05847331ce724c1",
		URL: "https://wap.newsmth.net/attachment/5bf1d49cc83cae01b05847331ce724c1",
		URLs: []string{
			"https://ks3-cn-beijing.ksyun.com/attachment/d2adf25f26908e84d5d6085d966872f3",
			"https://exp.newsmth.net/attachment/5bf1d49cc83cae01b05847331ce724c1",
		},
	}
	all := attachmentCandidates(a)
	joined := strings.Join(all, "\n")
	for _, want := range []string{
		"https://www.newsmth.net/attachment/5bf1d49cc83cae01b05847331ce724c1",
		"https://www.newsmth.net/attachment/d2adf25f26908e84d5d6085d966872f3",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("候选地址应包含 web 兜底 %q，got %v", want, all)
		}
	}
	// web 地址必须排在 wap/KS3 地址之后（wap 优先）。
	webIdx, wapIdx := -1, -1
	for i, u := range all {
		if strings.Contains(u, "www.newsmth.net") && webIdx < 0 {
			webIdx = i
		}
		if strings.Contains(u, "ksyun.com") && wapIdx < 0 {
			wapIdx = i
		}
	}
	if wapIdx < 0 || webIdx < 0 || webIdx <= wapIdx {
		t.Fatalf("web 地址应排在 KS3 之后：wapIdx=%d webIdx=%d all=%v", wapIdx, webIdx, all)
	}
}

func TestIsHTMLResponse(t *testing.T) {
	errPage := []byte(`<script>if(!document.getElementById('body')){...}</script><div class="error"><h5>论坛错误信息</h5><li>找不到指定文件</li>`)
	if !isHTMLResponse("text/html;charset=GBK", errPage) {
		t.Fatal("应识别 nForum 错误页")
	}
	// 真正的 html 附件不应被误杀。
	realHTML := []byte(`<html><head><title>我的网页</title></head><body>hello</body></html>`)
	if isHTMLResponse("text/html", realHTML) {
		t.Fatal("正常 html 附件不应被判为错误页")
	}
	png := []byte("\x89PNG\r\n\x1a\n" + string(errPage))
	if isHTMLResponse("image/png", png) {
		t.Fatal("图片附件不应被判为错误页")
	}
}

// 站点 SPA 会给未知附件路径 200 返回 text/html 首页，不能当图片预览。
func TestAttachmentContentTypeOK(t *testing.T) {
	img := Attachment{Name: "t1.png", Kind: "image"}
	if attachmentContentTypeOK(img, "text/html; charset=utf-8") {
		t.Fatal("png 附件返回 HTML 首页应被拒绝")
	}
	if !attachmentContentTypeOK(img, "image/png") || !attachmentContentTypeOK(img, "application/octet-stream") {
		t.Fatal("正常图片/二进制流应放行")
	}
	html := Attachment{Name: "page.html", Kind: "html"}
	if !attachmentContentTypeOK(html, "text/html;charset=utf-8") {
		t.Fatal("html 附件返回 html 应放行")
	}
}

func TestAttachmentKindAndURLWhitelist(t *testing.T) {
	cases := map[string]string{
		"a.mp3": "audio", "b.mp4": "video", "c.pdf": "pdf", "d.txt": "text",
		"e.html": "html", "f.docx": "doc", "g.xlsx": "excel", "h.bin": "file",
	}
	for name, want := range cases {
		if got := AttachmentKind(name, ""); got != want {
			t.Fatalf("%s 应为 %s，got %s", name, want, got)
		}
	}
	if !allowedAttachmentURL("https://ks3-cn-beijing.ksyun.com/attachment/abc") {
		t.Fatal("应允许 KS3 附件地址")
	}
	for _, u := range []string{
		"http://ks3-cn-beijing.ksyun.com/attachment/abc",
		"https://evil.example.com/attachment/abc",
		"https://wap.newsmth.net/user/profile",
	} {
		if allowedAttachmentURL(u) {
			t.Fatalf("不应允许附件地址 %q", u)
		}
	}
}

func TestFormatMS(t *testing.T) {
	if got := formatMS(0); got != "" {
		t.Fatalf("0 时间应为空，got %q", got)
	}
	// 1794397754000 ms = 2026-11-11 19:49:14 UTC+8
	if got := formatMS(1794397754000); got != "11-11 19:49" {
		t.Fatalf("时间格式错误：%q", got)
	}
}

// term 列表「上旧下新」：接口给的是新→旧，反转成旧→新；置顶公告沉到最底部。
func TestTermThreadOrder(t *testing.T) {
	in := []Thread{
		{Subject: "最新", Flag: " "},
		{Subject: "较新", Flag: " "},
		{Subject: "置顶甲", Flag: "◆"},
		{Subject: "较早", Flag: " "},
		{Subject: "置顶乙", Flag: "◆"},
		{Subject: "最早", Flag: " "},
	}
	got := termThreadOrder(in)
	want := []string{"最早", "较早", "较新", "最新", "置顶甲", "置顶乙"}
	if len(got) != len(want) {
		t.Fatalf("长度不符：%d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Subject != want[i] {
			t.Fatalf("第 %d 项应为 %q，实为 %q", i, want[i], got[i].Subject)
		}
	}
	// 不能改动入参切片底层数组（缓存/映射会复用 items）。
	if in[0].Subject != "最新" {
		t.Fatalf("termThreadOrder 改动了入参：%q", in[0].Subject)
	}
}

func TestWAPGetURL(t *testing.T) {
	// 仅校验 URL 拼接与查询串，不实际联网。
	w := &WapClient{Base: wapBaseURL}
	q := url.Values{"id": {"abc"}}
	got := strings.TrimRight(w.Base, "/") + "/wap/api/section/subs" + "?" + q.Encode()
	if !strings.Contains(got, "id=abc") || !strings.HasPrefix(got, "https://wap.newsmth.net/wap/") {
		t.Fatalf("wap URL 拼接错误：%s", got)
	}
}

// 联网冒烟：从真实 wap 站读取某版主题列表 + 单帖正文。用 -short 跳过。
// 这是「进入版面后的文章列表」端到端校验：列表返回 hash 主题，正文按 hash 可读。
func TestWapThreadsAndPostsLive(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网测试")
	}
	cookie := strings.TrimSpace(os.Getenv("SMTH_COOKIE"))
	w := NewWapClient(Config{Cookie: cookie})
	list, pages, err := w.Threads("Astrology", 1)
	if err != nil {
		t.Fatalf("wap 主题列表读取失败：%v", err)
	}
	if len(list) == 0 || pages < 1 {
		t.Fatalf("主题列表异常：items=%d pages=%d", len(list), pages)
	}
	th := list[0]
	if th.HashID == "" || th.ID <= 0 || th.ID >= 1<<53 {
		t.Fatalf("主题 ID/hash 异常：id=%d hash=%q", th.ID, th.HashID)
	}
	if th.Subject == "" {
		t.Fatal("主题标题为空")
	}
	posts, subject, pp, _, err := w.Posts(th.BoardID, th.HashID, 1)
	if err != nil {
		t.Fatalf("wap 正文读取失败：%v", err)
	}
	if len(posts) == 0 || pp < 1 {
		t.Fatalf("正文异常：items=%d pages=%d", len(posts), pp)
	}
	if subject == "" && posts[0].Subject == "" {
		t.Fatal("正文标题为空")
	}
}

// 联网冒烟：从真实 wap 站读取分区列表（游客可读）。用 -short 跳过。
func TestWapSectionsLive(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网测试")
	}
	w := NewWapClient(Config{})
	list, err := w.Sections()
	if err != nil {
		t.Fatalf("wap 分区读取失败：%v", err)
	}
	if len(list) < 5 {
		t.Fatalf("分区数量异常：%d", len(list))
	}
	if !strings.Contains(list[0].Name, "") || list[0].ID == "" {
		t.Fatal("分区数据不完整")
	}
}

// 联网冒烟：用指定 Cookie 读收藏。需环境变量 SMTH_COOKIE；未设置则跳过。
func TestWapFavoritesLive(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网测试")
	}
	cookie := ""
	if v := strings.TrimSpace(os.Getenv("SMTH_COOKIE")); v == "" {
		t.Skip("未设置 SMTH_COOKIE，跳过收藏夹联网测试")
	} else {
		cookie = v
	}
	w := NewWapClient(Config{Cookie: cookie})
	list, err := w.Favorites()
	if err != nil {
		t.Fatalf("wap 收藏读取失败：%v", err)
	}
	if len(list) == 0 {
		t.Fatal("收藏夹为空")
	}
	for _, b := range list {
		if b.ID == "" || isWAPHash(b.ID) {
			t.Fatalf("收藏版 ID 应为英文版名：%q", b.ID)
		}
	}
}

// web 正文兜底：静态附件路径必须放行，且能从 nForum HTML 里提取真实附件链接。
func TestWebAttachmentFallbackParsing(t *testing.T) {
	u := "https://static.mysmth.net/nForum/att/NewSoftware/295666/579"
	if !allowedAttachmentURL(u) {
		t.Fatalf("web 静态附件路径应放行：%s", u)
	}
	body := `<a href="//static.mysmth.net/nForum/att/NewSoftware/295666/579">` +
		`<img src="//static.mysmth.net/nForum/att/NewSoftware/295666/579/large" /></a>` +
		`<a href="//static.mysmth.net/nForum/att/NewSoftware/295666/381246">x</a>`
	matches := webAttRefRe.FindAllStringSubmatch(body, -1)
	if len(matches) < 3 {
		t.Fatalf("应提取到正文里的附件链接，got %v", matches)
	}
	if matches[0][2] != "295666" || matches[0][3] != "579" {
		t.Fatalf("文章号/附件号解析错误：%v", matches[0])
	}
	if got := normalizeSubject("Re: 我把FQTerm重写并魔改了"); got != "我把FQTerm重写并魔改了" {
		t.Fatalf("标题归一化错误：%q", got)
	}
}

// web 正文按楼层切块：不能把首帖的 6 个附件错并到回复层。
func TestWebArticleBlockByFloor(t *testing.T) {
	body := `<a name="a0"></a>first-t1 first-t2` +
		`<a name="a1"></a>reply-t1` +
		`<a name="a2"></a>reply2-t1`
	if got := webArticleBlock(body, 1); got != "first-t1 first-t2" {
		t.Fatalf("首帖切块错误：%q", got)
	}
	if got := webArticleBlock(body, 2); got != "reply-t1" {
		t.Fatalf("第 2 层切块错误：%q", got)
	}
	if got := webArticleBlock(body, 3); got != "reply2-t1" {
		t.Fatalf("第 3 层切块错误：%q", got)
	}
}

// 从「选择阅读讨论区」直接输入小写英文版名时，也要拿到服务器上的中文版名，
// 并在缓存里同时认得小写输入与服务器返回的规范版名。
func TestEnsureBoardChineseNameForLowercaseInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "search/board") {
			t.Errorf("意外的请求路径：%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"message":"","data":[` +
			`{"id":"c8d614e56acb8a192ec4af8b375a5eea","name":"NewSoftware","title":"新软件介绍"}]}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.wap.Base = srv.URL
	b := c.ensureBoard("newsoftware")
	if b.ID != "NewSoftware" || b.Name != "新软件介绍" {
		t.Fatalf("应按版名查到中文版名，got %+v", b)
	}
	if got := c.board("newsoftware"); got.Name != "新软件介绍" {
		t.Fatalf("小写版名也应命中缓存的中文名，got %+v", got)
	}
	if got := c.board("NewSoftware"); got.Name != "新软件介绍" {
		t.Fatalf("规范版名应命中缓存的中文名，got %+v", got)
	}
}

// RE2 不支持反向引用，htmlToText 里曾因 </\1> 在真实 web 正文上直接 panic。
func TestHTMLToTextStripsScriptAndStyle(t *testing.T) {
	got := htmlToText("<style>.a{color:red}</style><script>var x=1;</script>正文")
	if strings.TrimSpace(got) != "正文" {
		t.Fatalf("script/style 应被清除，got %q", got)
	}
}
