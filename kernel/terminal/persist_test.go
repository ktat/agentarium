package terminal

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "terminals.json")
	s := NewStore(path)
	in := []RegistryEntry{
		{ID: "a", Label: "A", SessionID: "s-a", Args: []string{"--resume", "s-a"}},
		{ID: "b", Label: "B", SessionID: "s-b", Args: nil},
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
