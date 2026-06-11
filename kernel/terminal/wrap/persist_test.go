package wrap

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "terminals.json")
	s := NewStore(path)
	in := []StoreEntry{
		{ID: "a", Label: "A", WorkDir: "/w", Agent: "claude", SessionID: "s-a", Model: "haiku", Cols: 120, AltRows: 40},
		{ID: "b", Label: "B", Agent: "codex"},
	}
	if err := s.Save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip mismatch:\n want %+v\n got  %+v", in, got)
	}
}

func TestStore_LoadMissingReturnsNil(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "nope.json"))
	got, err := s.Load()
	if err != nil {
		t.Fatalf("load missing: unexpected err %v", err)
	}
	if got != nil {
		t.Fatalf("want nil for missing file, got %+v", got)
	}
}

// TestStoreEntryMapper_RoundTrip は toStoreEntry → newEntryFromStore で
// 永続フィールドが取りこぼしなく往復することを検証する（D6 の同期ハザード防止）。
func TestStoreEntryMapper_RoundTrip(t *testing.T) {
	orig := &entry{
		Label:     "L",
		WorkDir:   "/w",
		AgentName: "claude",
		Model:     "opus",
		SessionID: "sid-1",
		Cols:      120,
		AltRows:   40,
	}
	se := toStoreEntry("t1", orig)
	if se.ID != "t1" || se.Agent != "claude" || se.WorkDir != "/w" ||
		se.Model != "opus" || se.SessionID != "sid-1" || se.Cols != 120 || se.AltRows != 40 || se.Label != "L" {
		t.Fatalf("toStoreEntry lost a field: %+v", se)
	}
	back := newEntryFromStore(se, se.WorkDir)
	if back.Label != orig.Label || back.WorkDir != orig.WorkDir || back.AgentName != orig.AgentName ||
		back.Model != orig.Model || back.SessionID != orig.SessionID || back.Cols != orig.Cols || back.AltRows != orig.AltRows {
		t.Fatalf("round-trip mismatch:\n orig=%+v\n back=%+v", orig, back)
	}
	// Process / State は写像対象外（呼び出し側が設定）。
	if back.Process != nil {
		t.Fatalf("newEntryFromStore must not set Process")
	}
}
