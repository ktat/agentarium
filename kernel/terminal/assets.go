package terminal

import "io/fs"

// EmptyAssets は何も格納されていない fs.FS を返す（Backend.Assets の nil 安全
// fallback）。フロント資産がまだ用意されていない開発段階の Backend や、
// 実 assets を持たない backend で interface 充足のために使う。
func EmptyAssets() fs.FS { return emptyFS{} }

type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) { return nil, fs.ErrNotExist }
