package wrap

import "testing"

// このファイルは仮想スクロール下の選択・コピーと、grid が縮んだときの描画範囲を
// vscroll_frontend_test.go と同じ疑似 DOM (runVscrollScenario) の上で検証する。

// 選択の端点の行は描画範囲から外れても DOM に残すこと。末尾追従中に数行を
// 選択し、その後 100 行以上流れても選択が潰れないこと。コピーは描画範囲外の
// 行も含めて grid から組み立てること (DOM に無い行が抜けない)。
func TestIndexJS_selectionSurvivesStreamingAndCopiesWholeRange(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 3000, { cursorY: 2999 }));
	flushFrames();
	const a = lineNode('row 2990'), b = lineNode('row 2995');
	// row 2990 の 2 文字目から row 2995 の 3 文字目までを選択 (span > text)。
	fakeSelection.setBaseAndExtent(a.firstChild.firstChild, 2, b.firstChild.firstChild, 3);
	const before = fakeSelection.toString();
	const lines = [];
	for (let y = 3000; y < 3150; y++) lines.push({ y, runs: [{ t: 'row ' + y }] });
	update(lines, { cursorY: 3149 });
	flushFrames();
	const res = { before, after: fakeSelection.toString(), anchorKept: a.parentNode !== null && fakeSelection.anchorNode === a.firstChild.firstChild,
	  focusKept: b.parentNode !== null, lines: texts().length, last: texts().slice(-1)[0],
	  bottom: viewport.scrollTop === bottomScrollTop() };
	// コピーは grid から組み立てる。
	let copied = null, prevented = false;
	document.dispatch('copy', { clipboardData: { setData: (k, v) => { if (k === 'text/plain') copied = v; } }, preventDefault() { prevented = true; } });
	res.copied = copied; res.prevented = prevented;
	// 選択が端末の外 (何も無い) ならコピーに介入しない。
	fakeSelection.removeAllRanges();
	copied = null; prevented = false;
	document.dispatch('copy', { clipboardData: { setData: (k, v) => { copied = v; } }, preventDefault() { prevented = true; } });
	res.outsideCopied = copied; res.outsidePrevented = prevented;
	console.log(JSON.stringify(res));
	`)
	var got struct {
		Before, After, Last   string
		AnchorKept, FocusKept bool
		Lines                 int
		Bottom                bool
		Copied                *string
		Prevented             bool
		OutsideCopied         *string
		OutsidePrevented      bool
	}
	decodeScenario(t, out, &got)
	if got.Before != "w 2990\nrow 2991\nrow 2992\nrow 2993\nrow 2994\nrow" {
		t.Fatalf("前提: 選択直後の文字列がおかしい: %q", got.Before)
	}
	if got.After == "" || !got.AnchorKept || !got.FocusKept {
		t.Errorf("流れた後に選択が潰れた: after=%q anchorKept=%v focusKept=%v", got.After, got.AnchorKept, got.FocusKept)
	}
	if got.Last != "row 3149" || !got.Bottom || got.Lines > 310 {
		t.Errorf("端点を残したせいで描画範囲が崩れた: last=%q bottom=%v lines=%d", got.Last, got.Bottom, got.Lines)
	}
	if got.Copied == nil || *got.Copied != got.Before || !got.Prevented {
		t.Errorf("コピーが選択範囲の全行になっていない: copied=%v prevented=%v (want %q)", got.Copied, got.Prevented, got.Before)
	}
	if got.OutsideCopied != nil || got.OutsidePrevented {
		t.Errorf("端末外の選択のコピーに介入している: %v %v", got.OutsideCopied, got.OutsidePrevented)
	}
}

// 選択の端点を残した行は、描画範囲外の本来の位置に置けないので spacer の高さで
// 吸収し、全体の高さ (scrollHeight) と可視行の縦位置を変えないこと。手前に
// 残した行の位置にカーソル/IME を合わせないこと。
func TestIndexJS_pinnedSelectionRowsKeepLayout(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	const a = lineNode('row 4990');
	fakeSelection.setBaseAndExtent(a.firstChild.firstChild, 0, a.firstChild.firstChild, 3);
	userScroll(2500 * LINE_H);
	const n = lineNode('row 2500');
	const res = { pinned: a.parentNode !== null, scrollHeight: viewport.scrollHeight, top2500: n ? n.offsetTop : -1 };
	// カーソル行 (row 4990) は端点として DOM にあるが描画範囲外。
	update([], { cursorY: 4990 });
	flushFrames();
	res.cursorDisplay = cursorEl.style.display;
	res.imeTop = parseFloat(imeEl.style.top);
	console.log(JSON.stringify(res));
	`)
	var got struct {
		Pinned                bool
		ScrollHeight, Top2500 float64
		CursorDisplay         string
		ImeTop                float64
	}
	decodeScenario(t, out, &got)
	if !got.Pinned {
		t.Error("選択の端点の行が DOM から外された")
	}
	if want := float64(4 + 4 + 5000*15.6); abs(got.ScrollHeight-want) > 1 {
		t.Errorf("scrollHeight=%v (want %v)。残した行の高さを spacer で吸収していない", got.ScrollHeight, want)
	}
	if want := 4 + 2500*15.6; abs(got.Top2500-want) > 0.01 {
		t.Errorf("row 2500 の offsetTop=%v (want %v)", got.Top2500, want)
	}
	if got.CursorDisplay != "none" {
		t.Errorf("描画範囲外のカーソル行 (端点として残した行) にカーソルを出している: %q", got.CursorDisplay)
	}
	if want := 600 - 15.6; abs(got.ImeTop-want) > 0.01 {
		t.Errorf("IME が端点の行の仮の位置に合わせられている: top=%v (want %v)", got.ImeTop, want)
	}
}

// 手動でスクロールしている間に grid が縮んだとき、最初のフレームで見える範囲に
// 行があること (縮む前の scrollTop のまま範囲を決めると、ブラウザが scrollTop を
// 詰めた先が spacer の空白になる)。
func TestIndexJS_shrinkWhileScrolledBackHasNoBlankFrame(t *testing.T) {
	out := runVscrollScenario(t, `
	send(rowsMsg('init', 5000));
	flushFrames();
	userScroll(4500 * LINE_H);
	const lines = [];
	for (let y = 1000; y < 5000; y++) lines.push({ y, runs: [] });
	update(lines, { cursorY: 999 });
	runOneFrame();
	const top = viewport.scrollTop;
	const firstVisible = Math.floor((top - PAD_T) / LINE_H);
	const tx = texts();
	const missing = [];
	for (let y = Math.max(0, firstVisible); y <= Math.min(999, firstVisible + Math.ceil(VP_H / LINE_H)); y++) {
	  if (!tx.includes('row ' + y)) missing.push(y);
	}
	console.log(JSON.stringify({ top, firstVisible, missing: missing.length, lines: tx.length }));
	`)
	var got struct {
		Top          float64
		FirstVisible int
		Missing      int
		Lines        int
	}
	decodeScenario(t, out, &got)
	if got.Missing != 0 {
		t.Errorf("縮んだ直後のフレームで見える行が %d 行欠けている (空白が見える): %+v", got.Missing, got)
	}
}
