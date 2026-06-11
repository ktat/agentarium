package wrap

import (
	"sync/atomic"
	"time"
)

// syncUpdateDebounce は Synchronized Output Mode (DEC 2026) が reset されてから
// flushLoop が broadcast を再開するまでの待ち時間。claude TUI は session resume
// 直後に ~33ms 周期で ?2026h/?2026l を高速発火するため、reset 直後にすぐ flush
// すると tick 瞬間の中途 grid 状態 (= 描画途中の文字列) が流出する。本値以上の
// 連続 reset 状態を確認できた tick でだけ flush することで連続発火中は skip 一択。
const syncUpdateDebounce = 80 * time.Millisecond

// Package wrap はサーバ側 VT エミュレータ（charmbracelet/x/vt）で PTY を「背の高い
// 仮想ターミナル」として描画する wrapper backend を実装する。クライアントは init/
// snapshot で全行を受け取り、以降は update で差分（LineUpdate/Run）のみを受ける。
// 生バイトを配信する xterm backend（kernel/terminal/xterm）と選択可能な第 2 経路。

// defaultVirtualRows は main-screen 時の PTY rows と vt.Emulator height の既定値。
// 5000 行あれば conversation の長さがその範囲を超えない限り Ink の redraw が
// emulator 内で完結し、scrollback 重複が起きない。
const defaultVirtualRows = 5000

// virtualRows は SetVirtualRows で上書きされる現在値 (atomic)。zero value (0)
// は「未設定」を意味し、VirtualRows() が defaultVirtualRows を返す。
//
// 起動時に SetVirtualRows で上書き可能 (Settings UI 経由)。値変更は新規 Start
// からのみ有効 (既存 Process の emulator height は変更されない)。
//
// sync/atomic を経由するのは、将来「ランタイム中の更新」が増えたときの
// data race を未然に防ぐため。get/set とも常に atomic 経由なので、複数
// goroutine からの読み書きが安全 (現状は起動時 set + Process 起動時 read
// だが、将来 SIGHUP 等で reload する余地を残す)。
var virtualRows atomic.Int64

// VirtualRows は現在の仮想 PTY rows を返す。atomic.Load なので Process 起動と
// SetVirtualRows が並行しても data race にならない。未設定 (zero value=0) なら
// defaultVirtualRows にフォールバック。
func VirtualRows() int {
	if v := virtualRows.Load(); v > 0 {
		return int(v)
	}
	return defaultVirtualRows
}

// VirtualRowsMin / Max は SetVirtualRows の有効範囲。最小は意味のある scrollback
// として 500、最大は PTY Winsize.Rows が uint16 のため 60000 (uint16 max=65535 の
// 安全マージン)。Emulator grid 走査コストもこれを超えると顕著に重くなる。
const (
	VirtualRowsMin = 500
	VirtualRowsMax = 60000
)

// SetVirtualRows は VirtualRows の値を上書きする。Settings から prefs 経由で
// 呼ばれる。VirtualRowsMin..VirtualRowsMax の範囲外は無視 (異常値ガード)。
// 既存 Process の emulator height は変更されない。
func SetVirtualRows(n int) {
	if n < VirtualRowsMin || n > VirtualRowsMax {
		return
	}
	virtualRows.Store(int64(n))
}

// DefaultAltRows は alt-screen TUI (vim/less 等) に通告する初期の行数。
// クライアントは自分の viewport から実際の高さを measure して resize で送ってくる。
const DefaultAltRows = 40

// DefaultCols は client から初回 resize が来るまでの仮 cols 値。
const DefaultCols = 120

// Run は line 内の同 style 連続セルをまとめた描画単位 (転送量削減用)。
// JSON タグは短く: t=text, f=fg, b=bg, a=attr bitmask。
type Run struct {
	T string `json:"t"`
	F string `json:"f,omitempty"`
	B string `json:"b,omitempty"`
	A int    `json:"a,omitempty"`
}

// LineUpdate は 1 行ぶんの変更通知。Y は grid 行番号 (0-origin)。
type LineUpdate struct {
	Y    int   `json:"y"`
	Runs []Run `json:"runs"`
}

// WSMessage は server → client の WebSocket メッセージ。
//
//	type=init:     初回接続時の現在 grid と mode
//	type=snapshot: alt-screen 切替に伴う grid 全行 + mode の atomic 再配信
//	type=update:   差分 (変更行のみ)
type WSMessage struct {
	Type    string       `json:"type"`
	Lines   []LineUpdate `json:"lines,omitempty"`
	CursorX int          `json:"cursorX"`
	CursorY int          `json:"cursorY"`
	// CursorHidden は DECTCEM (\x1b[?25l) でカーソルが隠されている間 true。
	// client はブロックカーソル描画の表示/非表示に使う。
	CursorHidden bool   `json:"cursorHidden,omitempty"`
	Cols         int    `json:"cols,omitempty"`
	Rows         int    `json:"rows,omitempty"`
	Mode         string `json:"mode,omitempty"`    // "main" / "alt"
	AltRows      int    `json:"altRows,omitempty"` // alt-screen 中の有効行数
}

// ClientInput は client → server の WebSocket メッセージ。
//
//	type=input:  キー入力 (raw bytes, IME 確定済み)
//	type=paste:  bracketed paste 経由で送りたいテキスト
//	type=resize: cols 変更通知。alt-screen 中なら altRows も
type ClientInput struct {
	Type    string `json:"type"`
	Data    string `json:"data"`
	Cols    int    `json:"cols"`
	AltRows int    `json:"altRows"`
}
