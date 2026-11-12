package smth

// wap 站内信与提醒接口。水木 wap 的个人中心把四类消息统一走 /api：
//   GET    /api/message/conversations?page=N        收件箱会话列表
//   GET    /api/message/<speakerID>/messages/<page> 会话详情
//   GET    /api/message/read?speakerId=<id>          标记会话已读
//   POST   /api/message/send                        发送站内信（recipientName/body）
//   GET    /api/notify/<type>/<page>                 1=@我的 2=回复我的 3=Like我的
//   PATCH  /api/notify/read/<id>                     标记单条已读
//   PATCH  /api/notify/read/all/<type>               标记一类全部已读
//
// 红点规则：会话 unread>0，或提醒的 status==1（wap 前端 list 里的 unread 样式）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// InboxItem 是 wap 收件箱会话列表的一项。
type InboxItem struct {
	ID     string `json:"id"` // speaker id，读消息详情时用
	Name   string `json:"name"`
	Nick   string `json:"nick"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Date   string `json:"date"`
	Unread int    `json:"unread"`
}

// MessageResult 会话列表。
type MessageResult struct {
	Items []InboxItem `json:"items"`
	Page  int         `json:"page"`
	Pages int         `json:"pages"`
}

// NotifyItem 是 @我的 / 回复我的 / Like我的 中的一条提醒。
// 这些提醒本质上都是「版面上的某一篇文章」，因此除展示字段外还带上定位用的
// 英文版名 / 主题 hash / 文章 hash，前端按 r 时才能跳到对应版面回复对应楼层。
type NotifyItem struct {
	ID          string `json:"id"`
	Type        int    `json:"type"`
	From        string `json:"from"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	Board       string `json:"board"`      // 版面中文名，列表展示用
	BoardID     string `json:"board_id"`   // 英文版名，跳转版面用
	TopicHash   string `json:"topic_hash"` // 主题 hash
	ArticleHash string `json:"article_hash"`
	Date        string `json:"date"`
	Unread      bool   `json:"unread"`
	TargetID    string `json:"target_id"`
}

// NotifyResult 提醒列表。
type NotifyResult struct {
	Items  []NotifyItem `json:"items"`
	Page   int          `json:"page"`
	Pages  int          `json:"pages"`
	Unread int          `json:"unread"`
}

// ReminderState 是主菜单红点用的汇总状态。
type ReminderState struct {
	Mail  int `json:"mail"`
	At    int `json:"at"`
	Reply int `json:"reply"`
	Like  int `json:"like"`
}

// Any 是否有任意一类新提醒。
func (r ReminderState) Any() bool {
	return r.Mail > 0 || r.At > 0 || r.Reply > 0 || r.Like > 0
}

func wapTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("01-02 15:04")
}

func wapPager(data json.RawMessage) (page, pages int) {
	var p struct {
		Page  int `json:"page"`
		Total int `json:"total"`
		Size  int `json:"size"`
	}
	_ = json.Unmarshal(data, &p)
	page = p.Page
	if p.Size > 0 {
		pages = (p.Total + p.Size - 1) / p.Size
	}
	if pages < 1 {
		pages = 1
	}
	return
}

