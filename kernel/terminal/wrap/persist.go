package wrap

import "github.com/ktat/agentarium/kernel/terminal"

// StoreEntry は wrap backend の再起動越え復元レコード。共通 terminal.SessionRecord の
// 別名（フィールド一致のため alias で吸収する。spec §A / Phase 1）。
type StoreEntry = terminal.SessionRecord

// toStoreEntry は entry を永続化レコードへ写す（entry→StoreEntry の唯一の写像）。
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
// （StoreEntry→entry の唯一の写像）。Process / State 系は復元方式ごとに呼び出し側が設定する。
// workDir は呼び出し側で解決済みの値を渡す（e.WorkDir を直接使わない。非 lazy 経路が
// NewProcess 用に同値を必要とするため、fallback はここに置かない）。
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

// Store は wrap backend 用の永続化ストア。共通 terminal.Store の別名。
type Store = terminal.Store

// NewStore は wrap 用 Store を返す。
func NewStore(path string) *Store {
	return terminal.NewStore(path)
}
