package pet

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRoutes_ConfigSkinsLaunchStatus(t *testing.T) {
	store := newStore(t)
	_ = store.Set(KeyBinary, fakePetBin(t))
	sup := New(store, func() int { return 3 })
	sup.SetAddr("127.0.0.1:8780")
	mux := http.NewServeMux()
	sup.MountOn(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// /pet/skins
	res, _ := http.Get(srv.URL + "/pet/skins")
	if res.StatusCode != 200 {
		t.Fatalf("skins status %d", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), "default") {
		t.Fatalf("skins body %s", b)
	}

	// /pet/status
	res2, _ := http.Get(srv.URL + "/pet/status")
	b2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if !strings.Contains(string(b2), `"subscriber_count":3`) {
		t.Fatalf("status body %s", b2)
	}

	// /pet/config POST then GET
	bin := fakePetBin(t)
	_, _ = http.Post(srv.URL+"/pet/config", "application/json",
		strings.NewReader(`{"binary":"`+bin+`","skin":"dark","autostart":true}`))
	res3, _ := http.Get(srv.URL + "/pet/config")
	b3, _ := io.ReadAll(res3.Body)
	res3.Body.Close()
	if !strings.Contains(string(b3), `"skin":"dark"`) || !strings.Contains(string(b3), `"autostart":true`) {
		t.Fatalf("config body %s", b3)
	}

	// cross-origin POST /pet/launch → 403
	req, _ := http.NewRequest("POST", srv.URL+"/pet/launch", nil)
	req.Header.Set("Origin", "https://evil.example")
	r4, _ := http.DefaultClient.Do(req)
	r4.Body.Close()
	if r4.StatusCode != 403 {
		t.Fatalf("cross-origin launch status %d want 403", r4.StatusCode)
	}
}
