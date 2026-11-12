/* 浏览器预览回退：在 Wails WebView 之外打开 dist/index.html 时，
 * 用一份与 Go 侧 demo 数据源一致的本地数据驱动界面。
 * 真实运行时（window.wails / chrome.webview 存在）完全不会走这里。
 */

export const browserMode = () =>
    typeof window !== 'undefined' &&
    !window.wails &&
    !(window.chrome && window.chrome.webview && window.chrome.webview.postMessage);

const CONFIG = {
    source: 'demo', base_url: 'https://www.newsmth.net', cookie: '', user: 'guest',
    client_id: '', client_secret: '', client_signature: '',
    redirect_uri: 'http://localhost/sfjth/oauth', access_token: '',
    terminal_cols: 80, terminal_rows: 24, font_size: 20, color_scheme: 'classic',
    favorites: ['NewExpress', 'Programming', 'ITExpress', 'Family', 'AutoWorld', 'Joke'],
    show_read_gray: true, fold_quotes: true, preload: true, mouse_enabled: true,
    last_user_id: 'ax.e', remember_id: true, auto_login: true, big5: false,
    login_mode: 'auto',
    data_dir: '%APPDATA%\\sfjth（浏览器预览）',
};
const STATUS = {source: 'demo', user: 'guest', online: false, board: '', message: '', uptime: 0, unread_all: 0, wiki: 'https://wiki.open.newsmth.net/'};
const VERSION = {name: 'sfjth', ver: '0.1.0', go: 'browser preview', os: 'web', wiki: 'https://wiki.open.newsmth.net/'};

