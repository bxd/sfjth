package smth

// wap 发文通道。水木 web/wap 的发帖都强制验证码，但 wap 的提交是干净的 JSON API：
// 未过验证时明确返回 code=0「无效的验证」，不会像 nForum 那样给出 ajax_st:1 的假成功。
//
// 已确认接口（2026-11 实测）：
//   GET  /wap/api/file/token                   发帖附件上传令牌（裸字符串）
//   POST /wap/api/file/upload/<hash>/<token>   multipart「files」，返回 key/name
//   GET  /wap/api/topic/publish/check          code.data.code=3 表示需要验证码
//   POST /wap/api/topic/publish                新主题：body/subject/boardId/uploadToken + 极验结果
//   POST /wap/api/topic/reply                  回帖：articleId/body/uploadToken + 极验结果
//
// 极验 captchaId 来自 wap 前端打包代码里 geetestType=3（发表文章）对应的常量；
// 验证通过后 getValidate() 给出 lot_number / captcha_output / pass_token / gen_time，
// 服务端会自行调用 /wap/api/geetest4/<captchaId>/validate 校验。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const wapPostCaptchaID = "ade4a85345062fda4657d64aa3206cba"

// WapCaptchaID 返回发表/回帖使用的极验 captchaId，供验证窗口加载组件。
func WapCaptchaID() string { return wapPostCaptchaID }

// WapCaptcha 是极验 4 的一次验证结果。
// 注意 captcha_id 必须一起提交：wap 前端 geetestResult 里明确带了这个字段，
// 服务端据此定位 validate 接口；缺了它即使验证本身通过也会报「请求无效」。
type WapCaptcha struct {
	LotNumber     string `json:"lot_number"`
	CaptchaOutput string `json:"captcha_output"`
	PassToken     string `json:"pass_token"`
	GenTime       string `json:"gen_time"`
	CaptchaID     string `json:"captcha_id"`
	Client        string `json:"client"`
}

func (c WapCaptcha) valid() bool {
	return strings.TrimSpace(c.LotNumber) != "" && strings.TrimSpace(c.PassToken) != "" &&
		strings.TrimSpace(c.CaptchaOutput) != "" && strings.TrimSpace(c.GenTime) != ""
}

// wapPay 把极验结果并入发文请求体，字段与 wap 前端 geetCallback 的 payload 一致。
func wapPay(base map[string]any, captcha WapCaptcha) map[string]any {
	id := captcha.CaptchaID
	if id == "" {
		id = wapPostCaptchaID
	}
	base["type"] = 3
	base["client"] = "wap"
	base["lot_number"] = captcha.LotNumber
	base["captcha_output"] = captcha.CaptchaOutput
	base["pass_token"] = captcha.PassToken
	base["gen_time"] = captcha.GenTime
	base["captcha_id"] = id
	return base
}

// postJSON 向 wap 发文 API 提交表单数据，并解包 code/data。
// wap 前端请求封装默认 Content-Type 为 application/x-www-form-urlencoded，
// 并用 qs.stringify 编码；这里用 net/url.Values 复刻同样的格式。
func (w *WapClient) postJSON(path string, payload map[string]any) (json.RawMessage, error) {
	values := url.Values{}
	values.Set("t", strconv.FormatInt(time.Now().UnixMilli(), 10))
	for k, v := range payload {
		if v == nil {
			continue
		}
		values.Set(k, fmt.Sprint(v))
	}
	body := values.Encode()
	u := strings.TrimRight(w.Base, "/") + path
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", w.Base+"/post")
	if w.Cookie != "" {
		req.Header.Set("Cookie", w.Cookie)
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wap HTTP %d", resp.StatusCode)
	}
	var r wapResp
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("wap 发文响应解析失败：%v", err)
	}
	if r.Code != 1 {
		msg := r.Message
		if msg == "" {
			msg = "未知错误"
		}
		return nil, fmt.Errorf("wap 发文失败：%s（code=%d，kbsCode=%d）", msg, r.Code, r.KbsCode)
	}
	return r.Data, nil
}

