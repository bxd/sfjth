package smth

// Demo 是内置离线数据源：在没有网络、没有 Cookie、没有开放平台 APP 密钥时，
// 也能完整体验 term 风格的全部界面与键盘操作。联网登录后自动切换为真实数据。

import (
	"fmt"
	"strings"
	"time"
)

func demoSections() []Section { return classicSections() }

func demoBoards() []Board {
	return []Board{
		{ID: "NewExpress", Name: "水木特快", Section: "0", Manager: "SYSOP", Desc: "站内最快的信息集散地", Users: 128, Today: 342, Topics: 98211, Articles: 1450233, Kind: "board"},
		{ID: "Programming", Name: "编程技术", Section: "4", Manager: "itgirl", Desc: "Stack Overflow每月提问数从20万暴跌至1000以下", Users: 37, Today: 26, Topics: 22899, Articles: 341872, Kind: "board"},
		{ID: "ITExpress", Name: "IT业界快讯", Section: "4", Manager: "hgoldfish", Desc: "技术新闻与行业八卦", Users: 21, Today: 18, Topics: 15602, Articles: 208441, Kind: "board"},
		{ID: "Family", Name: "家庭生活", Section: "9", Manager: "teandcoffee", Desc: "◇家─有爱就有责任◇", Users: 46, Today: 55, Topics: 44120, Articles: 720198, Kind: "board"},
		{ID: "AutoWorld", Name: "汽车世界", Section: "9", Manager: "jackok", Desc: "用车、选车、自驾", Users: 33, Today: 41, Topics: 39800, Articles: 660123, Kind: "board"},
		{ID: "Joke", Name: "笑话连篇", Section: "2", Manager: "ZhouHaiBin", Desc: "每天笑一笑", Users: 18, Today: 12, Topics: 21550, Articles: 302877, Kind: "board"},
		{ID: "EconForum", Name: "经济论坛", Section: "8", Manager: "rlcne", Desc: "宏观与微观", Users: 12, Today: 7, Topics: 18770, Articles: 251903, Kind: "board"},
		{ID: "Apple", Name: "苹果园", Section: "4", Manager: "hzxq", Desc: "Mac / iOS / 周边", Users: 15, Today: 9, Topics: 22104, Articles: 318442, Kind: "board"},
		{ID: "SMTHApp_Dev", Name: "客户端开发", Section: "4", Manager: "hotfix", Desc: "第三方客户端交流与反馈", Users: 9, Today: 5, Topics: 6463, Articles: 98210, Kind: "board"},
		{ID: "BBSNewbie", Name: "新手入门", Section: "0", Manager: "shen", Desc: "如何使用telnet方式访问水木社区", Users: 6, Today: 3, Topics: 6087, Articles: 71200, Kind: "board"},
		{ID: "Occupier", Name: "买房装修", Section: "9", Manager: "waterwei", Desc: "安居乐业", Users: 24, Today: 30, Topics: 51230, Articles: 880411, Kind: "board"},
		{ID: "Photo", Name: "摄影天地", Section: "6", Manager: "bluerock", Desc: "用光影记录生活", Users: 8, Today: 4, Topics: 15440, Articles: 210884, Kind: "board"},
		{ID: "Association", Name: "协会社团", Section: "1", Manager: "", Desc: "[二级目录]", Users: 0, Today: 0, Topics: 0, Articles: 0, Kind: "dir"},
	}
}

var demoSubjects = map[string][]struct {
	author, title string
	replies       int
	flag          string
	attach        bool
}{
	"NewExpress": {
		{"SYSOP", "【公告】水木社区服务条款更新说明", 128, "◆", false},
		{"shen", "如何使用 term 方式访问水木社区（含客户端推荐）", 86, "★", false},
		{"teandcoffee", "今晚的月亮真圆，配图", 42, " ", true},
		{"hgoldfish", "Re: 用 Wails 写个桌面客户端怎么样", 233, "★", false},
		{"rlcne", "求推荐一款适合长时间看帖的等宽字体", 57, " ", false},
		{"jackok", "双十一也没有十年前的味道了", 310, "★", false},
		{"ZhouHaiBin", "关于版面阅读记忆功能的一点建议", 19, " ", false},
		{"hotfix", "ismth 丝滑版更新 v1.2.5", 96, " ", true},
		{"njer", "VxTerm 可以完美访问文字版水木", 74, " ", false},
		{"moonwalker", "分享一个油猴脚本：自动登录 + 接口降级", 38, " ", false},
		{"waterwei", "今天食堂的糖醋排骨回来了", 12, " ", false},
		{"bluerock", "【合集】那些年我们用过的 BBS 终端", 205, "★", false},
	},
	"Programming": {
		{"itgirl", "发招聘贴的注意", 312, "◆", false},
		{"hgoldfish", "Stack Overflow每月提问数从20万暴跌至1000以下", 289, "★", false},
		{"ooolinux", "很多 AI 项目操之过急", 46, " ", false},
		{"ooolinux", "跨平台图形界面编程论好用还是Lazarus", 133, "★", true},
		{"z16166", "4090单显卡跑deepseek和GLM了", 87, " ", false},
		{"ooolinux", "Lazarus + 龙芯 + 国产Linux", 54, " ", true},
		{"z16166", "币安开发钱包的码农被钓鱼、社工", 71, " ", false},
		{"hgoldfish", "Codex 写 Delphi 程序一次过", 98, " ", true},
		{"hgoldfish", "Re: 突然发现，flutter已经成了桌面跨平台ui的最佳选择", 176, "★", false},
		{"itgirl", "【合集】Wails / Tauri / Electron 桌面方案对比", 244, "★", false},
	},
	"SMTHApp_Dev": {
		{"smthflow", "smthflow 让水木更流畅点", 64, "★", true},
		{"hotfix", "纯AI开发的水木APP - ismth征集需求中", 312, "◆", false},
		{"hotfix", "ismth 更新v1.2.5版，修复登录状态显示异常问题", 108, " ", false},
		{"rlcne", "有没有帖子浏览的记忆功能，退出后再点进去接着看", 21, " ", false},
		{"jackok", "能不能显示有多少未读的新回帖", 17, " ", false},
		{"hgoldfish", "水木官方是不是还有个官方 API 的版本，用的是 oauth 登录", 43, " ", false},
	},
	"Family": {
		{"teandcoffee", "◇家─有爱就有责任◇", 88, "◆", false},
		{"someone", "孩子上小学了，时间过得真快", 34, " ", false},
		{"waterwei", "周末带父母去郊外走走", 26, " ", true},
	},
}

