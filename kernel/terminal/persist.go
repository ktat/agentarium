package terminal

// SessionRecord は再起動越え復元の共通レコード（wrap/xterm 両 backend が使う）。
// 起動は生 args ではなく Agent 名 + Resume 情報を保存し、復元時に Agent.Invocation で
// 起動コマンドを再構成する（エージェント非依存・spec §A）。SessionID は resume の前提。
type SessionRecord struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	WorkDir   string `json:"work_dir,omitempty"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	AltRows   int    `json:"alt_rows,omitempty"`
}

// Store は再起動越え復元の永続化ストア。JSONStore[SessionRecord] の別名。
// Load は quarantine、Save は atomic + 親 dir fsync（jsonstore.go 参照）。
type Store = JSONStore[SessionRecord]

// NewStore は SessionRecord 用 Store を返す。
func NewStore(path string) *Store {
	return NewJSONStore[SessionRecord](path)
}
