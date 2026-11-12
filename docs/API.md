# sfjth 与 SMTH 开放平台 API 对照

本文是 `wiki.open.newsmth.net` 官方文档在 sfjth 里的落地说明：哪些规则被实现了、代码在哪、以及官方平台没覆盖的能力由谁补上。所有链接均为官方文档原地址。

---

## 1. 官方文档索引

| 页面 | 内容 | 对应代码 |
| --- | --- | --- |
| https://wiki.open.newsmth.net/ | 平台简介 | `main.go` 注释 |
| [/oauth](https://wiki.open.newsmth.net/oauth) | OAuth2 Authorization Code Grant | `openapi.go: AuthorizeURL / Exchange` |
| [/signature](https://wiki.open.newsmth.net/signature) | 数字签名 | `openapi.go: Signature` |
| [/pager](https://wiki.open.newsmth.net/pager) | 定位查询 `from` / `size`（size ≤ 50） | `openapi_read.go: Threads` |
| [/error](https://wiki.open.newsmth.net/error) | 错误码 10001~11209 | `openapi.go: Call` 里的 `error` 分支 |
| [/method_list](https://wiki.open.newsmth.net/method_list) | 方法汇总 | `openapi.go` 的 `Method*` 常量 |
| [/scope_list](https://wiki.open.newsmth.net/scope_list) | 权限列表 | `openapi.go: ScopeString` |
| [/entity/thread](https://wiki.open.newsmth.net/entity/thread) | thread 对象 | `openapi_read.go: Threads` |
| [/entity/article](https://wiki.open.newsmth.net/entity/article) | article 对象 | `openapi_read.go: toPost` |
| [/entity/board](https://wiki.open.newsmth.net/entity/board) | board 对象 | `openapi_read.go: toBoard` |
| [/entity/section](https://wiki.open.newsmth.net/entity/section) | section 对象 | `openapi_read.go: Sections` |
| [/entity/page](https://wiki.open.newsmth.net/entity/page) | 定位工具对象 | `pager` 游标 |

---

## 2. OAuth2 授权流程

官方文档：[/oauth](https://wiki.open.newsmth.net/oauth)、[/method/oauth2/authorize](https://wiki.open.newsmth.net/method/oauth2/authorize)、[/method/oauth2/access_token](https://wiki.open.newsmth.net/method/oauth2/access_token)。

```
sfjth 设置页 P
  → 系统浏览器打开 http://open.newsmth.net/oauth2/authorize
        ?client_id=...&redirect_uri=...&response_type=code&state=sfjth-<unix>
  → 用户登录并同意授权
  → 浏览器跳回 redirect_uri?code=...
  → 设置页 K 粘贴 code
  → POST http://open.newsmth.net/oauth2/access_token
        client_id / client_secret / grant_type=authorization_code / code / redirect_uri
  → 返回 access_token，写入 %APPDATA%\sfjth\config.json
```

实现位置：`OpenAPI.AuthorizeURL()`、`OpenAPI.Exchange()`；前端入口 `BbsService.OAuthURL()` / `OpenOAuth()` / `OAuthCode()`。

> 默认 `redirect_uri` 是 `http://localhost/sfjth/oauth`，页面打不开没关系，**地址栏里的 `code` 才是需要的**。申请 APP 时填写的回调地址必须与这里一致。

---

## 3. 数字签名

官方规则（[/signature](https://wiki.open.newsmth.net/signature)）：把一次 POST 中所有**非 `signature`** 的参数按变量名字典序排序，逐项拼成 `小写变量名=字符串形式变量值&`，最后附上 `client_signature`，对整个字符串做 MD5，得到 32 位小写十六进制串，作为 `signature` 一起提交。

sfjth 实现（`internal/smth/openapi.go`）：

```go
func Signature(params map[string]string, clientSignature string) string {
    keys := make([]string, 0, len(params))
    for k := range params {
        if strings.EqualFold(k, "signature") { continue }
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
```

`OpenAPI.Call()` 会自动补 `access_token`、算 `signature`，然后 `POST http://open.newsmth.net/<method>.json`（官方规定所有方法都是 POST）。返回若带非零 `error`，转成 Go error 并提示查 `/error`。

---

## 4. 分页与权限

- [/pager](https://wiki.open.newsmth.net/pager)：用 `from`（起始 ID）+ `size`（记录数，`size ≤ 50`）定位查询；时间线方法用上一页最后一条的 `timeline_id` 作下一页的 `from`。`apiRead.Threads` 用 `size=40`，并缓存 `board:page → from`。
- [/scope_list](https://wiki.open.newsmth.net/scope_list)：`ScopeString` 常量列出官方全部权限，申请 APP 时用空格分隔填入：

```
basic read_article read_board read_member read_user post read_mail edit_mail
send_mail send_message read_message edit_message read_friends edit_friends
read_refer edit_refer add_member del_member read_timeline read_profile
edit_profile read_favorite edit_favorite read_attachment edit_attachment
edit_article
```

几个关键方法需要的权限：`board/load_section` → `read_board`；`timeline/boards_threads` → `read_timeline`；`profile/load_favorites` → `read_favorite`；`article/post` → `post` + `read_board`；`article/reply` → 同上。

---

## 5. 实体字段映射

| 官方实体 | 字段 | 映射到 |
| --- | --- | --- |
| section | `id` `name` `desc` `code` | `Section{ID,Name,Desc}` |
| board | `bid` `id` `manager` `flag` `name` `section` `level` `current_users` `max_online` `last_post` `total` | `Board{ID,Name,Manager,Section,Users,Articles,Kind}` |
| thread | `id` `board_id` `subject` `time` `count` `author_id` `last_reply_id` `last_user_id` `last_time` `flag` `timeline_id` `article` `board` | `Thread{ID,BoardID,Subject,Author,Date,Time,Count,LastUser}` |
| article | `id` `board_id` `thread_id` `author_id` `time` `subject` `body` `reply_id` `flag` `timeline_id` | `Post{ID,BoardID,ThreadID,Author,Subject,Body,Date,From}` |

`time` / `last_time` 是 Unix Timestamp；`apiDate()` 转成 `01-02 15:04`（跨年则 `06-01-02`）。`board` 对象里 `id` 是**英文版名字符串**，目录（group）对象的 `id` 是数字 —— `toBoard()` 据此把 `Kind` 置为 `board` / `dir`，前端对 `dir` 会继续下钻。

---

## 6. sfjth 的数据源路由

| 界面 / 操作 | `demo` | `web` | `openapi` |
| --- | --- | --- | --- |
| 分类讨论区 | `demoSections` | `/nForum/section/*` 解析 | `board/load_section` |
| 版面列表 | `demoBoards` | `/nForum/section/<id>` 解析 | `board/read_section` / `board/load_boards` |
| 个人定制区 | 本地收藏 | 本地收藏 | `profile/load_favorites`（失败回落本地） |
| 版面主题列表 | `demoThreads` | `/nForum/board/<board>?p=<page>` 解析 | `timeline/boards_threads`（`article=1&body=1`，按 `board_id` 过滤） |
| 阅读文章 | `demoPosts` | `/nForum/article/<board>/<id>?p=<page>` 解析 | 时间线缓存里的 `thread.article` 原文（无同主题回复串） |
| 本日十大 | `demoTopTen` | 站点十大页解析 | `demoTopTen`（官方无此方法，避免误触游客验证码） |
| 信件 | `demoMails` | Web 信箱页 | Web 信箱页（官方邮件方法未接入） |
| 搜索 | 本地演示索引 | 遍历已知版面首页标题过滤 | 驻版主题时间线过滤（官方无全站搜索） |
| 发文 / 回帖 | 仅本地演示 | `web.PostArticle` | `article/post` / `article/reply` |
| 任意方法调试 | — | — | `APICall(method, paramsJSON)` |

**关键事实**：官方 `method_list` 里的 `article/*` 全是写操作（`post` / `reply` / `forward` / `forward_thread` / `cross` / `recommend` / `modify` / `delete`），**没有读取同主题文章串、也没有按版面列主题的方法**。官方读取能力集中在时间线（`timeline/boards_threads`、`timeline/boards_articles`）与版面／收藏夹元数据。因此 sfjth 保留 `web` 数据源作为「浏览任意版面」的通道，这不是偷懒，而是官方 API 的能力边界。

`APICall` 示例（在 Go 侧或前端 devtools 里调用）：

```js
await BbsService.APICall("board/query", JSON.stringify({query: "NewExpress"}));
await BbsService.APICall("timeline/boards_threads",
    JSON.stringify({article: "1", body: "1", from: "0", size: "10"}));
await BbsService.APICall("profile/user", "{}");
```

参数值统一传字符串，`Call` 会按官方规则签名并附加 `access_token`。

---

## 7. Web 数据源解析约定

| 页面 | 用途 |
| --- | --- |
| `/nForum/` | 首页状态、分区入口 |
| `/nForum/section/<id>` | 分区内版面列表 |
| `/nForum/board/<board>?p=<page>` | 版面主题列表 |
| `/nForum/article/<board>/<id>?p=<page>` | 同主题文章正文 |
| `/nForum/login` | 用户名／密码登录（可能因验证码失败） |

- 站点返回 GBK/GB18030，用 `golang.org/x/text/encoding/simplifiedchinese` 解码。
- 主题行按 `class="title_1"` … `class="title_12"` 的行结构用正则提取。
- 遇到 `guest-read-challenge` / `Read verification` 时返回 `ErrGuestChallenge`，前端提示「请登录或导入 Cookie」。
- Cookie 通过 `cookiejar` 注入；导入后按 `r` 刷新当前版面。

---

## 8. BbsService 方法一览

`frontend/bindings/sfjth/bbsservice.js` 由 `wails3 generate bindings` 自动生成，**不要手工编辑**。除标注外均返回 JSON 字符串；前端 `jcall()` 统一解析。

| 分类 | 方法 |
| --- | --- |
| 配置 | `GetConfig()` `SetConfig(patch)` `SetSource(source)` `DataDir()` `Status(board)` `Version()` |
| 登录授权 | `Login(user,pass)` `LoginWith(mode,user,pass,captcha)` `SetCookie(cookie)` `Logout()` `SessionInfo()` `GuestLogin()` `CheckSession()` `OAuthURL()` `OpenOAuth()` `OAuthCode(code)` `APICall(method,params)` |
| 浏览 | `Sections()` `Boards(section)` `Favorites()` `ToggleFavorite(board)` `TopTen()` `Threads(board,page)` `Thread(board,id,page)` |
| 阅读记忆 | `MarkRead(board,id,floor,count)` `Prefetch(board,ids)` |
| 写入 | `PostArticle(board,subject,content,replyID)` `MailBox(box,page)` `SendMail(to,title,content)` |
| 其他 | `Search(keyword,scope)` `OpenExternal(url)` |

`Prefetch` 的 `ids` 是 JSON 数组字符串；`OpenExternal` 无返回值。

---

## 9. 登录与会话（term 进站画面）

新增配置字段（写进 `%APPDATA%\sfjth\config.json`）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `last_user_id` | string | 上次登录成功的代号，**只记 ID，不保存密码** |
| `remember_id` | bool | 启动时自动填入该代号（默认 true） |
| `auto_login` | bool | 已有会话时登录屏按 Enter 免密继续（默认 true） |
| `big5` | bool | 代号后加 `.` 触发的 BIG5 标记，仅影响进站画面显示 |
| `login_mode` | string | `auto` / `web` / `wap`，默认 `auto` 依次尝试两条真实通道 |

| 方法 | 返回 | 作用 |
| --- | --- | --- |
| `SessionInfo()` | `SessionInfo` | 登录屏初始化：记住的代号、是否有会话、通道、经典统计行数值 |
| `GuestLogin()` | `Result` | 以 guest 试用进入，不联网、不写会话 |
| `CheckSession()` | `SessionCheck` | 真实请求两端会话接口（`/nForum/user/ajax_session.json`、`/wap/api/profile`）或官方 `profile/user`，返回 `valid/user/message` |
| `Login(user,pass)` | `Result` | 兼容入口，按 `login_mode` 调用 `LoginWith` |
| `LoginWith(mode,user,pass,captcha)` | `LoginOutcome` | 直连 API 双通道登录（保留给自动化；UI 主流程已不用） |
| `OpenLoginPopup(mode,user,pass)` | `Result` | 打开底部滑入的真实网页登录窗；账号密码预填，用户在真实站点完成极验 |

`LoginOutcome.attempts` 每项为 `{channel, ok, user, code, message, captcha_required}`，`auto` 模式会同时保留 web 与 wap 两条结果。

流程：启动 → `SessionInfo()` 预填代号与通道 → 输入代号 `Enter` → 若命中已保存会话且 `auto_login` 开，走 `CheckSession()` 免密进入；否则输入密码 → `OpenLoginPopup()`。弹窗为 web / wap 各起一个本地回环端口做 catch-all 反向代理：web 加载 nForum 侧栏登录框（`/nForum/#!mainpage`），wap 加载 `https://wap.newsmth.net/login`；页面里的静态资源域名被改写为同源 `/__static/` 路径并二次转发（前缀带前导 `/` 剥离，否则站点 JS 404、登录按钮无响应），保证站点脚本渲染出真实表单、绑定 click 处理并创建极验弹窗。外壳脚本预填账号密码、勾选「下次自动登录」，把页面裁剪到只留侧栏登录块，并保留极验宿主 `#dialog`；裁剪只执行一次，避免定时脚本重复隐藏验证框。**登录成功由代理层识别**：命中 web `ajax_login.json` 的 `ajax_st=1`、wap 登录接口的 `code=1`，或累积 Cookie 中 `main[UTMPUSERID]` 变为非 guest 真实 ID，就把代理累积的原始 `Set-Cookie` 通过 `completeLogin()` 回传；外壳 JS 跳 `/done` 仅作兜底，二者由 `sync.Once` 去重。Go 校验会话、写配置，并通过应用事件 `sfjth:login-success` / `sfjth:login-failed` / `sfjth:login-cancelled` 通知主窗口。这样 wap 登录成功回到 `/login` 时也能可靠进主选单。`F2` 切换通道（`auto` 默认打开 web），`F5` 可直接导入浏览器 Cookie。直连 `LoginWith()` 仍保留令牌透传：`parseLoginCaptcha` 支持 JSON、`键=值` 与单个 ticket，不会因格式问题阻断请求。

**`SetConfig` 是局部补丁**：`Store.Patch()` 用指针字段区分「没传」与「传了零值」，只修改 JSON 里显式出现的键；设置页里只改 `client_id` 不会把 `remember_id` / `preload` 等 bool 清零。`source` 会校验只能是 `demo/web/openapi`。

### 真实接口与验证码边界

- Web：`POST /nForum/user/ajax_login.json`，表单 `id/passwd/CookieDate=2`，头 `X-Requested-With: XMLHttpRequest`；成功 `ajax_st=1`，校验 `GET /nForum/user/ajax_session.json` 的 `id`。
- WAP：`POST /wap/authorize/sign-in`，JSON `username/password`，其中 `password` 按手机站 SPA 做 `encodeURIComponent`；成功 `code=1`，校验 `GET /wap/api/profile`。
- 站点当前两端都强制极验：无令牌时分别返回 `ajax_code=1103 验证码错误`、`kbsCode=9 无效的验证`，`LoginOutcome.captcha_required=true`。新的主登录流程不在客户端模拟验证，而是让用户在真实登录页完成极验；令牌透传与 Cookie 导入只作为备用路径。
- 水木下发 `main[UTMPUSERID]` 这类带方括号的 Cookie，Go 标准 `cookiejar` 会拒收，`loginTransport` 自行解析 `Set-Cookie` 并在每次请求手工回带。
- `Logout()` 会先真实请求 `/nForum/user/ajax_logout.json`，再清理本地配置。

> `SetCookie()` 后 `NewWebClient()` 把 Cookie 注入请求头，`WebClient.Cookies()` 负责 jar 与字符串 Cookie 的合并去重；登录成功后回写配置实现会话复用。`internal/smth/login_test.go` 用 `httptest` 覆盖两端请求字段、会话校验、验证码令牌透传与双通道回退。

> 附：**窗口控制不走 BbsService**。`main.go` 用 `Frameless: true` 去掉系统标题栏，右上角浮动按钮由 `frontend/src/main.js` 直接调用 `@wailsio/runtime` 的 `Window.Minimise / ToggleMaximise / Close / IsMaximised`；拖动与边缘缩放交给 Wails runtime 的 `--wails-draggable: drag` 与 `wails:drag` / `wails:resize:*` 消息。登录弹窗的创建/滑入/关闭则在 `login_popup.go` 里直接调用 `application.App.Window`，不占 BbsService 方法；新增的 `OpenLoginPopup` 使绑定方法总数为 33。

---

## 10. 待办与维护提示

- 官方 APP 凭据需向站务申请，本仓库不含任何真实 `client_id` / token。
- 官方 `message/*`（短消息）与邮件读写方法未接入，信件在 `openapi` 下仍走 Web 会话。
- `board/read_section` 下钻目录时，前端把目录 ID 当作 `section` 回传，深层目录的 `section_id` 归属未做精确追踪。
- Web 解析器依赖站点 HTML class 名，站点改版后需要同步 `internal/smth/web.go` 的正则。
- 官方时间线是「驻版」模型，只返回已驻版面的主题；`openapi` 下某版面主题为空属正常现象，切 `web` 即可。