const SECTIONS = [
    {id: '0', name: '本站系统', desc: '系统公告与服务', total: 12},
    {id: '1', name: '国内院校', desc: '兄弟院校版面', total: 86},
    {id: '2', name: '休闲娱乐', desc: '影视音乐游戏', total: 64},
    {id: '3', name: '五湖四海', desc: '各地风土人情', total: 51},
    {id: '4', name: '电脑技术', desc: '软硬件与网络', total: 73},
    {id: '5', name: '学术科学', desc: '理工与人文', total: 42},
    {id: '6', name: '艺术文化', desc: '文学艺术摄影', total: 38},
    {id: '7', name: '体育运动', desc: '球类与健身', total: 33},
    {id: '8', name: '财经时事', desc: '经济与新闻', total: 29},
    {id: '9', name: '生活百态', desc: '衣食住行情感', total: 57},
];
const BOARDS = [
    {id: 'NewExpress', name: '水木特快', section: '0', manager: 'SYSOP', desc: '站内最快的信息集散地', users: 128, today: 342, topics: 98211, articles: 1450233, kind: 'board'},
    {id: 'Programming', name: '编程技术', section: '4', manager: 'itgirl', desc: 'Stack Overflow每月提问数暴跌', users: 37, today: 26, topics: 22899, articles: 341872, kind: 'board'},
    {id: 'ITExpress', name: 'IT业界快讯', section: '4', manager: 'hgoldfish', desc: '技术新闻与行业八卦', users: 21, today: 18, topics: 15602, articles: 208441, kind: 'board'},
    {id: 'Family', name: '家庭生活', section: '9', manager: 'teandcoffee', desc: '◇家─有爱就有责任◇', users: 46, today: 55, topics: 44120, articles: 720198, kind: 'board'},
    {id: 'AutoWorld', name: '汽车世界', section: '9', manager: 'jackok', desc: '用车、选车、自驾', users: 33, today: 41, topics: 39800, articles: 660123, kind: 'board'},
    {id: 'Joke', name: '笑话连篇', section: '2', manager: 'ZhouHaiBin', desc: '每天笑一笑', users: 18, today: 12, topics: 21550, articles: 302877, kind: 'board'},
    {id: 'EconForum', name: '经济论坛', section: '8', manager: 'rlcne', desc: '宏观与微观', users: 12, today: 7, topics: 18770, articles: 251903, kind: 'board'},
    {id: 'Apple', name: '苹果园', section: '4', manager: 'hzxq', desc: 'Mac / iOS / 周边', users: 15, today: 9, topics: 22104, articles: 318442, kind: 'board'},
    {id: 'SMTHApp_Dev', name: '客户端开发', section: '4', manager: 'hotfix', desc: '第三方客户端交流与反馈', users: 9, today: 5, topics: 6463, articles: 98210, kind: 'board'},
    {id: 'BBSNewbie', name: '新手入门', section: '0', manager: 'shen', desc: '如何使用telnet方式访问水木社区', users: 6, today: 3, topics: 6087, articles: 71200, kind: 'board'},
    {id: 'Occupier', name: '买房装修', section: '9', manager: 'waterwei', desc: '安居乐业', users: 24, today: 30, topics: 51230, articles: 880411, kind: 'board'},
    {id: 'Photo', name: '摄影天地', section: '6', manager: 'bluerock', desc: '用光影记录生活', users: 8, today: 4, topics: 15440, articles: 210884, kind: 'board'},
    {id: 'Association', name: '协会社团', section: '1', manager: '', desc: '[二级目录]', users: 0, today: 0, topics: 0, articles: 0, kind: 'dir'},
];
const SUBJECTS = {
    NewExpress: [
        ['SYSOP', '【公告】水木社区服务条款更新说明', 128, '◆'],
        ['shen', '如何使用 term 方式访问水木社区（含客户端推荐）', 86, '★'],
        ['teandcoffee', '今晚的月亮真圆，配图', 42, ' '],
        ['hgoldfish', 'Re: 用 Wails 写个桌面客户端怎么样', 233, '★'],
        ['rlcne', '求推荐一款适合长时间看帖的等宽字体', 57, ' '],
        ['jackok', '双十一也没有十年前的味道了', 310, '★'],
        ['ZhouHaiBin', '关于版面阅读记忆功能的一点建议', 19, ' '],
        ['hotfix', 'ismth 丝滑版更新 v1.2.5', 96, ' '],
        ['njer', 'VxTerm 可以完美访问文字版水木', 74, ' '],
        ['moonwalker', '分享一个油猴脚本：自动登录 + 接口降级', 38, ' '],
        ['waterwei', '今天食堂的糖醋排骨回来了', 12, ' '],
        ['bluerock', '【合集】那些年我们用过的 BBS 终端', 205, '★'],
    ],
    Programming: [
        ['itgirl', '发招聘贴的注意', 312, '◆'],
        ['hgoldfish', 'Stack Overflow每月提问数从20万暴跌至1000以下', 289, '★'],
        ['ooolinux', '很多 AI 项目操之过急', 46, ' '],
        ['ooolinux', '跨平台图形界面编程论好用还是Lazarus', 133, '★'],
        ['z16166', '4090单显卡跑deepseek和GLM了', 87, ' '],
        ['ooolinux', 'Lazarus + 龙芯 + 国产Linux', 54, ' '],
        ['z16166', '币安开发钱包的码农被钓鱼、社工', 71, ' '],
        ['hgoldfish', 'Codex 写 Delphi 程序一次过', 98, ' '],
        ['hgoldfish', 'Re: 突然发现，flutter已经成了桌面跨平台ui的最佳选择', 176, '★'],
        ['itgirl', '【合集】Wails / Tauri / Electron 桌面方案对比', 244, '★'],
    ],
    SMTHApp_Dev: [
        ['smthflow', 'smthflow 让水木更流畅点', 64, '★'],
        ['hotfix', '纯AI开发的水木APP - ismth征集需求中', 312, '◆'],
        ['hotfix', 'ismth 更新v1.2.5版，修复登录状态显示异常问题', 108, ' '],
        ['rlcne', '有没有帖子浏览的记忆功能，退出后再点进去接着看', 21, ' '],
        ['jackok', '能不能显示有多少未读的新回帖', 17, ' '],
        ['hgoldfish', '水木官方是不是还有个官方 API 的版本，用的是 oauth 登录', 43, ' '],
    ],
};
function threads(board) {
    const src = SUBJECTS[board] || [
        ['guest', '欢迎来到 ' + board + ' 版面', 3, '◆'],
        ['olduser', '这里是离线演示数据，登录后会显示真实帖子', 8, ' '],
        ['hgoldfish', 'Re: 用 Wails 3 做 Windows 客户端的记录', 27, '★'],
        ['smthflow', '论 BBS 客户端的「丝滑」是怎么做出来的', 55, ' '],
        ['itgirl', '版面置顶规则说明', 0, '◆'],
    ];
    return src.map((s, i) => ({
        id: 100000 + board.length * 997 + i * 37, board_id: board, subject: s[1], author: s[0],
        date: `11-${String(11 - i).padStart(2, '0')}`, count: s[2] + 1,
        pages: Math.min(40, Math.floor(s[2] / 10) + 1), flag: s[3], attach: i % 3 === 0,
        read: i % 5 === 0, unread: i % 4 === 1 ? 5 : 0, last_user: s[0],
    }));
}
const BODY = [
    '这篇帖子来自 sfjth 离线演示数据源。',
    '',
    '按 ← 或 q 返回版面，↑ / ↓ 选择文章，→ 或 Enter 进入阅读。',
    'PgUp / PgDn（或空格）翻页，r 回复，Ctrl+P 发表新文章，h 查看帮助。',
    '',
    '【 在 olduser 的大作中提到: 】',
    ': term 下的操作基本不用鼠标，方向键就是全部：',
    ': ← 返回，→ 进入，↑↓ 移动光标，q 退出。',
    ': 这个习惯二十多年没变过。',
    '',
    '所以我做客户端的时候，第一件事就是把这套键位完整搬过来。',
    '数据层用 Go 抓页面 + 本地缓存预取，界面层只用等宽字符渲染，',
    '这样翻页不会白屏，也不会有网页那种拖泥带水的感觉。',
    '',
    '--',
    '※ 来源:·水木社区 newsmth.net·[FROM: 123.113.42.*]',
];
const REPLIES = [
    '确实流畅，请问下有没有帖子浏览的记忆功能，就是当前阅读到哪一楼，退出后再点进去就接着看。',
    '能不能显示有多少未读的新回帖？比如帖子有 15 个回帖，已经看过 10 个，那么显示 5 个没看过。',
    '这个需求已经记下了：阅读进度、已读置灰、未读数都在本地进度文件里。',
    '另外问一下，官方开放平台的 API 还能用吗？wiki 上写的是 OAuth2 + 数字签名。',
    '能申请到 client_id 就能用，申请不到就走 Web 数据源，Cookie 登录后一样读。',
    '顶，term 风格好评。',
];
function posts(board, id, page, subject) {
    const users = ['teandcoffee', 'rlcne', 'jackok', 'hotfix', 'smthflow', 'itgirl', 'bluerock'];
    return Array.from({length: 8}, (_, i) => {
        const floor = (page - 1) * 8 + i + 1;
        return {
            id: id + floor, board_id: board, thread_id: id, floor,
            author: floor === 1 ? 'hgoldfish' : users[(floor - 2) % users.length],
            subject: floor === 1 ? subject : 'Re: ' + subject,
            date: 'Wed Nov 11 22:02:07 2026',
            from: '水木社区 (Wed Nov 11 22:02:07 2026), 站内',
            attachments: floor === 1 ? [{
                id: 'demo-attachment', name: 'sfjth-term.png', type: 'image/png',
                size: 2048, kind: 'image', urls: [],
            }] : [],
            body: floor === 1 ? BODY.join('\n') :
                '【 在 hgoldfish 的大作中提到: 】\n: 所以我做客户端的时候，第一件事就是把这套键位完整搬过来。\n\n' +
                REPLIES[(floor - 2) % REPLIES.length] + '\n\n--\n※ 来源:·水木社区·[FROM: 221.216.140.*]',
        };
    });
}
const TOP10 = [
    ['水木特快', '【合集】用终端上文字版 BBS 的还有几个人', 'njer', 512, 'NewExpress'],
    ['编程技术', 'Stack Overflow 每月提问数从 20 万暴跌至 1000 以下', 'hgoldfish', 289, 'Programming'],
    ['客户端开发', 'smthflow 让水木更流畅点', 'smthflow', 233, 'SMTHApp_Dev'],
    ['家庭生活', '双十一也没有十年前的味道了', 'jackok', 187, 'Family'],
    ['苹果园', 'BBS客户端 Welly 正式上架 Mac App Store', 'tangyang', 156, 'Apple'],
    ['IT业界', '纯 AI 开发的水木 APP - ismth 征集需求中', 'hotfix', 141, 'ITExpress'],
    ['汽车世界', '冬季用车 checklist', 'someone', 120, 'AutoWorld'],
    ['笑话连篇', '程序员的周末', 'ZhouHaiBin', 98, 'Joke'],
    ['经济论坛', '关于明年的一点粗浅判断', 'rlcne', 87, 'EconForum'],
    ['新手入门', '如何使用 telnet 方式访问水木社区', 'shen', 64, 'BBSNewbie'],
];
const MAILS = [
    {id: 1, from: 'SYSOP', title: '欢迎使用 sfjth', date: '2026-11-11', unread: true, content: '你好：\n\n这是 sfjth 的演示信箱。联网登录后可以读取真实站内信。\n\n按 ← 返回，Enter 阅读，^P 写新信。\n\n※ 来源:·水木社区'},
    {id: 2, from: 'smthflow', title: 'Re: 让水木更流畅点', date: '2026-11-09', unread: true, content: '预取 + 本地缓存 + 不重新布局是关键。\n\n另外记得做阅读记忆和未读回帖数。\n\n--\nsmthflow'},
    {id: 3, from: 'hgoldfish', title: '官方 wiki 的地址', date: '2026-11-08', unread: false, content: 'https://wiki.open.newsmth.net/\n\nOAuth2、数字签名、错误码、分页说明都在里面。\n\n注意：APP Client 需要向站务申请。'},
];

