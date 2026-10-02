package wrap

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// このファイルは assets/index.js の描画 (仮想スクロール) を node で実際に動かして
// 検証する。render(root, ctx) をそのまま import し、最小限の疑似 DOM (ブロック
// 要素を縦に積むだけのレイアウト)・疑似 WebSocket・疑似 rAF の上で init/update
// メッセージを流し込む。node が無い環境では skip する。

// 疑似 DOM の寸法。行高は .twrap-line の min-height: 1.2em (13px → 15.6px) に
// 合わせて小数にしてある (offsetHeight の整数丸めに頼る実装だと spacer と実体行
// の高さがずれる)。viewport の clientHeight は padding を含む。
const vscrollDomJS = `
const LINE_H = 15.6, CELL_W = 8, PAD_T = 4, PAD_B = 4, PAD_L = 8, VP_W = 816, VP_H = 600;
const stats = { toggles: 0, mutations: 0, created: 0 };
let viewport = null;

class FakeClassList {
  constructor(el) { this.el = el; }
  _get() { return this.el.className.split(/\s+/).filter(Boolean); }
  add(c) { const s = this._get(); if (!s.includes(c)) { s.push(c); this.el.className = s.join(' '); } }
  remove(c) { this.el.className = this._get().filter((x) => x !== c).join(' '); }
  contains(c) { return this._get().includes(c); }
  toggle(c, force) {
    stats.toggles++;
    const has = this.contains(c);
    const want = force === undefined ? !has : !!force;
    if (want && !has) this.add(c); else if (!want && has) this.remove(c);
    return want;
  }
}
class FakeNode {
  constructor() { this.parentNode = null; this.childNodes = []; }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get nextSibling() {
    if (!this.parentNode) return null;
    const s = this.parentNode.childNodes;
    return s[s.indexOf(this) + 1] || null;
  }
  appendChild(c) { return this.insertBefore(c, null); }
  insertBefore(c, ref) {
    if (ref && ref.parentNode !== this) throw new Error('insertBefore: ref is not a child');
    if (c.parentNode) c.parentNode._detach(c);
    if (ref) this.childNodes.splice(this.childNodes.indexOf(ref), 0, c); else this.childNodes.push(c);
    c.parentNode = this;
    stats.mutations++;
    return c;
  }
  removeChild(c) {
    if (!c || c.parentNode !== this) throw new Error('removeChild: not a child');
    this._detach(c);
    return c;
  }
  _detach(c) { this.childNodes.splice(this.childNodes.indexOf(c), 1); c.parentNode = null; stats.mutations++; }
  remove() { if (this.parentNode) this.parentNode._detach(this); }
  get textContent() { return this.childNodes.map((n) => n.textContent).join(''); }
  set textContent(v) {
    for (const c of [...this.childNodes]) this._detach(c);
    if (v) this.appendChild(new FakeText(String(v)));
  }
}
class FakeText extends FakeNode {
  constructor(t) { super(); this.data = t; }
  get textContent() { return this.data; }
  set textContent(v) { this.data = v; }
}
function inFlow(el) {
  return (el instanceof FakeElement) && !el.classList.contains('twrap-cursor') && el.style.position !== 'absolute';
}
function layoutHeight(el) {
  if (el.style.height && /px$/.test(el.style.height)) return parseFloat(el.style.height);
  if (el.classList.contains('twrap-line')) return LINE_H;
  let h = 0;
  for (const c of el.children) if (inFlow(c)) h += layoutHeight(c);
  return h;
}
function layoutTop(el) {
  const p = el.parentNode;
  if (!p) return 0;
  let top = p.classList.contains('twrap-viewport') ? PAD_T : layoutTop(p);
  for (const s of p.children) { if (s === el) break; if (inFlow(s)) top += layoutHeight(s); }
  return top;
}
let pendingScrollEvent = false;
let hidden = false;
let focusCount = 0;
class FakeElement extends FakeNode {
  constructor(tag) {
    super();
    stats.created++;
    this.tagName = tag.toUpperCase();
    this.className = ''; this.style = {}; this.dataset = {}; this.attrs = {}; this.listeners = {};
    this.classList = new FakeClassList(this);
    this._scrollTop = 0;
  }
  get children() { return this.childNodes.filter((n) => n instanceof FakeElement); }
  get lastElementChild() { const c = this.children; return c[c.length - 1] || null; }
  set innerHTML(v) { if (v !== '') throw new Error('innerHTML: only empty string is supported'); this.textContent = ''; }
  setAttribute(k, v) { this.attrs[k] = v; }
  addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); }
  dispatch(t, ev) { for (const fn of this.listeners[t] || []) fn(Object.assign({ target: this, preventDefault() {} }, ev || {})); }
  focus() { focusCount++; }
  get offsetHeight() { return Math.round(layoutHeight(this)); }
  get offsetWidth() { return this.textContent.length * CELL_W; }
  getBoundingClientRect() { return { height: layoutHeight(this), width: this.offsetWidth }; }
  get offsetTop() { return layoutTop(this); }
  get offsetLeft() { return PAD_L; }
  get offsetParent() { return hidden ? null : {}; }
  get clientWidth() { return this.classList.contains('twrap-viewport') || this === rootEl ? VP_W : 0; }
  get clientHeight() { return this.classList.contains('twrap-viewport') || this === rootEl ? VP_H : 0; }
  get scrollHeight() {
    if (!this.classList.contains('twrap-viewport')) return 0;
    let h = PAD_T + PAD_B;
    for (const c of this.children) if (inFlow(c)) h += layoutHeight(c);
    return Math.max(VP_H, Math.round(h));
  }
  get scrollTop() { return this._scrollTop; }
  set scrollTop(v) {
    const max = Math.max(0, this.scrollHeight - this.clientHeight);
    const nv = Math.max(0, Math.min(max, v));
    if (nv !== this._scrollTop) { this._scrollTop = nv; if (this === viewport) pendingScrollEvent = true; }
  }
  scrollIntoView() {
    // block:'end' 相当: 要素の下端を viewport の下端に合わせる。
    viewport.scrollTop = this.offsetTop + layoutHeight(this) - VP_H;
  }
}
const rootEl = new FakeElement('div');

let rafQ = [], rafId = 0;
globalThis.requestAnimationFrame = (fn) => { rafQ.push({ id: ++rafId, fn }); return rafId; };
globalThis.cancelAnimationFrame = (id) => { rafQ = rafQ.filter((r) => r.id !== id); };
// flushFrames は rAF と (ブラウザと同じく非同期に届く) scroll イベントを
// 落ち着くまで回し、何フレーム回ったかを返す。描画→scroll→描画のループが
// 止まらない実装を検知するため上限を設ける。
function flushFrames() {
  let frames = 0;
  while (rafQ.length || pendingScrollEvent) {
    if (++frames > 50) return frames;
    if (pendingScrollEvent) { pendingScrollEvent = false; viewport.dispatch('scroll'); }
    const q = rafQ; rafQ = [];
    for (const r of q) r.fn();
  }
  return frames;
}
const timers = [];
globalThis.setTimeout = (fn) => { timers.push(fn); return timers.length; };
globalThis.clearTimeout = () => {};
globalThis.getComputedStyle = (el) => (el && el.classList && el.classList.contains('twrap-viewport'))
  ? { paddingLeft: PAD_L + 'px', paddingRight: PAD_L + 'px', paddingTop: PAD_T + 'px', paddingBottom: PAD_B + 'px' }
  : {};
globalThis.getSelection = () => ({ toString: () => '' });
let roCallback = null;
globalThis.ResizeObserver = class { constructor(cb) { roCallback = cb; } observe() {} disconnect() {} };
const sockets = [];
globalThis.WebSocket = class {
  constructor(url) { this.url = url; this.readyState = 1; this.sent = []; sockets.push(this); }
  send(s) { this.sent.push(JSON.parse(s)); }
  close() {}
};
globalThis.location = { protocol: 'http:', host: 'localhost' };
globalThis.document = {
  getElementById: () => ({}),
  head: new FakeElement('head'),
  createElement: (t) => new FakeElement(t),
  createTextNode: (t) => new FakeText(t),
};

// 以下はシナリオ側から使うヘルパ。
function linesOf(el, out) {
  out = out || [];
  for (const c of el.children) {
    if (c.classList.contains('twrap-line')) out.push(c); else linesOf(c, out);
  }
  return out;
}
function texts() { return linesOf(gridEl).map((n) => n.textContent.replace(/\n$/, '')); }
function lineNode(text) { return linesOf(gridEl).find((n) => n.textContent.replace(/\n$/, '') === text) || null; }
function rowsMsg(type, n, extra) {
  const lines = [];
  for (let y = 0; y < n; y++) lines.push({ y, runs: [{ t: 'row ' + y }] });
  return Object.assign({ type, mode: 'main', altRows: 38, lines, cursorX: 2, cursorY: n - 1 }, extra || {});
}
function send(msg) { ws.onmessage({ data: JSON.stringify(msg) }); }
function update(lines, extra) { send(Object.assign({ type: 'update', lines, cursorX: 2, cursorY: 4999 }, extra || {})); }
function bottomScrollTop() { return Math.max(0, viewport.scrollHeight - VP_H); }
function userScroll(top) { viewport.scrollTop = top; return flushFrames(); }

const mod = await import('./index.js');
await mod.render(rootEl, { id: 't1' });
viewport = rootEl.children[0];
const gridEl = viewport.children.find((c) => c.classList.contains('twrap-grid'));
const cursorEl = viewport.children.find((c) => c.classList.contains('twrap-cursor'));
const imeEl = rootEl.children.find((c) => c.classList.contains('twrap-ime'));
const ws = sockets[0];
ws.onopen();
`

