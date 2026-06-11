package wrap

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// refSnapshotLine は最適化前の snapshotLine 実装。最適化後の snapshotLine が
// これと完全一致する run 列を返すことを保証するための golden 基準。
func refSnapshotLine(p *Process, y int) []Run {
	var runs []Run
	var cur *Run
	w := p.emu.Width()
	for x := 0; x < w; {
		c := p.emu.CellAt(x, y)
		step := 1
		var content, fg, bg string
		var a int
		if c == nil || c.Content == "" {
			content = " "
		} else {
			content = c.Content
			fg = colorHex(c.Style.Fg)
			bg = colorHex(c.Style.Bg)
			a = cellAttrs(c.Style)
			if c.Width > 1 {
				step = c.Width
			}
		}
		if cur != nil && fg == cur.F && bg == cur.B && a == cur.A {
			cur.T += content
		} else {
			if cur != nil {
				runs = append(runs, *cur)
			}
			cur = &Run{T: content, F: fg, B: bg, A: a}
		}
		x += step
	}
	if cur != nil {
		if cur.B == "" {
			cur.T = strings.TrimRight(cur.T, " ")
		}
		if cur.T != "" || cur.B != "" {
			runs = append(runs, *cur)
		}
	}
	return runs
}

func TestSnapshotLine_matchesReference(t *testing.T) {
	cases := []struct {
		name  string
		write string // emu に書き込む内容 (行0に表示される想定)
	}{
		{"empty", ""},
		{"plain ascii", "hello world"},
		{"trailing spaces (trim)", "abc        "},
		{"color runs", "\x1b[31mred\x1b[32mgreen\x1b[0mplain"},
		{"bg color keeps trailing", "\x1b[44mblue bg   \x1b[0m"},
		{"attrs bold italic underline", "\x1b[1mbold\x1b[0m \x1b[3mital\x1b[0m \x1b[4mund\x1b[0m"},
		{"faint", "\x1b[2mfaint text\x1b[0m"},
		{"256-indexed fg/bg", "\x1b[38;5;196m\x1b[48;5;21mx256\x1b[0m"},
		{"truecolor", "\x1b[38;2;18;52;86mtruecolor\x1b[0m"},
		{"reverse", "\x1b[7mrev\x1b[0m"},
		{"wide cjk", "あいう test"},
		{"mixed long same-style run", strings.Repeat("a", 200)},
		{"alternating style", "\x1b[31ma\x1b[32mb\x1b[31mc\x1b[32md\x1b[0m"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(216, 50)
			p := &Process{emu: emu}
			if tc.write != "" {
				_, _ = emu.Write([]byte(tc.write))
			}
			got := p.snapshotLine(0)
			want := refSnapshotLine(p, 0)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("snapshotLine mismatch\n got=%#v\nwant=%#v", got, want)
			}
		})
	}
}

func BenchmarkSnapshotLine(b *testing.B) {
	emu := vt.NewEmulator(216, 50)
	// 実運用に近い 1 行: 長い同 style ラン + 数回の色切替 + 末尾空白。
	line := strings.Repeat("status ", 20) + "\x1b[31merror\x1b[32m ok\x1b[0m   "
	_, _ = emu.Write([]byte(line))
	p := &Process{emu: emu}
	b.ReportAllocs()
	for b.Loop() {
		_ = p.snapshotLine(0)
	}
}

// BenchmarkSnapshotLine_shortRuns は 1 セルごとに style が変わる行 (シンタックス
// ハイライト等で頻出)。run が全て長さ 1 になり、ゼロコピー経路の有無で差が出る。
func BenchmarkSnapshotLine_shortRuns(b *testing.B) {
	emu := vt.NewEmulator(216, 50)
	var sb strings.Builder
	for i := range 100 {
		// 赤/緑を 1 文字ごとに交互に切替。
		if i%2 == 0 {
			sb.WriteString("\x1b[31m")
		} else {
			sb.WriteString("\x1b[32m")
		}
		sb.WriteByte('x')
	}
	sb.WriteString("\x1b[0m")
	_, _ = emu.Write([]byte(sb.String()))
	p := &Process{emu: emu}
	b.ReportAllocs()
	for b.Loop() {
		_ = p.snapshotLine(0)
	}
}
