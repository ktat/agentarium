package wrap

import "github.com/ktat/agentarium/kernel/terminal"

// StoreEntry は wrap backend の再起動越え復元レコード。
// エージェント非依存: 起動時の raw args ではなく Agent 名 + 再開情報を保存し、
// 復元時に Agent.Invocation で起動コマンドを再構成する。
type StoreEntry struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	WorkDir   string `json:"work_dir,omitempty"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	AltRows   int    `json:"alt_rows,omitempty"`
}

// toStoreEntry は entry を永続化レコードへ写す（entry→StoreEntry の唯一の写像）。
// 永続化フィールドの正準マッピングを 1 箇所に集約し、逆写像 newEntryFromStore と
// フィールドの取りこぼし/不整合を防ぐ（D6）。
func toStoreEntry(id string, e *entry) StoreEntry {
	return StoreEntry{
		ID:        id,
		Label:     e.Label,
		WorkDir:   e.WorkDir,
		Agent:     e.AgentName,
		SessionID: e.SessionID,
		Model:     e.Model,
		Cols:      e.Cols,
		AltRows:   e.AltRows,
	}
}

// newEntryFromStore は StoreEntry から entry の永続フィールドを埋めて返す
// （StoreEntry→entry の唯一の写像）。Process / State 系は復元方式（即時起動 / lazy
// pending）ごとに呼び出し側が設定する。workDir は呼び出し側で解決済みの値を渡す
// （非 lazy 経路が NewProcess 用に同値を必要とするため、fallback はここに置かない）。
func newEntryFromStore(e StoreEntry, workDir string) *entry {
	return &entry{
		Label:     e.Label,
		WorkDir:   workDir,
		AgentName: e.Agent,
		Model:     e.Model,
		SessionID: e.SessionID,
		Cols:      e.Cols,
		AltRows:   e.AltRows,
	}
}

// Store は wrap backend 用の永続化ストア。terminal.JSONStore[StoreEntry] の別名。
// Load は quarantine、Save は atomic + 親 dir fsync。
type Store = terminal.JSONStore[StoreEntry]

// NewStore は wrap 用 Store を返す。
func NewStore(path string) *Store {
	return terminal.NewJSONStore[StoreEntry](path)
}