// runVscrollScenario は assets 配下の JS を一時ディレクトリへ書き出し、疑似 DOM
// の上で scenario を実行して stdout (JSON) を返す。
func runVscrollScenario(t *testing.T, scenario string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node が無いため skip")
	}
	dir := t.TempDir()
	err = fs.WalkDir(assetsFS, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		b, err := assetsFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, filepath.Base(p)), b, 0o600)
	})
	if err != nil {
		t.Fatalf("assets の書き出しに失敗: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harness.js"), []byte(vscrollDomJS+scenario), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, filepath.Join(dir, "harness.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("node 実行に失敗: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func decodeScenario(t *testing.T, out string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("node 出力が JSON でない: %v\n%s", err, out)
	}
}

// 5000 行の grid でも DOM に置く行は可視域 + バッファだけであること。全体の
// 高さ (scrollHeight) は全行ぶんを保ち、末尾追従で最終行が見えていること。
func TestIndexJS_largeGridRendersOnlyVisibleRows(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	const frames = flushFrames();
	const tx = texts();
	console.log(JSON.stringify({
	  frames, lines: tx.length, last: tx[tx.length - 1], hasRow0: tx.includes('row 0'),
	  scrollHeight: viewport.scrollHeight, scrollTop: viewport.scrollTop, bottom: bottomScrollTop(),
	}));
	`)
	var got struct {
		Frames                  int
		Lines                   int
		Last                    string
		HasRow0                 bool
		ScrollHeight, ScrollTop float64
		Bottom                  float64
	}
	decodeScenario(t, out, &got)
	if got.Lines > 300 {
		t.Errorf("DOM の行数が %d (可視域 + バッファの 300 行以下を期待。全行を DOM 化している?)", got.Lines)
	}
	if got.Last != "row 4999" || got.HasRow0 {
		t.Errorf("末尾追従の描画範囲がおかしい: last=%q hasRow0=%v", got.Last, got.HasRow0)
	}
	if want := float64(4 + 4 + 5000*15.6); got.ScrollHeight < want-1 || got.ScrollHeight > want+1 {
		t.Errorf("scrollHeight=%v (全 5000 行ぶんの %v を期待。spacer の高さがずれている)", got.ScrollHeight, want)
	}
	if got.ScrollTop != got.Bottom {
		t.Errorf("末尾にスクロールしていない: scrollTop=%v bottom=%v", got.ScrollTop, got.Bottom)
	}
	if got.Frames > 10 {
		t.Errorf("描画が落ち着くまで %d フレーム (描画→scroll→描画のループ?)", got.Frames)
	}
}

// 可視域内の行の更新は DOM に反映され、可視域外の行の更新は DOM を増やさないこと。
// 手動でスクロールして可視域外だった行が見えるようになったら、最新の内容で
// 描かれること (userScrolled なので次の更新でも末尾へ戻らないこと)。
func TestIndexJS_updateInsideAndOutsideWindow(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	const before = texts().length;
	update([{ y: 4990, runs: [{ t: 'changed 4990' }] }, { y: 10, runs: [{ t: 'changed 10' }] }]);
	flushFrames();
	const res = { before, after: texts().length, inside: !!lineNode('changed 4990'), outside: !!lineNode('changed 10') };
	userScroll(0);
	update([{ y: 4999, runs: [{ t: 'tail' }] }]);
	flushFrames();
	const tx = texts();
	res.top = tx[0]; res.scrolledShows10 = tx.includes('changed 10'); res.stayTop = viewport.scrollTop;
	res.linesAtTop = tx.length;
	console.log(JSON.stringify(res));
	`)
	var got struct {
		Before, After, LinesAtTop int
		Inside, Outside           bool
		Top                       string
		ScrolledShows10           bool
		StayTop                   float64
	}
	decodeScenario(t, out, &got)
	if !got.Inside {
		t.Error("可視域内の行の更新が DOM に反映されていない")
	}
	if got.Outside || got.After != got.Before || got.Before > 300 {
		t.Errorf("可視域外の行が DOM 化されている: outside=%v lines %d→%d", got.Outside, got.Before, got.After)
	}
	if got.Top != "row 0" || !got.ScrolledShows10 {
		t.Errorf("先頭へスクロールしても可視域外だった行が最新内容で描かれていない: top=%q shows10=%v", got.Top, got.ScrolledShows10)
	}
	if got.StayTop != 0 {
		t.Errorf("手動スクロール中の更新で末尾へ戻っている (scrollTop=%v)", got.StayTop)
	}
	if got.LinesAtTop > 300 {
		t.Errorf("先頭表示中の DOM 行数が %d (300 以下を期待)", got.LinesAtTop)
	}
}