// wapRequest 发一个带 Cookie 的 wap JSON API 请求并解包 data。
func (w *WapClient) wapRequest(method, path string, query url.Values, body io.Reader, contentType string) (json.RawMessage, error) {
	u := strings.TrimRight(w.Base, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", w.Base+"/")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
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
		return nil, fmt.Errorf("wap 响应解析失败：%v", err)
	}
	if r.Code != 1 {
		msg := r.Message
		if msg == "" {
			msg = fmt.Sprintf("code=%d", r.Code)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return r.Data, nil
}

// Conversations 读取收件箱会话列表。
func (w *WapClient) Conversations(page int) (MessageResult, error) {
	if page < 0 {
		page = 0
	}
	data, err := w.wapRequest(http.MethodGet, "/wap/api/message/conversations",
		url.Values{"page": {strconv.Itoa(page)}}, nil, "")
	if err != nil {
		return MessageResult{}, err
	}
	var raw struct {
		Pager struct {
			Page int `json:"page"`
		} `json:"pager"`
		Conversations []struct {
			Speaker struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Nick string `json:"nick"`
			} `json:"speaker"`
			Unread   int   `json:"unread"`
			LastTime int64 `json:"lastTime"`
		} `json:"conversations"`
		LastMessages []struct {
			ConversationID string `json:"conversationId"`
			SenderID       string `json:"senderId"`
			RecipientID    string `json:"recipientId"`
			Subject        string `json:"subject"`
			Body           string `json:"body"`
			SendTime       int64  `json:"sendTime"`
		} `json:"lastMessages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return MessageResult{}, err
	}
	type lastMsg struct {
		Subject  string
		Body     string
		SendTime int64
	}
	byFrom, byTo := map[string]lastMsg{}, map[string]lastMsg{}
	for _, m := range raw.LastMessages {
		v := lastMsg{m.Subject, m.Body, m.SendTime}
		if m.SenderID != "" {
			byFrom[m.SenderID] = v
		}
		if m.RecipientID != "" {
			byTo[m.RecipientID] = v
		}
	}
	out := MessageResult{Items: []InboxItem{}, Page: raw.Pager.Page}
	_, out.Pages = wapPager(data)
	for _, c := range raw.Conversations {
		if c.Speaker.ID == "" {
			continue
		}
		item := InboxItem{
			ID: c.Speaker.ID, Name: c.Speaker.Name, Nick: c.Speaker.Nick,
			Unread: c.Unread, Date: wapTime(c.LastTime),
		}
		m, ok := byFrom[c.Speaker.ID]
		if !ok {
			m, ok = byTo[c.Speaker.ID]
		}
		if ok {
			item.Title, item.Body = m.Subject, m.Body
			if m.SendTime > 0 {
				item.Date = wapTime(m.SendTime)
			}
		}
		if item.Title == "" {
			item.Title = "站内短信"
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// Messages 读取某个会话的消息。
func (w *WapClient) Messages(speakerID string, page int) ([]Mail, error) {
	if page < 0 {
		page = 0
	}
	data, err := w.wapRequest(http.MethodGet,
		fmt.Sprintf("/wap/api/message/%s/messages/%d", url.PathEscape(speakerID), page), nil, nil, "")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Speaker struct {
			Name string `json:"name"`
			Nick string `json:"nick"`
		} `json:"speaker"`
		Messages []struct {
			ID       string `json:"id"`
			SenderID string `json:"senderId"`
			Subject  string `json:"subject"`
			Body     string `json:"body"`
			SendTime int64  `json:"sendTime"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	from := raw.Speaker.Name
	if raw.Speaker.Nick != "" {
		from = raw.Speaker.Name + " (" + raw.Speaker.Nick + ")"
	}
	out := []Mail{}
	for _, m := range raw.Messages {
		who := from
		if m.SenderID != speakerID {
			who = "我"
		}
		out = append(out, Mail{
			Key: m.ID, From: who, Title: m.Subject, Date: wapTime(m.SendTime),
			Content: m.Body, Time: m.SendTime,
		})
	}
	return out, nil
}

// Notifies 读取一类提醒：1=@我的 2=回复我的 3=Like我的。
func (w *WapClient) Notifies(kind, page int) (NotifyResult, error) {
	if page < 0 {
		page = 0
	}
	data, err := w.wapRequest(http.MethodGet,
		fmt.Sprintf("/wap/api/notify/%d/%d", kind, page), nil, nil, "")
	if err != nil {
		return NotifyResult{}, err
	}
	var raw struct {
		Notifies []struct {
			ID             string `json:"id"`
			Type           int    `json:"type"`
			Status         int    `json:"status"`
			Subject        string `json:"subject"`
			TransactorName string `json:"transactorName"`
			CreateTime     int64  `json:"createTime"`
			TargetID       string `json:"targetId"`
			TargetType     int    `json:"targetType"`
			Cause          struct {
				ID      string `json:"id"`
				Subject string `json:"subject"`
				Body    string `json:"body"`
				TopicID string `json:"topicId"`
				ReplyID string `json:"replyId"`
				Board   struct {
					Name  string `json:"name"`
					Title string `json:"title"`
				} `json:"board"`
			} `json:"cause"`
			Target struct {
				Subject string `json:"subject"`
				Body    string `json:"body"`
				TopicID string `json:"topicId"`
			} `json:"target"`
		} `json:"notifies"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return NotifyResult{}, err
	}
	out := NotifyResult{Items: []NotifyItem{}, Page: page}
	_, out.Pages = wapPager(data)
	for _, n := range raw.Notifies {
		subject := n.Subject
		if subject == "" {
			subject = n.Cause.Subject
		}
		if subject == "" {
			subject = n.Target.Subject
		}
		body := n.Cause.Body
		if body == "" {
			body = n.Target.Body
		}
		target := n.TargetID
		if target == "" {
			target = n.Target.TopicID
		}
		// 要跳转/回复的是「触发提醒的那篇新文章」：回复我的 -> 回文本身，
		// @我的 -> @ 的那篇，Like我的 -> 被赞的文章。实测 cause.id 就是它；
		// cause.replyId/targetId 是被回复的我的原文，只作兜底。
		article := n.Cause.ID
		if article == "" {
			article = n.TargetID
		}
		if article == "" {
			article = n.Cause.ReplyID
		}
		topic := n.Cause.TopicID
		if topic == "" {
			topic = n.Target.TopicID
		}
		out.Items = append(out.Items, NotifyItem{
			ID: n.ID, Type: n.Type, From: n.TransactorName, Subject: subject,
			Body: body, Board: n.Cause.Board.Title, BoardID: n.Cause.Board.Name,
			TopicHash: topic, ArticleHash: article,
			Date: wapTime(n.CreateTime), Unread: n.Status == 1, TargetID: target,
		})
		if n.Status == 1 {
			out.Unread++
		}
	}
	return out, nil
}

// MarkNotifyRead 标记一条提醒已读。
func (w *WapClient) MarkNotifyRead(id string) error {
	_, err := w.wapRequest(http.MethodPatch, "/wap/api/notify/read/"+url.PathEscape(id), nil, nil, "")
	return err
}

// MarkNotifyReadAll 标记一类提醒全部已读。
func (w *WapClient) MarkNotifyReadAll(kind int) error {
	_, err := w.wapRequest(http.MethodPatch, fmt.Sprintf("/wap/api/notify/read/all/%d", kind), nil, nil, "")
	return err
}

// MarkMessageRead 标记一个会话已读。
func (w *WapClient) MarkMessageRead(speakerID string) error {
	_, err := w.wapRequest(http.MethodGet, "/wap/api/message/read",
		url.Values{"speakerId": {speakerID}}, nil, "")
	return err
}

// SendMessage 发送站内信；参数与 wap 前端一致：recipientName/body，subject 留空。
func (w *WapClient) SendMessage(recipient, body string) error {
	values := url.Values{"recipientName": {recipient}, "body": {body}, "subject": {""}}
	_, err := w.wapRequest(http.MethodPost, "/wap/api/message/send", nil,
		bytes.NewReader([]byte(values.Encode())), "application/x-www-form-urlencoded")
	return err
}

// Reminders 汇总四类红点。失败时返回空状态，不影响主界面渲染。
func (w *WapClient) Reminders() (ReminderState, error) {
	out := ReminderState{}
	convs, err := w.Conversations(0)
	if err == nil {
		for _, c := range convs.Items {
			out.Mail += c.Unread
		}
	}
	for kind := 1; kind <= 3; kind++ {
		r, err := w.Notifies(kind, 0)
		if err != nil {
			continue
		}
		switch kind {
		case 1:
			out.At = r.Unread
		case 2:
			out.Reply = r.Unread
		case 3:
			out.Like = r.Unread
		}
	}
	return out, nil
}