func demoThreads(board string) []Thread {
	src, ok := demoSubjects[board]
	if !ok {
		src = []struct {
			author, title string
			replies       int
			flag          string
			attach        bool
		}{
			{"guest", "欢迎来到 " + board + " 版面", 3, "◆", false},
			{"olduser", "这里是离线演示数据，登录后会显示真实帖子", 8, " ", false},
			{"hgoldfish", "Re: 用 Wails 3 做 Windows 客户端的记录", 27, "★", false},
			{"smthflow", "论 BBS 客户端的「丝滑」是怎么做出来的", 55, " ", false},
			{"itgirl", "版面置顶规则说明", 0, "◆", false},
		}
	}
	out := make([]Thread, 0, len(src))
	for i, s := range src {
		id := int64(100000 + len(board)*997 + i*37)
		pages := s.replies/10 + 1
		if pages > 40 {
			pages = 40
		}
		out = append(out, Thread{
			ID: id, BoardID: board, Subject: s.title, Author: s.author,
			Date:   time.Now().Add(-time.Duration(i*3+1) * time.Hour).Format("01-02"),
			Time:   time.Now().Add(-time.Duration(i*3+1) * time.Hour).Unix(),
			Count:  s.replies + 1,
			Pages:  pages,
			Flag:   s.flag,
			Attach: s.attach,
		})
	}
	return out
}

func demoPosts(board string, id int64, page int, subject string) []Post {
	base := []string{
		"这篇帖子来自 sfjth 离线演示数据源。",
		"",
		"按 ← 或 q 返回版面，↑ / ↓ 选择文章，→ 或 Enter 进入阅读。",
		"PgUp / PgDn（或空格）翻页，r 回复，^P 发表新文章，h 查看帮助。",
		"",
		"【 在 olduser 的大作中提到: 】",
		": term 下的操作基本不用鼠标，方向键就是全部：",
		": ← 返回，→ 进入，↑↓ 移动光标，q 退出。",
		": 这个习惯二十多年没变过。",
		"",
		"所以我做客户端的时候，第一件事就是把这套键位完整搬过来。",
		"数据层用 Go 抓页面 + 本地缓存预取，界面层只用等宽字符渲染，",
		"这样翻页不会白屏，也不会有网页那种拖泥带水的感觉。",
		"",
		"--",
		"※ 来源:·水木社区 newsmth.net·[FROM: 123.113.42.*]",
	}
	replies := []string{
		"确实流畅，请问下有没有帖子浏览的记忆功能，就是当前阅读到哪一楼，退出后再点进去就接着看。",
		"能不能显示有多少未读的新回帖？比如帖子有 15 个回帖，已经看过 10 个，那么显示 5 个没看过。",
		"这个需求已经记下了：阅读进度、已读置灰、未读数都在本地进度文件里。",
		"另外问一下，官方开放平台的 API 还能用吗？wiki 上写的是 OAuth2 + 数字签名。",
		"能申请到 client_id 就能用，申请不到就走 Web 数据源，Cookie 登录后一样读。",
		"顶，term 风格好评。",
	}
	out := []Post{}
	for i := 0; i < 8; i++ {
		floor := (page-1)*8 + i + 1
		body := ""
		author := "guest"
		title := subject
		if floor == 1 {
			author = "hgoldfish"
			body = strings.Join(base, "\n")
		} else {
			author = []string{"teandcoffee", "rlcne", "jackok", "hotfix", "smthflow", "itgirl", "bluerock"}[(floor-2)%7]
			title = "Re: " + subject
			body = "【 在 hgoldfish 的大作中提到: 】\n: 所以我做客户端的时候，第一件事就是把这套键位完整搬过来。\n\n" +
				replies[(floor-2)%len(replies)] + "\n\n--\n※ 来源:·水木社区·[FROM: 221.216.140.*]"
		}
		out = append(out, Post{
			ID: id + int64(floor), BoardID: board, ThreadID: id, Floor: floor,
			Author: author, Nick: "", Subject: title,
			Date: time.Now().Add(-time.Duration(24-floor) * time.Hour).Format("Mon Jan _2 15:04:05 2006"),
			From: "水木社区 (Wed Nov 11 22:02:07 2026), 站内",
			Body: body,
		})
	}
	return out
}

