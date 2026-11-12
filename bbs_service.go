package main

// BbsService 是暴露给前端的 Wails 服务。
// 所有方法统一返回 JSON 字符串：Go 侧负责网络/解析/缓存，前端只负责字符终端渲染。
// 这样 bindings 生成最简单，也避免了跨语言结构体映射的兼容问题。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sfjth/internal/smth"
)

// popupState 是登录弹窗的可轮询结果。Wails 应用事件在某些 Windows/WebView2
// 环境下可能没能及时送达前端，因此除事件外再提供 binding 轮询通道。
type popupState struct {
	Generation int64  `json:"generation"`
	Status     string `json:"status"` // open / success / failed / cancelled
	Message    string `json:"message"`
	User       string `json:"user"`
	Channel    string `json:"channel"`
}

// BbsService 终端界面与数据层之间的唯一通道。
type BbsService struct {
	client *smth.Client
	popup  *loginPopup
	post   *postPopup

	popupMu    sync.Mutex
	popupGen   int64
	popupState popupState

	postMu    sync.Mutex
	postState popupState
}

// NewBbsService 创建服务。
func NewBbsService() *BbsService {
	return &BbsService{client: smth.NewClient()}
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ok(msg string) (string, error) {
	return toJSON(smth.Result{OK: true, Message: msg})
}

// GetConfig 读取配置。
func (s *BbsService) GetConfig() (string, error) {
	return toJSON(s.client.Config())
}

// SetConfig 以 JSON 字符串合并配置。
func (s *BbsService) SetConfig(patch string) (string, error) {
	cfg, err := s.client.SaveConfig(patch)
	if err != nil {
		return "", err
	}
	return toJSON(cfg)
}

// SetSource 切换 demo / web / openapi。
func (s *BbsService) SetSource(source string) (string, error) {
	cfg, err := s.client.SetSource(source)
	if err != nil {
		return "", err
	}
	return toJSON(cfg)
}

// Status 返回顶栏/底栏状态。
func (s *BbsService) Status(board string) (string, error) {
	return toJSON(s.client.Status(board))
}

// DataDir 返回配置目录。
func (s *BbsService) DataDir() (string, error) {
	return s.client.Store().Dir(), nil
}

// Login Web 用户名密码登录。
func (s *BbsService) Login(user, pass string) (string, error) {
	name, err := s.client.Login(user, pass)
	if err != nil {
		return "", err
	}
	return ok("登录成功：" + name)
}

// LoginWith 真实登录：mode 为 auto / web / wap，captcha 是可选的极验令牌 JSON。
// 返回逐通道尝试结果，供登录屏就地展示（不弹窗、不跳屏）。
func (s *BbsService) LoginWith(mode, user, pass, captcha string) (string, error) {
	out, err := s.client.LoginWith(mode, user, pass, captcha)
	if err != nil {
		return "", err
	}
	return toJSON(out)
}

// AttachPopup 注入登录弹窗管理器（由 main 在创建应用后装配）。
func (s *BbsService) AttachPopup(p *loginPopup) { s.popup = p }

// AttachPostPopup 注入发帖验证窗口管理器（由 main 在创建应用后装配）。
func (s *BbsService) AttachPostPopup(p *postPopup) {
	s.post = p
	p.client = s.client
}

// MarkPostDone 记录发帖弹窗的最终状态，供前端轮询兜底。
func (s *BbsService) MarkPostDone(status, message, articleURL string) {
	s.postMu.Lock()
	s.postState.Status = status
	s.postState.Message = message
	s.postState.Channel = articleURL
	s.postMu.Unlock()
}

// InvalidatePostCache 发文成功后清除该主题与版面的列表/正文缓存，保证刷新能看到新文章。
func (s *BbsService) InvalidatePostCache(board string, threadID int64) (string, error) {
	s.client.InvalidatePostCache(board, threadID)
	return ok("ok")
}

// PostPopupState 返回最近一次发帖弹窗结果。
func (s *BbsService) PostPopupState() (string, error) {
	s.postMu.Lock()
	st := s.postState
	s.postMu.Unlock()
	return toJSON(st)
}

// ClearPostAttachments 保留 binding 兼容：wap 附件按会话关联，不需要预清理。
func (s *BbsService) ClearPostAttachments(board string) (string, error) {
	return ok("ok")
}

// UploadPostAttachment 从本地文件读取并上传到 wap 发帖临时区。
// wap 发文时 uploadToken 会把本次会话上传的附件自动附加，正文不需要 [upload=N] 标记。
func (s *BbsService) UploadPostAttachment(board, path, filename string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取文件失败：%v", err)
	}
	if filename == "" {
		filename = filepath.Base(path)
	}
	name, err := s.client.WapUploadAttachment(board, filename, data)
	if err != nil {
		return "", err
	}
	return toJSON(map[string]any{"ok": true, "name": name})
}

