# Bundled terminal font

`AgentariumTerminalJP-subset.woff2` is bundled and served at
`/terminal/assets/wrap/fonts/` (embedded via `//go:embed assets/*` in
`kernel/terminal/wrap/backend.go`). The wrap renderer's cursor / IME placement
uses a column model (`cursorX × cell width`), which only holds when every glyph
renders at a strict half-width (1.0×) / full-width (2.0×) grid, with
box-drawing kept half-width. System fonts do not guarantee this:

- `ui-monospace` may resolve to a proportional CJK font (ASCII not even 1.0×).
- Generic CJK monospace (e.g. Noto Sans Mono CJK) renders box-drawing as
  full-width, breaking TUI tables/borders.

So we self-host a terminal font (no CDN — OSS / offline requirement). See
`LICENSE` for license and attribution.

## Regenerating the subset

Source: UDEV Gothic (OFL 1.1) — https://github.com/yuru7/udev-gothic

```sh
# 1. Download a UDEV Gothic release and unzip; use UDEVGothic-Regular.ttf
#    (the 1:2 width variant — NOT the "35" 3:5 variant).
# 2. Subset to the JP-oriented Unicode ranges below and rename the name table
#    away from the reserved name "UDEV Gothic" (OFL clause 3).
python3 - <<'PY'
from fontTools.ttLib import TTFont, woff2
from fontTools.subset import Subsetter, Options
ranges = ["0000-00FF","2000-206F","2190-21FF","2300-23FF","2500-25FF",
          "2600-26FF","2700-27BF","3000-30FF","31F0-31FF","4E00-9FFF",
          "FF00-FFEF","3400-34FF"]
us = [c for r in ranges for c in range(int(r.split('-')[0],16), int(r.split('-')[1],16)+1)]
f = TTFont("UDEVGothic-Regular.ttf")
opts = Options(); opts.layout_features=[]; opts.hinting=False; opts.desubroutinize=True; opts.name_IDs=['*']
ss = Subsetter(options=opts); ss.populate(unicodes=us); ss.subset(f)
FAM, PS = "Agentarium Terminal JP", "AgentariumTerminalJP"
for rec in list(f["name"].names):
    if rec.nameID in (1,4,16): rec.string = FAM
    elif rec.nameID == 6:      rec.string = PS
    elif rec.nameID in (2,17): rec.string = "Regular"
f["name"].setName(PS+"-Regular", 3, 3, 1, 0x409)
f.flavor = "woff2"; f.save("AgentariumTerminalJP-subset.woff2")
PY
```

Glyphs outside these ranges (rare kanji, emoji) fall back to the next family in
`--twrap-font` and may have a slightly wrong width; the frequency is low.