func demoMails(box string) []Mail {
	if box == "sent" {
		return []Mail{
			{ID: 3, From: "me", To: "hotfix", Title: "关于 Wails 客户端的几个想法", Date: "2026-11-10", Unread: false},
		}
	}
	return []Mail{
		{ID: 1, From: "SYSOP", Title: "欢迎使用 sfjth", Date: "2026-11-11", Unread: true,
			Content: "你好：\n\n这是 sfjth 的离线演示信箱。联网登录后可以读取真实站内信。\n\n按 ← 返回，Enter 阅读，^P 写新信。\n\n※ 来源:·水木社区"},
		{ID: 2, From: "smthflow", Title: "Re: 让水木更流畅点", Date: "2026-11-09", Unread: true,
			Content: "预取 + 本地缓存 + 不重新布局是关键。\n\n另外记得做阅读记忆和未读回帖数。\n\n--\nsmthflow"},
		{ID: 4, From: "hgoldfish", Title: "官方 wiki 的地址", Date: "2026-11-08", Unread: false,
			Content: "https://wiki.open.newsmth.net/\n\nOAuth2、数字签名、错误码、分页说明都在里面。\n\n注意：APP Client 需要向站务申请。"},
	}
}

func demoTopTen() []TopItem {
	return []TopItem{
		{Rank: 1, BoardID: "NewExpress", Board: "水木特快", Title: "【合集】用终端上文字版 BBS 的还有几个人", Author: "njer", Count: 512, ID: 100001},
		{Rank: 2, BoardID: "Programming", Board: "编程技术", Title: "Stack Overflow 每月提问数从 20 万暴跌至 1000 以下", Author: "hgoldfish", Count: 289, ID: 100002},
		{Rank: 3, BoardID: "SMTHApp_Dev", Board: "客户端开发", Title: "smthflow 让水木更流畅点", Author: "smthflow", Count: 233, ID: 100003},
		{Rank: 4, BoardID: "Family", Board: "家庭生活", Title: "双十一也没有十年前的味道了", Author: "jackok", Count: 187, ID: 100004},
		{Rank: 5, BoardID: "Apple", Board: "苹果园", Title: "BBS客户端 Welly 正式上架 Mac App Store", Author: "tangyang", Count: 156, ID: 100005},
		{Rank: 6, BoardID: "ITExpress", Board: "IT业界快讯", Title: "纯 AI 开发的水木 APP - ismth 征集需求中", Author: "hotfix", Count: 141, ID: 100006},
		{Rank: 7, BoardID: "AutoWorld", Board: "汽车世界", Title: "冬季用车 checklist", Author: "someone", Count: 120, ID: 100007},
		{Rank: 8, BoardID: "Joke", Board: "笑话连篇", Title: "程序员的周末", Author: "ZhouHaiBin", Count: 98, ID: 100008},
		{Rank: 9, BoardID: "EconForum", Board: "经济论坛", Title: "关于明年的一点粗浅判断", Author: "rlcne", Count: 87, ID: 100009},
		{Rank: 10, BoardID: "BBSNewbie", Board: "新手入门", Title: "如何使用 telnet 方式访问水木社区", Author: "shen", Count: 64, ID: 100010},
	}
}

func demoBoardByID(id string) Board {
	for _, b := range demoBoards() {
		if b.ID == id {
			return b
		}
	}
	return Board{ID: id, Name: id, Section: "0", Manager: "SYSOP", Desc: "水木社区", Kind: "board"}
}

func demoSearch(keyword, scope string) []Thread {
	out := []Thread{}
	for _, b := range demoBoards() {
		if scope != "" && scope != "all" && scope != b.ID {
			continue
		}
		for _, t := range demoThreads(b.ID) {
			if keyword == "" || strings.Contains(strings.ToLower(t.Subject), strings.ToLower(keyword)) ||
				strings.Contains(strings.ToLower(t.Author), strings.ToLower(keyword)) {
				out = append(out, t)
			}
		}
	}
	return out
}

func demoSubject(board string, id int64, fallback string) string {
	for _, t := range demoThreads(board) {
		if t.ID == id {
			return t.Subject
		}
	}
	if fallback != "" {
		return fallback
	}
	return fmt.Sprintf("%s 版面主题 #%d", board, id)
}