// ResolvePostTarget 保留 binding 兼容（wap 发文不再需要 web 文章号）。
func (s *BbsService) ResolvePostTarget(board, subject string, threadID int64, floor int) (string, error) {
	id, err := s.client.ResolvePostTarget(board, subject, threadID, floor)
	if err != nil {
		return toJSON(map[string]any{"id": int64(0), "error": err.Error()})
	}
	return toJSON(map[string]any{"id": id})
}

// OpenPostPopup 打开 wap 发文验证窗口：用户点一下极验后由 Go 调用 wap API 提交。
// threadID/floor 用来定位所回复楼层的真实 wap 文章 hash。
func (s *BbsService) OpenPostPopup(board, subject, content string, threadID int64, floor int, isReply bool) (string, error) {
	if s.post == nil {
		return "", fmt.Errorf("发帖验证窗口尚未初始化")
	}
	target, err := s.client.WapPostTarget(board, subject, threadID, floor)
	if err != nil {
		return "", err
	}
	// 没有附件时令牌也不是必需的；取不到就继续，避免阻塞发帖。
	token, _ := s.client.WapUploadToken(board)
	s.post.setTarget(target)
	cfg := s.client.Config()
	req := PostRequest{
		Board: board, Subject: subject, Content: content,
		ThreadID: threadID, Floor: floor, IsReply: isReply,
		BoardHash: target.BoardHash, ArticleHash: target.ArticleHash, UploadToken: token,
	}
	msg, err := s.post.Open(cfg.Cookie, req)
	if err != nil {
		return "", err
	}
	s.postMu.Lock()
	s.postState = popupState{Status: "open", Message: msg}
	s.postMu.Unlock()
	return toJSON(map[string]any{"ok": true, "message": msg})
}

// PickFiles 打开系统多选文件对话框，返回选中的本地绝对路径（JSON 数组）。
func (s *BbsService) PickFiles() (string, error) {
	d := application.Get().Dialog.OpenFile()
	d.CanChooseFiles(true).CanChooseDirectories(false).AllowsOtherFileTypes(true)
	d.SetTitle("选择附件（可多选）")
	paths, err := d.PromptForMultipleSelection()
	if err != nil || len(paths) == 0 {
		empty, _ := toJSON([]string{})
		return empty, nil // 用户取消
	}
	return toJSON(paths)
}

// OpenLoginPopup 打开真实网页登录弹窗：账号密码预填，验证码由用户在本站页面完成。
// 返回 generation，前端用它轮询 LoginPopupState 取得最终结果。
func (s *BbsService) OpenLoginPopup(mode, user, pass string) (string, error) {
	if s.popup == nil {
		return "", fmt.Errorf("登录弹窗尚未初始化")
	}
	msg, err := s.popup.Open(mode, user, pass)
	if err != nil {
		return "", err
	}
	s.popupMu.Lock()
	s.popupGen++
	gen := s.popupGen
	s.popupState = popupState{Generation: gen, Status: "open"}
	s.popupMu.Unlock()
	diagLog("service", "OpenPopup gen=%d mode=%s", gen, mode)
	return toJSON(map[string]any{"ok": true, "message": msg, "generation": gen})
}

// LoginPopupState 返回最近一次登录弹窗的结果，供前端事件之外再轮询兜底。
func (s *BbsService) LoginPopupState() (string, error) {
	s.popupMu.Lock()
	st := s.popupState
	s.popupMu.Unlock()
	diagLog("poll", "LoginPopupState gen=%d status=%s", st.Generation, st.Status)
	return toJSON(st)
}

// MarkPopupDone 由 Go 侧登录回调查询最终结果。
func (s *BbsService) MarkPopupDone(status, message, user, channel string) {
	s.popupMu.Lock()
	gen := s.popupGen
	s.popupState.Status = status
	s.popupState.Message = message
	s.popupState.User = user
	s.popupState.Channel = channel
	s.popupMu.Unlock()
	diagLog("state", "MarkDone gen=%d status=%s user=%q channel=%s", gen, status, user, channel)
}

