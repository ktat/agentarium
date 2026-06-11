package wrap

import (
	"image/color"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// xterm.js (VS Code Dark) のデフォルト ANSI 16 色 palette。
// charmbracelet/x/vt の default palette は VGA 系で青 (#0000aa) が背景の dark grey
// に対して視認性が低い。xterm.js / VS Code Dark に揃えると、過去 xterm.js 経路で
// 慣れていた color に近くなり、リンクや diff の青が読みやすくなる。
var xtermPalette = [...]color.Color{
	color.RGBA{0x00, 0x00, 0x00, 0xff}, // 0 black
	color.RGBA{0xcd, 0x31, 0x31, 0xff}, // 1 red
	color.RGBA{0x0d, 0xbc, 0x79, 0xff}, // 2 green
	color.RGBA{0xe5, 0xe5, 0x10, 0xff}, // 3 yellow
	color.RGBA{0x24, 0x72, 0xc8, 0xff}, // 4 blue
	color.RGBA{0xbc, 0x3f, 0xbc, 0xff}, // 5 magenta
	color.RGBA{0x11, 0xa8, 0xcd, 0xff}, // 6 cyan
	color.RGBA{0xe5, 0xe5, 0xe5, 0xff}, // 7 white
	color.RGBA{0x66, 0x66, 0x66, 0xff}, // 8 bright black
	color.RGBA{0xf1, 0x4c, 0x4c, 0xff}, // 9 bright red
	color.RGBA{0x23, 0xd1, 0x8b, 0xff}, // 10 bright green
	color.RGBA{0xf5, 0xf5, 0x43, 0xff}, // 11 bright yellow
	color.RGBA{0x3b, 0x8e, 0xea, 0xff}, // 12 bright blue
	color.RGBA{0xd6, 0x70, 0xd6, 0xff}, // 13 bright magenta
	color.RGBA{0x29, 0xb8, 0xdb, 0xff}, // 14 bright cyan
	color.RGBA{0xe5, 0xe5, 0xe5, 0xff}, // 15 bright white
}

func applyXtermPalette(emu *vt.Emulator) {
	for i, c := range xtermPalette {
		emu.SetIndexedColor(i, c)
	}
}

const hexDigits = "0123456789abcdef"

// hexRGB は color.Color.RGBA() が返す 16bit チャネル値から "#rrggbb" を組む。
// fmt.Sprintf を避け、結果文字列ぶんの 1 アロケーションのみに抑える
// (flush 経路の Sprintf が GC 負荷の主因だった)。
func hexRGB(r, g, b uint32) string {
	r8, g8, b8 := byte(r>>8), byte(g>>8), byte(b>>8)
	buf := [7]byte{
		'#',
		hexDigits[r8>>4], hexDigits[r8&0x0f],
		hexDigits[g8>>4], hexDigits[g8&0x0f],
		hexDigits[b8>>4], hexDigits[b8&0x0f],
	}
	return string(buf[:])
}

// paletteHex[i] は indexed color (0-255) に対する colorHex の戻り "#rrggbb" を
// 事前計算した interned 文字列。indexed color は TUI セルの大多数を占めるため、
// flush ごとの hex 化アロケーションを完全に回避できる。i<16 は xtermPalette、
// 16-255 は ansi.IndexedColor の標準 256色 palette に従い、resolvePaletteColor +
// RGBA と同じ結果になる。
var paletteHex = func() [256]string {
	var t [256]string
	for i := range t {
		var c color.Color
		if i < len(xtermPalette) {
			c = xtermPalette[i]
		} else {
			c = ansi.IndexedColor(uint8(i))
		}
		r, g, b, _ := c.RGBA()
		t[i] = hexRGB(r, g, b)
	}
	return t
}()

// resolvePaletteColor は cell.Style.Fg/Bg が ansi の indexed color (BasicColor
// 16 色 / IndexedColor 256 色) の場合に xterm.js 系 palette に置き換える。
// lib の BasicColor.RGBA() は固定の default (VGA 系) を返すため、SetIndexedColor
// で設定した palette は反映されない。client に hex を送る直前で解決する。
// RGB direct color (TrueColor / RGBColor 等) はそのまま返す。
func resolvePaletteColor(c color.Color) color.Color {
	if c == nil {
		return nil
	}
	switch v := c.(type) {
	case ansi.BasicColor:
		if int(v) < len(xtermPalette) {
			return xtermPalette[int(v)]
		}
	case ansi.IndexedColor:
		if int(v) < len(xtermPalette) {
			return xtermPalette[int(v)]
		}
		// 16-255 は ansi.IndexedColor.RGBA() の xterm 256-color 標準 palette を使う
		// (背景に対する読みやすさが十分なので独自上書きは不要)。
	}
	return c
}
