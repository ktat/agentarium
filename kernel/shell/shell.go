// Package shell はカーネルのフロントシェル（index.html / app.js / app.css）を
// embed.FS の実ファイルとして公開する。
package shell

import (
	"embed"
	"io/fs"
)

//go:embed assets/*
var files embed.FS

// FS は assets/ をルートに持つファイルシステムを返す（index.html 等がトップに来る）。
func FS() fs.FS {
	sub, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}
