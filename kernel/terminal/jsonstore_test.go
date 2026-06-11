package terminal

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type jsTestEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestJSONStore_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "data.json")
	s := NewJSONStore[jsTestEntry](path)
	in := []jsTestEntry{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
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

func TestJSONStore_LoadMissingReturnsNil(t *testing.T) {
	s := NewJSONStore[jsTestEntry](filepath.Join(t.TempDir(), "nope.json"))
	got, err := s.Load()
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestJSONStore_LoadCorruptQuarantines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := NewJSONStore[jsTestEntry](path)
	got, err := s.Load()
	if err != nil {
		t.Fatalf("corrupt load should not error (quarantine): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("corrupt file should be quarantined to .bak: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original should be gone: %v", err)
	}
}

func TestJSONStore_SaveDurableAfterReopen(t *testing.T) {
	// Save → 別 Store インスタンスで Load して同一内容が読めること（atomic write 確認）。
	path := filepath.Join(t.TempDir(), "data.json")
	in := []jsTestEntry{{ID: "x", Name: "X"}}
	if err := NewJSONStore[jsTestEntry](path).Save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := NewJSONStore[jsTestEntry](path).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("reopen mismatch: %+v", got)
	}
}