// スクロールすると描画範囲が追従し、DOM 化された行が正しい縦位置 (spacer 込み)
// に置かれること。可視域に残る行は同じ DOM ノードを使い続けること (ノードを
// 位置で使い回して中身だけ差し替えると、選択範囲が別の行の文字にすり替わる)。
func TestIndexJS_scrollMovesWindow(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	const keep = lineNode('row 4980');
	const frames = userScroll(viewport.scrollTop - 10 * LINE_H);
	const res = { sameNode: keep !== null && lineNode('row 4980') === keep, frames };
	userScroll(2500 * LINE_H);
	const tx = texts();
	const n = lineNode('row 2500');
	res.has2500 = !!n; res.has2530 = tx.includes('row 2530');
	res.has4999 = tx.includes('row 4999'); res.has0 = tx.includes('row 0');
	res.top2500 = n ? n.offsetTop : -1;
	res.lines = tx.length;
	console.log(JSON.stringify(res));
	`)
	var got struct {
		SameNode, Has2500, Has2530, Has4999, Has0 bool
		Frames, Lines                             int
		Top2500                                   float64
	}
	decodeScenario(t, out, &got)
	if !got.SameNode {
		t.Error("少しスクロールしただけで可視域に残る行の DOM ノードが入れ替わった (選択範囲が壊れる)")
	}
	if !got.Has2500 || !got.Has2530 || got.Has4999 || got.Has0 {
		t.Errorf("中間へスクロールしたときの描画範囲がおかしい: %+v", got)
	}
	if want := 4 + 2500*15.6; got.Top2500 < want-0.01 || got.Top2500 > want+0.01 {
		t.Errorf("row 2500 の offsetTop=%v (%v を期待。spacer の高さがずれている)", got.Top2500, want)
	}
	if got.Lines > 300 {
		t.Errorf("DOM 行数が %d (300 以下を期待)", got.Lines)
	}
	if got.Frames > 10 {
		t.Errorf("スクロール後の描画が %d フレーム続いた (ループ?)", got.Frames)
	}
}

// 入力で末尾へ戻り、カーソルと IME がカーソル行に重なること。カーソル行が
// 描画範囲外 (手動で先頭へスクロールしている / カーソルが遠く上にある) でも
// IME は viewport 内へクランプされた位置に追従すること (直前の位置に取り残さない)。
func TestIndexJS_cursorAndImePosition(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	userScroll(0);
	imeEl.dispatch('keydown', { key: 'a' });
	flushFrames();
	const rowTop = 4 + 4999 * LINE_H;
	const res = {
	  input: ws.sent.filter((m) => m.type === 'input').length,
	  scrollTop: viewport.scrollTop, bottom: bottomScrollTop(),
	  cursorDisplay: cursorEl.style.display, cursorTop: parseFloat(cursorEl.style.top),
	  cursorLeft: parseFloat(cursorEl.style.left), wantCursorTop: rowTop,
	  imeTop: parseFloat(imeEl.style.top), wantImeTop: rowTop - viewport.scrollTop,
	  imeLeft: parseFloat(imeEl.style.left),
	};
	// 手動で先頭へ。カーソル行 (末尾) は DOM 化されていないが IME は下端にクランプ。
	userScroll(0);
	res.scrolledImeTop = parseFloat(imeEl.style.top);
	// 末尾へ戻して (末尾追従)、カーソルが遠く上の行へ移る。IME は上端にクランプ。
	userScroll(bottomScrollTop());
	update([], { cursorY: 10 });
	flushFrames();
	res.farUpImeTop = parseFloat(imeEl.style.top);
	res.farUpScrollTop = viewport.scrollTop;
	console.log(JSON.stringify(res));
	`)
	var got struct {
		Input                                int
		ScrollTop, Bottom                    float64
		CursorDisplay                        string
		CursorTop, CursorLeft, WantCursorTop float64
		ImeTop, WantImeTop, ImeLeft          float64
		ScrolledImeTop, FarUpImeTop          float64
		FarUpScrollTop                       float64
	}
	decodeScenario(t, out, &got)
	if got.Input != 1 {
		t.Fatalf("keydown が input を送っていない (%d 件)", got.Input)
	}
	if got.ScrollTop != got.Bottom {
		t.Errorf("入力で末尾へ戻っていない: scrollTop=%v bottom=%v", got.ScrollTop, got.Bottom)
	}
	if got.CursorDisplay != "block" || abs(got.CursorTop-got.WantCursorTop) > 0.01 || got.CursorLeft != 8+2*8 {
		t.Errorf("カーソルの位置がおかしい: display=%q top=%v (want %v) left=%v (want 24)", got.CursorDisplay, got.CursorTop, got.WantCursorTop, got.CursorLeft)
	}
	if abs(got.ImeTop-got.WantImeTop) > 0.01 || got.ImeLeft != 24 {
		t.Errorf("IME の位置がおかしい: top=%v (want %v) left=%v (want 24)", got.ImeTop, got.WantImeTop, got.ImeLeft)
	}
	if want := 600 - 15.6; abs(got.ScrolledImeTop-want) > 0.01 {
		t.Errorf("カーソル行が描画範囲外 (下) のとき IME が下端にクランプされていない: top=%v (want %v)", got.ScrolledImeTop, want)
	}
	if got.FarUpImeTop != 0 {
		t.Errorf("カーソル行が描画範囲外 (上) のとき IME が上端にクランプされていない: top=%v", got.FarUpImeTop)
	}
}

