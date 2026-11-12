package main

// sfjth —— 用 Wails 3 实现的 Windows 下水木社区客户端。
//
// 设计要点：
//   * 数据来源兼容「水木社区开放平台」官方 API（wiki.open.newsmth.net）与
//     Web 数据源（Cookie 会话 + nForum 页面解析），另内置离线演示数据；
//   * 交互与界面复刻 term/FTerm/CTerm：80x24 字符网格、ANSI 16 色、
//     方向键 + 单键命令、顶部状态行与底部功能提示；
//   * 借鉴 smthflow / ismth 的「丝滑」思路：Go 侧缓存 + 后台预取 +
//     阅读记忆 + 未读回帖数，前端只做本地渲染，切页不白屏。

import (
	"embed"
	"fmt"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	svc := NewBbsService()
	app := application.New(application.Options{
		Name:        "sfjth",
		Description: "sfjth - 水木社区 term 风格客户端",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Frameless：去掉 Windows 白色的系统标题栏，让 80x24 字符网格从窗口第一行开始。
	// 最小化 / 最大化 / 关闭改由前端 #winbtns 浮动按钮通过 Wails runtime 控制；
	// 拖动与边缘缩放由 Wails 的 --wails-draggable / wails:resize 机制接管。
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "sfjth - 水木社区",
		// 默认尺寸按 80x24 网格（880x528 像素）的 5:3 比例给，缩放正好 1.25，
		// 窗口内不留黑边（黑色留边会顶在蓝色标题栏上方，看着像多一条黑条）。
		Width:            1100,
		Height:           660,
		MinWidth:         720,
		MinHeight:        460,
		Frameless:        true,
		BackgroundColour: application.NewRGB(0, 0, 0),
		URL:              "/",
		Windows: application.WindowsWindow{
			// 保留 DWM 阴影与 Win11 圆角（否则窗口贴边后没有边界感）。
			DisableFramelessWindowDecorations: false,
		},
	})

	// 网页登录弹窗：真实登录完成后，把 WebView 里的 Cookie 落盘并通知前端进主选单。
	// 站点页面已经完成登录，这里不再用联网 CheckSession 阻塞跳屏；校验放到后台，
	// 只用于补充用户名。否则网络慢时弹窗关了、主界面却一直停在登录屏。
	// 发帖/回帖弹窗：站点提交强制验证码，交给真实网页完成；成功后刷新正文/列表。
	svc.AttachPostPopup(newPostPopup(app, svc.client, func(articleURL, message string) {
		msg := message
		if msg == "" {
			msg = "发表成功"
		}
		svc.MarkPostDone("success", msg, articleURL)
		app.Event.Emit("sfjth:post-success", map[string]any{
			"url": articleURL, "message": msg,
		})
	}, func() {
		svc.MarkPostDone("cancelled", "已取消发表", "")
	}))

	svc.AttachPopup(newLoginPopup(app, func(cookie, channel, user string) {
		diagLog("done", "onDone entered ch=%s user=%q cookieLen=%d", channel, user, len(cookie))
		if channel == "" {
			channel = "web"
		}
		_, err := svc.client.SaveConfig(fmt.Sprintf(`{"cookie":%q,"source":"web","login_mode":%q}`, cookie, channel))
		diagLog("done", "SaveConfig err=%v", err)
		if err != nil {
			msg := "保存会话失败：" + err.Error()
			svc.MarkPopupDone("failed", msg, user, channel)
			app.Event.Emit("sfjth:login-failed", map[string]any{"message": msg})
			return
		}
		if user == "" {
			user = svc.client.Config().LastUserID
		}
		if user != "" {
			_, _ = svc.client.SaveConfig(fmt.Sprintf(`{"user":%q,"last_user_id":%q}`, user, user))
		}
		msg := "登录成功：" + user
		// 结果先写入 binding 状态，前端即使收不到应用事件也能轮询到并跳主菜单。
		svc.MarkPopupDone("success", msg, user, channel)
		app.Event.Emit("sfjth:login-success", map[string]any{
			"user": user, "channel": channel, "message": msg,
		})
		diagLog("done", "success event emitted")
		// 后台校验仅用于修正用户名，不再决定能否进主菜单。
		go func(cookie, channel, user string) {
			check, err := svc.client.CheckSession()
			if err == nil && check.Valid && check.User != "" && check.User != user {
				_, _ = svc.client.SaveConfig(fmt.Sprintf(`{"user":%q,"last_user_id":%q}`, check.User, check.User))
			}
		}(cookie, channel, user)
	}, func() {
		svc.MarkPopupDone("cancelled", "已取消登录", "", "")
	}))

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
