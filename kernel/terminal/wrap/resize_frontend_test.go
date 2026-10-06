package wrap

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// このファイルは assets/index.js の resize 送信 (デバウンス・重複抑止・padding を
// 除いた寸法) を node で実際に動かして検証する。文字列一致だけだと「関数はあるが
// 呼ばれていない」退行を検知できないため、実装のブロックをそのまま切り出して
// 疑似 DOM / 疑似タイマーの上で実行する。node が無い環境では skip する。

// fakeTimerJS は setTimeout / clearTimeout を差し替えた疑似タイマー。
const fakeTimerJS = `
let fakeNow = 0, fakeNextId = 1;
const fakeTimers = new Map();
globalThis.setTimeout = (fn, ms) => {
  const id = fakeNextId++;
  fakeTimers.set(id, { fn: fn, at: fakeNow + (ms || 0) });
  return id;
};
globalThis.clearTimeout = (id) => { fakeTimers.delete(id); };
function timerCount() { return fakeTimers.size; }
function advance(ms) {
  fakeNow += ms;
  for (const [id, t] of [...fakeTimers]) {
    if (t.at <= fakeNow) { fakeTimers.delete(id); t.fn(); }
  }
}
`

func readIndexJS(t *testing.T) string {
	t.Helper()
	js, err := assetsFS.ReadFile("assets/index.js")
	if err != nil {
		t.Fatalf("read assets/index.js: %v", err)
	}
	return string(js)
}

// extractJSBlock は src から header 行で始まるブロックを、indent と同じ
// インデントの閉じ括弧までまるごと切り出す。
func extractJSBlock(t *testing.T, src, indent, header string) string {
	t.Helper()
	i := strings.Index(src, indent+header)
	if i < 0 {
		t.Fatalf("index.js に %q が見つからない", header)
	}
	rest := src[i:]
	closing := "\n" + indent + "}\n"
	end := strings.Index(rest, closing)
	if end < 0 {
		t.Fatalf("%q の終端が見つからない", header)
	}
	return rest[:end+len(closing)]
}

func runNode(t *testing.T, script string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node が無いため skip")
	}
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node 実行に失敗: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// helpersJS は index.js のモジュール直下の resize 用ヘルパを切り出す。
func helpersJS(t *testing.T) string {
	t.Helper()
	src := readIndexJS(t)
	return "const RESIZE_DEBOUNCE_MS = 150;\n" +
		extractJSBlock(t, src, "", "function contentBox(el) {") +
		extractJSBlock(t, src, "", "function debounce(delayMs, fn) {")
}

// cols / altRows は viewport の「文字を置ける幅」(padding を除いた寸法) から
// 出すこと。clientWidth は padding を含むため、そのまま割ると cols が過大になる。
func TestIndexJS_contentBoxSubtractsPadding(t *testing.T) {
	got := runNode(t, helpersJS(t)+`
	const el = { clientWidth: 1473, clientHeight: 1076 };
	globalThis.getComputedStyle = () => ({
	  paddingLeft: '8px', paddingRight: '8px', paddingTop: '4px', paddingBottom: '4px',
	});
	console.log(JSON.stringify(contentBox(el)));
	// padding が取れない環境でも壊れないこと (0 として扱う)。
	globalThis.getComputedStyle = () => ({});
	console.log(JSON.stringify(contentBox(el)));
	console.log(JSON.stringify(contentBox(null)));
	`)
	want := `{"w":1457,"h":1068}` + "\n" + `{"w":1473,"h":1076}` + "\n" + `{"w":0,"h":0}`
	if got != want {
		t.Errorf("結果が違う\n  got  = %s\n  want = %s", got, want)
	}
}

// sendResizeFixtureJS は sendResize (render 内のクロージャ) を単体で動かすための
// 足場。measureCell / positionIme は固定値のスタブ、viewport の padding は
// 左右 10px・上下 0 にしてある (padding を引かない実装だと cols が 2 ずれる)。
const sendResizeFixtureJS = `
globalThis.getComputedStyle = () => ({ paddingLeft: '10px', paddingRight: '10px', paddingTop: '0px', paddingBottom: '0px' });
const root = { clientWidth: 1000, clientHeight: 800 };
function measureCell() { return { w: 10, h: 20 }; }
function positionIme() {}
function scheduleRender() {}
const sentMsgs = [];
const entry = {
  ws: { readyState: 1, send(s) { sentMsgs.push(JSON.parse(s)); } },
  viewportEl: { clientWidth: 820, clientHeight: 600, offsetParent: {} },
  altRows: 40, fontMetric: null, sentCols: null, sentAltRows: null,
};
`

