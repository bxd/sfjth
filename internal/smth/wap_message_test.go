package smth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWapConversationsAndNotifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/wap/api/message/conversations":
			_, _ = w.Write([]byte(`{"code":1,"data":{"pager":{"page":1,"total":1,"size":20,"items":1},"conversations":[{"speaker":{"id":"sp1","name":"bxd","nick":"北西东"},"unread":2,"lastTime":1794326656000}],"lastMessages":[{"conversationId":"c1","senderId":"sp1","recipientId":"me","subject":"","body":"这是站内短信。","sendTime":1794326656000}]}}`))
		case r.URL.Path == "/wap/api/notify/2/0":
			_, _ = w.Write([]byte(`{"code":1,"data":{"pager":{"page":1,"total":1,"size":20,"items":2},"notifies":[{"id":"n1","type":2,"status":1,"subject":"Re: 测试","transactorName":"someone","createTime":1794395901000,"targetId":"art-mine","targetType":1,"cause":{"id":"art-new","subject":"Re: 测试","body":"回复内容","topicId":"topic-9","replyId":"art-mine","board":{"name":"Test","title":"测试版"}}},{"id":"n2","type":2,"status":2,"subject":"Re: 旧","transactorName":"other","createTime":1794395902000,"cause":{"subject":"Re: 旧","body":"已读内容"}}]}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"message":"unknown"}`))
		}
	}))
	defer srv.Close()
	cl := &WapClient{Base: srv.URL, HTTP: srv.Client()}

	convs, err := cl.Conversations(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(convs.Items) != 1 || convs.Items[0].Body != "这是站内短信。" || convs.Items[0].Unread != 2 {
		t.Fatalf("会话解析错误: %+v", convs.Items)
	}

	notifies, err := cl.Notifies(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifies.Items) != 2 || notifies.Unread != 1 || !notifies.Items[0].Unread {
		t.Fatalf("提醒解析错误: %+v", notifies)
	}
	if notifies.Items[0].Board != "测试版" || notifies.Items[0].From != "someone" {
		t.Fatalf("提醒字段解析错误: %+v", notifies.Items[0])
	}
	// 提醒项要能定位到版面上的文章：英文版名、主题 hash，以及「触发提醒的
	// 那篇新文章」的 hash（回复我的 -> 回文本身，不是我的原文）。
	first := notifies.Items[0]
	if first.BoardID != "Test" || first.TopicHash != "topic-9" || first.ArticleHash != "art-new" {
		t.Fatalf("提醒定位字段解析错误: %+v", first)
	}
}

// 提醒里的 cause.topicOrder 是主题在版面列表的排序号，不是楼层；
// 楼层必须按文章 hash 在正文分页里反查出来。
func TestNotifyTargetResolvesBoardThreadAndFloor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wap/api/topic/loadArticlesByMode/topic-9/1/1/20":
			_, _ = w.Write([]byte(`{"code":1,"data":{"articles":[{"id":"art-1","topicOrder":1},{"id":"art-2","topicOrder":2}]}}`))
		case "/wap/api/topic/loadArticlesByMode/topic-9/1/2/20":
			_, _ = w.Write([]byte(`{"code":1,"data":{"articles":[{"id":"art-21","topicOrder":21},{"id":"art-22","topicOrder":22}]}}`))
		default:
			_, _ = w.Write([]byte(`{"code":1,"data":{"articles":[]}}`))
		}
	}))
	defer srv.Close()

	c := &Client{cache: NewCache(), wapHashByID: map[int64]string{}}
	c.wap = &WapClient{Base: srv.URL, HTTP: srv.Client(), attachByArticle: map[string][]Attachment{}}

	got, err := c.NotifyTarget("Test", "测试版", "topic-9", "art-22", "Re: 测试")
	if err != nil {
		t.Fatal(err)
	}
	if got.Board != "Test" || got.BoardTitle != "测试版" {
		t.Fatalf("版面解析错误: %+v", got)
	}
	if got.ThreadID != hashID("topic-9") {
		t.Fatalf("主题号应为 topic hash 派生值: %+v", got)
	}
	if got.Floor != 22 {
		t.Fatalf("楼层应为 22，实际 %d", got.Floor)
	}
	// 派生号必须登记回映射，Thread/回帖才能走 wap 读取。
	if c.wapHashByID[got.ThreadID] != "topic-9" {
		t.Fatalf("未登记 hash 映射: %+v", c.wapHashByID)
	}
}