// UploadToken 取 wap 发帖附件的上传令牌。注意这不是 oauth access_token：
// wap 前端 fileUpload 服务走的是 /wap/api/file/token，返回 data 是裸字符串。
func (w *WapClient) UploadToken() (string, error) {
	data, err := w.get("/wap/api/file/token", nil)
	if err != nil {
		return "", err
	}
	var token string
	if err := json.Unmarshal(data, &token); err != nil || token == "" {
		return "", fmt.Errorf("wap 上传令牌解析失败")
	}
	return token, nil
}

// UploadAttachment 上传附件到 wap 的发帖临时区，返回站点分配的文件名（key）。
// 路径与 wap 前端一致：/wap/api/file/upload/<boardHash>/<token>。
func (w *WapClient) UploadAttachment(boardHash, token, filename string, data []byte) (string, error) {
	if boardHash == "" || token == "" {
		return "", fmt.Errorf("上传附件前尚未准备好版面或令牌")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	path := fmt.Sprintf("/wap/api/file/upload/%s/%s", url.PathEscape(boardHash), url.PathEscape(token))
	u := strings.TrimRight(w.Base, "/") + path
	req, err := http.NewRequest(http.MethodPost, u, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", w.Base+"/post")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 Chrome/131.0.0.0 Mobile Safari/537.36")
	if w.Cookie != "" {
		req.Header.Set("Cookie", w.Cookie)
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wap HTTP %d", resp.StatusCode)
	}
	var r wapResp
	if err := json.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("wap 附件上传响应解析失败：%v", err)
	}
	if r.Code != 1 {
		return "", fmt.Errorf("wap 附件上传失败：%s", r.Message)
	}
	var out []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(r.Data, &out); err != nil {
		return "", fmt.Errorf("wap 附件上传结果解析失败：%v", err)
	}
	for _, f := range out {
		if f.Key != "" {
			return f.Key, nil
		}
		if f.Name != "" {
			return f.Name, nil
		}
	}
	return "", fmt.Errorf("wap 附件上传未返回文件名")
}

// Publish 用 wap API 发表新主题；token 可以为空（没有附件时）。
func (w *WapClient) Publish(boardHash, subject, body, token string, captcha WapCaptcha) (string, error) {
	if !captcha.valid() {
		return "", fmt.Errorf("尚未完成验证，请点击验证按钮")
	}
	payload := wapPay(map[string]any{
		"boardId":     boardHash,
		"subject":     subject,
		"body":        body,
		"uploadToken": token,
	}, captcha)
	data, err := w.postJSON("/wap/api/topic/publish", payload)
	if err != nil {
		return "", err
	}
	return wapArticleURL(data), nil
}

// Reply 用 wap API 回复某层文章；articleHash 是 wap 的 32 位文章 hash。
func (w *WapClient) Reply(articleHash, body, token string, captcha WapCaptcha) (string, error) {
	if !captcha.valid() {
		return "", fmt.Errorf("尚未完成验证，请点击验证按钮")
	}
	payload := wapPay(map[string]any{
		"articleId":   articleHash,
		"body":        body,
		"uploadToken": token,
	}, captcha)
	data, err := w.postJSON("/wap/api/topic/reply", payload)
	return wapArticleURL(data), err
}

// NeedCaptcha 查询 wap 是否要求验证。code.data.code==3 表示需要。
func (w *WapClient) NeedCaptcha() (bool, error) {
	data, err := w.get("/wap/api/topic/publish/check", nil)
	if err != nil {
		return false, err
	}
	var out struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return false, err
	}
	return out.Code == 3, nil
}

// wapArticleURL 从发文返回里尽量取出可跳转的文章地址。站点返回结构可能只有 id/hash，
// 没有时给版面列表地址也不影响调用方（只用于成功提示）。
func wapArticleURL(data json.RawMessage) string {
	if len(data) == 0 {
		return "https://wap.newsmth.net/"
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		if id, ok := obj["encodeTopicId"].(string); ok && id != "" {
			return "https://wap.newsmth.net/topic/" + id
		}
		for _, k := range []string{"url", "topicUrl", "articleUrl"} {
			if u, ok := obj[k].(string); ok && u != "" {
				if strings.HasPrefix(u, "http") {
					return u
				}
				return "https://wap.newsmth.net" + u
			}
		}
	}
	return "https://wap.newsmth.net/"
}