// SessionInfo 返回 term 进站登录屏需要的会话信息（记住的代号、是否已有会话等）。
func (s *BbsService) SessionInfo() (string, error) {
	return toJSON(s.client.SessionInfo())
}

// GuestLogin 以 guest 试用身份进入，不联网、不写会话。
func (s *BbsService) GuestLogin() (string, error) {
	v, err := s.client.GuestLogin()
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// CheckSession 校验已保存的 Cookie / access_token，用于登录屏免密继续。
func (s *BbsService) CheckSession() (string, error) {
	v, err := s.client.CheckSession()
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// SetCookie 导入浏览器 Cookie。
func (s *BbsService) SetCookie(cookie string) (string, error) {
	if err := s.client.SetCookie(cookie); err != nil {
		return "", err
	}
	return ok("Cookie 已导入，按 R 刷新当前版面")
}

// Logout 退出登录。
func (s *BbsService) Logout() (string, error) {
	if err := s.client.Logout(); err != nil {
		return "", err
	}
	return ok("已退出登录")
}

// OAuthURL 返回官方开放平台授权页地址（wiki.open.newsmth.net/oauth）。
func (s *BbsService) OAuthURL() (string, error) {
	return s.client.OAuthURL(), nil
}

// OpenOAuth 打开系统浏览器进行 OAuth2 授权。
func (s *BbsService) OpenOAuth() (string, error) {
	u := s.client.OAuthURL()
	if err := openExternal(u); err != nil {
		return "", err
	}
	return ok("已在浏览器打开授权页，授权后把 code 粘贴回 sfjth（设置 → OAuth2）")
}

// OAuthCode 用授权码换取 token。
func (s *BbsService) OAuthCode(code string) (string, error) {
	if err := s.client.OAuthCode(code); err != nil {
		return "", err
	}
	return ok("开放平台授权成功")
}

// APICall 直接调用官方 API 方法（调试/高级功能）。
func (s *BbsService) APICall(method, params string) (string, error) {
	return s.client.APICall(method, params)
}

// Sections 分类讨论区。
func (s *BbsService) Sections() (string, error) {
	v, err := s.client.Sections()
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// Boards 版面列表。
func (s *BbsService) Boards(section string) (string, error) {
	v, err := s.client.Boards(section)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// SearchBoards 按关键词搜索版面（选择阅读讨论区），wap 优先。
func (s *BbsService) SearchBoards(keyword string) (string, error) {
	v, err := s.client.SearchBoards(keyword)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// Favorites 收藏夹。
func (s *BbsService) Favorites() (string, error) {
	v, err := s.client.Favorites()
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// ToggleFavorite 收藏/取消收藏版面。
func (s *BbsService) ToggleFavorite(board string) (string, error) {
	on := s.client.ToggleFavorite(board)
	if on {
		return ok("已加入收藏夹：" + board)
	}
	return ok("已移出收藏夹：" + board)
}

// TopTen 当日十大。
func (s *BbsService) TopTen() (string, error) {
	v, err := s.client.TopTen()
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// Threads 版面文章列表。
func (s *BbsService) Threads(board string, page int) (string, error) {
	v, err := s.client.Threads(board, page)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// Thread 文章正文。
func (s *BbsService) Thread(board string, id int64, page int) (string, error) {
	v, err := s.client.Thread(board, id, page)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// AttachmentPreview 读取附件并生成可内联预览的 data URL。
// 参数是一个 Attachment JSON 字符串（由文章正文带出来）。
func (s *BbsService) AttachmentPreview(attachmentJSON string) (string, error) {
	var a smth.Attachment
	if err := json.Unmarshal([]byte(attachmentJSON), &a); err != nil {
		return "", fmt.Errorf("附件参数错误：%w", err)
	}
	v, err := s.client.AttachmentPreview(a)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// OpenAttachment 把附件下载到本地并用系统默认程序打开（doc/excel/超大文件）。
func (s *BbsService) OpenAttachment(attachmentJSON string) (string, error) {
	var a smth.Attachment
	if err := json.Unmarshal([]byte(attachmentJSON), &a); err != nil {
		return "", fmt.Errorf("附件参数错误：%w", err)
	}
	path, err := s.client.DownloadAttachment(a)
	if err != nil {
		return "", err
	}
	if err := openExternal(path); err != nil {
		return "", err
	}
	return ok("已用系统程序打开：" + filepath.Base(path))
}

// MarkRead 记录阅读进度（阅读记忆 + 未读数）。
func (s *BbsService) MarkRead(board string, id int64, floor int, count int) (string, error) {
	s.client.MarkRead(board, id, floor, count)
	return ok("")
}

// Prefetch 预取主题正文，ids 为 JSON 数组字符串。
func (s *BbsService) Prefetch(board, ids string) error {
	var list []int64
	_ = json.Unmarshal([]byte(ids), &list)
	s.client.Prefetch(board, list)
	return nil
}

// PostArticle 发表/回复文章。
func (s *BbsService) PostArticle(board, subject, content string, replyID int64) (string, error) {
	v, err := s.client.Post(board, subject, content, replyID)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// ReplyArticle 回复当前楼层。threadID 是主题 ID，floor 是 1 起的楼层号。
func (s *BbsService) ReplyArticle(board, subject, content string, threadID int64, floor int) (string, error) {
	v, err := s.client.Reply(board, subject, content, threadID, floor)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// MailBox 信箱列表。
func (s *BbsService) MailBox(box string, page int) (string, error) {
	v, err := s.client.MailBox(box, page)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// MailDetail 读取 web 信箱里的一封信正文。
func (s *BbsService) MailDetail(box string, id int64) (string, error) {
	return s.client.MailDetail(box, id)
}

// Reminders 返回四类未读提醒数量，供主菜单红点显示。
func (s *BbsService) Reminders() (string, error) {
	v, err := s.client.WapReminders()
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// WapConversations wap 收件箱会话列表。
func (s *BbsService) WapConversations(page int) (string, error) {
	v, err := s.client.WapConversations(page)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// WapMessages 读取 wap 某个会话的消息。
func (s *BbsService) WapMessages(speakerID string, page int) (string, error) {
	v, err := s.client.WapMessages(speakerID, page)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// WapNotifies 读取 wap 一类提醒。
func (s *BbsService) WapNotifies(kind, page int) (string, error) {
	v, err := s.client.WapNotifies(kind, page)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// OpenNotifyTarget 把一条提醒（@我的 / 回复我的 / Like我的）定位到版面上的文章：
// 返回英文版名、派生主题号、所在楼层，前端据此进入版面并跳到该楼回复。
func (s *BbsService) OpenNotifyTarget(board, boardTitle, topicHash, articleHash, subject string) (string, error) {
	v, err := s.client.NotifyTarget(board, boardTitle, topicHash, articleHash, subject)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// WapMarkNotifyRead 标记一条提醒已读。
func (s *BbsService) WapMarkNotifyRead(id string) (string, error) {
	if err := s.client.WapMarkNotifyRead(id); err != nil {
		return "", err
	}
	return ok("ok")
}

// WapMarkNotifyReadAll 标记一类提醒全部已读。
func (s *BbsService) WapMarkNotifyReadAll(kind int) (string, error) {
	if err := s.client.WapMarkNotifyReadAll(kind); err != nil {
		return "", err
	}
	return ok("ok")
}

// WapMarkMessageRead 标记一个会话已读。
func (s *BbsService) WapMarkMessageRead(speakerID string) (string, error) {
	if err := s.client.WapMarkMessageRead(speakerID); err != nil {
		return "", err
	}
	return ok("ok")
}

// WapSendMessage 用 wap 接口发送站内信。
func (s *BbsService) WapSendMessage(to, body string) (string, error) {
	if err := s.client.WapSendMessage(to, body); err != nil {
		return "", err
	}
	return ok("信件已发出")
}

// SendMail 发信。
func (s *BbsService) SendMail(to, title, content string) (string, error) {
	v, err := s.client.SendMail(to, title, content)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// Search 搜索帖子。
func (s *BbsService) Search(keyword, scope string) (string, error) {
	v, err := s.client.Search(keyword, scope)
	if err != nil {
		return "", err
	}
	return toJSON(v)
}

// OpenExternal 用系统浏览器打开链接。
func (s *BbsService) OpenExternal(u string) error {
	return openExternal(u)
}

// Version 返回版本信息（关于界面）。
func (s *BbsService) Version() (string, error) {
	return toJSON(map[string]string{
		"name": "sfjth",
		"ver":  "0.1.0",
		"go":   runtime.Version(),
		"os":   runtime.GOOS + "/" + runtime.GOARCH,
		"wiki": smth.Wiki,
		"doc":  "https://v3.wails.io/",
	})
}

func openExternal(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法打开浏览器: %w", err)
	}
	return nil
}
