package wrap

import (
	"fmt"
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// snapshotLine: y 行を runs[] に変換。同 style 連続セルを 1 run に圧縮、
// wide char (Width>1) は cell.Width ぶん x を進めて tail cell の二重カウント回避。
// 末尾の空白だけの run は bg 無しなら trim (転送量削減)。
func (p *Process) snapshotLine(y int) []Run {
	var runs []Run
	w := p.emu.Width()
	// run 本文の連結戦略:
	//   - 単一セルの run は curT にセル文字列をゼロコピーで保持 (alloc なし)。
	//   - 同 style セルが 2 つ以上続いたら sb に昇格して連結し、確定時に 1 度だけ
	//     文字列化する (旧実装の cur.T += content は長い run で O(n^2) alloc)。
	// 多くの TUI 行は style 切替が多く単一セル run が大半なので、ゼロコピー経路を
	// 残すことが効く。sb.String() は buf を共有するが、次 run の Reset() で buf=nil
	// になり別 buffer を確保するため、確定済み Run.T は壊れない。
	var sb strings.Builder
	var curT, curF, curB string
	var curA int
	usingSB := false
	haveRun := false
	for x := 0; x < w; {
		c := p.emu.CellAt(x, y)
		step := 1
		var content, fg, bg string
		var a int
		if c == nil || c.Content == "" {
			content = " "
		} else {
			content = c.Content
			// colorHex 内で resolvePaletteColor を呼ぶので、ここでは raw のまま渡す
			// (重複呼び出しを避けて意図を 1 箇所に集約)。
			fg = colorHex(c.Style.Fg)
			bg = colorHex(c.Style.Bg)
			a = cellAttrs(c.Style)
			if c.Width > 1 {
				step = c.Width
			}
		}
		if haveRun && fg == curF && bg == curB && a == curA {
			if !usingSB {
				sb.Reset()
				sb.WriteString(curT)
				usingSB = true
			}
			sb.WriteString(content)
		} else {
			if haveRun {
				t := curT
				if usingSB {
					t = sb.String()
				}
				runs = append(runs, Run{T: t, F: curF, B: curB, A: curA})
			}
			curT, curF, curB, curA = content, fg, bg, a
			usingSB = false
			haveRun = true
		}
		x += step
	}
	if haveRun {
		t := curT
		if usingSB {
			t = sb.String()
		}
		if curB == "" {
			t = strings.TrimRight(t, " ")
		}
		if t != "" || curB != "" {
			runs = append(runs, Run{T: t, F: curF, B: curB, A: curA})
		}
	}
	return runs
}

// colorHex は cell の色を "#rrggbb" に変換する。
//
// indexed color (BasicColor / IndexedColor) は TUI セルの大多数を占めるため、
// 事前計算済みの paletteHex から interned 文字列を返してアロケーションを完全に
// 回避する (flush ごとの hex 化が GC 負荷の主因だった。palette.go 参照)。
// lib の default RGBA() は VGA 系で青が暗すぎ背景に溶けるため、解決済みの
// xterm.js 系 palette を引く。truecolor / その他は手書き hex 化 (fmt 不使用)。
func colorHex(c color.Color) string {
	if c == nil {
		return ""
	}
	// idx 経由で境界を明示し、ライブラリ側の型・値域が将来変わっても
	// out-of-range panic を避ける (範囲外は generic 経路に落とす)。
	// BasicColor は 0-15 のみ xterm.js palette に解決する仕様なので
	// len(xtermPalette) で、IndexedColor は 0-255 全域なので len(paletteHex) で
	// 境界をとる。
	switch v := c.(type) {
	case ansi.BasicColor:
		if idx := int(v); idx >= 0 && idx < len(xtermPalette) {
			return paletteHex[idx]
		}
	case ansi.IndexedColor:
		if idx := int(v); idx >= 0 && idx < len(paletteHex) {
			return paletteHex[idx]
		}
	}
	c = resolvePaletteColor(c)
	r, g, b, _ := c.RGBA()
	return hexRGB(r, g, b)
}

// cellAttrs は uv.Style の Attrs と Underline を 1 つの bitmask に圧縮する。
//
//	1=Bold, 2=Italic, 4=Reverse, 8=Underline, 16=Faint
//
// Faint (SGR 2) は Claude Code TUI が autosuggest を「薄く」表示するのに
// 使っており、ここで拾わないと通常テキストと色が同じで視認区別がつかない。
func cellAttrs(s uv.Style) int {
	a := 0
	if s.Attrs&uv.AttrBold != 0 {
		a |= 1
	}
	if s.Attrs&uv.AttrItalic != 0 {
		a |= 2
	}
	if s.Attrs&uv.AttrReverse != 0 {
		a |= 4
	}
	if s.Underline != 0 {
		a |= 8
	}
	if s.Attrs&uv.AttrFaint != 0 {
		a |= 16
	}
	return a
}

// runsKey は変更検知用の文字列キー (JSON より軽い)。
func runsKey(runs []Run) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.F)
		sb.WriteByte('|')
		sb.WriteString(r.B)
		sb.WriteByte('|')
		fmt.Fprintf(&sb, "%d", r.A)
		sb.WriteByte(':')
		sb.WriteString(r.T)
		sb.WriteByte('\x1e')
	}
	return sb.String()
}
