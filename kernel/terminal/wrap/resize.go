package wrap

import (
	"github.com/creack/pty"
)

// このファイルはクライアント由来の resize 反映を持つ。PTY / emulator の
// lifecycle 本体は process.go、差分 sweep は flush.go を参照。

// ptyRowsLocked は現在の画面モードに対応する PTY rows / emulator height を返す。
// main 画面では VirtualRows()、alt 画面ではクライアントが measure した altRows
// (未設定なら DefaultAltRows)。p.mu 保持前提。
func (p *Process) ptyRowsLocked() int {
	if !p.altScreen {
		return VirtualRows()
	}
	if ar := p.clientAltRows; ar > 0 {
		return ar
	}
	return DefaultAltRows
}

// Resize は cols と altRows をクライアントから受け取って反映する。
// rows は常に VirtualRows() (main-screen 時) または altRows (alt-screen 時)。
//
// 【寸法が変わらない resize は何もしない】クライアントは WS 接続・リロード・
// ResizeObserver のたびに resize を送るため、寸法の変わらない resize が
// 高頻度で届く。vt の Screen.Resize は寸法が同じでも
//   - buf.Touched = nil で全 Touched マークを破棄する
//   - スクロール領域 (DECSTBM) を全画面へ、tabstop を既定へ戻す
//
// ため、素通しすると「反映するものは無いのに子と emulator の状態だけが
// 巻き戻る」。とくに Touched マークの破棄は、子が消したがまだ sweep していない
// 行の消去を永久に観測できなくする (sweepLocked は main 画面では Touched しか
// 見ないため。flush.go 参照)。その行は mainShadow / lastSent に旧内容のまま
// 残り、init snapshot に実在しない行 (幽霊行) として流れ続ける。
//
// 判定は「クライアントの値から導いた寸法 (cols, ptyRowsLocked())」と
// 「emulator の現在の寸法」の比較で行う。alt 画面かどうかで実効高さが変わるので、
// altRows 単体ではなく ptyRowsLocked() の結果を比べるのが要点:
// main 画面なら altRows だけが変わっても実効高さは VirtualRows() のままなので
// 寸法不変と判定される (clientAltRows の更新自体は比較より先に済ませるので、
// 次に alt 画面へ入るときには新しい altRows が使われる)。
//
// pty.Setsize も同時に見送る。TIOCSWINSZ は Linux / Darwin とも winsize が
// 現在値と同一なら何もせず SIGWINCH も送らないため、呼んでも呼ばなくても
// 子から見た挙動は同じで、無駄な syscall が減るだけ。
func (p *Process) Resize(cols, altRows int) error {
	if cols <= 0 {
		return nil
	}
	p.mu.Lock()
	if altRows > 0 {
		p.clientAltRows = altRows
	}
	ptyRows := p.ptyRowsLocked()
	if p.emu != nil && p.emu.Width() == cols && p.emu.Height() == ptyRows {
		p.mu.Unlock()
		return nil
	}
	if p.ptmx != nil {
		_ = pty.Setsize(p.ptmx, &pty.Winsize{Rows: uint16(ptyRows), Cols: uint16(cols)})
	}
	if p.emu != nil {
		p.emu.Resize(cols, ptyRows)
	}
	p.mu.Unlock()
	return nil
}
