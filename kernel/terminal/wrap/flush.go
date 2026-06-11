package wrap

import (
	"time"
)

// このファイルは Process の flush パイプライン (100ms tick の差分 sweep と
// broadcast) を実装する。描画ヘルパ (snapshotLine / colorHex 等) は render.go、
// PTY / emulator の lifecycle は process.go を参照。

// idleFlushDecimation: 購読者ゼロの Process が sweep を行う間隔 (tick 数)。
// 100ms tick × 10 = 実効 1Hz。
const idleFlushDecimation = 10

func (p *Process) hasSubscribers() bool {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	return len(p.subs) > 0
}

// flushLoop: 100ms ごとに差分を broadcast。
//
//	alt-screen 中: 全行 sweep (lib Touched が less `>` 等を mark し損ねる回避)
//	main-screen: Touched() + lastSent diff (5000 行毎回 sweep は重い)
//
// 購読者 (Claude タブ / AgentsView の WS) がいない Process は sweep を
// idleFlushDecimation tick に 1 回へ間引く。snapshotLine の全行 sweep が
// CPU の支配項であり、broadcast 先が無い間に毎 tick 回す意味がないため。
// ゼロにせず低頻度で回し続けるのは、buildSnapshot / onAltScreenChange が
// 参照する lastSent / mainShadow の鮮度を最大 ~1s に保ち、再購読時の表示が
// 古くならないようにするため。購読が付けば次 tick からフルレートに戻る。
// 判定は p.mu 外で行うため、判定直後に subscribe された tick だけは取りこぼし
// 最大 1 tick (100ms) 余分に遅れるが、接続時は Snapshot が sweep 済みの最新
// grid を返すため表示の古さにはならず、厳密化 (kick 配線や lock 内判定) の
// 複雑さに見合わないと判断して許容する。
func (p *Process) flushLoop() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	// lastCX/lastCY/lastCH: 最後に broadcast したカーソル状態。行変更が無い tick
	// でもカーソルだけ動いたら (シェルでの矢印キー移動や DECTCEM 切替) update を
	// 送るための比較基準。
	lastCX, lastCY, lastCH, idleTick := 0, 0, false, 0
	for range t.C {
		// 間引き判定は p.mu の外 (subMu のみ) で行う。間引かれた tick は
		// closed 判定もスキップするため、購読者ゼロで閉じた Process の
		// goroutine 終了は最大 ~1s 遅れるが、その間 sweep はしないので無害。
		if !p.hasSubscribers() {
			idleTick++
			if idleTick%idleFlushDecimation != 0 {
				continue
			}
		} else {
			idleTick = 0
		}
		p.mu.Lock()
		if p.closed || p.emu == nil {
			p.mu.Unlock()
			return
		}
		// Synchronized Output Mode (DEC 2026) 中は client に途中 frame を送らない。
		// claude TUI は \x1b[?2026h ... \x1b[?2026l を ~33ms 周期で繰り返すため、
		// 単純な `if syncUpdate` だけでは tick 瞬間に偶然 false になった中途 grid を
		// 流してしまう。reset 後 syncUpdateDebounce (~80ms) 連続して false を保てた
		// tick でだけ flush することで、最終 frame のみ送る挙動になる。
		// (本来追っていた session 名残骸は OSC fix で消えた。詳細は Process.syncUpdate
		// のコメント参照。)
		if p.syncUpdate || time.Since(p.syncUpdateLastReset) < syncUpdateDebounce {
			p.mu.Unlock()
			continue
		}
		lines := p.sweepLocked()
		cx, cy, ch := p.cursorX, p.cursorY, p.cursorHidden
		p.mu.Unlock()
		if len(lines) == 0 && cx == lastCX && cy == lastCY && ch == lastCH {
			continue
		}
		lastCX, lastCY, lastCH = cx, cy, ch
		p.broadcast(WSMessage{
			Type:         "update",
			Lines:        lines,
			CursorX:      cx,
			CursorY:      cy,
			CursorHidden: ch,
		})
	}
}

// sweepLocked は flushLoop 1 tick ぶんの差分 sweep を行う (p.mu 保持前提)。
// 変更行の lastSent / mainShadow を更新し、broadcast すべき行を返す。
// flushLoop のほか、Snapshot が接続時に最新状態を映すためにも呼ぶ。
func (p *Process) sweepLocked() []LineUpdate {
	var lines []LineUpdate
	if p.altScreen {
		ar := p.clientAltRows
		if ar <= 0 {
			ar = DefaultAltRows
		}
		if ar > p.emu.Height() {
			ar = p.emu.Height()
		}
		for y := 0; y < ar; y++ {
			runs := p.snapshotLine(y)
			key := runsKey(runs)
			if p.lastSent[y] == key {
				continue
			}
			p.lastSent[y] = key
			lines = append(lines, LineUpdate{Y: y, Runs: runs})
		}
	} else {
		touched := p.emu.Touched()
		for y, ld := range touched {
			if ld == nil || y < 0 || y >= VirtualRows() {
				continue
			}
			// 採用した行の Touched マークをここで消費する。lib はマークを
			// resize 時にしか消さないため、消費しないと「過去に変更された全行」
			// が毎 tick 再 sweep され続け、バッファ満杯後はスクロール 1 回で
			// 全行再マークされる仕様と相まって定常 CPU の支配項になる。
			// Touched() は emulator 内部スライスをそのまま返すので、エントリを
			// nil に戻せば次の変更まで sweep 対象から外れる (次の書き込みで
			// TouchLine が LineData を作り直す)。emu への書き込み (readPump) と
			// 本関数は同じ p.mu 下で動くため、読み取り〜クリア間にマークを
			// 取りこぼすことはない。上の guard で skip した範囲外の行は消費
			// しない (VirtualRows と emulator 行数がズレた場合に「まだ送って
			// いない変更」を落とさないため。残っても skip を通るだけで無害)。
			// NOTE: lib が将来 Touched() でコピーを返すよう変わるとこの消費は
			// 静かに無効化される (壊れないが定常 sweep が全行に戻る)。その退行
			// は TestSweepLocked_consumesTouchedMarks が検知する。
			touched[y] = nil
			runs := p.snapshotLine(y)
			key := runsKey(runs)
			if p.lastSent[y] == key {
				continue
			}
			p.lastSent[y] = key
			if len(runs) == 0 {
				delete(p.mainShadow, y)
			} else {
				p.mainShadow[y] = runs
			}
			lines = append(lines, LineUpdate{Y: y, Runs: runs})
		}
	}
	return lines
}
