package pet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/secrets"
)

func newStore(t *testing.T) *secrets.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := secrets.NewStore(filepath.Join(dir, "d.json"), filepath.Join(dir, "k.key"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return s
}

// fakePetBin は --list-skin で 2 行返すスクリプトを temp に作りパスを返す。
func fakePetBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "pet.sh")
	script := "#!/bin/sh\nif [ \"$1\" = \"--list-skin\" ]; then echo default; echo dark; echo ''; fi\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestListSkins(t *testing.T) {
	store := newStore(t)
	sup := New(store, func() int { return 0 })
	if _, err := sup.ListSkins(); err == nil {
		t.Fatal("unconfigured binary should error")
	}
	_ = store.Set(KeyBinary, fakePetBin(t))
	skins, err := sup.ListSkins()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(skins) != 2 || skins[0] != "default" || skins[1] != "dark" {
		t.Fatalf("skins=%v want [default dark]", skins)
	}
}

func TestAutostart(t *testing.T) {
	store := newStore(t)
	sup := New(store, func() int { return 0 })
	if sup.Autostart() {
		t.Fatal("default autostart should be false")
	}
	_ = store.Set(KeyAutostart, "1")
	if !sup.Autostart() {
		t.Fatal("autostart should be true when '1'")
	}
}

func TestLaunch_Unconfigured(t *testing.T) {
	sup := New(newStore(t), func() int { return 0 })
	if _, err := sup.Launch("127.0.0.1:8780"); err == nil {
		t.Fatal("launch without binary should error")
	}
}
