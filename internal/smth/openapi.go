package smth

// 本文件严格按「水木社区开放平台」官方文档实现：
//
//   https://wiki.open.newsmth.net/            平台简介
//   https://wiki.open.newsmth.net/oauth       OAuth2 授权（Authorization Code Grant）
//   https://wiki.open.newsmth.net/signature  数字签名
//   https://wiki.open.newsmth.net/pager      分页 / 定位查询（page、size、from，size<=50）
//   https://wiki.open.newsmth.net/error      错误码（10001~11209）
//   https://wiki.open.newsmth.net/method_list 方法汇总
//   https://wiki.open.newsmth.net/scope_list  权限列表
//
// 签名规则（官方文档原文）：把 POST 中所有非 signature 数据按变量名字典序排序，
// 逐项拼成 "小写变量名=字符串形式变量值&"，最后附上 client_signature，
// 对整个字符串做 MD5，得到 32 位小写十六进制串作为 signature 一起 POST。

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 官方文档 /method_list 与各方法页中出现的全部方法名。
// 注意：官方平台是「时间线」模型，article/* 全部是写操作，并没有
// 「按版面列主题」「按主题读同主题文章」的读取方法；因此版面/文章浏览
// 仍由 Web 数据源承担，openapi 数据源负责授权、分区/版面/收藏夹、
// 时间线主题与发文回帖（详见 openapi_read.go 与 docs/API.md）。
const (
	// OAuth2 授权
	MethodOAuthAuthorize   = "oauth2/authorize"
	MethodOAuthAccessToken = "oauth2/access_token"

	// 个人信息 / 收藏夹
	MethodProfileUser       = "profile/user"
	MethodProfileModifyNick = "profile/modify_nick"
	MethodProfileAddFriend  = "profile/add_friend"
	MethodProfileDelFriend  = "profile/delete_friend"
	MethodProfileSignature  = "profile/read_signature"
	MethodProfileAddSign    = "profile/add_signature"
	MethodProfileDelSign    = "profile/delete_signature"
	MethodLoadFavorites     = "profile/load_favorites"
	MethodAddFavorite       = "profile/add_favorite_board"
	MethodDelFavorite       = "profile/delete_favorite_board"
	MethodCreateFavDir      = "profile/create_favorite_directory"
	MethodRemoveFavDir      = "profile/remove_favorite_directory"

	// 时间线（注意官方把 friends 拼成了 firends，按原文保留）
	MethodBoardsThreads   = "timeline/boards_threads"
	MethodBoardsArticles  = "timeline/boards_articles"
	MethodFriendsThreads  = "timeline/friends_threads"
	MethodFirendsArticles = "timeline/firends_articles"

	// 用户相关
	MethodUserQuery       = "user/query"
	MethodUserGetFriends  = "user/get_friends"
	MethodUserLoadFriends = "user/load_friends"
	MethodUserGetFans     = "user/get_fans"
	MethodUserLoadFans    = "user/load_fans"

	// 版面相关
	MethodBoardQuery       = "board/query"
	MethodBoardLoadBoards  = "board/load_boards"
	MethodBoardLoadSection = "board/load_section"
	MethodBoardReadSection = "board/read_section"

	// 主题与文章（均为写操作）
	MethodArticlePost       = "article/post"
	MethodArticleReply      = "article/reply"
	MethodArticleForward    = "article/forward"
	MethodArticleForwardThr = "article/forward_thread"
	MethodArticleCross      = "article/cross"
	MethodArticleRecommend  = "article/recommend"
	MethodArticleModify     = "article/modify"
	MethodArticleDelete     = "article/delete"

	// 附件操作
	MethodAttachmentList   = "attachment/read_list"
	MethodAttachmentUpload = "attachment/upload"
	MethodAttachmentRemove = "attachment/remove"

	// 短消息
	MethodMessageForward = "message/forward"
)

// 官方权限（scope_list），申请 APP 时用空格分隔填入。
const ScopeString = "basic read_article read_board read_member read_user post " +
	"read_mail edit_mail send_mail send_message read_message edit_message " +
	"read_friends edit_friends read_refer edit_refer add_member del_member " +
	"read_timeline read_profile edit_profile read_favorite edit_favorite " +
	"read_attachment edit_attachment edit_article"

// Signature 计算官方数字签名。
func Signature(params map[string]string, clientSignature string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if strings.EqualFold(k, "signature") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	for _, k := range keys {
		buf.WriteString(strings.ToLower(k))
		buf.WriteByte('=')
		buf.WriteString(params[k])
		buf.WriteByte('&')
	}
	buf.WriteString(clientSignature)
	sum := md5.Sum([]byte(buf.String()))
	return hex.EncodeToString(sum[:])
}

// OpenAPI 是开放平台客户端。
type OpenAPI struct {
	Base           string
	ClientID       string
	ClientSecret   string
	ClientSign     string
	RedirectURI    string
	AccessToken    string
	HTTP           *http.Client
	OnTokenChanged func(string)
}

