package terminal

import "github.com/ktat/agentarium/kernel/store"

// JSONStore[T] は kernel/store.JSONStore[T] の後方互換 alias。
// 実体は kernel/store へ移動した（プラグインからも使える公開ストア）。
type JSONStore[T any] = store.JSONStore[T]

// NewJSONStore は store.New への委譲（後方互換）。
func NewJSONStore[T any](path string) *store.JSONStore[T] {
	return store.New[T](path)
}
