// Package store はプラグイン/カーネルが小さな状態を JSON ファイルへ
// atomic に永続化するための汎用ストアを提供する。消費者 main が
// plugin ごとに別パスを渡して名前空間を分ける（D7 消費モデル）。
package store

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// JSONStore[T] は []T を JSON ファイルへ atomic に永続化する汎用ストア。
//
// 設計:
//   - Save: tmp ファイルへ書いて rename（atomic）、さらに親ディレクトリを fsync して
//     rename の耐久性を上げる（kernel crash 後も rename が残る可能性を高める）
//   - Load: パース失敗時は破損ファイルを <path>.bak へ退避して空で続行（quarantine）
type JSONStore[T any] struct {
	path string
	mu   sync.Mutex
}

// New は path をバッキングファイルとする JSONStore を返す。
func New[T any](path string) *JSONStore[T] {
	return &JSONStore[T]{path: path}
}

// Load は永続化済みの []T を読み出す。ファイルが無ければ (nil, nil)。
// パース失敗時は破損ファイルを <path>.bak へ退避し、log を出して (nil, nil) を返す。
func (s *JSONStore[T]) Load() ([]T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []T
	if err := json.Unmarshal(b, &entries); err != nil {
		bak := s.path + ".bak"
		log.Printf("store: corrupt %s (%v); quarantining to %s and starting empty", s.path, err, bak)
		if rerr := os.Rename(s.path, bak); rerr != nil {
			log.Printf("store: failed to quarantine %s: %v", s.path, rerr)
		}
		return nil, nil
	}
	return entries, nil
}

// Save は entries を atomic（tmp→rename）に書き出し、親ディレクトリを fsync する。
func (s *JSONStore[T]) Save(entries []T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// 親ディレクトリの fsync（rename の durability 向上。失敗は best-effort で無視）。
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