// alt 画面は altRows 行を全て描き先頭に固定すること。init/snapshot は grid を
// 作り直し、前の内容を残さないこと。
func TestIndexJS_altScreenAndSnapshotClear(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	const alt = [];
	for (let y = 0; y < 38; y++) alt.push({ y, runs: [{ t: 'alt ' + y }] });
	send({ type: 'snapshot', mode: 'alt', altRows: 38, lines: alt, cursorX: 0, cursorY: 5 });
	flushFrames();
	let tx = texts();
	const res = { altLines: tx.length, altFirst: tx[0], altLast: tx[tx.length - 1], altScrollTop: viewport.scrollTop,
	  altCursorTop: parseFloat(cursorEl.style.top), altMain: tx.some((s) => s.startsWith('row ')) };
	send(rowsMsg('snapshot', 100));
	flushFrames();
	tx = texts();
	res.mainLines = tx.length; res.mainLast = tx[tx.length - 1];
	res.stale = tx.some((s) => s.startsWith('alt ') || s === 'row 4999');
	console.log(JSON.stringify(res));
	`)
	var got struct {
		AltLines          int
		AltFirst, AltLast string
		AltScrollTop      float64
		AltCursorTop      float64
		AltMain           bool
		MainLines         int
		MainLast          string
		Stale             bool
	}
	decodeScenario(t, out, &got)
	if got.AltLines != 38 || got.AltFirst != "alt 0" || got.AltLast != "alt 37" || got.AltMain || got.AltScrollTop != 0 {
		t.Errorf("alt 画面の描画がおかしい: %+v", got)
	}
	if want := 4 + 5*15.6; abs(got.AltCursorTop-want) > 0.01 {
		t.Errorf("alt 画面のカーソル位置: top=%v (want %v)", got.AltCursorTop, want)
	}
	if got.MainLines == 0 || got.MainLines > 100 || got.MainLast != "row 99" || got.Stale {
		t.Errorf("snapshot 後に前の内容が残っている: lines=%d last=%q stale=%v", got.MainLines, got.MainLast, got.Stale)
	}
}

// 非表示 (display:none) の間にスクロール位置が失われても、表示に戻ったとき
// (ResizeObserver 発火) に描き直して末尾へ戻ること。仮想スクロールでは描き直さ
// ないと scrollTop=0 の位置に spacer の空白だけが見える。spacer をクリックしても
// ime に focus が戻ること。
func TestIndexJS_reshowRendersAndSpacerClickFocuses(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	viewport._scrollTop = 0; // display:none でスクロール位置が失われた状態
	roCallback();
	flushFrames();
	const res = { scrollTop: viewport.scrollTop, bottom: bottomScrollTop(), last: texts().slice(-1)[0] };
	const target = gridEl.children[0];
	focusCount = 0;
	viewport.dispatch('mouseup', { target });
	res.focus = focusCount;
	console.log(JSON.stringify(res));
	`)
	var got struct {
		ScrollTop, Bottom float64
		Last              string
		Focus             int
	}
	decodeScenario(t, out, &got)
	if got.ScrollTop != got.Bottom || got.Last != "row 4999" {
		t.Errorf("表示に戻ったときに末尾を描き直していない: scrollTop=%v bottom=%v last=%q", got.ScrollTop, got.Bottom, got.Last)
	}
	if got.Focus != 1 {
		t.Errorf("grid 先頭の要素 (spacer) の mouseup で ime に focus が戻らない (%d 回)", got.Focus)
	}
}

