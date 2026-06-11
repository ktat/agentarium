package wrap

import "testing"

func TestHexRGB(t *testing.T) {
	// hexRGB は color.Color.RGBA() の 16bit チャネル値を受ける（上位 8bit を使う）。
	if got := hexRGB(0xffff, 0x0000, 0x0000); got != "#ff0000" {
		t.Fatalf("want #ff0000, got %q", got)
	}
	if got := hexRGB(0x0000, 0xffff, 0x0000); got != "#00ff00" {
		t.Fatalf("want #00ff00, got %q", got)
	}
	if got := hexRGB(0x1234, 0x5678, 0x9abc); got != "#12569a" {
		t.Fatalf("want #12569a, got %q", got)
	}
}

func TestPaletteHex_Populated(t *testing.T) {
	for i, h := range paletteHex {
		if len(h) != 7 || h[0] != '#' {
			t.Fatalf("paletteHex[%d] malformed: %q", i, h)
		}
	}
}
