package wrap

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// refColorHex は最適化前の colorHex 実装 (fmt.Sprintf 版) を参照として保持する。
// 最適化後の colorHex がこれと完全一致することを保証するための golden 基準。
func refColorHex(c color.Color) string {
	if c == nil {
		return ""
	}
	c = resolvePaletteColor(c)
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func TestColorHex_matchesReference(t *testing.T) {
	var cases []color.Color
	cases = append(cases, nil)
	for i := range 256 {
		cases = append(cases, ansi.BasicColor(uint8(i)))
		cases = append(cases, ansi.IndexedColor(uint8(i)))
	}
	// truecolor / RGB direct の代表値 (端・中間・各チャネル単独)。
	rgbs := []ansi.RGBColor{
		{R: 0x00, G: 0x00, B: 0x00},
		{R: 0xff, G: 0xff, B: 0xff},
		{R: 0xff, G: 0x00, B: 0x00},
		{R: 0x00, G: 0xff, B: 0x00},
		{R: 0x00, G: 0x00, B: 0xff},
		{R: 0x12, G: 0x34, B: 0x56},
		{R: 0xab, G: 0xcd, B: 0xef},
		{R: 0x01, G: 0x0f, B: 0x10},
	}
	for _, rc := range rgbs {
		cases = append(cases, rc)
	}
	// image/color.RGBA 直値 (xtermPalette 要素と同型) も確認。
	cases = append(cases, color.RGBA{0x24, 0x72, 0xc8, 0xff})

	for _, c := range cases {
		got := colorHex(c)
		want := refColorHex(c)
		if got != want {
			t.Errorf("colorHex(%#v) = %q, want %q", c, got, want)
		}
	}
}

func BenchmarkColorHex_indexed(b *testing.B) {
	c := ansi.IndexedColor(196)
	b.ReportAllocs()
	for b.Loop() {
		_ = colorHex(c)
	}
}

func BenchmarkColorHex_truecolor(b *testing.B) {
	c := ansi.RGBColor{R: 0x12, G: 0x34, B: 0x56}
	b.ReportAllocs()
	for b.Loop() {
		_ = colorHex(c)
	}
}
