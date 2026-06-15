package observer

import "sync"

// lineBuffer は PTY 入出力ストリームを行単位に分割する。
// 行確定 (\r または \n) ごとに onLine を呼ぶ。BS(0x08)/DEL(0x7f) は末尾 1 文字削除。
// CSI(ESC[ … 終端 0x40-0x7e) と文字列シーケンス(OSC ESC] / DCS ESC P / PM ESC ^ /
// APC ESC _、終端 BEL または ST) は payload ごと読み飛ばす（OSC8 payload 混入防止）。
type lineBuffer struct {
	mu       sync.Mutex
	buf      []byte
	onLine   func(string)
	escState escapeState
}

type escapeState int

const (
	escNone escapeState = iota
	escESC
	escCSI
	escString
	escStringESC
)

func newLineBuffer(onLine func(string)) *lineBuffer {
	return &lineBuffer{onLine: onLine}
}

func (b *lineBuffer) Feed(data []byte) {
	b.mu.Lock()
	var lines []string
	for _, c := range data {
		switch b.escState {
		case escESC:
			switch c {
			case '[':
				b.escState = escCSI
			case ']', 'P', '^', '_':
				b.escState = escString
			default:
				b.escState = escNone
			}
			continue
		case escCSI:
			if c >= 0x40 && c <= 0x7e {
				b.escState = escNone
			}
			continue
		case escString:
			switch c {
			case 0x07:
				b.escState = escNone
			case 0x1b:
				b.escState = escStringESC
			}
			continue
		case escStringESC:
			switch c {
			case '\\':
				b.escState = escNone
			case 0x1b:
				// ESC 連続: ST 候補として待機継続
			default:
				b.escState = escString
			}
			continue
		}

		switch c {
		case '\r', '\n':
			lines = append(lines, string(b.buf))
			b.buf = b.buf[:0]
		case 0x08, 0x7f:
			if n := len(b.buf); n > 0 {
				b.buf = b.buf[:n-1]
			}
		case 0x1b:
			b.escState = escESC
		default:
			if c >= 0x20 {
				b.buf = append(b.buf, c)
			}
		}
	}
	onLine := b.onLine
	b.mu.Unlock()
	for _, line := range lines {
		onLine(line)
	}
}
