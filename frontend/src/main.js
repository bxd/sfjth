/* sfjth — term 风格水木客户端前端
 * 只做一件事：把 Go 侧给的 JSON 渲染成 80x24 的字符网格，并用 term 的键位驱动。
 * 参考：wiki.open.newsmth.net（官方开放平台）、m.newsmth.net SMTHApp_Dev 版
 *      smthflow「让水木更流畅点」、ismth「丝滑版」。
 */
import './term.css';
import {BbsService} from '../bindings/sfjth/index.js';
import {Window as WailsWindow, Events} from '@wailsio/runtime';
import {browserMode, mockCall} from './mock.js';

const COLS = 80;
const BODY_ROWS = 21;      // 24 - 顶栏 - 底栏 - 帮助栏
const LIST_ROWS = 17;      // 列表屏每页条目数
// 阅读区内正文与附件是同一个可滚屏文档（#reader），滚动用 DOM 的 scrollTop。
const READER_VISIBLE = BODY_ROWS - 1;  // 一屏显示 20 行正文

// ------------------------------------------------------------------ 工具
// 字符宽度必须跟着「当前实际用的字体」走，不能写死编码表：宋体下 ◆ █ ▲ 是
// 全角（2 格），Maple Mono 这类字体里 █ 只有半角（1 格），而 ▲ ◆ ★ 干脆没
// 有字形、只能回退到中文字体、落在一个「1.67 格」的宽度上——按哪种写死都会错位。
//
// 规则：
//   1. ASCII 一律 1 格；
//   2. 制表/方块/几何符号（U+2500–U+27BF，菜单与进站画面的美术图）固定 2 格，
//      并用 .art2 指定的字体按 2 倍字号渲染，保证笔画宽度真的占满两格；
//   3. 其余字符实测前进宽度：≈2 格（汉字、【】等）保持本体字体渲染，
//      明显超出 1 格又不到 2 格的（回退字形的 1.67 格）也归到 .art2 两格，
//      否则逐字累积的 0.33 格误差会把整行推歪。
const ART_RANGE = ch => { const c = ch.charCodeAt(0); return c >= 0x2500 && c <= 0x27bf; };
const ONE = {w: 1, art: false}, TWO = {w: 2, art: false}, TWO_ART = {w: 2, art: true};
const glyphCache = new Map();
let _mctx = null, _halfW = 0;
function initMeasure() {
    const cs = getComputedStyle(document.documentElement);
    const fs = cs.getPropertyValue('--fs').trim() || '22px';
    const fam = cs.getPropertyValue('--font').trim() || 'monospace';
    _mctx = document.createElement('canvas').getContext('2d');
    _mctx.font = fs + ' ' + fam;
    _halfW = _mctx.measureText(' ').width || 0;
    glyphCache.clear();
}
// {w: 1|2, art: 是否用 .art2 字体（中文/全角等宽字体）渲染}
function glyphInfo(ch) {
    if (ch.charCodeAt(0) < 0x80) return ONE;
    let g = glyphCache.get(ch);
    if (g) return g;
    if (!_mctx) initMeasure();
    if (ART_RANGE(ch)) {
        g = TWO_ART;
    } else {
        const adv = _mctx ? _mctx.measureText(ch).width : _halfW * 2;
        const r = _halfW > 0 ? adv / _halfW : 2;
        if (r > 1.45 && r < 1.85) g = TWO_ART;      // 回退字形，宽度对不上整格
        else g = r > 1.45 ? TWO : ONE;
    }
    glyphCache.set(ch, g);
    return g;
}
const charW = ch => glyphInfo(ch).w;
const dw = s => { let n = 0; for (const ch of String(s)) n += charW(ch); return n; };
const esc = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
// 把一段文字按「是否需要用美术字体」切成若干 span，连续同类字符合并，避免逐字一个节点。
function textSpans(text, cls) {
    let out = '', buf = '', art = null;
    const flush = () => {
        if (!buf) return;
        out += `<span class="${cls}${art ? ' art2' : ''}">${esc(buf)}</span>`;
        buf = '';
    };
    for (const ch of text) {
        const a = glyphInfo(ch).art;
        if (art !== null && a !== art) flush();
        art = a;
        buf += ch;
    }
    flush();
    return out;
}

class Line {
    constructor() { this.parts = []; this.w = 0; this.cls = ''; this.rowIndex = -1; }
    add(text, cls = 'w') {
        text = String(text);
        this.parts.push({text, cls});
        this.w += dw(text);
        return this;
    }
    at(col, text, cls = 'w') {
        if (col > this.w) this.add(' '.repeat(col - this.w));
        return this.add(text, cls);
    }
    fill(width = COLS, cls = '') {
        if (this.w < width) this.add(' '.repeat(width - this.w), cls);
        return this;
    }
    bg(cls) { this.cls = cls; return this; }
    row(i) { this.rowIndex = i; return this; }
    html() {
        const body = this.parts.map(p => textSpans(p.text, p.cls || 'w')).join('');
        const attr = this.rowIndex >= 0 ? ` data-row="${this.rowIndex}"` : '';
        return `<div class="row ${this.cls}"${attr}>${body || ' '}</div>`;
    }
}
const L = () => new Line();
const emptyLine = () => L().add('');

