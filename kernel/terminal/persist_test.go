package terminal

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "terminals.json")
	s := NewStore(path)
	in := []SessionRecord{
		{ID: "t1", Label: "L1", WorkDir: "/w", Agent: "claude", SessionID: "s1", Model: "opus", Cols: 80, AltRows: 30},
		{ID: "t2", Label: "L2", Agent: "cat"},
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
