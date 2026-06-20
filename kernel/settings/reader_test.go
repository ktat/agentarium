package settings_test

import (
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
)

func newReaderStore(t *testing.T) *secrets.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := secrets.NewStore(filepath.Join(dir, "data.json"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestReader_LiteralValue(t *testing.T) {
	s := newReaderStore(t)
	_ = s.Set("eval.NOTION_APP_TOKEN", "literal-val")
	r := settings.NewReader(s, "eval")
	if v, ok := r.Get("NOTION_APP_TOKEN"); !ok || v != "literal-val" {
		t.Fatalf("literal get = %q,%v", v, ok)
	}
}

func TestReader_RefResolvesKernelSecret(t *testing.T) {
	s := newReaderStore(t)
	_ = s.SetSecret(settings.KernelSecretPrefix+"NOTION_TOKEN", "kernel-secret")
	_ = s.Set("eval.NOTION_APP_TOKEN"+settings.RefSuffix, "NOTION_TOKEN")
	r := settings.NewReader(s, "eval")
	if v, ok := r.Get("NOTION_APP_TOKEN"); !ok || v != "kernel-secret" {
		t.Fatalf("ref get = %q,%v", v, ok)
	}
}

func TestReader_DanglingRefIsUnset(t *testing.T) {
	s := newReaderStore(t)
	_ = s.Set("eval.NOTION_APP_TOKEN"+settings.RefSuffix, "GONE")
	r := settings.NewReader(s, "eval")
	if v, ok := r.Get("NOTION_APP_TOKEN"); ok {
		t.Fatalf("dangling ref should be unset, got %q,%v", v, ok)
	}
}

func TestReader_MissingFieldIsUnset(t *testing.T) {
	s := newReaderStore(t)
	r := settings.NewReader(s, "eval")
	if _, ok := r.Get("NOPE"); ok {
		t.Fatal("missing field should be unset")
	}
}