// sendResize は前回と同じ寸法なら送らず、寸法が変わったときと記録を捨てた
// とき (再接続) だけ送ること。cols は padding を除いた幅から出すこと。
func TestIndexJS_sendResizeDedupsAndUsesContentBox(t *testing.T) {
	src := readIndexJS(t)
	block := extractJSBlock(t, src, "  ", "function sendResize() {")
	out := runNode(t, helpersJS(t)+sendResizeFixtureJS+block+`
	const res = {};
	sendResize();
	res.first = sentMsgs.slice();
	sendResize(); // 同じ寸法
	res.dup = sentMsgs.length;
	entry.viewportEl.clientWidth = 420; // 実際に変わる
	sendResize();
	res.changed = sentMsgs.length;
	res.lastCols = sentMsgs[sentMsgs.length - 1].cols;
	entry.sentCols = null; entry.sentAltRows = null; // 再接続 (onopen) 相当
	sendResize();
	res.resent = sentMsgs.length;
	entry.viewportEl.offsetParent = null; // 非表示
	entry.viewportEl.clientWidth = 0;
	sendResize();
	res.hidden = sentMsgs.length;
	// 非表示から表示へ戻る (自分の寸法は非表示前と同じ)。非表示の間に別ウィンドウ
	// が同じ PTY を別の寸法へ変えている可能性があるので、送り直すこと。
	entry.viewportEl.offsetParent = {};
	entry.viewportEl.clientWidth = 420;
	sendResize();
	res.reshown = sentMsgs.length;
	res.reshownCols = sentMsgs[sentMsgs.length - 1].cols;
	sendResize(); // 表示が続く間の同じ寸法は従来どおり省く
	res.reshownDup = sentMsgs.length;
	console.log(JSON.stringify(res));
	`)
	var got struct {
		First []struct {
			Type          string
			Cols, AltRows int
		}
		Dup         int
		Changed     int
		LastCols    int
		Resent      int
		Hidden      int
		Reshown     int
		ReshownCols int
		ReshownDup  int
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("node 出力が JSON でない: %s", out)
	}
	if len(got.First) != 1 || got.First[0].Type != "resize" || got.First[0].Cols != 80 || got.First[0].AltRows != 30 {
		t.Errorf("初回送信がおかしい (cols=80 / altRows=30 を期待。padding を引いていない?): %+v", got.First)
	}
	if got.Dup != 1 {
		t.Errorf("同じ寸法で再送している (累計 %d 件、1 件を期待)", got.Dup)
	}
	if got.Changed != 2 || got.LastCols != 40 {
		t.Errorf("寸法が変わったのに送っていない (累計 %d 件 / cols=%d、2 件 / cols=40 を期待)", got.Changed, got.LastCols)
	}
	if got.Resent != 3 {
		t.Errorf("送信記録を捨てた後 (再接続) に送り直していない (累計 %d 件、3 件を期待)", got.Resent)
	}
	if got.Hidden != 3 {
		t.Errorf("非表示中に送信している (累計 %d 件、3 件を期待)", got.Hidden)
	}
	if got.Reshown != 4 || got.ReshownCols != 40 {
		t.Errorf("非表示→表示で同じ寸法を送り直していない (累計 %d 件 / cols=%d、4 件 / cols=40 を期待。別ウィンドウが PTY を別寸法にしていると取り残される)", got.Reshown, got.ReshownCols)
	}
	if got.ReshownDup != 4 {
		t.Errorf("表示が続く間の同じ寸法を再送している (累計 %d 件、4 件を期待)", got.ReshownDup)
	}
}

// ResizeObserver の通知は 150ms のトレーリングデバウンスでまとめ、途中経過が
// 何度届いても sendResize は最後の 1 回だけ呼ばれること。cancel 後は呼ばれない
// こと (close 後に送らない)。配線 (ResizeObserver に debounce を渡しているか) は
// 文字列で固定する。
func TestIndexJS_resizeObserverIsDebounced(t *testing.T) {
	src := readIndexJS(t)
	if !strings.Contains(src, "const debouncedResize = debounce(RESIZE_DEBOUNCE_MS, sendResize);") ||
		!strings.Contains(src, "new ResizeObserver(() => { scheduleRender(); debouncedResize(); })") {
		t.Error("ResizeObserver がデバウンス経由で sendResize を呼んでいない")
	}
	if !strings.Contains(src, "debouncedResize.cancel();") {
		t.Error("close で保留中の resize を捨てていない")
	}
	out := runNode(t, helpersJS(t)+fakeTimerJS+`
	let calls = 0;
	const d = debounce(RESIZE_DEBOUNCE_MS, () => { calls++; });
	const res = {};
	d(); advance(50); d(); advance(50); d();
	res.beforeQuiet = calls;
	advance(149);
	res.at149 = calls;
	advance(1);
	res.at150 = calls;
	d(); d.cancel(); advance(1000);
	res.afterCancel = calls;
	res.pending = timerCount();
	console.log(JSON.stringify(res));
	`)
	want := `{"beforeQuiet":0,"at149":0,"at150":1,"afterCancel":1,"pending":0}`
	if out != want {
		t.Errorf("デバウンスの挙動が違う\n  got  = %s\n  want = %s", out, want)
	}
}