// NewOpenAPI 创建开放平台客户端。
func NewOpenAPI(cfg Config, onToken func(string)) *OpenAPI {
	base := "http://open.newsmth.net"
	return &OpenAPI{
		Base:           strings.TrimRight(base, "/"),
		ClientID:       cfg.ClientID,
		ClientSecret:   cfg.ClientSecret,
		ClientSign:     cfg.ClientSign,
		RedirectURI:    cfg.RedirectURI,
		AccessToken:    cfg.AccessToken,
		HTTP:           &http.Client{Timeout: 15 * time.Second},
		OnTokenChanged: onToken,
	}
}

// AuthorizeURL 按 oauth2/authorize 生成授权页地址（在浏览器中打开）。
func (o *OpenAPI) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", o.ClientID)
	q.Set("redirect_uri", o.RedirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	return o.Base + "/" + MethodOAuthAuthorize + "?" + q.Encode()
}

// Exchange 用 authorize code 换取 access_token（oauth2/access_token）。
func (o *OpenAPI) Exchange(code string) error {
	if o.ClientID == "" || o.ClientSecret == "" {
		return errors.New("未配置 client_id / client_secret，请先在设置里填入开放平台 APP 信息")
	}
	form := url.Values{}
	form.Set("client_id", o.ClientID)
	form.Set("client_secret", o.ClientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.RedirectURI)
	resp, err := o.HTTP.PostForm(o.Base+"/"+MethodOAuthAccessToken, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int64  `json:"expires_in"`
		TokenType        string `json:"token_type"`
		Scope            string `json:"scope"`
		RefreshToken     string `json:"refresh_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("解析授权响应失败: %w", err)
	}
	if out.Error != "" {
		return fmt.Errorf("OAuth2 %s: %s", out.Error, out.ErrorDescription)
	}
	if out.AccessToken == "" {
		return errors.New("未获得 access_token，请检查 authorize code 是否过期")
	}
	o.AccessToken = out.AccessToken
	if o.OnTokenChanged != nil {
		o.OnTokenChanged(out.AccessToken)
	}
	return nil
}

// Call 调用任一官方 API 方法：POST <base>/<method>.json，自动附加签名。
// 官方文档规定所有方法均为 POST，size 上限 50，错误码见 /error。
func (o *OpenAPI) Call(method string, params map[string]string) (map[string]any, error) {
	if o.AccessToken == "" {
		return nil, errors.New("尚未授权：请先在设置里完成 OAuth2 授权（或改用 Web 数据源）")
	}
	if o.ClientSign == "" {
		return nil, errors.New("未配置 client_signature（签名密钥）")
	}
	data := map[string]string{}
	for k, v := range params {
		data[k] = v
	}
	data["access_token"] = o.AccessToken
	data["signature"] = Signature(data, o.ClientSign)

	form := url.Values{}
	for k, v := range data {
		form.Set(k, v)
	}
	endpoint := o.Base + "/" + strings.Trim(method, "/") + ".json"
	resp, err := o.HTTP.PostForm(endpoint, form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("API 返回非 JSON（HTTP %d）: %s", resp.StatusCode, limit(string(b), 160))
	}
	if code, ok := out["error"]; ok && fmt.Sprint(code) != "0" && fmt.Sprint(code) != "" {
		return out, fmt.Errorf("API 错误 %v: %v（见 wiki.open.newsmth.net/error）", code, out["error_description"])
	}
	return out, nil
}

// Profile 调用 profile/user 获取当前登录用户。
func (o *OpenAPI) Profile() (map[string]any, error) {
	return o.Call(MethodProfileUser, nil)
}

// Sections 调用 board/load_section 读取传统分区列表。
func (o *OpenAPI) Sections() (map[string]any, error) {
	return o.Call(MethodBoardLoadSection, nil)
}

// Board 调用 board/query 查询版面信息。
func (o *OpenAPI) Board(name string) (map[string]any, error) {
	return o.Call(MethodBoardQuery, map[string]string{"query": name})
}

// Timeline 调用 timeline/boards_threads 读取驻版主题时间线。
// 文档参数：article / body / attachment / from / size。
func (o *OpenAPI) Timeline(from, size int) (map[string]any, error) {
	return o.Call(MethodBoardsThreads, map[string]string{
		"article": "1",
		"body":    "1",
		"from":    fmt.Sprint(from),
		"size":    fmt.Sprint(size),
	})
}

// Post 调用 article/post 发表文章。
func (o *OpenAPI) Post(board, subject, content string) (map[string]any, error) {
	return o.Call(MethodArticlePost, map[string]string{
		"board_id": board, "subject": subject, "content": content,
	})
}

// Reply 调用 article/reply 回复文章。
func (o *OpenAPI) Reply(board string, replyID int64, subject, content string) (map[string]any, error) {
	return o.Call(MethodArticleReply, map[string]string{
		"board_id": board,
		"reply_id": fmt.Sprint(replyID),
		"subject":  subject,
		"content":  content,
	})
}

func limit(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
