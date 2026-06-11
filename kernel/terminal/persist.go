package terminal

// RegistryEntry は永続化される 1 セッション分のレコード（xterm backend が使う）。
// SessionID は復元の前提条件（resume 引数の元）。
type RegistryEntry struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	SessionID string   `json:"session_id"`
	Args      []string `json:"args"`
}

// Store は xterm backend 用の永続化ストア。JSONStore[RegistryEntry] の別名。
// Load は quarantine、Save は atomic + 親 dir fsync（jsonstore.go 参照）。
type Store = JSONStore[RegistryEntry]

// NewStore は xterm 用 Store を返す。
func NewStore(path string) *Store {
	return NewJSONStore[RegistryEntry](path)
}