function cut(str, n) {
    str = String(str == null ? '' : str).replace(/\r/g, '');
    let out = '', w = 0;
    for (const ch of str) {
        const cw = charW(ch);
        if (w + cw > n) break;
        out += ch; w += cw;
    }
    return out;
}
const MON = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
function clockText() {
    const d = new Date();
    return `${MON[d.getMonth()]} ${String(d.getDate()).padStart(2, ' ')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}
function dur(sec) {
    sec = Math.max(0, sec | 0);
    return `${String(Math.floor(sec / 60)).padStart(2, ' ')}:${String(sec % 60).padStart(2, '0')}`;
}
function errMsg(e) {
    const s = String((e && (e.message || e)) || e || '未知错误');
    return s.replace(/^Error:\s*/, '');
}

// ------------------------------------------------------------------ 状态
const S = {
    screen: 'splash', stack: [],
    sel: 0, top: 0, page: 1, pages: 1,
    board: '', section: '', box: 'inbox',
    items: [], header: '', title: '', boardInfo: null,
    // 正文：posts 是已经加载的整篇主题（跨 wap/web 分页累积），
    // curPostIdx 是滚动视口当前所在帖子，postPage/postPages 只表示分页进度。
    posts: [], postIndex: 0, postLine: 0, postPage: 1, postPages: 1, threadID: 0, threadCount: 0,
    postTotal: 0, curPostIdx: 0, loadedPages: [], pageLoading: false,
    // 版面列表：items 跨页累积（上旧下新），滚动接近底部自动续载下一页。
    listLoadedPages: [], listLoading: false, listScrollTop: 0, listMouseSel: -1,
    subject: '', resume: 0, mail: null,
    // 信件处理菜单：N=只看未读，R=全部会话；unreadOnly 只影响会话列表过滤。
    unreadOnly: false,
    reminders: {mail: 0, at: 0, reply: 0, like: 0}, notifyKind: 2,
    config: {}, status: {}, version: {},
    prompt: null, editor: null, confirm: null,
    // 正文附件预览面板
    attachOpen: false, attachIndex: 0, attachPreview: null, attachLoading: false,
    attachCache: {}, attachPending: new Set(),
    // 放大查看器：缩放比例与平移偏移
    zoom: {scale: 1, x: 0, y: 0, fit: 1, naturalW: 0, naturalH: 0},
    message: '', toastUntil: 0, searchKeyword: '',
    // term 进站登录画面状态：只记代号，绝不保存密码。
    login: {
        field: 'id', id: '', pass: '', big5: false,
        mode: 'auto', captcha: '',
        error: '', hint: '', busy: false,
        savedUser: '', hasSession: false, autoLogin: true, remember: true,
        online: 69878, maxOnline: 406798, guests: 64029,
    },
};
function snapshot() {
    return JSON.parse(JSON.stringify({
        screen: S.screen, sel: S.sel, top: S.top, page: S.page, pages: S.pages,
        board: S.board, section: S.section, box: S.box,
        items: S.items, header: S.header, title: S.title, boardInfo: S.boardInfo,
        posts: S.posts, postIndex: S.postIndex, postLine: S.postLine, postPage: S.postPage,
        postPages: S.postPages, postTotal: S.postTotal, curPostIdx: S.curPostIdx,
        loadedPages: S.loadedPages, threadID: S.threadID, threadCount: S.threadCount,
        listLoadedPages: S.listLoadedPages, listScrollTop: elThreadList.scrollTop,
        listMouseSel: S.listMouseSel,
        subject: S.subject, resume: S.resume, mail: S.mail,
    }));
}
function restore(s) { Object.assign(S, s); }

// ------------------------------------------------------------------ DOM
const $ = id => document.getElementById(id);
const elScreen = $('screen'), elTop = $('topline'), elBottom = $('bottomline'), elHelp = $('helpbar');
const elPrompt = $('prompt'), elPromptLabel = $('prompt-label'), elPromptInput = $('prompt-input');
const elEditor = $('editor'), elEditorHead = $('editor-head'), elEditorBody = $('editor-body');
const elToast = $('toast'), elWinBtns = $('winbtns');
const elAttach = $('attach-view'), elAttachHead = $('attach-head'),
      elAttachPrev = $('attach-prev'), elAttachFoot = $('attach-foot');
const elReader = $('reader');
elReader.addEventListener('scroll', updateReaderScrollState, {passive: true});
// 滚轮由阅读页自己接管：楼内先滚，到边界再整楼顶上/退回，避免浏览器只滚半屏。
elReader.addEventListener('wheel', onReaderWheel, {passive: false});
const elThreadList = $('threadlist');
elThreadList.addEventListener('scroll', updateThreadListScrollState, {passive: true});

// ------------------------------------------------------------------ 无边框窗口
// Frameless 后系统标题栏消失，最小化/最大化/关闭由右上角浮动按钮调用
// Wails runtime 的窗口 API 完成；普通浏览器预览时只给提示，不触发原生调用。
function winReserveCols() {
    // 按钮是 fixed 定位，而字符网格按窗口比例缩放居中；只有两者矩形真正
    // 相交时才需要把顶栏右侧状态文字往左让，否则保持 term 原有的满行排版。
    const b = elWinBtns.getBoundingClientRect();
    const t = elTop.getBoundingClientRect();
    if (!b.width || !t.height) return 0;
    if (b.bottom <= t.top || b.top >= t.bottom || b.left >= t.right || b.right <= t.left) return 0;
    const scale = Number.parseFloat(
        getComputedStyle(document.documentElement).getPropertyValue('--scale')) || 1;
    const overlap = t.right - b.left + 6; // 再留一点余量
    return Math.min(16, Math.max(2, Math.ceil(overlap / (11 * scale))));
}
function setMaximised(on) {
    const changed = elWinBtns.classList.contains('maximised') !== !!on;
    elWinBtns.classList.toggle('maximised', !!on);
    $('win-max').title = on ? '还原' : '最大化';
    if (changed) render();
}
async function syncMaximised() {
    if (browserMode()) return;
    try {
        // runtime 可能返回 boolean、JSON 对象或纯文本 "true"/"false"，
        // 不能简单 !!r（!!"false" 也是 true）。
        const r = await WailsWindow.IsMaximised();
        let on;
        if (typeof r === 'string') on = /^(true|1)$/i.test(r.trim());
        else if (r && typeof r === 'object') on = !!(r.value ?? r.maximised ?? r.data);
        else on = !!r;
        setMaximised(on);
    } catch (e) { /* runtime 尚未就绪时忽略 */ }
}
async function winAction(action) {
    if (browserMode()) {
        toast('预览模式：窗口按钮需运行 sfjth.exe 才生效');
        return;
    }
    try {
        if (action === 'min') await WailsWindow.Minimise();
        else if (action === 'max') await WailsWindow.ToggleMaximise();
        else if (action === 'close') await WailsWindow.Close();
        await syncMaximised();
    } catch (e) { toast('窗口操作失败：' + errMsg(e)); }
}
// 点完立刻失焦：否则按钮保持焦点时，term 的 Enter / 空格会被原生 button
// 当成「再次点击」，可能误触发最小化或关闭。
[['win-min', 'min'], ['win-max', 'max'], ['win-close', 'close']].forEach(([id, act]) => {
    $(id).addEventListener('click', e => { e.currentTarget.blur(); winAction(act); });
});
// 双击顶栏最大化/还原，贴近原生标题栏习惯（Windows 上 runtime 不代劳）。
elTop.addEventListener('dblclick', () => winAction('max'));
// 进站登录屏隐藏了状态栏，整屏都是拖动区，空白处双击同样切换最大化。
elScreen.addEventListener('dblclick', () => { if (S.screen === 'login') winAction('max'); });

// ------------------------------------------------------------------ 字体
// term 网格成立的前提是「一个汉字占两格」，所以默认字体不能随便挑：
// 逐个候选实测半角宽度、空格宽度、汉字宽度，只接受严格等宽、且汉字宽度
// 正好是半角两倍的字体（Maple Mono SC NF 这类带等宽中文的字体才合格；
// Cascadia/Consolas 没有汉字，回退到宋体后比例不对，会被自动跳过）。
// 选定后反算字号，让一个半角字符的宽度正好等于 --cw。
const FONT_STACKS = [
    '"Maple Mono SC NF"', '"Maple Mono NF"', '"Cascadia Code NF"', '"Cascadia Mono"',
    '"Sarasa Mono SC"', '"Noto Sans Mono CJK SC"', '"NSimSun"', '"MS Gothic"',
];
function applyFont() {
    const root = document.documentElement;
    const ctx = document.createElement('canvas').getContext('2d');
    const cw = parseFloat(getComputedStyle(root).getPropertyValue('--cw')) || 11;
    let picked = '';
    for (const fam of FONT_STACKS) {
        ctx.font = '100px ' + fam;
        const half = ctx.measureText('M').width;
        if (!(half > 0)) continue;
        if (Math.abs(ctx.measureText(' ').width - half) > 0.05) continue;      // 必须等宽
        if (Math.abs(ctx.measureText('中').width - half * 2) > 0.5) continue;  // 汉字必须两格
        root.style.setProperty('--font', fam);
        // 半角宽度 = 字号 × half/100，反推字号使半角正好是 --cw。
        root.style.setProperty('--fs', (cw * 100 / half).toFixed(2) + 'px');
        picked = fam;
        break;
    }
    if (!picked) {
        // 没有合格的 Mono 字体就退回新宋体（同样是 1:2 网格）。
        root.style.setProperty('--font', '"NSimSun", "SimSun", "宋体", monospace');
        root.style.setProperty('--fs', (cw * 2) + 'px');
    }
    _mctx = null;   // 字体变了，字符宽度缓存重新量
    return picked;
}

function fit() {
    const cs = getComputedStyle(document.documentElement);
    const cw = parseFloat(cs.getPropertyValue('--cw')) || 11;
    const lh = parseFloat(cs.getPropertyValue('--lh')) || 22;
    const w = COLS * cw, h = 24 * lh;
    const scale = Math.min(window.innerWidth / w, window.innerHeight / h, 2.2);
    document.documentElement.style.setProperty('--scale', String(scale.toFixed(4)));
    $('viewport').style.width = (w * scale) + 'px';
    $('viewport').style.height = (h * scale) + 'px';
}
// 缩放变化后先重算网格尺寸，再重画顶栏（浮动按钮避让列依赖新的矩形），
// 最后同步最大化按钮图标。
window.addEventListener('resize', () => {
    fit(); render(); syncMaximised();
    if (S.attachOpen && zoomImg && zoomImg.complete && zoomImg.naturalWidth) fitZoomImage(zoomImg);
});

function draw(lines, top, bottom, help) {
    // 进站登录画面按 term 原样占满 24 行，隐藏顶栏/底栏/帮助栏。
    const full = S.screen === 'login';
    const rows = full ? 24 : BODY_ROWS;
    $('term').classList.toggle('login-mode', full);
    if (full) {
        elTop.innerHTML = ''; elBottom.innerHTML = ''; elHelp.innerHTML = '';
    } else {
        elTop.innerHTML = top.fill(COLS, 'W').html();
        elBottom.innerHTML = bottom.fill(COLS, 'W').html();
        elHelp.innerHTML = help.fill(COLS, 'W').html();
    }
    elScreen.innerHTML = lines.slice(0, rows).map(l => l.fill(COLS).html()).join('') +
        Array.from({length: Math.max(0, rows - lines.length)}, () => emptyLine().fill(COLS).html()).join('');
}
function bar(left, center, right) {
    const l = L().bg('bar');
    l.add(' ' + left, 'W');
    l.at(Math.max(0, Math.floor((COLS - dw(center)) / 2)), center, 'W');
    // 右上角浮动窗口按钮不占网格，把状态文字按当前缩放往左让开，避免被遮住。
    l.at(Math.max(0, COLS - dw(right) - 1 - winReserveCols()), right, 'W');
    return l;
}
function bottomBar() {
    const user = (S.status.user || 'guest');
    // term 原版在新提醒时把 R / @ / L 放在底栏使用者后面，既是提示也是快捷键。
    const marks = [];
    if (S.reminders.reply > 0) marks.push('R');
    if (S.reminders.at > 0) marks.push('@');
    if (S.reminders.like > 0) marks.push('L');
    const left = `时间[${clockText()}] 呼叫器[好友:Y 一般:Y] 使用者[${user}] [${marks.join('')}]`;
    const stay = `停留[${dur(S.status.uptime || 0)}]`;
    const l = L().bg('bar');
    l.add(' ' + left, 'W');
    l.at(Math.max(0, COLS - dw(stay) - 1), stay, 'W');
    return l;
}
function helpBar(text) { return L().bg('help').add(' ' + text, 'W'); }
// 二次确认的提问行：占据蓝色底栏那一行，黄字提问，底栏常规信息暂时让位。
function confirmBar() {
    const l = L().bg('bar');
    l.at(2, S.confirm.text || '', 'Y');
    l.at(Math.max(0, COLS - 22), ' 按 Y 确认 / N 取消', 'W');
    return l;
}
function toast(msg) {
    S.message = '※ ' + msg;
    S.toastUntil = Date.now() + 4200;
    elToast.textContent = '※ ' + msg;
    elToast.classList.remove('hidden');
    setTimeout(() => { if (Date.now() >= S.toastUntil) elToast.classList.add('hidden'); }, 4300);
    render();
}

// ------------------------------------------------------------------ API
// 生产构建会压缩函数名，所以显式建立「绑定函数 -> 方法名」映射，
// 供浏览器预览回退分发使用。
const METHOD_NAMES = new Map([
    [BbsService.GetConfig, 'GetConfig'], [BbsService.SetConfig, 'SetConfig'],
    [BbsService.SetSource, 'SetSource'], [BbsService.SetCookie, 'SetCookie'],
    [BbsService.Login, 'Login'], [BbsService.LoginWith, 'LoginWith'], [BbsService.Logout, 'Logout'],
    [BbsService.SessionInfo, 'SessionInfo'], [BbsService.GuestLogin, 'GuestLogin'],
    [BbsService.CheckSession, 'CheckSession'],
    [BbsService.OpenLoginPopup, 'OpenLoginPopup'], [BbsService.LoginPopupState, 'LoginPopupState'],
    [BbsService.DataDir, 'DataDir'], [BbsService.Status, 'Status'],
    [BbsService.Version, 'Version'], [BbsService.Sections, 'Sections'],
    [BbsService.Boards, 'Boards'], [BbsService.Favorites, 'Favorites'],
    [BbsService.SearchBoards, 'SearchBoards'],
    [BbsService.ToggleFavorite, 'ToggleFavorite'], [BbsService.TopTen, 'TopTen'],
    [BbsService.Threads, 'Threads'], [BbsService.Thread, 'Thread'],
    [BbsService.AttachmentPreview, 'AttachmentPreview'], [BbsService.OpenAttachment, 'OpenAttachment'],
    [BbsService.MailBox, 'MailBox'], [BbsService.Search, 'Search'],
    [BbsService.Reminders, 'Reminders'], [BbsService.WapConversations, 'WapConversations'],
    [BbsService.WapMessages, 'WapMessages'], [BbsService.WapNotifies, 'WapNotifies'],
    [BbsService.WapMarkNotifyRead, 'WapMarkNotifyRead'],
    [BbsService.OpenNotifyTarget, 'OpenNotifyTarget'],
    [BbsService.WapMarkNotifyReadAll, 'WapMarkNotifyReadAll'],
    [BbsService.WapMarkMessageRead, 'WapMarkMessageRead'],
    [BbsService.WapSendMessage, 'WapSendMessage'],
    [BbsService.PostArticle, 'PostArticle'], [BbsService.ReplyArticle, 'ReplyArticle'],
    [BbsService.SendMail, 'SendMail'],
    [BbsService.OpenPostPopup, 'OpenPostPopup'], [BbsService.PostPopupState, 'PostPopupState'],
    [BbsService.PickFiles, 'PickFiles'], [BbsService.UploadPostAttachment, 'UploadPostAttachment'],
    [BbsService.ClearPostAttachments, 'ClearPostAttachments'],
    [BbsService.MarkRead, 'MarkRead'], [BbsService.Prefetch, 'Prefetch'],
    [BbsService.OpenOAuth, 'OpenOAuth'], [BbsService.OpenExternal, 'OpenExternal'],
    [BbsService.OAuthURL, 'OAuthURL'], [BbsService.OAuthCode, 'OAuthCode'],
    [BbsService.APICall, 'APICall'],
]);
async function jcall(fn, ...args) {
    // 在普通浏览器里打开 dist/index.html 时，用本地演示数据驱动（便于预览/排版校验）。
    if (browserMode()) return mockCall(METHOD_NAMES.get(fn) || fn.name, args);
    const raw = await fn(...args);
    if (raw === undefined || raw === null || raw === '') return null;
    if (typeof raw === 'string') { try { return JSON.parse(raw); } catch { return raw; } }
    return raw;
}
async function safe(promise, onOk) {
    try { const d = await promise; if (onOk) await onOk(d); }
    catch (e) { toast(errMsg(e)); }
}

// 网页登录弹窗完成后由 Go 侧发事件通知；主窗口据此进入主选单或回显失败。
Events.On('sfjth:login-success', async ev => {
    const li = S.login;
    li.busy = false; li.error = ''; li.pass = '';
    li.hint = ' 登录成功，正在进入 ...';
    render();
    await refreshConfig();
    await showMain(false);
    toast((ev && ev.data && ev.data.message) || '登录成功');
});
Events.On('sfjth:login-failed', ev => {
    const li = S.login;
    li.busy = false; li.pass = ''; li.field = 'password';
    li.error = '※ ' + cut((ev && ev.data && ev.data.message) || '网页登录未完成', 78);
    li.hint = ' 可重新输入后打开登录窗，或按 F5 导入 Cookie';
    render();
});
Events.On('sfjth:login-cancelled', () => {
    const li = S.login;
    li.busy = false; li.pass = ''; li.field = 'password';
    li.error = '';
    li.hint = ' 已关闭登录窗，可重新按 Enter 打开';
    render();
});

// 发帖/回帖弹窗：成功后关闭编辑器、提示并回到列表/正文刷新。
let postHandled = false;
async function onPostSuccess(ev) {
    if (postHandled) return;
    postHandled = true;
    const msg = (ev && ev.data && ev.data.message) || '发表成功';
    if (S.editor) closeEditor();
    toast(msg);
    // 站点缓存默认 3 分钟，不失效的话前端「刷新」仍会看到发表前的旧内容。
    try { await jcall(BbsService.InvalidatePostCache, S.board, S.threadID || 0); } catch (e) { /* 忽略 */ }
    // 回帖/发帖后重新载入当前页，确保新文章/回复可见。
    try {
        if (S.screen === 'post') {
            await loadThread(S.board, S.threadID, 1, true, 'replace');
            await warmupPages();
        } else if (S.screen === 'threads' || S.screen === 'search') {
            await showThreads(S.board, 1);
        }
    } catch (e) { /* 刷新失败不影响成功提示 */ }
}
Events.On('sfjth:post-success', onPostSuccess);
Events.On('sfjth:post-cancelled', () => { toast('已取消发表'); });

// 应用事件在部分 WebView2 环境里可能丢失，这里再走 binding 轮询兜底。
// Go 侧保存登录结果到 LoginPopupState，前端按 generation 认领本次弹窗。
async function pollLoginPopup(gen) {
    const deadline = Date.now() + 180000; // 给用户足够时间完成极验
    while (Date.now() < deadline) {
        await new Promise(r => setTimeout(r, 600));
        if (S.screen !== 'login') return; // 事件通道已经处理过了
        let st;
        try { st = await jcall(BbsService.LoginPopupState); } catch (e) { continue; }
        if (!st || st.generation !== gen) continue;
        const li = S.login;
        if (st.status === 'success') {
            li.busy = false; li.error = ''; li.pass = '';
            li.hint = ' 登录成功，正在进入 ...';
            render();
            await refreshConfig();
            await showMain(false);
            toast(st.message || '登录成功');
            return;
        }
        if (st.status === 'failed') {
            li.busy = false; li.pass = ''; li.field = 'password';
            li.error = '※ ' + cut(st.message || '网页登录未完成', 78);
            li.hint = ' 可重新输入后打开登录窗，或按 F5 导入 Cookie';
            render();
            return;
        }
        if (st.status === 'cancelled') {
            li.busy = false; li.pass = ''; li.field = 'password';
            li.error = ''; li.hint = ' 已关闭登录窗，可重新按 Enter 打开';
            render();
            return;
        }
    }
}

// 发帖弹窗轮询兜底：事件通道在部分 WebView2 环境可能丢失。
async function pollPostPopup() {
    const deadline = Date.now() + 180000;
    let seen = false;
    while (Date.now() < deadline) {
        await new Promise(r => setTimeout(r, 700));
        let st;
        try { st = await jcall(BbsService.PostPopupState); } catch (e) { continue; }
        if (!st || st.status === 'open') continue;
        if (seen) break;
        seen = true;
        if (st.status === 'success') await onPostSuccess({data: {message: st.message}});
        else if (st.status === 'cancelled') toast('已取消发表');
        break;
    }
}

// ------------------------------------------------------------------ 导航
function push() { S.stack.push(snapshot()); if (S.stack.length > 40) S.stack.shift(); }
async function back() {
    if (S.attachOpen) { closeAttachView(); return; }
    if (S.prompt) { closePrompt(); return; }
    if (S.editor) { closeEditor(); return; }
    if (!S.stack.length) { if (S.screen !== 'main') await showMain(false); else askLeave(); return; }
    restore(S.stack.pop());
    render();
}
// 「离开水木」不是关窗口，而是下线回进站登录屏，所以要先二次确认：
// 确认后清掉会话（Cookie/token）、清空导航栈与缓存，再走进站画面。
function askLeave() {
    if (S.confirm) return;
    S.confirm = {text: ' 您确定要离开水木吗？', onYes: doLeave};
    render();
}
async function doLeave() {
    S.screen = 'bye';
    render();
    try { await jcall(BbsService.Logout); } catch (e) { /* 离线也照样回登录屏 */ }
    S.stack = [];
    S.items = []; S.posts = []; S.mail = null;
    S.reminders = {mail: 0, at: 0, reply: 0, like: 0};
    await new Promise(r => setTimeout(r, 500));
    await showLogin();
    // 登录屏用底部提示行报信，不弹 toast：toast 会正好压住「请输入密码 / 按 RETURN」两行。
    S.login.hint = ' 已离开水木，可重新进站。' + (S.login.hint || '').trim();
    render();
}
// 主菜单方向键移动：环形跳过 disabled 项。
function menuMove(delta) {
    const n = MAIN_MENU.length;
    let idx = S.sel;
    for (let step = 0; step < n; step++) {
        idx = (idx + delta + n) % n;
        if (isMenuEnabled(MAIN_MENU[idx])) { S.sel = idx; break; }
    }
    render();
}
function move(delta) {
    if (S.screen === 'mail') { mailMenuMove(delta); return; }
    if (!S.items.length) return;
    if (S.screen === 'main') { menuMove(delta); return; }
    if (S.screen === 'threads' || S.screen === 'search') { scrollThreadList(delta, true); return; }
    S.sel = Math.max(0, Math.min(S.items.length - 1, S.sel + delta));
    if (S.sel < S.top) S.top = S.sel;
    if (S.sel >= S.top + LIST_ROWS) S.top = S.sel - LIST_ROWS + 1;
    render();
    maybePrefetch();
}
function pageMove(delta) {
    const p = Math.max(1, Math.min(S.pages || 1, (S.page || 1) + delta));
    if (p === S.page) return;
    S.page = p; S.sel = 0; S.top = 0;
    // threads / post 都用连续滚动，不走整屏翻页。
    if (S.screen === 'threads' || S.screen === 'post') return;
    if (S.screen === 'inbox') {
        jcall(BbsService.MailBox, 'inbox', p).then(d => {
            const all = (d && d.items) || [];
            S.items = S.unreadOnly ? all.filter(m => m.unread) : all;
            render();
        }).catch(e => toast(errMsg(e)));
        return;
    }
    if (S.screen === 'notify') {
        jcall(BbsService.WapNotifies, S.notifyKind, p - 1).then(d => { S.items = (d && d.items) || []; render(); }).catch(e => toast(errMsg(e)));
        return;
    }
    render();
}
function maybePrefetch() {
    if (S.screen !== 'threads' || S.config.source === 'demo' || !S.config.preload) return;
    const ids = S.items.slice(Math.max(0, S.sel - 1), S.sel + 4).map(i => i.id).filter(Boolean);
    BbsService.Prefetch(S.board, JSON.stringify(ids)).catch(() => {});
}

// ------------------------------------------------------------------ 各屏加载
async function showMain(pushIt = true) {
    if (pushIt && S.screen !== 'splash' && S.screen !== 'main') push();
    S.screen = 'main'; S.sel = 2; S.items = MAIN_MENU; S.pages = 1; S.page = 1; S.top = 0;
    render();
}

// ------------------------------------------------------------------ 进站登录
// 仿 term 进站画面：全屏 24 行、彩色 ASCII 占位、底部「代号 / 密码 / RETURN」。
// 流程借鉴 smthflow：记住代号、会话复用免密、失败就地提示不跳屏、guest 直通。
async function showLogin() {
    const li = S.login;
    S.screen = 'login'; S.stack = [];
    li.field = 'id'; li.pass = ''; li.error = ''; li.hint = ''; li.busy = false;
    let info = null;
    try { info = await jcall(BbsService.SessionInfo); } catch (e) { /* 首次启动可忽略 */ }
    if (info) {
        li.online = info.online || 69878;
        li.maxOnline = info.max_online || 406798;
        li.guests = info.guests || 64029;
        li.savedUser = info.last_user_id || info.user || '';
        li.hasSession = !!info.has_session;
        li.autoLogin = info.auto_login !== false;
        li.remember = info.remember_id !== false;
        li.big5 = !!info.big5;
        li.mode = ['auto', 'web', 'wap'].includes(info.login_mode) ? info.login_mode : 'auto';
        if (li.remember && li.savedUser && li.savedUser !== 'guest') {
            li.id = li.savedUser;
            li.hint = (li.autoLogin && li.hasSession)
                ? `已保存 ${li.savedUser} 的会话，按 Enter 直接继续；按 Del 重新登录`
                : `上次代号：${li.savedUser}，按 Enter 后输入密码`;
        } else {
            li.id = ''; li.hint = '';
        }
    }
    render();
}
async function loginEnter() {
    const li = S.login;
    if (li.field === 'id') {
        let id = li.id.trim();
        if (!id) id = 'guest';
        // term 提示里的 BIG5：代号后加 '.'，例如 ax.e.
        li.big5 = id.endsWith('.');
        if (li.big5) id = id.slice(0, -1);
        li.id = id;
        if (id.toLowerCase() === 'guest') return doGuestLogin();
        if (li.hasSession && li.autoLogin && li.savedUser &&
            id.toLowerCase() === li.savedUser.toLowerCase()) {
            return doReuseSession();
        }
        li.field = 'password'; li.pass = ''; li.error = ''; li.hint = '';
        render();
        return;
    }
    await doLogin();
}
async function doGuestLogin() {
    const li = S.login;
    li.busy = true; li.error = ''; li.hint = ' 正在进入 ...'; render();
    try {
        await jcall(BbsService.GuestLogin);
        await jcall(BbsService.SetConfig, JSON.stringify({big5: li.big5}));
        await refreshConfig();
        li.busy = false;
        await showMain(false);
        toast('以 guest 身份试用（离线演示数据），按 U 可重新登录');
    } catch (e) {
        li.busy = false; li.error = '※ ' + errMsg(e); render();
    }
}
function loginModeLabel(mode) {
    return mode === 'web' ? 'WEB' : mode === 'wap' ? 'WAP' : 'WEB+WAP 自动';
}
async function doLogin() {
    const li = S.login;
    if (!li.pass) { li.error = '※ 密码不能为空'; render(); return; }
    li.busy = true; li.error = '';
    li.hint = ' 正在从底部打开水木登录窗 ...';
    render();
    try {
        // 只持久化代号与偏好，密码绝不落盘；账号密码只在内存里交给弹窗预填。
        await jcall(BbsService.SetConfig, JSON.stringify({
            remember_id: li.remember,
            last_user_id: li.remember ? li.id : '',
            big5: li.big5,
            login_mode: li.mode,
        }));
        await refreshConfig();
        const r = await jcall(BbsService.OpenLoginPopup, li.mode, li.id, li.pass);
        li.busy = false; li.pass = ''; li.field = 'password'; li.error = '';
        if (r && r.simulated) {
            await refreshConfig();
            await showMain(false);
            toast((r && r.message) || '浏览器预览：已模拟登录');
            return;
        }
        li.hint = ' 登录窗已打开：账号密码已填好，完成验证码后会自动返回 sfjth';
        render();
        toast((r && r.message) || '已打开登录窗');
        // 事件 + binding 轮询双通道，任一到达即可完成跳屏/回显。
        if (r && r.generation) pollLoginPopup(r.generation);
    } catch (e) {
        li.busy = false; li.pass = ''; li.field = 'password';
        li.error = '※ ' + cut(errMsg(e), 78);
        li.hint = ' Esc 重输 · F5 导 Cookie · o 进设置';
        render();
    }
}
async function doReuseSession() {
    const li = S.login;
    li.busy = true; li.error = ''; li.hint = ' 正在恢复上次会话 ...'; render();
    let r = null;
    try { r = await jcall(BbsService.CheckSession); } catch (e) { r = {valid: false, message: errMsg(e)}; }
    if (r && r.valid) {
        await refreshConfig();
        li.busy = false;
        await showMain(false);
        toast('已恢复 ' + (r.user || li.id) + ' 的登录会话');
    } else {
        li.busy = false; li.hasSession = false; li.field = 'password'; li.pass = '';
        li.error = '※ ' + ((r && r.message) || '会话不可用');
        li.hint = ' 请输入密码重新登录';
        render();
    }
}
async function onLoginKey(e) {
    const li = S.login;
    const k = e.key;
    if (e.ctrlKey) {
        // 只接管 Ctrl+U 清空当前输入，其余组合（如 Ctrl+R）交还浏览器。
        if (k === 'u' || k === 'U') {
            e.preventDefault();
            if (!li.busy) {
                if (li.field === 'id') li.id = ''; else li.pass = '';
                li.error = ''; render();
            }
        }
        return;
    }
    e.preventDefault();
    if (li.busy) return;
    if (k === 'Enter') { await loginEnter(); return; }
    if (k === 'Backspace') {
        if (li.field === 'id') li.id = li.id.slice(0, -1); else li.pass = li.pass.slice(0, -1);
        li.error = ''; render(); return;
    }
    if (k === 'Escape') {
        li.field = 'id'; li.pass = ''; li.error = '';
        li.hint = ' 已清空密码，可重新输入代号'; render(); return;
    }
    if (k === 'Delete') {
        li.hasSession = false; li.field = 'password'; li.pass = ''; li.error = '';
        li.hint = ' 已忽略保存的会话，请输入密码'; render(); return;
    }
    if (k === 'Tab') {
        li.field = li.field === 'id' ? 'password' : 'id'; render(); return;
    }
    if (k === 'F2') {
        // 登录通道：自动（web+wap 依次尝试）→ 仅 web → 仅 wap。
        li.mode = li.mode === 'auto' ? 'web' : li.mode === 'web' ? 'wap' : 'auto';
        li.error = '';
        li.hint = ` 登录通道已切换为 ${loginModeLabel(li.mode)}（F3 可设置验证码令牌）`;
        render(); return;
    }
    if (k === 'F3') {
        openPrompt('验证码令牌 (JSON/键=值/单串，留空清除): ', li.captcha || '', v => {
            li.captcha = v.trim();
            li.error = '';
            li.hint = li.captcha ? ' 已记录验证码令牌，按 Enter 提交登录' : ' 已清除验证码令牌';
            render();
        });
        render(); return;
    }
    if (k === 'F4') {
        li.captcha = ''; li.error = '';
        li.hint = ' 已清除验证码令牌，按 Enter 直接登录';
        render(); return;
    }
    if (k === 'F5') {
        // 站点强制验证码时，浏览器 Cookie 是最省事的免验证码通道。
        openPrompt('粘贴浏览器 Cookie (从 newsmth.net 复制): ', S.config.cookie || '', v => {
            const cookie = v.trim();
            if (!cookie) { li.hint = ' 已取消 Cookie 导入'; render(); return; }
            safe(jcall(BbsService.SetCookie, cookie), async () => {
                await refreshConfig();
                await showLogin();
                li.hint = S.login.hasSession
                    ? ' Cookie 已导入，按 Enter 校验会话并进入'
                    : ' Cookie 已导入，按 Enter 继续';
                render();
            });
        });
        render(); return;
    }
    if (k.length === 1) {
        const key = li.field === 'id' ? 'id' : 'pass';
        if (li[key].length < 40) li[key] += k;
        li.error = ''; render();
    }
}
async function showSections() {
    push(); S.screen = 'sections'; S.sel = 0; S.top = 0; S.title = '分类讨论区';
    await safe(jcall(BbsService.Sections), d => { S.items = d || []; S.pages = 1; render(); });
    render();
}
async function showBoards(section = '', label = '所有版列表') {
    push(); S.screen = 'boards'; S.section = section; S.sel = 0; S.top = 0; S.title = label;
    await safe(jcall(BbsService.Boards, section), d => { S.items = d || []; S.pages = 1; render(); });
    render();
}
async function showFavorites() {
    push(); S.screen = 'favorites'; S.sel = 0; S.top = 0; S.title = '个人定制区（收藏夹）';
    await safe(jcall(BbsService.Favorites), d => { S.items = d || []; S.pages = 1; render(); });
    render();
}
// ---- 选择阅读讨论区（复刻 term：屏幕顶部输入版名，回车直达） ----
// term 下按 S 不是列出版面，而是在正文第一行出现
//   「请输入讨论区名称 [上次版名]: 」并配顶部快捷键说明：
//   # 版名/关键字搜索、@ URL 跳转、SPACE/TAB 自动补全。
const DEFAULT_BOARD = 'SMTHApp_Dev';
function defaultBoard() { return S.board || S.config.default_board || DEFAULT_BOARD; }

function selectBoardBar() {
    return L().bg('bar')
        .add(' 选择讨论区  ', 'W')
        .add('[', 'W')
        .add('#', 'g').add(' - 版面名称/关键字搜索', 'G')
        .add(', ', 'W').add('@', 'g').add(' - URL跳转', 'G')
        .add(', ', 'W').add('SPACE/TAB', 'g').add(' - 自动补全 ', 'G')
        .add(']', 'W');
}
function renderBoardPicker(lines) {
    lines.push(emptyLine());        // 第 1 行留给输入框（#prompt.at-top 覆盖在这里）
    const art = []; mainArt(art);
    lines.push(...art.slice(1));    // 去掉「目前选择」行，避免与输入框重叠
    MAIN_MENU.forEach((m, i) => {
        const l = L().row(i);
        l.at(2, ' ', 'w').at(5, m.key + ')', 'w').at(8, m.label, 'w');
        rightArt(l, i - 2);
        lines.push(l);
    });
}
function openBoardPicker() {
    S.pickerBack = snapshot();
    S.screen = 'selectboard'; S.sel = 0; S.top = 0; S.items = [];
    render();
    openPrompt(`请输入讨论区名称 [${defaultBoard()}]: `, '', boardPickerSubmit,
        false, {kind: 'board', atTop: true});
}
function restorePickerBack() {
    if (S.pickerBack) { restore(S.pickerBack); S.pickerBack = null; }
}
// 从水木各类 URL 中解析版名：nForum #!board/X、m/wap /board/X、bbsdoc.php?board=X，或直接是版名。
function boardFromURL(s) {
    s = String(s || '').trim();
    const m = s.match(/[?&#!/]board[/=]([A-Za-z0-9_.\-]+)/i) ||
              s.match(/\/board\/([A-Za-z0-9_.\-]+)/i) ||
              s.match(/[?&]board=([A-Za-z0-9_.\-]+)/i);
    if (m) return m[1];
    return /^[A-Za-z0-9_.\-]+$/.test(s) ? s : '';
}
// 搜索结果列表（由 # 关键词触发），从服务器真实读取。
async function showBoardSearch(keyword) {
    push();
    S.screen = 'boardsearch'; S.sel = 0; S.top = 0; S.pages = 1;
    S.title = `版名/关键字搜索「${keyword}」`;
    await safe(jcall(BbsService.SearchBoards, keyword), d => { S.items = d || []; render(); });
    render();
}
async function boardPickerSubmit(raw) {
    restorePickerBack();
    const v = String(raw || '').trim();
    if (!v) { await showThreads(defaultBoard(), 1); return; }          // 回车用默认版
    if (v[0] === '#') {
        const kw = v.slice(1).trim();
        if (kw) await showBoardSearch(kw); else openBoardPicker();
        return;
    }
    if (v[0] === '@') {
        const name = boardFromURL(v.slice(1));
        if (name) await showThreads(name, 1);
        else { toast('无法从 URL 解析版名'); openBoardPicker(); }
        return;
    }
    await showThreads(v, 1);                                          // 直接输入版名
}
// SPACE/TAB：用当前输入作为前缀向服务器查询并补全版名。
async function boardAutocomplete() {
    const cur = elPromptInput.value.trim();
    if (!cur || cur[0] === '#' || cur[0] === '@') return;
    let list = [];
    try { list = (await jcall(BbsService.SearchBoards, cur)) || []; }
    catch (e) { toast(errMsg(e)); return; }
    if (!list.length) { toast('没有匹配的讨论区'); return; }
    const lc = cur.toLowerCase();
    const pick = list.find(b => String(b.id || '').toLowerCase() === lc) ||
                 list.find(b => String(b.id || '').toLowerCase().startsWith(lc)) || list[0];
    elPromptInput.value = pick.id || pick.name || cur;
    if (list.length > 1) toast('候选: ' + list.slice(0, 6).map(b => b.id).join(' '));
}
async function showTopTen() {
    push(); S.screen = 'topten'; S.sel = 0; S.top = 0; S.title = '本日十大热门话题';
    await safe(jcall(BbsService.TopTen), d => { S.items = d || []; S.pages = 1; cursorToLatest(); render(); });
    render();
}
// 信件处理菜单：仿 term「处理信件选单」界面。只展示真实可用的五项，
// 其余 term 项按原版保留为淡色不可选，避免误以为已经接通。
async function showMailMenu() {
    push();
    S.screen = 'mail'; S.sel = 0; S.top = 0; S.title = '处理信件选单';
    S.items = [];
    refreshReminders();
    render();
}
function plainText(s) {
    return String(s || '').replace(/\x1b\[[0-9;]*m/g, '')
        .replace(/<br\s*\/?>/gi, '\n').replace(/<[^>]+>/g, '')
        .replace(/&nbsp;/g, ' ').replace(/&lt;/g, '<').replace(/&gt;/g, '>')
        .replace(/&amp;/g, '&').replace(/&quot;/g, '"').trim();
}
const NOTIFY_TITLES = {1: '@我的提醒', 2: '回函提醒', 3: 'Like提醒'};
// 信件走 web nForum 信箱：N=只看未读，R=全部。列表/详情都是站点真实数据。
async function showInbox(page = 0, unreadOnly = false) {
    push(); S.screen = 'inbox'; S.sel = 0; S.top = 0; S.page = page + 1;
    S.unreadOnly = unreadOnly;
    S.title = unreadOnly ? '浏览新信件' : '浏览全部信件';
    await safe(jcall(BbsService.MailBox, 'inbox', page + 1), d => {
        const all = (d && d.items) || [];
        S.items = unreadOnly ? all.filter(m => m.unread) : all;
        S.pages = (d && d.pages) || 1;
    });
    render();
}
async function showNotifies(kind, page = 0) {
    push(); S.screen = 'notify'; S.notifyKind = kind; S.sel = 0; S.top = 0; S.page = page + 1;
    S.title = NOTIFY_TITLES[kind] || '提醒';
    await safe(jcall(BbsService.WapNotifies, kind, page), d => {
        S.items = (d && d.items) || []; S.pages = (d && d.pages) || 1;
    });
    render();
}
async function openInboxItem(item) {
    if (!item || !item.id) return;
    let content = '';
    if (item.box) {
        // web 信箱：详情是 GBK JSON，正文由后端转好。
        try { content = await jcall(BbsService.MailDetail, item.box, Number(item.id)); }
        catch (e) { toast(errMsg(e)); }
        S.mail = {from: item.from || '', title: item.title || '信件', date: item.date, content};
    } else {
        // 兼容旧的 wap 会话结构。
        await safe(jcall(BbsService.WapMarkMessageRead, item.id));
        let d = null;
        try { d = await jcall(BbsService.WapMessages, item.id, 0); } catch (e) { toast(errMsg(e)); }
        const msgs = (d && d.items) || [];
        content = msgs.map(m => `${m.from}  ${m.date || ''}\n${plainText(m.content)}`).join('\n\n' + '─'.repeat(40) + '\n\n');
        S.mail = {from: item.name, title: item.title || '站内短信', date: item.date, content};
    }
    refreshReminders();
    showMailView(S.mail);
}
// @我的 / 回复我的 / Like我的 提醒项本质上都是「版面上的某一篇文章」。
// 仿 wap 的结构：→/Enter 先看提醒详情，详情里可「看原帖」进版面读原文，
// r 直接到该版、跳到那一楼打开回复框。
async function openNotifyThread(item, replyNow = false) {
    if (!item) return;
    let t = null;
    try {
        t = await jcall(BbsService.OpenNotifyTarget, item.board_id || '', item.board || '',
            item.topic_hash || '', item.article_hash || '', item.subject || '');
    } catch (e) { toast('定位文章失败：' + errMsg(e)); return; }
    const board = (t && t.board) || item.board_id;
    const threadID = t ? Number(t.thread_id) : 0;
    if (!board || !threadID) { toast('这条提醒缺少版面或主题信息'); return; }
    const floor = t ? Number(t.floor) || 0 : 0;
    await safe(jcall(BbsService.WapMarkNotifyRead, item.id));
    await showThread(board, threadID, 1);
    if (floor > 1) await gotoFloor(floor, false);
    refreshReminders();
    if (!replyNow) return;
    const idx = floor > 0 ? S.posts.findIndex(p => Number(p.floor) === floor) : -1;
    if (idx < 0) { toast('未能定位到这一楼，请滚动到该楼后按 r'); return; }
    startReply(idx);
}
// 提醒详情：仿 wap 打开提醒的阅读页，右上角是「看原帖」链接。
async function openNotifyDetail(item) {
    if (!item) return;
    await safe(jcall(BbsService.WapMarkNotifyRead, item.id));
    S.mail = {from: item.from, title: item.subject || NOTIFY_TITLES[S.notifyKind],
        date: item.date, notify: item,
        content: `${item.board ? '信区: ' + item.board + '\n' : ''}${plainText(item.body)}`};
    refreshReminders();
    showMailView(S.mail);
}

async function showThreads(board, page = 1, pushIt = true) {
    if (pushIt) push();
    S.screen = 'threads'; S.board = board; S.page = page; S.sel = 0; S.top = 0;
    S.items = []; S.listLoadedPages = []; S.listLoading = false;
    S.listScrollTop = 0; S.listMouseSel = -1;
    refreshReminders();
    await loadThreads(board, page, true, 'replace');
    // 先把紧随其后的两页更早主题接进来，往上滚时基本感觉不到 wap/web 的分页边界。
    warmupThreadPages();
}
// mode: replace（进版/换版）/ prepend（向上加载更早的分页，接到列表顶部）
// term 顺序保持「上旧下新、最新在底部、置顶沉底」不变，连续滚动只改变翻页方式。
async function loadThreads(board, page, setTitle, mode = 'replace') {
    if (mode !== 'replace' && S.listLoading) return;
    if (mode !== 'replace' && S.listLoadedPages.includes(page)) return;
    S.listLoading = true;
    try {
        const d = await jcall(BbsService.Threads, board, page);
        if (!d) return;
        const items = d.items || [];
        if (mode === 'replace') {
            S.items = items; S.listLoadedPages = [d.page || page];
            S.page = d.page || page; S.sel = Math.max(0, items.length - 1);
        } else {
            // page 越大越旧：把更早的一批接到顶部，当前选中项在 DOM 中随之整体下移。
            const known = new Set(S.items.map(t => String(t.id)));
            const fresh = items.filter(t => !known.has(String(t.id)));
            S.items = fresh.concat(S.items);
            S.sel += fresh.length;
            if (!S.listLoadedPages.includes(d.page || page)) S.listLoadedPages.push(d.page || page);
            S.listLoadedPages.sort((a, b) => a - b);
            S.page = Math.max(S.page, d.page || page);
        }
        S.pages = d.pages || S.pages;
        S.boardInfo = d.board || null;
        if (setTitle) S.title = `讨论区 [${board}] ${S.boardInfo ? S.boardInfo.name : ''}`;
        S.header = '  序号  标记  作者          日期    标题                            回/新';
        render();
        updateThreadListScrollState();
        maybePrefetch();
    } catch (e) {
        toast(errMsg(e));
    } finally {
        S.listLoading = false;
    }
}
// 预载更早的若干页；向上滚到顶时也会调用，保证分页无感。
async function warmupThreadPages(maxAhead = 2) {
    if (S.screen !== 'threads' || S.listLoading) return;
    const target = Math.min(S.pages, S.page + maxAhead);
    for (let p = S.page + 1; p <= target; p++) {
        if (S.listLoadedPages.includes(p)) continue;
        await loadThreads(S.board, p, false, 'prepend');
    }
}
async function showThread(board, id, page = 1) {
    push();
    S.screen = 'post'; S.board = board; S.threadID = id;
    S.posts = []; S.loadedPages = []; S.postPage = page; S.postPages = 1; S.postTotal = 0;
    S.curPostIdx = 0; S.postIndex = 0; S.postLine = 0; S.attachIndex = 0;
    const item = S.items.find(x => Number(x.id) === Number(id));
    S.threadCount = item ? (item.count || 1) : 1;
    await loadThread(board, id, page, true);
    // 先把紧随其后的两页接进来，往下滚时基本感觉不到 wap/web 的分页边界。
    warmupPages();
}

// mode: replace（进帖/换帖）/ append（往下续载）/ prepend（向上补页）
async function loadThread(board, id, page, setTitle, mode = 'replace') {
    if (S.attachOpen && mode === 'replace') closeAttachView();
    if (mode !== 'replace' && S.pageLoading) return;
    if (mode !== 'replace' && S.loadedPages.includes(page)) return;
    S.pageLoading = true;
    const oldTop = elReader.scrollTop, oldHeight = elReader.scrollHeight;
    try {
        const d = await jcall(BbsService.Thread, board, id, page);
        if (!d) return;
        const items = d.items || [];
        if (mode === 'replace') {
            S.posts = items; S.loadedPages = [d.page || page];
            S.postPage = d.page || page; S.postTotal = d.total || 0; S.curPostIdx = 0;
        } else {
            const known = new Set(S.posts.map(p => p.floor));
            const fresh = items.filter(p => !known.has(p.floor));
            if (mode === 'append') {
                S.posts = S.posts.concat(fresh);
                S.postPage = Math.max(S.postPage, d.page || page);
            } else {
                S.posts = fresh.concat(S.posts);
            }
            if (!S.loadedPages.includes(d.page || page)) S.loadedPages.push(d.page || page);
            S.loadedPages.sort((a, b) => a - b);
            S.postTotal = d.total || S.postTotal;
        }
        S.postPages = d.pages || S.postPages;
        S.subject = d.subject || S.subject;
        S.resume = d.resume || S.resume;
        S.postIndex = S.curPostIdx;
        if (setTitle && d.board) S.title = `讨论区 [${board}] ${d.board.name || ''}`;
        render();
        if (mode === 'replace') {
            elReader.scrollTop = 0;
            if (S.resume > 1) gotoFloor(S.resume, false);
        } else if (mode === 'prepend') {
            // 新内容插在顶部：把视口跟着下移，用户看到的帖子保持不动。
            elReader.scrollTop = oldTop + (elReader.scrollHeight - oldHeight);
        }
        updateReaderScrollState();
        markRead();
    } catch (e) {
        toast(errMsg(e));
    } finally {
        S.pageLoading = false;
    }
}

// 预载后续若干页；滚动接近底部时也会调用，保证翻页无感。
async function warmupPages(maxAhead = 2) {
    if (S.screen !== 'post' || S.pageLoading) return;
    const target = Math.min(S.postPages, S.postPage + maxAhead);
    for (let p = S.postPage + 1; p <= target; p++) {
        if (S.loadedPages.includes(p)) continue;
        await loadThread(S.board, S.threadID, p, false, 'append');
    }
}

function markRead() {
    const p = S.posts[S.curPostIdx];
    const floor = p ? (p.floor || S.curPostIdx + 1) : 1;
    BbsService.MarkRead(S.board, S.threadID, floor, S.threadCount).catch(() => {});
}
function postBodyLen() {
    const p = S.posts[S.curPostIdx] || S.posts[0];
    return p ? String(p.body || '').split('\n').length : 0;
}
async function showHelp() { push(); S.screen = 'help'; S.title = '操作说明（term 键位）'; render(); }
async function showSettings() { push(); S.screen = 'settings'; S.title = '系统设置 / 数据源'; render(); }
async function showAbout() {
    push(); S.screen = 'about'; S.title = '系统信息及服务';
    await safe(jcall(BbsService.Version), d => { S.version = d || {}; render(); });
    render();
}
async function showSearch(keyword) {
    push(); S.searchKeyword = keyword; S.screen = 'search'; S.sel = 0; S.top = 0;
    S.title = `搜索结果：${keyword}`;
    await safe(jcall(BbsService.Search, keyword, ''), d => { S.items = (d && d.items) || []; S.pages = 1; cursorToLatest(); render(); });
    render();
}
async function showMailView(mail) { push(); S.mail = mail; S.screen = 'mailview'; S.title = '阅读信件'; render(); }

// ------------------------------------------------------------------ 主菜单（复刻截图）
// disabled 的项尚未接入真实功能：淡色显示、方向键跳过、鼠标不可选、开头快捷键无效。
const MAIN_MENU = [
    {key: '0', label: '精华公布栏', act: 'announce'},
    {key: 'X', label: '分类讨论区', act: 'sections'},
    {key: 'F', label: '个人定制区', act: 'favorites'},
    {key: 'E', label: '旧分类讨论区', act: 'sections', disabled: true},
    {key: 'M', label: '信件处理', act: 'mail'},
    {key: 'T', label: '进入聊天选单', act: 'chat', disabled: true},
    {key: 'I', label: '个人工具箱', act: 'settings'},
    {key: 'N', label: '新开启讨论区', act: 'newboards', disabled: true},
    {key: 'S', label: '选择阅读讨论区', act: 'selectboard'},
    {key: 'B', label: '进入所有版列表', act: 'boards'},
    {key: 'C', label: '系统信息及服务', act: 'about'},
    {key: 'K', label: '进入二站', act: 'second', disabled: true},
    {key: 'G', label: '离开水木', act: 'quit'},
];
const isMenuEnabled = m => !!m && !m.disabled;

// 处理信件选单：截图中的 N/R/K/L/B 五项接到 wap 真实数据，其余保留为占位项。
const MAIL_MENU = [
    {key: 'N', label: '浏览新信件', act: 'newmail'},
    {key: 'R', label: '浏览全部信件', act: 'allmail'},
    {key: 'K', label: '@我的文章', act: 'at'},
    {key: 'L', label: '回复我的文章', act: 'reply'},
    {key: 'B', label: 'Like我的文章', act: 'like'},
    {key: 'T', label: '短信', act: '', disabled: true},
    {key: 'W', label: '发送短信', act: '', disabled: true},
    {key: 'S', label: '寄信', act: '', disabled: true},
    {key: 'G', label: '群体信件选单', act: '', disabled: true},
    {key: 'F', label: '寄信给所有好友', act: '', disabled: true},
    {key: 'C', label: '清空备份的邮箱', act: '', disabled: true},
    {key: 'X', label: '设置邮箱选项', act: '', disabled: true},
];
function mailMenuMove(delta) {
    const n = MAIL_MENU.length;
    let idx = S.sel;
    for (let step = 0; step < n; step++) {
        idx = (idx + delta + n) % n;
        if (isMenuEnabled(MAIL_MENU[idx])) { S.sel = idx; break; }
    }
    render();
}
async function mailMenuAction(m) {
    if (!isMenuEnabled(m)) return;
    switch (m.act) {
        case 'newmail': await showInbox(0, true); break;
        case 'allmail': await showInbox(0, false); break;
        case 'at': await showNotifies(1); break;
        case 'reply': await showNotifies(2); break;
        case 'like': await showNotifies(3); break;
        default: break;
    }
}
function mainArt(lines) {
    // 进站画面：黄/棕/蓝/绿 ANSI 彩条 + 字符画（对应截图 Family@newsmth）
    lines.push(L().at(0, '目前选择：', 'w').add('[', 'W').add('FavBoard', 'inv').add(']', 'W').add('  ', 'gray'));
    lines.push(
        L().at(0, '  ', 'yellow').add('▄▄', 'yellow y')
            .add(' Father and mother I love you ! ', 'brown W')
            .add('▄▄', 'yellow y')
            .at(46, ' '.repeat(26), 'blue')
            .at(74, '⌒  ⌒  ⌒', 'y')
    );
    lines.push(L().at(1, '█', 'y').at(46, '   o   o        ⌒     ⌒', 'Y').at(72, '‖', 'g'));
    lines.push(L().at(1, '█', 'y').at(9, '▲', 'y').at(14, '▲▲', 'y').at(21, '▼', 'y')
        .at(29, '▼', 'y').at(46, '    ⌒      （  ）', 'g'));
    lines.push(L().at(1, '█', 'y').at(5, '█', 'y').at(9, '█', 'y').at(14, '▼█', 'y').at(21, 'M', 'red W')
        .at(28, '▲', 'r').at(32, '★', 'y').at(46, '   ___n___     （  ）', 'g'));
    lines.push(L().at(1, '█', 'y').at(9, '█', 'y').at(14, '█', 'y').at(21, '◀', 'y')
        .at(28, '▲', 'r').at(32, '█', 'y').at(46, '  /       \\   つ (  )', 'g'));
    lines.push(L().at(0, '▄', 'y').at(5, '▼', 'y').at(9, '▼', 'y').at(14, '▼', 'y')
        .at(21, '███', 'y').at(28, '██', 'y').at(46, ' ##|田 n|##   ∪ (-)', 'g').at(72, '‖', 'g'));
    lines.push(L().at(1, '◇家─有爱就有责任◇', 'green W')
        .at(28, '─ Family@newsmth ─', 'green W')
        .at(52, ' '.repeat(14), 'blue'));
}
function rightArt(line, row) {
    switch (row) {
        case 0: line.at(46, '水', 'W'); line.at(56, '是', 'W'); line.at(64, '木.', 'W'); break;
        case 1: line.at(46, '也   心   种', 'W'); line.at(66, '∵', 'y'); break;
        case 2: line.at(46, '生   是   情', 'W'); line.at(66, '∴', 'y'); break;
        case 3: line.at(46, '活   一   方  种', 'W'); line.at(66, '∵', 'y'); break;
        case 4: line.at(46, '式', 'W'); line.at(66, '∴', 'g'); break;
        case 5: line.at(46, 'NewSMTH.net', 'W'); line.at(62, '· ··  ··  ·', 'c'); break;
        case 6: line.at(50, '·  ·', 'b'); line.at(62, '· ··  ··  ·', 'g'); break;
        default: break;
    }
}
function renderMain(lines) {
    mainArt(lines); // 8 行
    MAIN_MENU.forEach((m, i) => {
        const l = L().row(i);
        const on = i === S.sel && isMenuEnabled(m);
        const dim = !isMenuEnabled(m);
        // 未实现项：暗灰色，即使游标意外落到上面也不高亮。
        l.at(2, on ? '◆' : ' ', on ? 'W' : 'w');
        l.at(5, m.key + ')', on ? 'Y' : (dim ? 'k' : 'w'));
        l.at(8, m.label, on ? 'W' : (dim ? 'k' : 'w'));
        rightArt(l, i - 2);
        lines.push(l);
    });
    return '主菜单';
}

// term 文章/信列表：进入或翻页后光标停在最后一项（最新文章在最下面），
// 并确保该项在可见区内。sections/boards/favorites 等导航列表不用这个。
function cursorToLatest() {
    const n = S.items.length;
    S.sel = Math.max(0, n - 1);
    S.top = Math.max(0, n - LIST_ROWS);
}

// ------------------------------------------------------------------ 通用列表渲染
function renderList(lines, rowFn) {
    // 所有列表屏的标题都已显示在顶栏，正文第一行不再重复画一条。
    lines.push(L().add(S.header || '  编号  名称                        说明', 'Y'));
    for (let i = 0; i < LIST_ROWS; i++) {
        const idx = S.top + i;
        if (idx >= S.items.length) { lines.push(emptyLine()); continue; }
        const l = L().row(idx);
        if (idx === S.sel) l.cls = 'selected';
        l.add(idx === S.sel ? '◆' : ' ', idx === S.sel ? 'W' : 'w');
        rowFn(l, S.items[idx], idx);
        lines.push(l);
    }
    lines.push(L().add('  ' + '-'.repeat(55), 'k')
        .at(60, `第 ${S.page}/${S.pages || 1} 页  共 ${S.items.length} 项`, 'Y'));
    lines.push(L().add(cut(S.message || ' ', COLS - 2), 'c'));
}
function renderSections(lines) {
    S.header = '  序号  分区名称              说明                        版面数';
    renderList(lines, (l, s, i) => {
        l.at(2, String(i + 1).padStart(3, ' '), 'c');
        l.at(9, cut(s.name, 20), i === S.sel ? 'W' : 'w');
        l.at(32, cut(s.desc || '', 28), 'k');
        l.at(64, String(s.total || '—'), 'y');
    });
}
function renderBoards(lines) {
    S.header = '  编号  英文版名        中文名          版主          在线   今日   文章数';
    renderList(lines, (l, b, i) => {
        l.at(2, String(i + 1).padStart(3, ' '), 'c');
        l.at(8, cut(b.id, 15), b.kind === 'dir' ? 'g' : (i === S.sel ? 'W' : 'w'));
        l.at(24, cut(b.name || '', 14), i === S.sel ? 'W' : 'w');
        l.at(40, cut(b.manager || '诚征版主中', 12), 'k');
        l.at(55, String(b.users ?? '-').padStart(4), 'y');
        l.at(62, String(b.today ?? '-').padStart(4), 'y');
        l.at(70, String(b.articles ?? '-').padStart(7), 'g');
    });
}
// ---- 版面文章列表：跨页累积成一个可滚屏文档（与正文 #reader 同一体验） ----
function threadListDocKey() {
    return [S.board, S.listLoadedPages.join(','), S.items.length].join('|');
}
// 左侧数字只是当前载入列表里的动态位置：最下面（最新）为 1，越往上越大。
// 它不是 term 的文章编号，也不是服务器 ID，加载更早的页时会整体变化。
function threadDisplayNo(idx) {
    return Math.max(1, S.items.length - idx);
}
function buildThreadList() {
    if (!['threads', 'search'].includes(S.screen) || !S.items.length) {
        elThreadList.classList.add('hidden'); return;
    }
    // 从正文/其它屏返回时，render() 会先把列表隐藏；这里必须无条件恢复显示，
    // 否则 key 未变时命中快速分支，屏幕上只剩表头和状态栏（一片空白）。
    const wasHidden = elThreadList.classList.contains('hidden');
    elThreadList.classList.remove('hidden');
    const key = threadListDocKey();
    const board = String(S.board) + '/' + S.screen;
    const same = elThreadList.dataset.board === board;
    const rendered = Number(elThreadList.dataset.rendered || 0);
    if (same && elThreadList.dataset.key === key) {
        if (wasHidden) {
            // display:none 会让 WebView 丢掉 scrollTop，用进帖前保存的位置还原，
            // 再把选中行确保在视口内，光标就停在刚读过的那篇上。
            elThreadList.scrollTop = S.listScrollTop || 0;
            ensureThreadRowVisible(S.sel, false);
            S.listScrollTop = elThreadList.scrollTop;
        }
        highlightThreadRow();
        return;
    }
    const lh = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh')) || 22;
    const oldTop = wasHidden ? (S.listScrollTop || 0) : elThreadList.scrollTop;
    if (same && rendered > 0 && rendered < S.items.length) {
        // 向上续载：新行插到顶部。把视口跟着下移同样的高度，用户看到的条目保持不动。
        const added = S.items.length - rendered;
        const frag = document.createDocumentFragment();
        for (let i = 0; i < added; i++) {
            const node = lineNode(threadRowLine(S.items[i], i));
            node.dataset.row = String(i);
            frag.appendChild(node);
        }
        elThreadList.insertBefore(frag, elThreadList.firstChild);
        // 已有行整体上移：data-row、可见序号、鼠标选中提示都要跟着重排。
        // 注意行内文字是生成时写死的，只改 data-row 会让序号和光标错位。
        elThreadList.querySelectorAll('.row[data-row]').forEach((n, i) => {
            n.dataset.row = String(i);
            const num = n.querySelector('.row-num');
            if (num) num.textContent = String(threadDisplayNo(i)).padStart(3, ' ');
            n.classList.toggle('selected', i === S.listMouseSel);
        });
        elThreadList.scrollTop = oldTop + added * lh;
    } else {
        elThreadList.innerHTML = '';
        const frag = document.createDocumentFragment();
        S.items.forEach((t, i) => {
            const node = lineNode(threadRowLine(t, i));
            node.dataset.row = String(i);
            frag.appendChild(node);
        });
        elThreadList.appendChild(frag);
        if (same) elThreadList.scrollTop = oldTop;
    }
    elThreadList.dataset.board = board;
    elThreadList.dataset.rendered = String(S.items.length);
    elThreadList.dataset.key = key;
    if (!same) {
        // term：最新的主题在最下面，进版光标和视口都停在最底部。
        elThreadList.scrollTop = elThreadList.scrollHeight;
    }
    S.listScrollTop = elThreadList.scrollTop;
    highlightThreadRow();
}
// 只切换光标/鼠标提示，不重建 DOM：滚动时不会打断平滑滚动或鼠标操作。
// 键盘光标是独立元素里的 ◆，随 S.sel 移动；蓝色底色只表示鼠标点击选中，
// 两者彻底分开，避免旧行残留 ◆ 造成“两个光标”。
function highlightThreadRow() {
    if (!['threads', 'search'].includes(S.screen)) return;
    elThreadList.querySelectorAll('.row[data-row]').forEach(n => {
        const idx = Number(n.dataset.row);
        n.classList.toggle('selected', idx === S.listMouseSel);
        const mark = n.querySelector('.cursor-cell');
        if (!mark) return;
        const on = idx === S.sel;
        mark.textContent = on ? '◆' : '  ';
        mark.className = on ? 'W cursor-cell' : 'w cursor-cell';
    });
}
// 根据滚动位置定位当前条目，并在接近底部时自动续载下一页。
let threadListScrollTick = 0;
function updateThreadListScrollState() {
    if (!['threads', 'search'].includes(S.screen) || elThreadList.classList.contains('hidden')) return;
    const lh = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh')) || 22;
    S.listScrollTop = elThreadList.scrollTop;
    // 鼠标滚轮/触摸板滚动时，只在光标滚出视口后才把它拉回可见边缘；
    // 光标本来就在视口内时不抢 S.sel，否则方向键移动会被滚动事件抵消。
    const first = Math.floor(elThreadList.scrollTop / lh);
    const last = Math.max(first, Math.min(S.items.length - 1,
        first + Math.max(1, Math.floor(elThreadList.clientHeight / lh)) - 1));
    let idx = S.sel;
    if (idx < first) idx = first;
    else if (idx > last) idx = last;
    if (idx !== S.sel) { S.sel = idx; highlightThreadRow(); maybePrefetch(); }
    const nearTop = elThreadList.scrollTop <= 160;
    if (nearTop && S.screen === 'threads' && !S.listLoading && S.page < S.pages) {
        loadThreads(S.board, S.page + 1, false, 'prepend').then(warmupThreadPages);
    }
    const now = Date.now();
    if (now - threadListScrollTick > 250) { threadListScrollTick = now; render(); }
}
function threadListProgress() {
    if (!['threads', 'search'].includes(S.screen) || elThreadList.classList.contains('hidden')) return '';
    const max = elThreadList.scrollHeight - elThreadList.clientHeight;
    if (max <= 2) return '一屏显示完';
    return `滚屏 ${Math.round(elThreadList.scrollTop / max * 100)}%`;
}
function threadRowsPerScreen() {
    const lh = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh')) || 22;
    return Math.max(1, Math.floor(elThreadList.clientHeight / lh) - 1);
}
// 方向键移动的是光标本身，不是单纯滚动容器：光标仍在视口内时不滚动，
// 移出顶部/底部时才把那一行顶到边缘，符合 term 的逐行选择习惯。
function moveThreadCursor(delta) {
    if (!['threads', 'search'].includes(S.screen) || elThreadList.classList.contains('hidden')) return false;
    if (!S.items.length) return false;
    const next = Math.max(0, Math.min(S.items.length - 1, S.sel + delta));
    if (next === S.sel) return false;
    S.sel = next;
    const lh = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh')) || 22;
    const node = elThreadList.querySelector(`.row[data-row="${next}"]`);
    if (node) {
        const top = node.offsetTop, bottom = top + lh;
        let dest = elThreadList.scrollTop;
        if (top < dest) dest = top;
        else if (bottom > dest + elThreadList.clientHeight) dest = bottom - elThreadList.clientHeight;
        if (Math.abs(dest - elThreadList.scrollTop) > 1) {
            elThreadList.scrollTo({top: Math.max(0, dest), behavior: 'auto'});
        }
    }
    S.listScrollTop = elThreadList.scrollTop;
    highlightThreadRow(); maybePrefetch(); render();
    return true;
}
// 方向键一次一行，PgDn/空格一次一屏；都走同一个光标移动逻辑。
function scrollThreadList(dir, lineMode = false) {
    return moveThreadCursor(dir * (lineMode ? 1 : threadRowsPerScreen()));
}
function scrollToThreadItem(idx, smooth = true) {
    if (idx < 0 || idx >= S.items.length) return false;
    const node = elThreadList.querySelector(`.row[data-row="${idx}"]`);
    if (!node) return false;
    S.sel = idx; highlightThreadRow();
    elThreadList.scrollTo({top: Math.max(0, node.offsetTop - 8), behavior: smooth ? 'smooth' : 'auto'});
    S.listScrollTop = elThreadList.scrollTop;
    return true;
}
// 返回列表后，若保存的视口位置已失效，至少把光标行拉回可视区，避免“看不到当前文章”。
function ensureThreadRowVisible(idx, smooth = true) {
    const node = elThreadList.querySelector(`.row[data-row="${idx}"]`);
    if (!node) return false;
    const lh = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh')) || 22;
    const top = node.offsetTop, bottom = top + lh;
    let dest = elThreadList.scrollTop;
    if (top < dest + 4) dest = Math.max(0, top - 8);
    else if (bottom > dest + elThreadList.clientHeight - 4) {
        dest = bottom - elThreadList.clientHeight + 8;
    }
    if (Math.abs(dest - elThreadList.scrollTop) < 2) return true;
    elThreadList.scrollTo({top: Math.max(0, dest), behavior: smooth ? 'smooth' : 'auto'});
    S.listScrollTop = elThreadList.scrollTop;
    return true;
}

// 列表行内容：term 网格与可滚容器共用同一套列布局，保证外观完全一致。
function threadRowLine(t, i) {
    const l = L().row(i);
    // 光标单独占一个 span，并预留两个字符宽：◆ 在宋体/新宋体下算双宽，
    // 若初始只放一个空格，换成 ◆ 后整行会右移一格（光标行缩进）。
    // highlightThreadRow() 只改这个 span 的文字和颜色，不碰后面的列。
    l.add('  ', 'w cursor-cell');
    l.at(2, String(threadDisplayNo(i)).padStart(3, ' '), 'c row-num');
    // term：带附件的主题在标记列先显示 @；置顶/精华标记紧跟其后。
    const flag = (t.flag && t.flag !== ' ') ? t.flag : '';
    l.at(8, (t.attach ? '@' : ' ') + flag, t.attach ? 'r' : (t.flag === '◆' ? 'r' : 'Y'));
    l.at(11, cut(t.author || 'guest', 12), 'g');
    l.at(24, cut(t.date || '', 6), 'k');
    const cls = t.read ? (S.config.show_read_gray ? 'k' : 'w') : 'w';
    l.at(32, cut(t.subject || '(无标题)', 34), cls);
    l.at(67, String(t.count || 0).padStart(4), 'y');
    if (t.unread > 0) l.at(73, `[+${t.unread}]`, 'r');
    return l;
}
function renderThreads(lines) {
    S.header = '  序号  标记  作者          日期    标题                            回/新';
    // 表头固定在第 1 行，列表本体交给 #threadlist 平滑滚动，最后一行放状态栏。
    lines.push(L().add(S.header, 'Y'));
    for (let i = 1; i < BODY_ROWS - 1; i++) lines.push(emptyLine());
    buildThreadList();
    lines.push(L().add(` 第 ${threadDisplayNo(S.sel)}/${S.items.length} 条  已载入 ${S.items.length} 篇`, 'y')
        .at(52, threadListProgress(), 'c')
        .at(74, S.pages > S.page ? '↑更早' : '已到顶', 'g'));
}
function renderTopTen(lines) {
    S.header = '  排名  看板          标题                                    作者     篇数';
    renderList(lines, (l, t, i) => {
        l.at(2, String(t.rank || i + 1).padStart(3, ' '), 'Y');
        l.at(8, cut(t.board || t.board_id || '', 12), 'c');
        l.at(22, cut(t.title || '', 40), i === S.sel ? 'W' : 'w');
        l.at(64, cut(t.author || '', 8), 'g');
        l.at(74, String(t.count || 0).padStart(4), 'r');
    });
}
function renderFavorites(lines) { renderBoards(lines); }
function renderMail(lines) {
    lines.push(L().add(' 主选单[←,e]  进入[Enter]  选择[↑,↓]  左右键翻页', 'w'));
    lines.push(L().add('    ').add('-'.repeat(26), 'c').add(' 功能选单 ', 'Y').add('-'.repeat(22), 'c')
        .add(' 自定义邮箱', 'c'));
    lines.push(emptyLine());
    MAIL_MENU.forEach((m, i) => {
        const l = L().row(i);
        const on = i === S.sel && isMenuEnabled(m);
        const dim = !isMenuEnabled(m);
        l.at(3, on ? '◆' : ' ', on ? 'W' : 'w');
        // term 原版：选中项写成 [R] 且字母反白，未选中项写成 N)。
        if (on) {
            l.at(6, '[', 'W').at(7, m.key, 'inv').at(8, ']', 'W');
        } else {
            l.at(6, m.key + ')', dim ? 'k' : 'w');
        }
        l.at(10, m.label, on ? 'W' : (dim ? 'k' : 'w'));
        lines.push(l);
    });
    lines.push(emptyLine());
    lines.push(L().add('  可用功能已接通 wap 真实数据；其余项目原版占位，暂未开放。', 'k'));
}
function renderInbox(lines) {
    S.header = '  编号  状态  发信人        标题                                日期';
    renderList(lines, (l, m, i) => {
        l.at(2, String(i + 1).padStart(3, ' '), 'c');
        // web 信箱的 unread 是布尔；wap 会话是未读条数，两者都能显示。
        const unread = typeof m.unread === 'number' ? m.unread : (m.unread ? 1 : 0);
        l.at(8, unread ? String(Math.min(99, unread)) : ' ', unread ? 'r' : 'k');
        l.at(12, cut(m.from || m.name || '', 12), 'g');
        const text = m.title && m.title !== '站内短信'
            ? m.title : (plainText(m.body).replace(/\n/g, ' ') || '站内短信');
        l.at(26, cut(text, 38), unread ? 'W' : 'k');
        l.at(66, cut(m.date || '', 10), 'k');
    });
}
// 提醒列表：这些条目都是版面上的文章，所以除了发信人/摘要，还标出版面，
// 让「进版面读、按 r 回」有明确落点。
const NOTIFY_HEAD = (() => {
    const h = L().at(2, '编号').at(7, '状态').at(12, '版面')
        .at(24, '发信人').at(37, '标题 / 内容摘要').at(66, '日期');
    return h.parts.map(p => p.text).join('');
})();
function renderNotify(lines) {
    S.header = NOTIFY_HEAD;
    renderList(lines, (l, n, i) => {
        l.at(2, String(i + 1).padStart(3, ' '), 'c');
        l.at(7, n.unread ? 'N' : ' ', n.unread ? 'r' : 'k');
        l.at(12, cut(n.board || n.board_id || '', 10), 'g');
        l.at(24, cut(n.from || '', 12), 'g');
        const text = (n.subject || '') + '  ' + plainText(n.body).replace(/\n/g, ' ');
        l.at(37, cut(text, 28), n.unread ? 'W' : 'k');
        l.at(66, cut(n.date || '', 10), 'k');
    });
}
function renderSearch(lines) { renderThreads(lines); }

// ------------------------------------------------------------------ 文章正文（term 单帖阅读）
function renderPost(lines) {
    const p = S.posts[S.postIndex] || S.posts[0];
    // 顶栏已有「阅读文章」和分页信息；这里保留一行占位，让 #reader 仍从
    // 原来的行开始覆盖，否则正文头会露在滚动区上方，出现两份发信人信息。
    lines.push(emptyLine());
    if (!p) {
        elReader.classList.add('hidden');
        lines.push(L().add('  （暂无内容）', 'r'));
        for (let i = 0; i < BODY_ROWS - 1; i++) lines.push(emptyLine());
        return;
    }
    lines.push(L().add('发信人: ', 'g').add(p.author || 'guest', 'W')
        .add(p.nick ? ` (${p.nick})` : '', 'k').add(',  信区: ', 'g').add(S.board, 'W'));
    lines.push(L().add('标  题: ', 'g').add(cut(p.subject || S.subject || '', COLS - 10), 'Y'));
    lines.push(L().add('发信站: ', 'g').add(cut(p.from || '水木社区', COLS - 10), 'k'));
    lines.push(L().add('-'.repeat(COLS), 'b'));
    // 正文和附件都渲染进 #reader（同一个可滚屏文档）；这里只留状态栏那一行。
    // #reader 覆盖正文前 20 行，状态栏必须落在正文区最后一行才不会被盖住。
    while (lines.length < BODY_ROWS - 1) lines.push(emptyLine());
    buildReader();
    const attachments = currentAttachments();
    const total = postBodyLen();
    const p0 = S.posts[S.curPostIdx] || p;
    const floor = p0 ? (p0.floor || S.curPostIdx + 1) : 1;
    const totalFloor = S.postTotal || S.postPages * 10;
    lines.push(L().add(` 第 ${floor} 楼 / 共 ${totalFloor} 楼`, 'y')
        .at(28, `r 回复 ${floor} 楼`, 'Y')
        .at(46, `正文 ${total} 行`, 'g')
        .at(58, readerProgress(), 'c')
        .at(71, attachments.length ? '[ ]v附件' : '↑↓滚屏', 'y'));
}

// ------------------------------------------------------------------ 正文附件预览
function currentAttachments() {
    const p = S.posts[S.curPostIdx] || S.posts[0];
    return (p && Array.isArray(p.attachments)) ? p.attachments : [];
}
function attachName(a) { return String((a && (a.name || a.id)) || '附件'); }
function attachKind(a) {
    const known = ['image', 'audio', 'video', 'pdf', 'text', 'html', 'doc', 'excel', 'file'];
    if (a && known.includes(a.kind)) return a.kind;
    const name = attachName(a).toLowerCase();
    const type = String((a && a.type) || '').toLowerCase();
    const ext = name.split('.').pop();
    if (type.startsWith('image/') || ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'svg'].includes(ext)) return 'image';
    if (type.startsWith('audio/') || ['mp3', 'wav', 'm4a', 'aac', 'ogg', 'flac'].includes(ext)) return 'audio';
    if (type.startsWith('video/') || ['mp4', 'mkv', 'avi', 'mov', 'webm'].includes(ext)) return 'video';
    if (ext === 'pdf') return 'pdf';
    if (['html', 'htm'].includes(ext)) return 'html';
    if (['doc', 'docx', 'rtf', 'odt', 'wps'].includes(ext)) return 'doc';
    if (['xls', 'xlsx', 'ods', 'et'].includes(ext)) return 'excel';
    if (type.startsWith('text/') || ['txt', 'md', 'log', 'json', 'xml', 'csv', 'go', 'py', 'js', 'ts', 'css'].includes(ext)) return 'text';
    return 'file';
}
const KIND_ICON = {image: '[IMG]', audio: '[WAV]', video: '[VID]', pdf: '[PDF]',
    text: '[TXT]', html: '[HTML]', doc: '[DOC]', excel: '[XLS]', file: '[FILE]'};
function attachSize(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    return (n / 1024 / 1024).toFixed(1) + ' MB';
}
function dataURLToText(url) {
    try {
        const b64 = String(url || '').split(',').slice(1).join(',');
        const bin = atob(b64);
        const bytes = Uint8Array.from(bin, ch => ch.charCodeAt(0));
        return new TextDecoder('utf-8').decode(bytes);
    } catch (e) { return '（附件文本解码失败）'; }
}
function openAttachView(index = 0) {
    const list = currentAttachments();
    if (!list.length) { toast('这篇帖子没有附件'); return; }
    S.attachOpen = true;
    S.attachIndex = Math.max(0, Math.min(index, list.length - 1));
    elAttach.classList.remove('hidden');
    loadAttachPreview(S.curPostIdx, S.attachIndex);
}
function closeAttachView() {
    S.attachOpen = false;
    elAttach.classList.add('hidden');
}
function moveAttach(delta) {
    const list = currentAttachments();
    if (!list.length) return;
    const next = Math.max(0, Math.min(list.length - 1, S.attachIndex + delta));
    if (next === S.attachIndex) return;
    S.attachIndex = next;
    renderAttachPanel();
    loadAttachPreview(S.curPostIdx, S.attachIndex);
}
function updateAttachmentViews(idx, ai) {
    const sec = elReader.querySelector(`.reader-attach[data-post-idx="${idx}"]`);
    if (sec) renderAttachSection(sec, idx);
    if (S.attachOpen && idx === S.curPostIdx && ai === S.attachIndex) renderAttachPanel();
}
// 正文下方每个有附件的楼层都会自动开始预览，不只限当前视口那一层。
function ensureAttachPreview() {
    const list = currentAttachments();
    if (!list.length) return;
    if (S.attachIndex >= list.length) S.attachIndex = 0;
    const key = JSON.stringify(list[S.attachIndex]);
    if (!S.attachCache[key]) loadAttachPreview(S.curPostIdx, S.attachIndex);
    else {
        const sec = elReader.querySelector(`.reader-attach[data-post-idx="${S.curPostIdx}"]`);
        if (sec) renderAttachSection(sec, S.curPostIdx);
    }
}
// 切换当前楼层正文下方预览的附件（[ / ]）。
function switchInlineAttach(delta) {
    const list = currentAttachments();
    if (list.length < 2) return;
    S.attachIndex = Math.max(0, Math.min(list.length - 1, S.attachIndex + delta));
    const sec = elReader.querySelector(`.reader-attach[data-post-idx="${S.curPostIdx}"]`);
    if (sec) {
        renderAttachSection(sec, S.curPostIdx);
        sec.scrollIntoView({behavior: 'smooth', block: 'nearest'});
    }
    ensureAttachPreview();
}
async function loadAttachPreview(idx = S.curPostIdx, ai = S.attachIndex) {
    const post = S.posts[idx];
    const a = post && (post.attachments || [])[ai];
    if (!a) return;
    const key = JSON.stringify(a);
    const isCurrent = idx === S.curPostIdx && ai === S.attachIndex;
    if (S.attachCache[key]) {
        if (isCurrent) { S.attachPreview = S.attachCache[key]; S.attachLoading = false; }
        updateAttachmentViews(idx, ai);
        return;
    }
    if (S.attachPending.has(key)) return;
    S.attachPending.add(key);
    if (isCurrent) { S.attachLoading = true; S.attachPreview = null; renderAttachPanel(); }
    try {
        const r = await jcall(BbsService.AttachmentPreview, key);
        S.attachCache[key] = r || {};
    } catch (e) {
        S.attachCache[key] = {error: errMsg(e)};
    } finally {
        S.attachPending.delete(key);
        if (isCurrent) { S.attachPreview = S.attachCache[key]; S.attachLoading = false; }
        updateAttachmentViews(idx, ai);
    }
}
async function openAttachmentAt(idx = S.curPostIdx, ai = S.attachIndex) {
    const post = S.posts[idx];
    const a = post && (post.attachments || [])[ai];
    if (!a) return;
    await safe(jcall(BbsService.OpenAttachment, JSON.stringify(a)), r => {
        toast((r && r.message) || '已用系统程序打开附件');
    });
}
function openCurrentAttachment() { return openAttachmentAt(); }
function attachOpenButton() {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'attach-btn';
    btn.textContent = '用系统程序打开 (o)';
    btn.addEventListener('click', () => { btn.blur(); openCurrentAttachment(); });
    return btn;
}
// ---- 放大查看器：宽高自适应 + 滚轮缩放 + 拖动/方向键平移 ----
let zoomImg = null;
function resetZoom() { S.zoom = {scale: 1, x: 0, y: 0, fit: 1, naturalW: 0, naturalH: 0}; }
// 让图片初始宽高都落进视窗（contain），计算一个基准适配比例。
function fitZoomImage(img) {
    const box = elAttachPrev;
    resetZoom();
    S.zoom.naturalW = img.naturalWidth || img.width;
    S.zoom.naturalH = img.naturalHeight || img.height;
    const vw = Math.max(1, box.clientWidth), vh = Math.max(1, box.clientHeight);
    S.zoom.fit = Math.min(vw / S.zoom.naturalW, vh / S.zoom.naturalH);
    applyZoom();
}
function applyZoom() {
    if (!zoomImg || S.zoom.naturalW <= 0 || S.zoom.naturalH <= 0 || !isFinite(S.zoom.fit)) return;
    const z = S.zoom;
    // 基准尺寸按适配比例铺进视窗，再乘用户缩放；宽高始终保持原始比例。
    const w = z.naturalW * z.fit, h = z.naturalH * z.fit;
    zoomImg.style.width = w + 'px';
    zoomImg.style.height = h + 'px';
    const cx = elAttachPrev.clientWidth / 2, cy = elAttachPrev.clientHeight / 2;
    const tx = cx + z.x - (w * z.scale) / 2;
    const ty = cy + z.y - (h * z.scale) / 2;
    zoomImg.style.transform = `translate(${tx}px, ${ty}px) scale(${z.scale})`;
    zoomImg.dataset.scale = z.scale.toFixed(2);
}
// 以视窗中心为锚点缩放，滚轮向上放大、向下缩小。
function zoomBy(factor) {
    if (!zoomImg) return;
    const z = S.zoom;
    const next = Math.max(0.25, Math.min(6, z.scale * factor));
    z.scale = next;
    applyZoom();
}
function panZoom(dx, dy) { S.zoom.x += dx; S.zoom.y += dy; applyZoom(); }
function applyZoomReset() { S.zoom.scale = 1; S.zoom.x = 0; S.zoom.y = 0; applyZoom(); }
function bindZoomEvents(box) {
    box.addEventListener('wheel', e => {
        if (!zoomImg) return;
        e.preventDefault();
        zoomBy(e.deltaY < 0 ? 1.15 : 1 / 1.15);
    }, {passive: false});
    let dragging = false, sx = 0, sy = 0, ox = 0, oy = 0;
    box.addEventListener('mousedown', e => {
        if (!zoomImg || e.button !== 0) return;
        dragging = true; sx = e.clientX; sy = e.clientY;
        ox = S.zoom.x; oy = S.zoom.y;
        box.classList.add('dragging');
        e.preventDefault();
    });
    window.addEventListener('mousemove', e => {
        if (!dragging) return;
        S.zoom.x = ox + (e.clientX - sx);
        S.zoom.y = oy + (e.clientY - sy);
        applyZoom();
    });
    window.addEventListener('mouseup', () => { if (dragging) { dragging = false; box.classList.remove('dragging'); } });
}
let zoomEventsBound = false;
function ensureZoomEvents() { if (!zoomEventsBound) { bindZoomEvents(elAttachPrev); zoomEventsBound = true; } }

function renderAttachPanel() {
    if (!S.attachOpen) return;
    const list = currentAttachments();
    const a = list[S.attachIndex];
    if (!a) { closeAttachView(); return; }
    elAttachHead.textContent = `附件预览 [${S.attachIndex + 1}/${list.length}]  ${attachName(a)}  ${attachSize(a.size)}`;
    elAttachFoot.textContent = '滚轮 缩放   拖动/方向键 移动   ←/→ 切换   + - 缩放   0 复位   Esc 关闭';
    const box = elAttachPrev;
    box.innerHTML = '';
    box.classList.remove('zoom-mode');
    zoomImg = null;
    if (S.attachLoading) {
        const d = document.createElement('div'); d.className = 'attach-info';
        d.textContent = '正在读取附件 …'; box.appendChild(d); return;
    }
    const p = S.attachPreview || {};
    if (p.error) {
        const d = document.createElement('div'); d.className = 'attach-info attach-error';
        d.textContent = '预览失败：' + p.error; box.appendChild(d); box.appendChild(attachOpenButton()); return;
    }
    const kind = attachKind(a);
    if (!p.inline || p.too_large || !p.data_url) {
        const d = document.createElement('div'); d.className = 'attach-file';
        d.innerHTML = `<div class="attach-icon">${KIND_ICON[kind] || '[FILE]'}</div>` +
            `<div class="attach-filename"></div><div class="attach-meta"></div>` +
            `<div class="attach-info">${p.too_large ? '附件较大，未内联预览。' : '此格式无法在终端窗口内直接预览。'}</div>`;
        d.querySelector('.attach-filename').textContent = attachName(a);
        d.querySelector('.attach-meta').textContent = `${kind.toUpperCase()}  ${attachSize(a.size)}`;
        box.appendChild(d); box.appendChild(attachOpenButton()); return;
    }
    const src = p.data_url;
    if (kind === 'image') {
        ensureZoomEvents();
        box.classList.add('zoom-mode');
        const stage = document.createElement('div'); stage.id = 'attach-zoom';
        const img = document.createElement('img');
        img.alt = attachName(a);
        img.addEventListener('load', () => fitZoomImage(img));
        img.src = src;
        stage.appendChild(img);
        box.appendChild(stage);
        zoomImg = img;
    } else if (kind === 'audio') {
        const au = document.createElement('audio'); au.src = src; au.controls = true; box.appendChild(au);
    } else if (kind === 'video') {
        const vd = document.createElement('video'); vd.src = src; vd.controls = true; box.appendChild(vd);
    } else if (kind === 'pdf') {
        const fr = document.createElement('iframe'); fr.src = src; fr.title = attachName(a); box.appendChild(fr);
    } else if (kind === 'html') {
        const fr = document.createElement('iframe');
        fr.setAttribute('sandbox', '');
        fr.srcdoc = dataURLToText(src).slice(0, 200000);
        box.appendChild(fr);
    } else if (kind === 'text') {
        const pre = document.createElement('pre');
        pre.textContent = dataURLToText(src).slice(0, 20000);
        box.appendChild(pre);
    }
}
function inlineOpenButton(idx = S.curPostIdx, ai = S.attachIndex) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'ia-open';
    btn.textContent = '打开 (o)';
    btn.addEventListener('click', () => { btn.blur(); openAttachmentAt(idx, ai); });
    return btn;
}
// ---- 阅读文档：正文与附件拼成一个可滚屏内容流 ----
// 正文以字符行渲染（term 风格），附件节点接在正文最后一行之后；
// 空格/PgUp/PgDn/滚轮滚动整个 #reader，和长正文续读的体验一致。
const lineNode = line => {
    const d = document.createElement('div');
    d.innerHTML = line.fill(COLS).html();
    return d.firstChild;
};
function readerDocKey() {
    return [S.board, S.threadID, S.loadedPages.join(','), S.posts.length,
        S.config.fold_quotes !== false].join('|');
}

// 整篇主题共用一个滚动文档：新分页只往尾部追加节点，不重建 DOM，
// 因此滚动位置、音视频播放都不会被翻页边界打断。
function buildReader() {
    if (S.screen !== 'post' || !S.posts.length) { elReader.classList.add('hidden'); return; }
    const key = readerDocKey();
    const thread = String(S.board) + '/' + S.threadID;
    const sameThread = elReader.dataset.thread === thread;
    const rendered = Number(elReader.dataset.rendered || 0);
    if (sameThread && elReader.dataset.key === key) return;
    elReader.classList.remove('hidden');
    const oldTop = elReader.scrollTop;
    if (sameThread && rendered > 0 && rendered < S.posts.length) {
        const frag = document.createDocumentFragment();
        for (let i = rendered; i < S.posts.length; i++) appendPostNode(frag, S.posts[i], i);
        elReader.appendChild(frag);
        elReader.dataset.rendered = String(S.posts.length);
        elReader.dataset.key = key;
        updateReaderScrollState();
        return;
    }
    elReader.innerHTML = '';
    const frag = document.createDocumentFragment();
    S.posts.forEach((p, i) => appendPostNode(frag, p, i));
    elReader.appendChild(frag);
    elReader.dataset.thread = thread;
    elReader.dataset.rendered = String(S.posts.length);
    elReader.dataset.key = key;
    elReader.scrollTop = sameThread ? oldTop : 0;
    updateReaderScrollState();
}

function appendPostNode(frag, p, idx) {
    const wrap = document.createElement('div');
    wrap.className = 'reader-post';
    wrap.dataset.floor = String(p.floor || idx + 1);
    wrap.dataset.postIdx = String(idx);
    const floor = Number(p.floor || idx + 1);
    const badge = `第 ${floor} 楼`;
    // 滚动条占最右一列，楼层号往左让两列，避免最后一个「楼」字被盖住。
    wrap.appendChild(lineNode(L().at(Math.max(0, COLS - dw(badge) - 2), badge, 'y').bg('reader-floor-line')));
    wrap.appendChild(lineNode(L().add('发信人: ', 'g').add(p.author || 'guest', 'W')
        .add(p.nick ? ` (${p.nick})` : '', 'k').add(',  信区: ', 'g')
        .add(p.board_id || S.board, 'W')));
    wrap.appendChild(lineNode(L().add('标  题: ', 'g').add(cut(p.subject || S.subject || '', COLS - 10), 'Y')));
    wrap.appendChild(lineNode(L().add('日  期: ', 'g').add(cut(p.date || '', COLS - 10), 'k')));
    wrap.appendChild(lineNode(L().add('-'.repeat(COLS), 'b')));
    const quoteFold = S.config.fold_quotes !== false;
    for (const raw of String(p.body || '').split('\n')) {
        const t = raw.trimStart();
        let cls = 'w';
        if (/^[:：>|]/.test(t)) cls = quoteFold ? 'k' : 'c';
        else if (/^【/.test(t)) cls = 'c';
        else if (/^--+$|^※|^FROM|^\[FROM/.test(t)) cls = 'kk';
        else if (/^Re:|^发信人|^标\s*题/.test(t)) cls = 'g';
        wrap.appendChild(lineNode(L().add(cut(raw, COLS), cls)));
    }
    wrap.appendChild(lineNode(L().add('', 'w')));
    if ((p.attachments || []).length) wrap.appendChild(attachSectionNode(idx));
    frag.appendChild(wrap);
}

function attachSectionNode(idx) {
    const sec = document.createElement('div');
    sec.className = 'reader-attach';
    sec.dataset.postIdx = String(idx);
    renderAttachSection(sec, idx);
    return sec;
}

// 只重画某一层的附件区：预览异步回来时不会重建整篇正文。
function renderAttachSection(sec, idx) {
    const p = S.posts[idx];
    const list = p && p.attachments;
    if (!sec || !list || !list.length) return;
    const ai = (idx === S.curPostIdx) ? Math.min(S.attachIndex, list.length - 1) : 0;
    const a = list[ai];
    if (!a) return;
    sec.innerHTML = '';
    sec.appendChild(lineNode(L().add('-'.repeat(COLS), 'b').bg('ia-sep')));
    sec.appendChild(lineNode(L().add(`附件 [${ai + 1}/${list.length}] `, 'Y')
        .add(cut(attachName(a), 28), 'c').add('  ' + attachSize(a.size), 'k')
        .add(list.length > 1 ? '  [ ]切换  v放大' : '  v放大', 'y')));
    const key = JSON.stringify(a);
    const preview = S.attachCache[key];
    if (!preview) {
        const d = document.createElement('div'); d.className = 'ia-meta';
        d.textContent = '正在读取附件预览 …'; sec.appendChild(d);
        loadAttachPreview(idx, ai);
        return;
    }
    if (preview.error) {
        const d = document.createElement('div'); d.className = 'ia-meta';
        d.textContent = '预览失败：' + preview.error + '  ';
        sec.appendChild(d); sec.appendChild(inlineOpenButton(idx, ai)); return;
    }
    const kind = attachKind(a);
    if (!preview.inline || preview.too_large || !preview.data_url) {
        const d = document.createElement('div'); d.className = 'ia-card';
        d.innerHTML = `<span class="ia-icon">${KIND_ICON[kind] || '[FILE]'}</span> ` +
            `<span class="ia-name"></span> <span class="ia-meta"></span>`;
        d.querySelector('.ia-name').textContent = attachName(a);
        d.querySelector('.ia-meta').textContent = preview.too_large ? '文件较大，点击打开' : (kind.toUpperCase() + '  ' + attachSize(a.size));
        sec.appendChild(d); sec.appendChild(inlineOpenButton(idx, ai)); return;
    }
    const src = preview.data_url;
    if (kind === 'image') {
        const img = document.createElement('img'); img.src = src; img.alt = attachName(a); sec.appendChild(img);
    } else if (kind === 'audio') {
        const au = document.createElement('audio'); au.src = src; au.controls = true; sec.appendChild(au);
    } else if (kind === 'video') {
        const vd = document.createElement('video'); vd.src = src; vd.controls = true; sec.appendChild(vd);
    } else if (kind === 'pdf') {
        const fr = document.createElement('iframe'); fr.src = src; fr.title = attachName(a); sec.appendChild(fr);
    } else if (kind === 'html') {
        const fr = document.createElement('iframe');
        fr.setAttribute('sandbox', ''); fr.srcdoc = dataURLToText(src).slice(0, 200000); sec.appendChild(fr);
    } else if (kind === 'text') {
        const pre = document.createElement('pre'); pre.textContent = dataURLToText(src).slice(0, 4000); sec.appendChild(pre);
    }
}

// 每楼固定一屏，外层滚动位置除以一屏高度就是当前楼索引。
// 按键处理不能只依赖 scroll 事件：平滑滚动中立刻按 r/v 时事件可能还没更新状态。
function readerPosts() { return elReader.querySelectorAll('.reader-post'); }
let readerTargetIdx = -1, readerTargetUntil = 0;
function currentReaderPostIdx() {
    const nodes = readerPosts();
    if (!nodes.length) return 0;
    // 平滑切楼途中，r/v 应以「正在切入的那一楼」为准，不能取动画中间的半屏位置。
    if (readerTargetIdx >= 0 && Date.now() < readerTargetUntil
        && Math.abs(elReader.scrollTop - nodes[readerTargetIdx].offsetTop) > 2) {
        return readerTargetIdx;
    }
    const h = elReader.clientHeight || 1;
    return Math.max(0, Math.min(nodes.length - 1, Math.round(elReader.scrollTop / h)));
}
// 楼内还有没读完的内容时，优先在楼内滚；到楼顶/楼底才翻到相邻楼。
function innerFloorAt(idx) {
    const el = readerPosts()[idx];
    return el && el.scrollHeight > el.clientHeight + 2 ? el : null;
}
function innerCanScroll(el, dir) {
    if (!el) return false;
    const max = el.scrollHeight - el.clientHeight;
    return dir > 0 ? el.scrollTop < max - 1 : el.scrollTop > 1;
}

// 连续快滚时直接切楼（避免动画排队），慢速操作时给一段柔和的过渡。
let lastFloorStepAt = 0;
function floorStepBehavior() {
    const now = Date.now();
    const rapid = now - lastFloorStepAt < 260;
    lastFloorStepAt = now;
    return rapid ? 'auto' : 'smooth';
}

// 滚到某一楼：楼顶正好贴住滚动区顶部，不会露出上一楼的尾巴或下一楼的开头。
function goToPostIndex(idx, smooth = true) {
    const nodes = readerPosts();
    if (!nodes.length) return false;
    const i = Math.max(0, Math.min(nodes.length - 1, idx));
    const el = nodes[i];
    const behavior = smooth ? floorStepBehavior() : 'auto';
    readerTargetIdx = behavior === 'smooth' ? i : -1;
    readerTargetUntil = behavior === 'smooth' ? Date.now() + 600 : 0;
    elReader.scrollTo({top: el.offsetTop, behavior});
    S.curPostIdx = i; S.postIndex = i; S.attachIndex = 0;
    markRead(); ensureAttachPreview();
    return true;
}

// 根据滚动位置定位当前楼层，并在读到最后一楼时自动续载下一页。
let readerScrollTick = 0;
function updateReaderScrollState() {
    if (S.screen !== 'post' || elReader.classList.contains('hidden')) return;
    const idx = currentReaderPostIdx();
    if (idx !== S.curPostIdx) {
        S.curPostIdx = idx; S.postIndex = idx; S.attachIndex = 0;
        markRead(); ensureAttachPreview();
    }
    const nearBottom = elReader.scrollTop + elReader.clientHeight >= elReader.scrollHeight - 4;
    if (nearBottom && !S.pageLoading && S.postPage < S.postPages) {
        loadThread(S.board, S.threadID, S.postPage + 1, false, 'append').then(warmupPages);
    }
    const now = Date.now();
    if (now - readerScrollTick > 250) { readerScrollTick = now; render(); }
}

function readerProgress() {
    if (S.screen !== 'post' || elReader.classList.contains('hidden')) return '';
    const max = elReader.scrollHeight - elReader.clientHeight;
    if (max <= 2) return '一屏显示完';
    const pct = Math.round(elReader.scrollTop / max * 100);
    return `滚屏 ${pct}%`;
}
// ↑↓ 在楼内一次一行；空格/PgDn 在楼内一次一屏；越过楼内边界才翻到相邻楼。
function scrollReader(dir, lineMode = false) {
    if (S.screen !== 'post' || elReader.classList.contains('hidden')) return false;
    const idx = currentReaderPostIdx();
    const inner = innerFloorAt(idx);
    if (innerCanScroll(inner, dir)) {
        const lh = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh')) || 22;
        const step = lineMode ? lh : inner.clientHeight;
        const max = inner.scrollHeight - inner.clientHeight;
        // 行滚动用即时定位，保证下一次按键能准确判断是否已到楼内边界。
        const top = Math.max(0, Math.min(max, inner.scrollTop + dir * step));
        inner.scrollTo({top, behavior: lineMode ? 'auto' : floorStepBehavior()});
        return true;
    }
    return goToPostIndex(idx + dir, true);
}
// 鼠标滚轮：楼内还有内容就按一格约 4 行滚动；到楼顶/楼底才整楼切换。
// 触控板会连发大量 wheel 事件，切楼加冷却，避免一次滑动跳过好几楼。
let lastWheelFloorAt = 0;
function onReaderWheel(e) {
    if (S.screen !== 'post' || elReader.classList.contains('hidden') || S.editor || S.prompt) return;
    const raw = e.deltaMode === 1 ? e.deltaY * 22 : e.deltaY;
    const dir = raw > 0 ? 1 : (raw < 0 ? -1 : 0);
    if (!dir) return;
    e.preventDefault();
    const idx = currentReaderPostIdx();
    const inner = innerFloorAt(idx);
    if (innerCanScroll(inner, dir)) {
        const lh = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh')) || 22;
        const step = Math.max(lh, Math.min(inner.clientHeight, Math.abs(raw)));
        const max = inner.scrollHeight - inner.clientHeight;
        inner.scrollTo({top: Math.max(0, Math.min(max, inner.scrollTop + dir * step)), behavior: 'auto'});
        return;
    }
    const now = Date.now();
    if (now - lastWheelFloorAt < 320) return;
    lastWheelFloorAt = now;
    goToPostIndex(idx + dir, true);
}
function scrollToFloor(n, smooth = true) {
    const el = elReader.querySelector(`.reader-post[data-floor="${n}"]`);
    if (!el) return false;
    return goToPostIndex(Array.prototype.indexOf.call(readerPosts(), el), smooth);
}
// 跳楼：目标楼层还没加载时先把后面的页接进来，再平滑滚过去。
async function gotoFloor(n, smooth = true) {
    if (S.screen !== 'post' || !n) return;
    while (!scrollToFloor(n, false) && !S.pageLoading && S.postPage < S.postPages) {
        await loadThread(S.board, S.threadID, S.postPage + 1, false, 'append');
    }
    scrollToFloor(n, smooth);
}
function jumpPost(delta) {
    if (S.screen !== 'post') return;
    goToPostIndex(currentReaderPostIdx() + delta, true);
}
function renderMailView(lines) {
    const m = S.mail || {};
    // 顶栏已经显示「阅读信件」，正文直接从信件头开始。
    lines.push(L().add('发信人: ', 'g').add(m.from || '', 'W').add('    日期: ', 'g').add(m.date || '', 'k'));
    lines.push(L().add('标  题: ', 'g').add(m.title || '', 'Y'));
    lines.push(L().add('-'.repeat(COLS), 'b'));
    let used = 3;
    for (const raw of String(m.content || '').split('\n')) {
        if (used >= BODY_ROWS - 1) break;
        lines.push(L().add(cut(raw, COLS), /^[:：>|]/.test(raw.trimStart()) ? 'k' : 'w'));
        used++;
    }
    while (used < BODY_ROWS - 1) { lines.push(emptyLine()); used++; }
    const notify = !!(m && m.notify);
    lines.push(L().add(notify ? ' ←/q 返回   o/→ 看原帖   r 到该版回复该楼' : ' ←/q 返回    r 回信', 'c'));
}

// ------------------------------------------------------------------ 帮助 / 设置 / 关于
const HELP_LINES = [
    ['方向键', '↑ / ↓ 移动光标    → 或 Enter 进入    ← / q / Esc 返回上一级'],
    ['翻页', 'PgDn / 空格 往下滚一屏    PgUp 往上滚    Home/End 首尾'],
    ['跳转', '阅读时 g / 数字 + Enter 跳到指定楼层    s 选择阅读讨论区（服务器版列表）'],
    ['阅读', '每层至少占一屏，右上角显示楼层；空格/PgDn 下一楼，r 回复当前楼，→ 下一帖，a 收藏'],
    ['附件', '每层附件接在该层正文之后，与整篇正文同一屏滚动；图片按宽度自适应'],
    ['', '↑↓ 逐行滚，空格/PgDn 一屏，PgUp 回滚；[ ] 切附件，v 放大；放大后滚轮缩放、方向键/拖动移动、0 复位'],
    ['发文', 'Ctrl+P 发表新文章    Ctrl+O 添加附件（可多选）    Ctrl+W / Ctrl+S 发送并在网页完成验证    Esc 放弃'],
    ['全站', 't 本日十大    f 收藏夹    m 信件处理    / 搜索    o 设置'],
    ['提醒', '讨论区列表：R 回函提醒   @ @我的提醒   L Like提醒；底栏 [R@L] 为未读提示'],
    ['主菜单', '0 精华公布栏  X 分类讨论区  F 个人定制区  M 信件处理  B 所有版列表'],
    ['', 'S 选择阅读讨论区  C 系统信息  G 离开水木  h 本帮助'],
    ['数据源', 'w 打开官方 wiki    O 打开原站    d 切换 demo/web/openapi'],
    ['登录', 'U 进站  F2 通道  F5 Cookie  代号后加 . 为 BIG5'],
    ['官方API', '时间线模型，无按版面读主题方法；完整版面浏览用 web + Cookie'],
    ['其他', '鼠标可点击行；已读标题置灰；[+N] 为未读新回帖数'],
    ['窗口', '拖蓝色状态栏移动；双击顶栏最大化；右上角按钮 最小化/最大化/关闭'],
];
function renderHelp(lines) {
    lines.push(L().add('  功 能     说 明', 'Y'));
    HELP_LINES.forEach(([k, v]) => lines.push(L().at(2, k, 'y').at(12, cut(v, 66), 'w')));
    lines.push(emptyLine());
    lines.push(L().add('  官方文档: https://wiki.open.newsmth.net/    Wails 3: https://v3.wails.io/', 'c'));
}
const SETTINGS_ITEMS = [
    ['D', '切换数据源', 'demo（离线演示） / web（Cookie） / openapi（官方 API）'],
    ['L', 'Web 登录', '用户名 + 密码；站点带验证码时请改用 C 导入 Cookie'],
    ['C', '导入 Cookie', '浏览器登录 newsmth.net 后，把 Cookie 字符串整段粘贴进来'],
    ['O', '打开原站登录', '用系统浏览器打开水木（登录后复制 Cookie）'],
    ['P', 'OAuth2 授权', '打开 wiki.open.newsmth.net/oauth 授权页'],
    ['K', '填入授权码', '把 redirect_uri 里的 code 粘贴回来换取 access_token'],
    ['1', 'client_id', '开放平台 APP ID（需向站务申请）'],
    ['2', 'client_secret', '开放平台 APP Secret'],
    ['3', 'client_signature', '签名密钥（MD5 签名用，见官方 wiki/signature）'],
    ['4', '站点地址', '默认 https://www.newsmth.net'],
    ['R', '记住代号', '下次启动自动填入上次登录的 ID（不保存密码）'],
    ['A', '会话复用', '已有 Cookie / token 时，登录屏按 Enter 免密继续'],
    ['U', '切换用户', '回到 term 进站登录画面'],
    ['W', '官方开发文档', '打开 wiki.open.newsmth.net'],
    ['Q', '退出登录', '清除 Cookie / access_token'],
];
function renderSettings(lines) {
    const c = S.config || {};
    lines.push(L().add('  键   项目                 说明 / 当前值', 'Y'));
    SETTINGS_ITEMS.forEach(([k, name], i) => {
        const l = L().at(2, k, 'r').at(7, cut(name, 20), i === S.sel ? 'W' : 'w');
        let val = '';
        if (k === 'D') val = c.source || 'demo';
        if (k === 'C') val = c.cookie ? '已设置 (' + String(c.cookie).length + ' 字符)' : '未设置';
        if (k === 'K') val = c.access_token ? '已授权' : '未授权';
        if (k === '1') val = c.client_id || '未设置';
        if (k === '2') val = c.client_secret ? '已设置' : '未设置';
        if (k === '3') val = c.client_signature ? '已设置' : '未设置';
        if (k === '4') val = c.base_url || '';
        if (k === 'L') val = c.user || 'guest';
        if (k === 'R') val = c.remember_id === false ? '关闭' : '开启（不保存密码）';
        if (k === 'A') val = c.auto_login === false ? '关闭' : '开启';
        if (k === 'U') val = c.last_user_id || '未记住代号';
        l.at(30, cut(val, 40), 'g');
        lines.push(l);
    });
    lines.push(L().add('  提示：数据源说明见 wiki.open.newsmth.net/oauth 与 /signature', 'k'));
}
function renderAbout(lines) {
    lines.push(emptyLine());
    lines.push(L().at(4, 'sfjth', 'Y').at(14, '水木社区 term 风格客户端', 'W'));
    lines.push(L().at(4, '版本', 'g').at(14, S.version.ver || '0.1.0', 'w'));
    lines.push(L().at(4, '运行环境', 'g').at(14, (S.version.go || '') + '  ' + (S.version.os || ''), 'w'));
    lines.push(L().at(4, '数据源', 'g').at(14, (S.config.source || 'demo') + (S.status.user ? '  用户:' + S.status.user : ''), 'w'));
    lines.push(emptyLine());
    lines.push(L().at(4, '官方开放平台文档', 'g').at(24, 'wiki.open.newsmth.net', 'c'));
    lines.push(L().at(4, 'API 方法汇总', 'g').at(24, '/method_list', 'c'));
    lines.push(L().at(4, 'OAuth2 授权', 'g').at(24, '/oauth', 'c'));
    lines.push(L().at(4, '数字签名', 'g').at(24, '/signature', 'c'));
    lines.push(L().at(4, '分页定位', 'g').at(24, '/pager', 'c'));
    lines.push(L().at(4, '错误码', 'g').at(24, '/error', 'c'));
    lines.push(emptyLine());
    lines.push(L().at(4, '数据目录', 'g').at(14, cut(S.config.data_dir || '%APPDATA%\\sfjth', 60), 'k'));
    lines.push(emptyLine());
    lines.push(L().at(4, '按 w 在浏览器中打开官方文档，按 ← 返回', 'y'));
}
// 进站彩图：按用户要求只做风格占位（黑底 + 彩色 ASCII 块 + 双线路域名 +
// 备案号），不逐笔复刻原图的凤凰与空心大字；底部登录区则严格对齐截图。
// 进站彩图的块字符宽度已统一到顶部 dw()（全角 = 2 ASCII），
// aw 保留为别名，避免两处宽度规则不一致。
const aw = dw;
function art(items) {
    const l = L();
    let col = 0;
    // 按列升序绘制，否则低列内容会被追加到行尾。
    for (const [c, text, cls] of [...items].sort((a, b) => a[0] - b[0])) {
        if (c > col) { l.add(' '.repeat(c - col)); col = c; }
        l.add(text, cls || 'w');
        col += aw(text);
    }
    return l;
}
// 彩虹色点阵 SMTH（青 → 绿 → 黄 → 绿 → 红），对应截图顶部的彩条。
const BIG_SMTH = [
    '███ █ █ ███ █ █',
    '█   ███  █  █ █',
    '███ █ █  █  ███',
    '  █ █ █  █  █ █',
    '███ █ █  █  █ █',
];
const BIG_COLORS = ['c', 'g', 'y', 'g', 'r'];
function renderLogin(lines) {
    const li = S.login;
    const rows = Array.from({length: 24}, () => L().add(''));
    const set = (i, items) => { rows[i] = art(items); };

    for (let i = 0; i < 5; i++) {
        const deco = [
            "  ;;;;' ;", " ;;  ;;  '", ";;   ;;   ',", ";'  ;;   ;", "  ;;    ;",
        ][i];
        set(i, [[3, deco, 'g'], [26, BIG_SMTH[i], BIG_COLORS[i]], [62, deco, 'g']]);
    }
    set(6, [[14, '网', 'C'], [17, 'bbs.mysmth.net', 'g'], [40, '电', 'C'], [43, 'bbs.mysmth.net', 'g']]);
    set(7, [[14, '通', 'C'], [17, 'www.mysmth.net', 'y'], [40, '信', 'C'], [43, 'www.mysmth.net', 'g']]);

    set(9, [[1, '  ▄▄▄  ▄▄▄  ', 'r'], [62, 'J  J  J  J', 'm']]);
    set(10, [[1, ' ████  ████ ', 'r'], [62, 'L  U  L  L', 'm'],
        [26, ' ███   ███   ███ ', 'y']]);
    set(11, [[1, '██  ████  ██', 'r'], [26, '█   █ █   █ █   █', 'y']]);
    set(12, [[1, '   ██████   ', 'r'], [26, '█████ █████ █████', 'y']]);
    set(13, [[1, '  ███  ███  ', 'r'], [26, '█   █ █   █ █   █', 'y']]);
    set(14, [[1, ' ██      ██  ', 'r'], [26, '█   █ █   █ █   █', 'y']]);
    set(15, [[10, '北京市通信管理局ICP备案号（京ICP证110768号）', 'c']]);
    rows[16] = L().at(50, `通道:${loginModeLabel(li.mode)}${li.captcha ? '*' : ''} F2`, 'y');

    rows[17] = L()
        .add('欢迎光临', 'W')
        .add(' ◆ ', 'W').add('水木社区', 'r').add(' ◆ ', 'W')
        .add('  ', 'w')
        .add('上线人数 ', 'c').add(String(li.online), 'g')
        .add('[最高: ', 'w').add(String(li.maxOnline), 'W').add(']', 'w')
        .add('(', 'c').add(String(li.guests), 'c').add(' WWW GUEST)', 'c')
        .add(li.big5 ? '  [BIG5]' : '', 'y');
    rows[18] = L().add(cut("试用请输入 'guest'，注册请通过 web 方式访问, add '.' after your ID for BIG5", 78), 'c');
    rows[19] = L().add('请输入代号：', 'W').add(li.id, 'W');
    if (li.field === 'id' && !li.busy) rows[19].add('█', 'blink W');
    rows[20] = L().add('请输入密码：', 'W').add('*'.repeat(li.pass.length), 'W');
    if (li.field === 'password' && !li.busy) rows[20].add('█', 'blink W');
    rows[21] = L().add('按', 'inv').add(' [RETURN] 继续', 'W');
    rows[22] = L().add(li.error ? cut(li.error, 78) : cut(li.hint || '', 78), li.error ? 'r' : 'g');
    rows[23] = L().add(cut(li.error && li.hint
        ? ' ' + li.hint
        : '  Esc重输  [^U]清空  [Del]跳过  [F2]通道  [F5]Cookie  guest试用', 78),
        li.error ? 'c' : 'k');
    lines.push(...rows);
}

function renderSplash(lines) {
    lines.push(emptyLine());
    lines.push(L().at(8, '  ███████ ███████ ██ ████████ ██   ██ ', 'y'));
    lines.push(L().at(8, ' ██      ██      ██    ██    ██   ██ ', 'y'));
    lines.push(L().at(8, ' ███████ █████   ██    ██    ███████ ', 'Y'));
    lines.push(L().at(8, '      ██ ██      ██    ██    ██   ██ ', 'y'));
    lines.push(L().at(8, ' ███████ ██      ██    ██    ██   ██ ', 'y'));
    lines.push(emptyLine());
    lines.push(L().at(24, '水  木  社  区', 'W'));
    lines.push(L().at(20, 'N e w S M T H . n e t', 'c'));
    lines.push(emptyLine());
    lines.push(L().at(14, 'term 风格客户端  ·  Wails 3  ·  80x24', 'g'));
    lines.push(L().at(14, '数据层：开放平台 API / Web 会话 / 本地缓存预取', 'k'));
    lines.push(emptyLine());
    lines.push(L().at(22, S.splashMsg ? cut(S.splashMsg, 60) : '[ 按任意键进入进站画面 ]', 'y'));
    lines.push(emptyLine());
    lines.push(L().at(10, '官方文档：wiki.open.newsmth.net', 'k'));
}
function renderBye(lines) {
    lines.push(emptyLine()); lines.push(emptyLine());
    lines.push(L().at(20, '感谢使用 sfjth，再见！', 'Y'));
    lines.push(L().at(18, '◇家─有爱就有责任◇  Family@newsmth', 'g'));
}

// ------------------------------------------------------------------ 主渲染
const HELP_TEXT = {
    login: '代号→Enter→密码→Enter→底部弹出真实登录窗；F2 通道  F5 Cookie  guest 直通',
    main: '↑↓ 选择  →/Enter 进入  h 帮助  t 十大  f 收藏  m 信件  o 设置  G 离开',
    sections: '↑↓ 选择  →/Enter 进入  ←/q 返回  PgUp/PgDn 翻页  h 帮助',
    selectboard: '直接输入版名回车进入  # 关键词搜索  @ URL跳转  SPACE/TAB 补全  Esc 取消',
    boardsearch: '↑↓ 选择  →/Enter 进版  ←/q 返回  h 帮助',
    boards: '↑↓ 选择  →/Enter 进版  ←/q 返回  a 收藏  s 版名直达  h 帮助',
    favorites: '↑↓ 选择  →/Enter 进版  ←/q 返回  a 移出收藏  h 帮助',
    threads: '↑↓ 移动光标  →/Enter 阅读  g 跳序号  r 刷新  R 回函  @ @我  L Like  ←/q 返回',
    post: '空格/PgDn/滚轮 下一楼  ↑↓ 滚屏  → 下一帖  r 回当前楼  [ ] 切附件  ←/q 返回  h 帮助',
    topten: '↑↓ 选择  →/Enter 阅读  ←/q 返回  h 帮助',
    mail: '↑↓ 选择  →/Enter 进入  ←/e 返回主选单  N/R/K/L/B 直达功能  h 帮助',
    inbox: '↑↓ 选择  →/Enter 查看会话  PgUp/PgDn 翻页  ←/q 返回',
    notify: '↑↓ 选择  →/Enter 看提醒  r 进版面回复该楼  PgUp/PgDn 翻页  ←/q 返回',
    mailview: '←/q 返回  r 回信  o 看原帖（提醒）  h 帮助',
    search: '↑↓ 逐行滚  空格/PgDn 一屏  →/Enter 阅读  ←/q 返回',
    help: '←/q 返回',
    settings: '↑↓ 选择  →/Enter 执行  ←/q 返回  或直接按红色快捷键',
    about: '←/q 返回  w 打开官方 wiki',
    splash: '按任意键继续',
    bye: '',
};
function render() {
    const lines = [];
    let left = '主菜单', right = '讨论区 [NewExpress]';
    switch (S.screen) {
        case 'login': renderLogin(lines); left = '进站登录'; right = '水木社区'; break;
        case 'splash': renderSplash(lines); left = 'sfjth'; right = '水木社区'; break;
        case 'bye': renderBye(lines); left = 'sfjth'; right = '离开水木'; break;
        case 'main': left = renderMain(lines); break;
        case 'sections': renderSections(lines); left = '分类讨论区'; break;
        case 'boards': renderBoards(lines); left = '讨论区列表'; break;
        case 'selectboard': renderBoardPicker(lines); left = '选择阅读讨论区'; break;
        case 'boardsearch': renderBoards(lines); left = '版名搜索'; break;
        case 'favorites': renderFavorites(lines); left = S.title || '个人定制区'; break;
        case 'threads':
            renderThreads(lines);
            // 顶栏一次显示英文版名 + 中文版名；列表正文不再重复一行版面提示。
            left = '讨论区 [' + ((S.boardInfo && S.boardInfo.id) || S.board || '') + ']';
            if (S.boardInfo && S.boardInfo.name &&
                S.boardInfo.name !== (S.boardInfo.id || S.board)) {
                left += ' ' + S.boardInfo.name;
            }
            break;
        case 'post':
            renderPost(lines);
            left = `阅读文章 [${S.board || ''}]  第 ${S.postPage}/${S.postPages} 页`;
            break;
        case 'topten': renderTopTen(lines); left = '本日十大'; break;
        case 'mail': renderMail(lines); left = '处理信件选单'; break;
        case 'inbox': renderInbox(lines); left = '收件箱 · 会话列表'; break;
        case 'notify': renderNotify(lines); left = NOTIFY_TITLES[S.notifyKind] || '提醒'; break;
        case 'mailview': renderMailView(lines); left = (S.mail && S.mail.notify) ? '查看提醒' : '阅读信件'; break;
        case 'search': renderSearch(lines); left = '搜索文章'; break;
        case 'help': renderHelp(lines); left = '操作说明'; break;
        case 'settings': renderSettings(lines); left = '系统设置 / 数据源'; break;
        case 'about': renderAbout(lines); left = '系统信息及服务'; break;
        default: renderMain(lines);
    }
    if (!['main', 'splash', 'bye'].includes(S.screen)) {
        right = (S.status.source || S.config.source || 'demo') + '  ' + (S.status.user || 'guest');
    }
    const topBar = S.screen === 'selectboard' ? selectBoardBar() : bar(left, '水木社区', right);
    if (S.screen === 'threads' || S.screen === 'search') {
        const notes = [];
        if (S.reminders.reply > 0) notes.push('【您有回函提醒】');
        if (S.reminders.at > 0) notes.push('【您有@提醒】');
        if (S.reminders.like > 0) notes.push('【您有Like提醒】');
        if (notes.length) topBar.at(46, cut(notes.join(' '), 26), 'r');
    }
    draw(lines, topBar,
        S.confirm ? confirmBar() : bottomBar(),
        S.confirm ? helpBar(' Y 确定离开  N/Esc 取消 ') : helpBar(HELP_TEXT[S.screen] || ''));
    if (S.screen === 'post') {
        ensureAttachPreview();
    } else {
        elReader.classList.add('hidden');
    }
    if (!['threads', 'search'].includes(S.screen)) elThreadList.classList.add('hidden');
    if (Date.now() > S.toastUntil) elToast.classList.add('hidden');
}

// ------------------------------------------------------------------ 输入（命令行）
function openPrompt(label, value, onSubmit, password = false, opts = {}) {
    S.prompt = {onSubmit, kind: opts.kind || 'generic'};
    elPromptLabel.textContent = label;
    elPromptInput.type = password ? 'password' : 'text';
    elPromptInput.value = value || '';
    elPrompt.classList.toggle('at-top', !!opts.atTop);
    elPrompt.classList.remove('hidden');
    // 禁止聚焦引发容器滚动：#term 是 overflow:hidden，浏览器仍可能把它横向滚到
    // 输入框右端，造成整个主菜单左移截断。顺手归零，防止上一次遗留的横向滚动。
    $('term').scrollLeft = 0;
    elPromptInput.focus({preventScroll: true});
    elPromptInput.select();
}
function closePrompt(submit = false) {
    if (!S.prompt) return;
    const {onSubmit, kind} = S.prompt;
    const v = elPromptInput.value;
    S.prompt = null;
    elPrompt.classList.add('hidden');
    elPrompt.classList.remove('at-top');
    elPromptInput.blur();
    if (submit && onSubmit) onSubmit(v);
    else if (kind === 'board') { restorePickerBack(); render(); }
}
function askBoard() {
    openPrompt('请输入版名 (如 NewExpress): ', S.board || 'NewExpress', v => {
        const b = v.trim(); if (b) showThreads(b, 1);
    });
}
// 文章列表按左侧“序号”跳转：1 是最下面（最新），序号越大越靠上（越旧）。
function gotoThreadItem(n, smooth = true) {
    if (!['threads', 'search'].includes(S.screen) || !n) return;
    if (n < 1 || n > S.items.length) return;
    scrollToThreadItem(S.items.length - n, smooth);
}
function askNumber() {
    const label = S.screen === 'post' ? '跳转到楼层: '
        : (['threads', 'search'].includes(S.screen) ? '跳转到序号: ' : '跳转到编号: ');
    openPrompt(label, '', v => {
        const n = parseInt(v.trim(), 10);
        if (isNaN(n) || n <= 0) return;
        if (S.screen === 'post') gotoFloor(n);
        else if (S.screen === 'threads' || S.screen === 'search') gotoThreadItem(n);
        else { move(n - 1 - S.sel); enter(); }
    });
}

// ------------------------------------------------------------------ 编辑器
function openEditor(mode, defaultTitle, replyID = 0, to = '', ctx = {}) {
    S.editor = {mode, title: defaultTitle, replyID, to, ...ctx};
    const hint = mode === 'mail' ? '' : '\n Ctrl+O 添加附件   Ctrl+W 发表（点一下 wap 验证）';
    elEditorHead.textContent = (mode === 'mail'
        ? ` 写信  收信人: ${to}\n 标题  : ${defaultTitle}\n` + '-'.repeat(COLS)
        : mode === 'reply'
            ? ` 回复第 ${ctx.floor || 1} 楼  [${S.board}]\n 标题  : ${defaultTitle}\n` + '-'.repeat(COLS)
            : ` ${replyID ? '回复文章' : '发表文章'}  [${S.board}]\n 标题  : ${defaultTitle}\n` + '-'.repeat(COLS)) + hint;
    elEditorBody.value = '';
    elEditor.classList.remove('hidden');
    elEditorBody.focus();
}
function closeEditor() {
    S.editor = null;
    elEditor.classList.add('hidden');
    elEditorBody.blur();
}
// 在编辑器里按 Ctrl+O：弹出系统多选文件框，上传到 wap 发帖临时区。
// wap 发文时 uploadToken 会自动关联这些附件，正文不需要插入 [upload=N] 标记。
async function pickAttachments() {
    if (!S.editor || S.editor.mode === 'mail') { toast('写信暂不支持附件'); return; }
    let paths;
    try { paths = await jcall(BbsService.PickFiles); } catch (e) { toast(errMsg(e)); return; }
    if (!paths || !paths.length) return;
    toast(`正在上传 ${paths.length} 个附件 ...`);
    let okCount = 0;
    for (const p of paths) {
        try {
            const r = await jcall(BbsService.UploadPostAttachment, S.board, p, '');
            if (r && r.ok) okCount++;
        } catch (e) { toast('附件上传失败：' + errMsg(e)); }
    }
    if (okCount) toast(`已添加 ${okCount} 个附件，发表时会自动附上`);
}

async function sendEditor() {
    if (!S.editor) return;
    const {mode, title, replyID, to, threadID, floor} = S.editor;
    const content = elEditorBody.value;
    if (!content.trim()) { toast('正文不能为空'); return; }
    if (mode === 'mail') {
        // 站内信不涉及站点验证码，仍走原接口。
        try {
            const r = await jcall(BbsService.WapSendMessage, to, content);
            closeEditor(); toast((r && r.message) || '发送成功');
        } catch (e) { toast(errMsg(e)); }
        return;
    }
    // 发帖/回帖：弹出 wap 验证窗口，点一下验证后由 Go 调 wap 发文 API 提交。
    try {
        closeEditor();
        postHandled = false;
        await jcall(BbsService.OpenPostPopup, S.board, title, content,
            threadID || S.threadID, floor || 0, mode === 'reply');
        pollPostPopup();
    } catch (e) { toast(errMsg(e)); }
}
function startPost() {
    if (S.screen !== 'threads' && S.screen !== 'post') { toast('请先进入一个版面'); return; }
    openPrompt('标题: ', '', v => { if (v.trim()) openEditor('post', v.trim(), 0); });
}
// 回复当前屏幕所在的楼层；forceIdx 用于从提醒列表直接跳进来回指定楼层。
function startReply(forceIdx) {
    // 默认从滚动位置算，平滑滚动中也不会取到旧楼层。
    const idx = Number.isInteger(forceIdx) && forceIdx >= 0 ? forceIdx : currentReaderPostIdx();
    S.curPostIdx = idx; S.postIndex = idx; S.attachIndex = 0;
    const p = S.posts[idx] || S.posts[S.postIndex];
    if (!p) { toast('当前没有可回复的文章'); return; }
    const floor = Number(p.floor || idx + 1);
    const title = /^Re:/.test(p.subject || '') ? p.subject : 'Re: ' + (p.subject || S.subject || '');
    openEditor('reply', title, 0, '', {threadID: S.threadID, floor});
}
function startMail() {
    openPrompt('收信人 ID: ', '', to => {
        if (!to.trim()) return;
        openPrompt('标题: ', '', title => { if (title.trim()) openEditor('mail', title.trim(), 0, to.trim()); });
    });
}

// ------------------------------------------------------------------ 动作
async function enter() {
    const item = S.items[S.sel];
    switch (S.screen) {
        case 'main': return mainAction(MAIN_MENU[S.sel]);
        case 'sections': if (item) await showBoards(item.id, '分类讨论区 / ' + item.name); return;
        case 'boards': case 'favorites': case 'boardsearch':
            if (item) { if (item.kind === 'dir') await showBoards(item.id, '目录 / ' + item.name); else await showThreads(item.id, 1); }
            return;
        case 'threads': case 'search':
            if (item) await showThread(item.board_id || S.board, Number(item.id), 1);
            return;
        case 'topten':
            if (item) await showThread(item.board_id || S.board || 'NewExpress', Number(item.id || 0), 1);
            return;
        case 'mail': return mailMenuAction(MAIL_MENU[S.sel]);
        case 'inbox': if (item) await openInboxItem(item); return;
        case 'notify': if (item) await openNotifyDetail(item); return;
        case 'mailview': if (S.mail && S.mail.notify) await openNotifyThread(S.mail.notify, false); return;
        case 'settings': return settingsAction(SETTINGS_ITEMS[S.sel] ? SETTINGS_ITEMS[S.sel][0] : '');
        default: return;
    }
}
async function mainAction(m) {
    if (!isMenuEnabled(m)) return;
    switch (m.act) {
        case 'sections': await showSections(); break;
        case 'favorites': await showFavorites(); break;
        case 'mail': await showMailMenu(); break;
        case 'boards': await showBoards('', '所有版列表'); break;
        case 'about': await showAbout(); break;
        case 'settings': await showSettings(); break;
        case 'selectboard': openBoardPicker(); break;
        case 'announce': await showTopTen(); break;
        case 'newboards': await showBoards('', '新开启讨论区'); break;
        case 'chat': toast('聊天选单暂未开放（term 下为呼叫器）'); break;
        case 'second': toast('二站暂不支持'); break;
        case 'quit': askLeave(); break;
        default: break;
    }
}
async function settingsAction(key) {
    switch (key) {
        case 'D': {
            const order = ['demo', 'web', 'openapi'];
            const next = order[(order.indexOf(S.config.source || 'demo') + 1) % order.length];
            await safe(jcall(BbsService.SetSource, next), async () => {
                await refreshConfig(); toast('数据源已切换为 ' + next);
            });
            break;
        }
        case 'L':
            openPrompt('用户名: ', S.config.user || '', u => {
                openPrompt('密码: ', '', p => safe(jcall(BbsService.Login, u, p), async r => {
                    await refreshConfig(); toast((r && r.message) || '登录完成');
                }), true);
            });
            break;
        case 'C':
            openPrompt('粘贴 Cookie: ', '', v => safe(jcall(BbsService.SetCookie, v.trim()), async r => {
                await refreshConfig(); toast((r && r.message) || 'Cookie 已导入');
            }));
            break;
        case 'O': await safe(jcall(BbsService.OpenExternal, 'https://www.newsmth.net/nForum/'), r => toast((r && r.message) || '已打开原站')); break;
        case 'P': await safe(jcall(BbsService.OpenOAuth), r => toast((r && r.message) || '已打开授权页')); break;
        case 'K':
            openPrompt('authorize code: ', '', v => safe(jcall(BbsService.OAuthCode, v.trim()), async r => {
                await refreshConfig(); toast((r && r.message) || '授权成功');
            }));
            break;
        case '1': case '2': case '3': case '4': {
            const fields = {'1': 'client_id', '2': 'client_secret', '3': 'client_signature', '4': 'base_url'};
            const names = {'1': 'APP client_id', '2': 'APP client_secret', '3': '签名密钥 client_signature', '4': '站点地址'};
            openPrompt(names[key] + ': ', S.config[fields[key]] || '', v => {
                safe(jcall(BbsService.SetConfig, JSON.stringify({[fields[key]]: v.trim()})), async () => {
                    S.config = await jcall(BbsService.GetConfig); render();
                    toast('已保存 ' + names[key]);
                });
            });
            break;
        }
        case 'R': {
            const on = S.config.remember_id === false;
            await safe(jcall(BbsService.SetConfig, JSON.stringify({remember_id: on})), async () => {
                await refreshConfig(); toast(on ? '已开启「记住代号」' : '已关闭「记住代号」');
            });
            break;
        }
        case 'A': {
            const on = S.config.auto_login === false;
            await safe(jcall(BbsService.SetConfig, JSON.stringify({auto_login: on})), async () => {
                await refreshConfig(); toast(on ? '已开启「会话复用」' : '已关闭「会话复用」');
            });
            break;
        }
        case 'U': await showLogin(); break;
        case 'W': await safe(jcall(BbsService.OpenExternal, 'https://wiki.open.newsmth.net/')); break;
        case 'Q': await safe(jcall(BbsService.Logout), async r => { await refreshConfig(); toast((r && r.message) || '已退出'); }); break;
        default: break;
    }
}

// ------------------------------------------------------------------ 键盘
async function onKey(e) {
    const k = e.key;
    // 二次确认是模态的：只认 Y / N（Enter=Y，Esc/q=取消），其它键一律吞掉。
    if (S.confirm) {
        e.preventDefault();
        const yes = k === 'y' || k === 'Y' || k === 'Enter';
        const no = k === 'n' || k === 'N' || k === 'Escape' || k === 'q' || k === 'Q';
        const act = S.confirm.onYes;
        if (yes) { S.confirm = null; await act(); }
        else if (no) { S.confirm = null; render(); toast('已取消'); }
        return;
    }
    if (S.prompt) {
        if (S.prompt.kind === 'board' && (k === 'Tab' || k === ' ')) {
            e.preventDefault(); await boardAutocomplete(); return;
        }
        if (k === 'Enter') { e.preventDefault(); closePrompt(true); }
        else if (k === 'Escape') { e.preventDefault(); closePrompt(false); toast('已取消'); }
        return;
    }
    if (S.editor) {
        if ((e.ctrlKey && (k === 's' || k === 'S' || k === 'w' || k === 'W')) || (e.ctrlKey && k === 'Enter')) { e.preventDefault(); await sendEditor(); }
        else if (e.ctrlKey && (k === 'o' || k === 'O')) { e.preventDefault(); await pickAttachments(); }
        else if (k === 'Escape') { e.preventDefault(); closeEditor(); toast('已放弃编辑'); }
        else if (e.ctrlKey && (k === 'q' || k === 'Q')) { e.preventDefault(); toast('Ctrl+W/S 发送  Ctrl+O 加附件  Esc 放弃'); }
        return;
    }
    // 附件预览面板优先消费按键：图片用方向键平移、滚轮/+-缩放；[ ] 切附件。
    if (S.attachOpen) {
        if (k === 'Escape' || k === 'q' || k === 'Q') { e.preventDefault(); closeAttachView(); return; }
        if (k === 'o' || k === 'O') { e.preventDefault(); openCurrentAttachment(); return; }
        if (k === '[') { e.preventDefault(); moveAttach(-1); return; }
        if (k === ']') { e.preventDefault(); moveAttach(1); return; }
        if (zoomImg) {
            const step = 28;
            if (k === 'ArrowUp') { e.preventDefault(); panZoom(0, step); return; }
            if (k === 'ArrowDown') { e.preventDefault(); panZoom(0, -step); return; }
            if (k === 'ArrowLeft') { e.preventDefault(); panZoom(step, 0); return; }
            if (k === 'ArrowRight') { e.preventDefault(); panZoom(-step, 0); return; }
            if (k === '+' || k === '=') { e.preventDefault(); zoomBy(1.2); return; }
            if (k === '-' || k === '_') { e.preventDefault(); zoomBy(1 / 1.2); return; }
            if (k === '0') { e.preventDefault(); applyZoomReset(); return; }
        } else {
            // 非图片附件：左右键仍可切换附件。
            if (k === 'ArrowLeft') { e.preventDefault(); moveAttach(-1); return; }
            if (k === 'ArrowRight') { e.preventDefault(); moveAttach(1); return; }
        }
        return;
    }
    if (S.screen === 'login') { await onLoginKey(e); return; }
    if (S.screen === 'splash') { e.preventDefault(); if (S.autoChecking) return; await showLogin(); return; }
    if (S.screen === 'bye') return;
    if (e.ctrlKey && (k === 'p' || k === 'P')) { e.preventDefault(); S.screen === 'mail' ? startMail() : startPost(); return; }
    if (e.ctrlKey) return;
    // 信件处理菜单的快捷键要先于全局 R/@/L 与 k/K（子上移）处理，否则会被吞掉。
    if (S.screen === 'mail' && k.length === 1) {
        if (k === 'e' || k === 'E') { e.preventDefault(); await back(); return; }
        const idx = MAIL_MENU.findIndex(m => m.key.toLowerCase() === k.toLowerCase());
        if (idx >= 0) {
            e.preventDefault();
            if (!isMenuEnabled(MAIL_MENU[idx])) return;
            S.sel = idx; render(); await mailMenuAction(MAIL_MENU[idx]); return;
        }
    }

    switch (k) {
        case 'ArrowUp': case 'k': case 'K':
            e.preventDefault();
            if (S.screen === 'post') { scrollReader(-1, true); }
            else if (S.screen === 'threads' || S.screen === 'search') { scrollThreadList(-1, true); }
            else if (S.screen === 'settings') { S.sel = (S.sel - 1 + SETTINGS_ITEMS.length) % SETTINGS_ITEMS.length; render(); }
            else move(-1);
            return;
        case 'ArrowDown': case 'j': case 'J':
            e.preventDefault();
            if (S.screen === 'post') { scrollReader(1, true); }
            else if (S.screen === 'threads' || S.screen === 'search') { scrollThreadList(1, true); }
            else if (S.screen === 'settings') { S.sel = (S.sel + 1) % SETTINGS_ITEMS.length; render(); }
            else move(1);
            return;
        case 'ArrowRight': case 'Enter':
            e.preventDefault();
            if (S.screen === 'post') { jumpPost(1); }
            else await enter();
            return;
        case 'ArrowLeft': case 'Escape': case 'q': case 'Q': case 'Backspace':
            e.preventDefault(); await back(); return;
        case 'PageDown': case ' ': case 'f':
            e.preventDefault();
            if (S.screen === 'post') { scrollReader(1); }   // 整篇正文/附件一起平滑往下滚
            else if (S.screen === 'threads' || S.screen === 'search') { scrollThreadList(1); }
            else if (['boards', 'sections', 'topten', 'inbox', 'notify'].includes(S.screen)) pageMove(1);
            else if (S.screen === 'main') await showFavorites();
            return;
        case 'PageUp':
            e.preventDefault();
            if (S.screen === 'post') { scrollReader(-1); }
            else if (S.screen === 'threads' || S.screen === 'search') { scrollThreadList(-1); }
            else pageMove(-1);
            return;
        case 'Home': e.preventDefault();
            if (S.screen === 'post') { goToPostIndex(0, false); render(); return; }
            if (S.screen === 'threads' || S.screen === 'search') {
                S.sel = 0; elThreadList.scrollTo({top: 0, behavior: 'auto'});
                S.listScrollTop = elThreadList.scrollTop; highlightThreadRow(); render(); return;
            }
            if (S.screen === 'main') S.sel = Math.max(0, MAIN_MENU.findIndex(isMenuEnabled));
            else { S.sel = 0; S.top = 0; }
            render(); return;
        case 'End': e.preventDefault();
            if (S.screen === 'post') { goToPostIndex(readerPosts().length - 1, false); render(); return; }
            if (S.screen === 'threads' || S.screen === 'search') {
                S.sel = Math.max(0, S.items.length - 1);
                elThreadList.scrollTo({top: elThreadList.scrollHeight, behavior: 'auto'});
                S.listScrollTop = elThreadList.scrollTop; highlightThreadRow(); render(); return;
            }
            if (S.screen === 'main') {
                for (let i = MAIN_MENU.length - 1; i >= 0; i--) if (isMenuEnabled(MAIN_MENU[i])) { S.sel = i; break; }
            } else {
                S.sel = Math.max(0, S.items.length - 1);
                S.top = Math.max(0, S.sel - LIST_ROWS + 1);
            }
            render(); return;
        default: break;
    }
    if (k.length !== 1) return;
    const ch = k;
    if (S.screen === 'main') {
        const idx = MAIN_MENU.findIndex(m => m.key.toLowerCase() === ch.toLowerCase());
        if (idx >= 0) {
            e.preventDefault();
            // 未实现项的开头快捷键直接吞掉，不选中、也不落到后面的全局快捷键。
            if (!isMenuEnabled(MAIN_MENU[idx])) return;
            S.sel = idx; render(); await mainAction(MAIN_MENU[idx]); return;
        }
    }
    if (S.screen === 'settings') {
        const idx = SETTINGS_ITEMS.findIndex(it => it[0].toLowerCase() === ch.toLowerCase());
        if (idx >= 0) { e.preventDefault(); S.sel = idx; render(); await settingsAction(SETTINGS_ITEMS[idx][0]); return; }
    }
    if (/^[0-9]$/.test(ch)) { e.preventDefault(); await askNumber(); return; }
    switch (ch) {
        case 'h': case '?': e.preventDefault(); await showHelp(); break;
        case 't': case 'T': e.preventDefault(); await showTopTen(); break;
        case 'm': case 'M': e.preventDefault(); await showInbox(0); break;
        case 'o': case 'O':
            e.preventDefault();
            // wap 提醒详情右上角的「看原帖」：进版面跳到那一楼阅读；否则打开设置。
            if (S.screen === 'mailview' && S.mail && S.mail.notify) await openNotifyThread(S.mail.notify, false);
            else await showSettings();
            break;
        case 'u': case 'U': e.preventDefault(); await showLogin(); break;
        case '/': e.preventDefault();
            if (S.screen === 'boardsearch') {
                openPrompt('搜索版名/关键词: ', '', v => { if (v.trim()) showBoardSearch(v.trim()); });
            } else {
                openPrompt('搜索关键词: ', '', v => { if (v.trim()) showSearch(v.trim()); });
            }
            break;
        case 's': case 'S': e.preventDefault(); openBoardPicker(); break;
        case 'v': case 'V':
            e.preventDefault();
            if (S.screen === 'post') openAttachView(0);
            else toast('进入文章后按 v 放大附件预览');
            break;
        case '[': e.preventDefault();
            if (S.screen === 'post') switchInlineAttach(-1);
            break;
        case ']': e.preventDefault();
            if (S.screen === 'post') switchInlineAttach(1);
            break;
        case 'g': case 'G': e.preventDefault(); await askNumber(); break;
        case 'R':
            e.preventDefault();
            if (S.screen === 'threads' || S.screen === 'search') await showNotifies(2);
            else if (S.screen === 'post') startMail();
            else if (S.screen === 'mailview') startMail();
            else toast('回函提醒请在讨论区列表按 R 查看');
            break;
        case '@':
            e.preventDefault();
            if (S.screen === 'threads' || S.screen === 'search') await showNotifies(1);
            else await showNotifies(1);
            break;
        case 'L':
            e.preventDefault();
            await showNotifies(3);
            break;
        case 'r':
            e.preventDefault();
            if (S.screen === 'post') startReply();
            else if (S.screen === 'mailview') {
                // 提醒详情里 r 是「到该版回复该楼」；普通信件仍是回信。
                if (S.mail && S.mail.notify) await openNotifyThread(S.mail.notify, true);
                else startMail();
            }
            else if (S.screen === 'notify') await openNotifyThread(S.items[S.sel], true);
            else if (S.screen === 'threads') {
                S.items = []; S.listLoadedPages = [];
                loadThreads(S.board, 1, false, 'replace').then(warmupThreadPages).then(() => toast('已刷新版面'));
            }
            break;
        case 'a': case 'A':
            e.preventDefault();
            if (['threads', 'post', 'boards', 'favorites'].includes(S.screen)) {
                const b = S.board || (S.items[S.sel] && S.items[S.sel].id) || '';
                await safe(jcall(BbsService.ToggleFavorite, b), async r => { await refreshConfig(); toast((r && r.message) || '已切换收藏'); });
            }
            break;
        case 'd': case 'D': e.preventDefault(); await settingsAction('D'); break;
        case 'w': case 'W': e.preventDefault(); await safe(jcall(BbsService.OpenExternal, 'https://wiki.open.newsmth.net/')); break;
        case 'p': case 'P': e.preventDefault(); startPost(); break;
        default: break;
    }
}
document.addEventListener('keydown', onKey);
// term 习惯是 Ctrl+W 发送；必须在捕获阶段先拦下，避免浏览器把它当关闭窗口。
document.addEventListener('keydown', e => {
    if (S.editor && e.ctrlKey && (e.key === 'w' || e.key === 'W')) {
        e.preventDefault(); e.stopPropagation(); sendEditor();
    }
}, true);

// 鼠标：term 客户端也支持「目前选择」点击（term 网格与可滚列表共用逻辑）
async function onRowClick(e) {
    const row = e.target.closest('[data-row]');
    if (!row || S.prompt || S.editor || S.screen === 'splash') return;
    const idx = Number(row.dataset.row);
    if (S.screen === 'mail') {
        if (idx >= 0 && idx < MAIL_MENU.length && isMenuEnabled(MAIL_MENU[idx])) {
            S.sel = idx; render();
            if (e.detail >= 2) await mailMenuAction(MAIL_MENU[idx]);
        }
        return;
    }
    if (idx >= 0 && idx < S.items.length) {
        const item = S.items[idx];
        // 主菜单未实现项：鼠标单击不选中、双击也不进入。
        if (S.screen === 'main' && !isMenuEnabled(item)) return;
        S.sel = idx;
        // 蓝色底色只跟着鼠标点击走，键盘光标（◆）由 S.sel 独立驱动。
        if (['threads', 'search'].includes(S.screen)) S.listMouseSel = idx;
        highlightThreadRow(); render();
        if (e.detail >= 2) await enter();
    }
}
elScreen.addEventListener('click', onRowClick);
elThreadList.addEventListener('click', onRowClick);

// ------------------------------------------------------------------ 启动
// 红点状态：进入任意版面/刷新配置时后台补一次，失败时静默（不影响浏览）。
async function refreshReminders() {
    if (S.config.source === 'demo') { S.reminders = {mail: 0, at: 0, reply: 0, like: 0}; return; }
    try {
        const r = await jcall(BbsService.Reminders);
        if (r) S.reminders = {mail: r.mail || 0, at: r.at || 0, reply: r.reply || 0, like: r.like || 0};
    } catch (e) { /* 离线时保持上一次状态 */ }
    render();
}

async function refreshConfig() {
    try {
        S.config = await jcall(BbsService.GetConfig) || {};
        S.config.data_dir = await jcall(BbsService.DataDir);
        S.status = await jcall(BbsService.Status, S.board) || {};
    } catch (e) { /* 首次启动可忽略 */ }
    refreshReminders();
    render();
}
// 启动时尝试用本地已保存的会话免登录进入主菜单。
// 返回 true 表示已成功进入；否则回到进站登录屏。
async function tryAutoEnter() {
    let info = null;
    try { info = await jcall(BbsService.SessionInfo); } catch (e) { return false; }
    if (!info || !info.has_session || info.auto_login === false) return false;
    // demo 数据源没有真实会话，不走免登录。
    if (info.source === 'demo') return false;
    // 校验期间给出可见反馈，避免看似卡住；并屏蔽按键，防止与跳转竞态。
    S.screen = 'splash';
    S.splashMsg = ' 正在校验本地会话 ...';
    S.autoChecking = true;
    render();
    let chk = null;
    try { chk = await jcall(BbsService.CheckSession); } catch (e) { chk = {valid: false}; }
    S.splashMsg = '';
    S.autoChecking = false;
    if (chk && chk.valid) {
        await refreshConfig();
        await showMain(false);
        toast('已恢复 ' + (chk.user || info.user || info.last_user_id || '') + ' 的会话');
        return true;
    }
    return false;
}

async function boot() {
    applyFont();
    fit(); render();
    await refreshConfig();
    S.sel = 2;
    const params = new URLSearchParams(location.search);
    // 预览深链：index.html?screen=login / threads / post / help / settings / ...
    // ?screen=login&filled=1 会预填代号与密码，便于和 term 截图做视觉对照。
    const qa = params.get('screen');
    if (qa) {
        S.qa = qa;
        if (qa === 'login') {
            await showLogin();
            if (params.get('filled')) {
                S.login.id = 'ax.e'; S.login.pass = '12345678';
                S.login.field = 'password'; S.login.hint = ''; S.login.error = '';
                render();
            }
            return;
        }
        if (qa === 'splash') return;
        if (qa === 'main') { await showMain(false); return; }
        if (qa === 'threads' || qa === 'post') await showThreads('NewExpress', 1, false);
        if (qa === 'post') await showThread('NewExpress', S.items[0].id, 1);
        if (qa === 'boards') await showBoards('', '所有版列表');
        if (qa === 'sections') await showSections();
        if (qa === 'topten') await showTopTen();
        if (qa === 'mail') await showMailMenu();
        if (qa === 'notify') await showNotifies(Number(params.get('kind')) || 2);
        if (qa === 'help') await showHelp();
        if (qa === 'settings') await showSettings();
        if (qa === 'about') await showAbout();
        if (qa === 'favorites') await showFavorites();
        return;
    }
    // 免登录直达：若本地保存了有效会话，直接进入主菜单，不再每次登录。
    // 校验期间显示 splash；无效/过期才落到 term 进站登录画面。
    const autoEntered = await tryAutoEnter();
    if (!autoEntered) await showLogin();
    setInterval(async () => {
        try { S.status = await jcall(BbsService.Status, S.board) || S.status; render(); }
        catch (e) { /* 忽略 */ }
    }, 15000);
}
boot();
