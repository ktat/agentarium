module github.com/ktat/agentarium

go 1.26.2

require (
	github.com/charmbracelet/ultraviolet v0.0.0-20260303162955-0b88c25f3fff
	github.com/charmbracelet/x/ansi v0.11.7
	github.com/charmbracelet/x/vt v0.0.0-20260527151214-009e6338d40d
	github.com/creack/pty v1.1.24
	github.com/gomarkdown/markdown v0.0.0-20260417124207-7d523f7318df
	github.com/gorilla/websocket v1.5.3
	github.com/microcosm-cc/bluemonday v1.0.27
)

require (
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.2 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

// charmbracelet/x/ansi の OSC 0 / UTF-8 multi-byte (0x9C 誤認) バグ回避フォーク。
// wrap renderer がセッション名を input 行へ echo する現象の根本対策。
// TODO: upstream charmbracelet/x が UTF-8 修正を取り込んだら本 replace を外し、
// require を upstream バージョンへ戻す。
replace github.com/charmbracelet/x/ansi => github.com/ktat/x/ansi v0.0.0-20260604104346-6d0878b777c6