// 1 打鍵あたりの描画コストが総行数ではなく可視域の行数で決まること。5000 行の
// セッションで 1 行更新 + 再描画を繰り返し、1 フレームあたりに触る行数
// (classList.toggle の回数) と所要時間を測る。所要時間は環境依存なので
// 記録 (t.Log) のみで、合否は触る行数で判定する。
func TestIndexJS_typingCostScalesWithVisibleRows(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	const N = 200;
	stats.toggles = 0;
	const t0 = performance.now();
	for (let i = 0; i < N; i++) {
	  update([{ y: 4999, runs: [{ t: '> typed ' + 'x'.repeat(i % 50) }] }], { cursorX: 9 + (i % 50) });
	  flushFrames();
	}
	const ms = performance.now() - t0;
	console.log(JSON.stringify({ togglesPerFrame: stats.toggles / N, msPerKey: ms / N }));
	`)
	var got struct {
		TogglesPerFrame float64
		MsPerKey        float64
	}
	decodeScenario(t, out, &got)
	t.Logf("5000 行: 1 打鍵あたり %.3f ms / 行 toggle %.0f 回 (疑似 DOM 上)", got.MsPerKey, got.TogglesPerFrame)
	// 1 打鍵 = update による描画 + scroll 由来の描画 (高々 2 フレーム) を想定。
	if got.TogglesPerFrame > 2*300 {
		t.Errorf("1 打鍵あたり %v 行を触っている (総行数に比例している?)", got.TogglesPerFrame)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
