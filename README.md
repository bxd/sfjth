# sfjth — 水木社区 term 风格客户端

用 **Wails 3** 写的 Windows 桌面水木社区（newsmth.net）客户端。名字 `sfjth`。

界面和操作尽可能复刻 **term / FTerm / CTerm 下访问水木 BBS** 的体验：80×24 字符网格、黑底 ANSI 16 色、蓝色顶栏／底栏、进站 ASCII 彩图、方向键加单键命令；功能与实现方式借鉴 **smthflow / ismth「丝滑版」** 的思路：缓存、后台预取、阅读记忆、未读回帖数、已读置灰、引用折叠。数据层按 **[水木社区开放平台官方文档](https://wiki.open.newsmth.net/)** 实现 OAuth2、数字签名与方法常量，并保留 Web 会话与离线演示两条回退路径。

> 详细 API 对照、签名算法与已知边界见 [docs/API.md](docs/API.md)。

---

## 1. 快速开始

### 环境

| 依赖 | 本项目验证版本 |
| --- | --- |
| Windows | 10 / 11（x64） |
| Go | 1.26.x |
| Node.js / npm | 22.x / 11.x |
| Wails CLI | v3.1.0（`go install github.com/wailsapp/wails/v3/cmd/wails3@latest`） |
| WebView2 Runtime | Windows 11 自带；Win10 需安装 |

### 构建

```bash
# 生成前端绑定（改过 bbs_service.go 的公开方法后必须重新生成）
wails3 generate bindings

# 开发模式（前端热重载）
wails3 dev

# 生产构建，产物在 bin/sfjth.exe
wails3 build
```

`wails3 build` 会依次执行 `go mod tidy` → `wails3 generate icons` → `generate bindings` → `npm install` → `npm run build` → `generate syso` → `go build`，并把 `frontend/dist` 通过 `//go:embed all:frontend/dist` 嵌进单个 exe，分发时不需要附带前端文件。

只验证 Go 代码时用 `go build .`，**不要**用 `go build ./...`：模板里的 `build/ios`、`build/android` 是平台专用 main 包，桌面构建会报 `function main is undeclared`。

### 首次启动

启动后先出现 **term 进站登录画面**（见下方「进站登录画面」）。输入 `guest` 回车即可免密进入主选单，用离线演示数据完整体验所有界面、键位、发文编辑器与阅读记忆；也可以输入真实水木账号登录。想连真实站点，见下面「数据源」。

---

## 2. 界面与操作

启动后先是 term 进站登录画面（见下），进入后为主菜单。主菜单复刻了 term 截图：顶栏 `主菜单 / 水木社区 / 讨论区 [NewExpress]`，第二行 `目前选择：[FavBoard]`，中部黄／棕／蓝／绿进站彩图，左侧 13 项菜单，右侧 ASCII 艺术字，底部 `时间 / 呼叫器 / 使用者 / 停留` 状态行。

### 窗口外观（无边框）

窗口用 Wails 3 的 `Frameless: true` 创建，**没有 Windows 白色系统标题栏**，字符网格从窗口内容区第一行开始，最大化时也不会再出现系统栏。

- 右上角三个半透明浮动按钮：`─` 最小化 / `□` 最大化（最大化后切换为还原图标）/ `×` 关闭；鼠标移入才完全显现，关闭按钮悬停变红。按钮浮在画面之上，**不占字符网格**。
- **移动窗口**：按住蓝色顶栏、底部状态栏或帮助栏拖动；进站登录屏没有状态栏，整屏都可拖动。窗口边缘仍可拖拽缩放。
- **双击**顶栏（登录屏为画面空白处）切换最大化／还原；`Alt+F4` 仍可关闭窗口。
- 顶栏右侧状态文字按窗口缩放自动避开浮动按钮，只有在按钮与顶栏真正重叠时才左移，保持 term 的满行排版。

实现上按钮调用 `@wailsio/runtime` 的 `Window.Minimise / ToggleMaximise / Close`，拖动与边缘缩放由 Wails 的 `--wails-draggable: drag` / `wails:drag` 机制接管；普通浏览器预览（`frontend/dist`）下按钮只给出提示，不会触发原生窗口调用。

字符网格严格按 term 规则排版：**一个中文／全角字符（含 `◆ █ ▄ ▲` 等块符号）等于两个 ASCII 字符宽**，字体栈优先宋体／新宋体等等宽中文字体。这样菜单当前行的菱形光标不会把它后面的字母和汉字推右一格，任何一行选中时文字起始列都保持一致。

### 通用键位

| 键 | 作用 |
| --- | --- |
| `↑` `↓`（或 `k` `j`） | 移动光标；阅读文章时为「上一篇／下一篇同主题文章」 |
| `→` / `Enter` | 进入；阅读文章时为下一页 |
| `←` / `q` / `Esc` / `Backspace` | 返回上一级 |
| `PgDn` / `空格` / `f` | 下一页；阅读文章时先续读长正文，读完再换下一篇，再翻页 |
| `PgUp` | 上一页；阅读文章时回退正文 |
| `Home` / `End` | 跳到首／末项 |
| 数字 `0-9` / `g` | 跳到指定编号（楼层、序号） |
| `s` / `S` | 直接输入英文版名进版 |
| `/` | 搜索文章 |
| `h` / `?` | 操作说明（应用内帮助页） |
| `t` | 本日十大 |
| `m` | 信件处理 |
| `o` | 系统设置 |
| `w` | 用系统浏览器打开官方 wiki |
| `r` | 阅读文章时回复；读信时回信；版面列表时刷新 |
| `R` | 阅读文章时回信给作者 |
| `a` | 加入／移出收藏夹 |
| `d` | 循环切换数据源 `demo → web → openapi` |
| `U` | 回到 term 进站登录画面（切换用户） |
| `p` / `Ctrl+P` | 发表新文章（信件处理界面为写信） |
| `Ctrl+S` / `Ctrl+Enter` | 在编辑器里发送 |
| `Esc` | 放弃编辑 |
| 鼠标单击 / 双击 | 选中行／直接进入 |

### 主菜单单键

`0` 精华公布栏 · `X` 分类讨论区 · `F` 个人定制区 · `E` 旧分类讨论区 · `M` 信件处理 · `T` 聊天选单 · `I` 个人工具箱（设置）· `N` 新开启讨论区 · `S` 选择阅读讨论区 · `B` 所有版列表 · `C` 系统信息及服务 · `K` 二站 · `G` 离开水木。

### 设置页单键

| 键 | 项目 |
| --- | --- |
| `D` | 切换数据源 |
| `L` | 按当前登录通道（web/wap/auto）发起真实用户名／密码登录 |
| `C` | 粘贴浏览器 Cookie |
| `O` | 打开原站登录页 |
| `P` | 打开官方 OAuth2 授权页 |
| `K` | 填入 authorize `code` 换取 token |
| `1` `2` `3` `4` | `client_id` / `client_secret` / `client_signature` / 站点地址 |
| `R` | 记住代号开关（只记 ID，不保存密码） |
| `A` | 会话复用开关（已有会话时免密继续） |
| `U` | 切换用户，回到进站登录画面 |
| `W` | 打开官方开发文档 |
| `Q` | 退出登录（清除 Cookie 与 token） |

### 进站登录画面（启动首屏）

复刻用户提供的 term 登录截图：**全屏 24 行纯黑，隐藏顶栏/底栏/帮助栏**。上半部按用户要求只做风格占位——彩虹色 SMTH 点阵、绿色 `;;;` 装饰、`网通 / 电信` 双线路 `bbs.mysmth.net` / `www.mysmth.net`、红色像素图案、紫色 `LULL`、黄色方块字、青色备案号；下半部严格对齐截图：

```
欢迎光临 ◆ 水木社区 ◆   上线人数 69878[最高: 406798](64029 WWW GUEST)
试用请输入 'guest'，注册请通过 web 方式访问, add '.' after your ID for BIG5
请输入代号：ax.e█
请输入密码：********█
按 [RETURN] 继续
```

| 键 | 作用 |
| --- | --- |
| 字符键 | 输入代号或密码（密码显示为 `*`，带闪烁方块光标） |
| `Enter` | 代号 → 进入密码行；密码 → 提交登录 |
| `Backspace` | 删除一个字符 |
| `Esc` | 清空密码、回到代号输入 |
| `Ctrl+U` | 清空当前输入行 |
| `Del` | 忽略已保存会话，强制重新输密码 |
| `Tab` | 在代号/密码两行间切换 |
| `F2` | 切换登录通道：`WEB+WAP 自动` → `WEB` → `WAP`（`auto` 的弹窗默认走 web） |
| `F5` | 直接粘贴浏览器 Cookie 导入会话（不想走弹窗时的退路） |
| `guest` + `Enter` | 免密试用，直接进入主选单 |
| `ax.e.` + `Enter` | 按截图提示，代号后加 `.` 启用 BIG5 标记 |

### 真实登录链路：底部弹出的网页登录窗

密码输完按 `Enter` 后，**不会再用程序模拟登录请求**，而是从主窗口底部滑出一个无边框登录窗：

1. 本地为 web / wap 各起一个只监听 `127.0.0.1` 的随机端口，各自提供同源外壳页和 catch-all 反向代理：web 端口全部转发 `www.newsmth.net`，wap 端口全部转发 `wap.newsmth.net`；
2. web 加载 nForum 的**侧栏登录框**（`/nForum/#!mainpage`，带 hash 防止跳回主站首页），wap 加载 `https://wap.newsmth.net/login`；
3. HTML 里指向 `static.mysmth.net` 的 CSS/JS 被改写成同源 `/__static/` 路径并二次转发（前缀剥离必须带前导 `/`，否则站点 JS 会 404、登录按钮点了没反应），确保站点脚本能真正加载、登录框能被渲染、极验弹窗能创建；同时去掉 `X-Frame-Options` / CSP，改写跳转与 Cookie 域名；
4. 外壳脚本把**代号和密码自动填好**，勾选「下次自动登录」，并把整页裁剪到只留下侧栏那一小块（帐号 / 密码 / 下次自动登录 / 登录按钮）。裁剪时**保留极验弹窗宿主 `#dialog`**，否则点登录看不到验证框；布局裁剪只做一次，避免定时脚本把刚弹出的验证框又隐藏；
5. 用户自己完成极验验证并提交。**登录成功由反向代理直接识别**：捕获 web `ajax_login.json` 的 `ajax_st=1` / wap 登录接口的 `code=1`，以及会话 Cookie 中 `main[UTMPUSERID]` 从 `guest` 变为真实 ID；代理会累积整个登录过程中的原始 `Set-Cookie`。外壳 JS 跳转 `/done` 只作为兜底，两条路径由 `sync.Once` 保证只回传一次；
6. Go 用 `CheckSession()` 真实验证会话，写入 `config.json`，再通知主窗口进入主选单。这样即使 wap 登录成功后页面回到 `/login`，也不会再因外壳脚本的路径判断漏判而不跳主选单。

因为登录发生在真实水木页面上，`main[UTMPUSERID]` 这类带方括号的 Cookie 在浏览器侧映射成 `sfjth_main_UTMPUSERID`，回传时再还原；Go 侧仍然自己解析 `Set-Cookie` 并手工回带，标准 CookieJar 的 RFC 限制不影响后续读帖。

`LoginWith()` / `Login()` 这两个直连 API 的 service 方法仍然保留（供自动化或站点关闭验证码时使用），但 term 界面的主登录流程已经改为上面的网页弹窗——这样极验由用户真实操作，不再需要手工复制令牌。

「功能实现仿 smthflow」落在这几点：

- **记住代号**：登录成功后把 ID 写进 `config.json`，下次启动自动填好；**密码绝不落盘**，交给弹窗后立刻从主界面内存清掉。
- **会话复用**：登录成功后把会话 Cookie 落盘；下次启动在登录屏直接按 `Enter` 会请求两端会话接口校验，有效就免密进主选单，失效才要求输密码。设置页 `A` 可关闭，`Del` 可临时绕过。
- **就地反馈**：打开弹窗、登录成功、校验失败都显示在第 22/23 行或弹窗顶栏，不弹系统对话框、不跳屏。
- **登录即上线**：默认 `demo` 下登录成功会自动把数据源切到 `web`，不用再手动按 `d`。
- 全局 `U` 随时回到登录画面切换用户。

> **为什么不再让用户输入验证码令牌**：站点两端都开了极验，令牌有很强的一次性和设备绑定，复制粘贴经常失败；让用户直接在真实登录页上点一下验证，是唯一稳定的方式。若弹窗加载失败，可按 `F5` 从系统浏览器导入 Cookie，或在设置页（`o` → `C`）导入。

---

## 3. 三种数据源

在设置页按 `D` 循环切换，或直接改 `%APPDATA%\sfjth\config.json` 的 `"source"` 字段。

| source | 数据来自 | 需要什么 | 能做什么 |
| --- | --- | --- | --- |
| `demo`（默认） | 内置演示数据 | 无 | 全部界面、键位、编辑器、阅读记忆；发文/发信只写本地，不联网 |
| `web` | `www.newsmth.net/nForum/` 页面（GBK 解析） | 登录 Cookie | 分类讨论区、版面列表、主题列表、同主题正文、十大、信箱、发文回帖 |
| `openapi` | 官方开放平台 `open.newsmth.net` | APP 凭据 + OAuth2 token | 官方分区／版面／收藏夹、驻版主题时间线及原文、发文回帖、任意方法调试 |

在进站登录画面用真实账号登录成功时，若当前是 `demo`，会自动切到 `web` 并把会话 Cookie 落盘；不需要手动按 `D`。

### web 数据源：导入 Cookie（推荐）

站点对游客读帖有 `Read verification` 验证码，登录表单还带极验等隐藏字段，**程序化用户名／密码登录不一定成功**，最稳的做法是导入浏览器 Cookie：

1. 用 Edge / Chrome 打开 `https://www.newsmth.net/nForum/` 并登录。
2. F12 → 网络（Network）→ 刷新 → 任选一个 `nForum` 请求 → 请求标头（Request Headers）→ 复制整行 `Cookie:` 的值。
3. 在 sfjth 设置页按 `C`，整段粘贴回车。
4. 按 `d` 切到 `web`，回版面按 `r` 刷新。

### openapi 数据源：OAuth2 授权

官方平台需要先向站务申请 APP，拿到 `client_id`、`client_secret`、`client_signature`：

1. 设置页按 `1` `2` `3` 分别填入三项；`4` 保持默认站点地址。
2. 按 `P` 打开 `oauth2/authorize` 授权页，用浏览器登录并同意授权。
3. 浏览器会跳到 `redirect_uri`（默认 `http://localhost/sfjth/oauth`），地址栏里会有 `code=...`；复制这段 code。
4. 设置页按 `K` 粘贴 code，程序调用 `oauth2/access_token` 换取 token 并持久化。
5. 按 `d` 切到 `openapi`。签名规则、scope、错误码见 [docs/API.md](docs/API.md)。

> **重要边界**：官方 wiki 的 API 是「时间线」模型，`article/*` 全部是**写操作**，没有「按版面列主题」「按主题读同主题文章」的读取方法。所以 `openapi` 下主题列表来自 `timeline/boards_threads`（只覆盖已驻版面），阅读页显示时间线里带回来的原文；要看任意版面的完整同主题串，请切回 `web`。十大、信件在 `openapi` 下同样没有对应官方方法，分别使用本地演示数据与 Web 会话。

---

## 4. 「丝滑」能力

对应 smthflow 帖子里提到的诉求：

| 能力 | 实现位置 |
| --- | --- |
| 退出后再进帖接着看（阅读记忆） | `Store.MarkRead` / `ThreadResult.Resume`，落盘 `progress.json` |
| 显示未读新回帖数 `[+N]` | `Store.SeenCount` / `Store.Unread` + `decorateThreads` |
| 已读标题置灰、引用灰色、引用折叠 | 前端 `renderThreads` / `renderPost` + `config.show_read_gray` / `fold_quotes` |
| 流畅翻页、减少等待 | Go 侧 TTL `Cache`、下一页与光标附近主题后台 `Prefetch`、前端纯本地字符网格渲染（无白屏、无网页重排） |
| 紧凑模式 | `config.terminal_cols` / `terminal_rows` / `font_size` 可调 |
| 少登录一次 | 记住代号、Cookie 落盘会话复用、失败就地提示、登录即自动切在线源（见「进站登录画面」） |

---

## 5. 本地数据

所有数据放在 `%APPDATA%\sfjth\`（在「系统信息及服务」页可看到实际路径），不写注册表，绿色可迁移：

- `config.json` — 数据源、Cookie、token、APP 凭据、收藏夹、显示选项，以及 `last_user_id` / `remember_id` / `auto_login` / `big5` / `login_mode` 登录偏好（**不含密码**）。
- `progress.json` — 每个主题的已读楼层与上次看到的回复总数。

删除这两个文件即恢复出厂状态。配置中含会话凭据，请勿分享。

---

## 6. 目录结构

```
main.go                       Wails 3 入口，注册 BbsService、嵌入 frontend/dist
bbs_service.go                暴露给前端的 30 个 Wails service 方法（统一返回 JSON 字符串）
internal/smth/types.go         Section/Board/Thread/Post/Mail/Config/... 统一模型
internal/smth/store.go         配置与阅读进度持久化 + TTL 内存缓存
internal/smth/openapi.go       官方 OAuth2、数字签名、方法常量、通用 Call
internal/smth/openapi_read.go  官方实体（section/board/thread/article）→ 本地模型
internal/smth/login.go         web/wap 真实登录、验证码令牌、会话校验与方括号 Cookie 处理
internal/smth/login_test.go    httptest 验证两端请求字段与自动回退
internal/smth/web.go           nForum GBK 页面解析、Cookie 会话、游客验证码识别
internal/smth/demo.go          离线演示数据
internal/smth/client.go        数据层门面：三源路由 + 缓存/预取/阅读记忆
frontend/index.html            字符终端 DOM 容器
frontend/src/main.js           Line 网格渲染器、进站登录屏与状态机、各屏渲染、term 键盘、编辑器
frontend/src/term.css          80×24 网格、ANSI 配色、蓝条、等宽中文字体
frontend/src/mock.js           普通浏览器预览用的演示数据（真实 Wails 环境不启用）
frontend/bindings/             wails3 generate bindings 自动生成，勿手工改
docs/API.md                    官方文档对照与 API 说明
bin/sfjth.exe                  生产构建产物
```

浏览器预览：起一个静态服务指向 `frontend/dist`，用 `index.html?screen=login|threads|post|help|settings|boards|topten|mail|sections|about|favorites|main` 可直接深链到对应界面（`mock.js` 提供数据，仅用于 UI 校验）。`?screen=login&filled=1` 会预填代号与 8 位密码，便于和 term 截图做视觉对照。

---

## 7. 已知限制

- 官方开放平台需要申请 APP 凭据；没有凭据时只能配置流程，无法调用受保护方法。
- `nForum` 游客读帖会触发 `Read verification`；`web` 数据源必须导入登录 Cookie。
- Web / WAP 登录接口本身是真实可用的，但站点目前两端都强制极验：无令牌时分别返回 `1103 验证码错误` / `kbsCode 9 无效的验证`。可用 `F2` 单通道重试、站点关闭验证码时直登，`F3` 粘贴令牌，或导入浏览器 Cookie。
- Web 页面解析基于正则与 `title_1`…`title_12` 等 class 名，站点改版后需维护选择器。
- 官方 API 无按版面读主题／读同主题串的方法（见上文「重要边界」）。
- 聊天选单、二站只保留入口占位，未实现。
- 目前只按用户提供的「term 主菜单」截图做过像素级校准，其余界面按通用 BBS/term 习惯还原，欢迎继续提供截图以便对齐。

---

## 8. 参考

- 水木社区开放平台文档：https://wiki.open.newsmth.net/ （[OAuth2](https://wiki.open.newsmth.net/oauth)、[签名](https://wiki.open.newsmth.net/signature)、[方法汇总](https://wiki.open.newsmth.net/method_list)、[权限](https://wiki.open.newsmth.net/scope_list)、[分页](https://wiki.open.newsmth.net/pager)、[错误码](https://wiki.open.newsmth.net/error)）
- Wails 3：https://v3.wails.io/
- smthflow「让水木更流畅点」／ismth「丝滑版」：水木 `SMTHApp_Dev` 版相关讨论（功能诉求来源）