const state = {config: {...CONFIG}, start: Date.now(), read: {}};

export function mockCall(name, args = []) {
    const [a, b, c] = args;
    switch (name) {
        case 'GetConfig': return {...state.config};
        case 'SetConfig': Object.assign(state.config, JSON.parse(a || '{}')); return {...state.config};
        case 'SetSource': state.config.source = a; return {...state.config};
        case 'SetCookie': state.config.cookie = a; return {ok: true, message: 'Cookie 已导入（浏览器预览）'};
        case 'Login':
            state.config.user = a; state.config.last_user_id = a;
            state.config.cookie = 'demo-session=1'; state.config.source = 'web';
            return {ok: true, message: '登录成功：' + a};
        case 'LoginWith': {
            const mode = ['auto', 'web', 'wap'].includes(a) ? a : 'auto';
            const user = String(b || '').replace(/\.$/, '');
            const pass = String(c || '');
            const attempts = [];
            if (mode !== 'wap') attempts.push({channel: 'web', ok: pass === 'good', code: pass === 'good' ? '0005' : '1103', message: pass === 'good' ? 'Web 登录成功，会话有效' : 'Web 通道需要验证码：验证码错误', captcha_required: pass !== 'good'});
            if (mode === 'auto' && pass !== 'good') attempts.push({channel: 'wap', ok: false, code: '0/9', message: 'WAP 通道需要验证码：无效的验证', captcha_required: true});
            if (mode === 'wap') attempts.push({channel: 'wap', ok: pass === 'good', code: pass === 'good' ? '1/0' : '0/9', message: pass === 'good' ? 'WAP 登录成功，会话有效' : 'WAP 通道需要验证码：无效的验证', captcha_required: pass !== 'good'});
            const okAttempt = attempts.find(x => x.ok);
            state.config.login_mode = mode;
            if (!okAttempt) return {ok: false, user: '', channel: mode, message: attempts.map(x => x.channel + ': ' + x.message).join(' | '), captcha_required: true, attempts};
            state.config.user = okAttempt.channel === 'wap' ? user : user;
            state.config.last_user_id = user;
            state.config.cookie = 'demo-session=' + okAttempt.channel;
            state.config.source = 'web';
            return {ok: true, user, channel: okAttempt.channel, message: '登录成功（' + okAttempt.channel + '）：' + user + '（浏览器预览）', captcha_required: false, attempts};
        }
        case 'OpenLoginPopup': {
            // 浏览器预览无法弹出真实水木 WebView，直接模拟一次完成后的会话。
            const user = String(b || '').replace(/\.$/, '');
            state.config.user = user; state.config.last_user_id = user;
            state.config.cookie = 'demo-webview-session';
            state.config.source = 'web';
            state.config.login_mode = ['auto', 'web', 'wap'].includes(a) ? a : 'auto';
            return {ok: true, simulated: true, message: '浏览器预览：已模拟网页登录（' + state.config.login_mode + '）'};
        }
        case 'Logout': state.config.user = ''; state.config.cookie = ''; return {ok: true, message: '已退出登录'};
        case 'SessionInfo': return {
            user: state.config.user || 'guest', last_user_id: state.config.last_user_id,
            has_session: !!state.config.cookie || !!state.config.access_token,
            source: state.config.source, auto_login: state.config.auto_login,
            remember_id: state.config.remember_id, big5: state.config.big5,
            login_mode: state.config.login_mode || 'auto',
            message: '', online: 69878, max_online: 406798, guests: 64029,
        };
        case 'GuestLogin':
            state.config.user = ''; state.config.cookie = '';
            return {ok: true, message: '以 guest 身份试用（浏览器预览）'};
        case 'CheckSession': return {
            valid: !!state.config.cookie || !!state.config.access_token,
            user: state.config.user || state.config.last_user_id,
            message: state.config.cookie ? '会话有效（浏览器预览）' : '没有已保存的会话',
        };
        case 'DataDir': return state.config.data_dir;
        case 'Status': return {...STATUS, user: state.config.user || 'guest', source: state.config.source, board: a || '', uptime: Math.floor((Date.now() - state.start) / 1000)};
        case 'Version': return VERSION;
        case 'Sections': return SECTIONS;
        case 'Boards': return a ? BOARDS.filter(x => x.section === a || x.kind === 'dir') : BOARDS;
        case 'SearchBoards': {
            const kw = String(a || '').toLowerCase();
            if (!kw) return [];
            return BOARDS.filter(x => (x.id || '').toLowerCase().includes(kw) || (x.name || '').includes(kw));
        }
        case 'Favorites': return state.config.favorites.map(id => BOARDS.find(x => x.id === id)).filter(Boolean);
        case 'ToggleFavorite': {
            const i = state.config.favorites.indexOf(a);
            if (i >= 0) { state.config.favorites.splice(i, 1); return {ok: true, message: '已移出收藏夹：' + a}; }
            state.config.favorites.push(a); return {ok: true, message: '已加入收藏夹：' + a};
        }
        case 'TopTen': return TOP10.map((t, i) => ({rank: i + 1, board: t[0], title: t[1], author: t[2], count: t[3], board_id: t[4], id: 100001 + i}));
        case 'Threads': {
            const base = threads(a);
            const page = b || 1;
            // 每页生成不同条目，便于验证列表的跨页累积/自动续载。
            const items = base.map((t, i) => ({
                ...t,
                id: t.id + (page - 1) * 100000,
                subject: page === 1 ? t.subject : `[第${page}页] ${t.subject}`,
            }));
            return {board: BOARDS.find(x => x.id === a) || BOARDS[0], items, page, pages: 3, mode: '最新回复'};
        }
        case 'Thread': {
            const all = threads(a);
            const t = all.find(x => Number(x.id) === Number(b)) || all[0];
            return {board: BOARDS.find(x => x.id === a) || BOARDS[0], subject: t.subject,
                items: posts(a, Number(b), c || 1, t.subject), page: c || 1, pages: 4, total: 32, resume: 0};
        }
        case 'MailBox': return {items: a === 'sent' ? [] : MAILS, page: b || 1, pages: 1};
        case 'MailDetail': return '【演示模式】这是一封真实水木信件的离线示例。';
        case 'Search': {
            const out = [];
            for (const bd of BOARDS) for (const t of threads(bd.id)) {
                if (!a || t.subject.includes(a) || t.author.includes(a)) out.push(t);
            }
            return {items: out, query: a, scope: b};
        }
        case 'PostArticle': return {ok: true, message: '【演示模式】文章已保存（未发送到水木）', id: Date.now()};
        case 'ReplyArticle': return {ok: true, message: `【演示模式】已回复第 ${args[4] || 1} 楼（未发送到水木）`, id: Date.now()};
        case 'ResolvePostTarget': return {id: 0};
        case 'OpenPostPopup': { setTimeout(() => window.__mockPostSuccess && window.__mockPostSuccess(), 400); return {ok: true, message: '【演示模式】已打开发表窗口'}; }
        case 'PostPopupState': return {status: 'open', message: ''};
        case 'InvalidatePostCache': return {ok: true};
        case 'PickFiles': return [];
        case 'UploadPostAttachment': return {ok: true, mark: `[upload=${Date.now() % 9 + 1}][/upload]`, name: a};
        case 'ClearPostAttachments': return {ok: true};
        case 'SendMail': return {ok: true, message: '【演示模式】信件已保存'};
        case 'Reminders': return {mail: 0, at: 0, reply: 0, like: 0};
        case 'WapConversations': return {items: [], page: 1, pages: 1};
        case 'WapMessages': return {items: [], page: 1, pages: 1};
        case 'WapNotifies': {
            // 演示数据：提醒条目本身就是版面上的文章，带版面名与主题/文章 hash。
            const kind = Number(a) || 2;
            const titles = {1: '@我的提醒', 2: '回函提醒', 3: 'Like提醒'};
            return {
                items: [{
                    id: 'demo-notify-' + kind, type: kind, from: 'demoUser',
                    subject: 'Re: ' + (titles[kind] || '提醒'),
                    body: '这是一条演示提醒，指向版面里的一篇文章。',
                    board: '特快万象', board_id: 'Universal',
                    topic_hash: 'demo-topic-hash', article_hash: 'demo-article-hash',
                    date: '11-12 18:00', unread: true, target_id: 'demo-article-hash',
                }],
                page: 1, pages: 1, unread: 1,
            };
        }
        case 'OpenNotifyTarget':
            return {board: a || 'Universal', board_title: b || '特快万象',
                thread_id: 100001, floor: 2, subject: args[4] || ''};
        case 'WapMarkNotifyRead': case 'WapMarkNotifyReadAll': case 'WapMarkMessageRead': case 'WapSendMessage': return {ok: true};
        case 'MarkRead': return {ok: true};
        case 'AttachmentPreview': {
            const att = JSON.parse(a || '{}');
            const kind = att.kind || 'file';
            const demoText = 'sfjth 浏览器预览：附件预览需在运行中的 Wails 窗口里读取真实数据。\n' +
                '文件名：' + (att.name || 'attachment') + '\n类型：' + kind;
            const dataUrl = kind === 'text' || kind === 'html'
                ? 'data:text/plain;base64,' + btoa(unescape(encodeURIComponent(demoText)))
                : 'data:image/svg+xml;base64,' + btoa(unescape(encodeURIComponent(
                    '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="320" height="180" fill="#000080"/><text x="20" y="90" fill="#ffff55" font-size="20">' +
                    (att.name || 'attachment') + '</text></svg>')));
            return {name: att.name, kind, mime: att.type, size: att.size, data_url: dataUrl, inline: true, too_large: false};
        }
        case 'OpenAttachment': return {ok: true, message: '浏览器预览：已请求打开 ' + (JSON.parse(a || '{}').name || '附件')};
        case 'Prefetch': return null;
        case 'OAuthURL': return 'https://wiki.open.newsmth.net/oauth';
        case 'OpenOAuth': case 'OpenExternal': return {ok: true, message: '浏览器预览：' + (a || '')};
        case 'OAuthCode': return {ok: true, message: '授权成功（浏览器预览）'};
        case 'APICall': return JSON.stringify({error: 0, demo: true, method: a});
        default: return null;
    }
}