// wapBoardHash 供 Client 门面调用。
func (w *WapClient) wapBoardHash(board string) (string, error) { return w.boardHash(board) }

// TopicHashBySubject 按标题在 wap 版面列表里反查主题 hash。
// 从 web 数据源进入的文章没有 wapHashByID 映射，回帖时用标题兜底。
func (w *WapClient) TopicHashBySubject(board, subject string) (string, error) {
	boardID, err := w.boardHash(board)
	if err != nil {
		return "", err
	}
	for page := 1; page <= 3; page++ {
		data, err := w.get("/wap/api/board/topic/list", url.Values{
			"id": {boardID}, "page": {strconv.Itoa(page)},
		})
		if err != nil {
			return "", err
		}
		var out struct {
			Topics []struct {
				ID      string `json:"id"`
				Subject string `json:"subject"`
			} `json:"topics"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", err
		}
		for _, t := range out.Topics {
			if t.Subject == subject {
				return t.ID, nil
			}
		}
		if len(out.Topics) == 0 {
			break
		}
	}
	return "", fmt.Errorf("wap 找不到主题：《%s》", subject)
}

// ArticleFloor 按文章 hash 反查它在主题里的楼层（topicOrder）。
// 注意提醒里的 cause.topicOrder 是「主题在版面列表里的排序号」，不是楼层，
// 只能按文章 hash 在正文分页里逐页找。返回 0 表示没找到（不阻塞跳转）。
func (w *WapClient) ArticleFloor(topicHash, articleHash string) (int, error) {
	if topicHash == "" || articleHash == "" {
		return 0, fmt.Errorf("缺少主题或文章编号")
	}
	// 一页 20 楼，50 页足够覆盖 1000 楼的长贴；遇到空页即停。
	for page := 1; page <= 50; page++ {
		path := fmt.Sprintf("/wap/api/topic/loadArticlesByMode/%s/1/%d/20", url.PathEscape(topicHash), page)
		data, err := w.get(path, nil)
		if err != nil {
			return 0, err
		}
		var out struct {
			Articles []struct {
				ID         string `json:"id"`
				TopicOrder int    `json:"topicOrder"`
			} `json:"articles"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return 0, err
		}
		if len(out.Articles) == 0 {
			break
		}
		for i, a := range out.Articles {
			if a.ID != articleHash {
				continue
			}
			if a.TopicOrder > 0 {
				return a.TopicOrder, nil
			}
			return (page-1)*20 + i + 1, nil
		}
	}
	return 0, fmt.Errorf("主题里找不到这篇文章")
}

// wapArticleHashByFloor 按楼层从 wap 正文接口取真实文章 hash。
// wap 前端回帖用的就是这里返回的 article id，而不是 topic hash。
func (w *WapClient) wapArticleHashByFloor(topicHash string, floor int) (string, error) {
	// 回帖要的是文章 id（encodeArticleId），不是主题 id。wap 前端点某一楼回复时
	// 传的就是 article.id；一楼也不例外，早期直接返回 topicHash 会被服务端当成
	// 只读版面并拒绝。
	if floor < 1 {
		floor = 1
	}
	pages := (floor + 19) / 20
	for page := 1; page <= pages; page++ {
		path := fmt.Sprintf("/wap/api/topic/loadArticlesByMode/%s/1/%d/20", url.PathEscape(topicHash), page)
		data, err := w.get(path, nil)
		if err != nil {
			return "", err
		}
		var out struct {
			Articles []struct {
				ID         string `json:"id"`
				TopicOrder int    `json:"topicOrder"`
			} `json:"articles"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", err
		}
		for _, a := range out.Articles {
			if a.TopicOrder == floor && a.ID != "" {
				return a.ID, nil
			}
		}
	}
	return "", fmt.Errorf("wap 找不到第 %d 楼的文章 ID", floor)
}
