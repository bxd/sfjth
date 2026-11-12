package smth

import (
	"fmt"
	"os"
	"testing"
)

// 端到端：模拟客户端「进入版面 -> 文章列表 -> 点开正文」的完整链路。
func TestClientThreadsThenThreadLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skip")
	}
	ck := os.Getenv("SMTH_COOKIE")
	if ck == "" {
		t.Skip("SMTH_COOKIE not set")
	}
	c := NewClient()
	if _, err := c.SaveConfig(fmt.Sprintf(`{"source":"web","cookie":%q}`, ck)); err != nil {
		t.Fatal(err)
	}
	res, err := c.Threads("Astrology", 1)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(res.Items) == 0 {
		t.Fatal("empty list")
	}
	th := res.Items[0]
	t.Logf("list: %d items, pages=%d, first hash=%s id=%d subj=%s", len(res.Items), res.Pages, th.HashID, th.ID, th.Subject)
	if th.HashID == "" {
		t.Fatal("expected wap hash in first item")
	}
	got, err := c.Thread(th.BoardID, th.ID, 1)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(got.Items) == 0 {
		t.Fatal("no posts")
	}
	t.Logf("post: subject=%s posts=%d first=%.40s", got.Subject, len(got.Items), got.Items[0].Body)
}
