package pet

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	status, b := doReq(t, http.MethodGet, srv.URL+"/pet/skins", "", nil)
	if status != 200 {
		t.Fatalf("skins status %d", status)
	}
	if !strings.Contains(b, "default") {
		t.Fatalf("skins body %s", b)
	}

	// /pet/status
	_, b2 := doReq(t, http.MethodGet, srv.URL+"/pet/status", "", nil)
	if !strings.Contains(b2, `"subscriber_count":3`) {
		t.Fatalf("status body %s", b2)
	}

	// /pet/config POST then GET
	bin := fakePetBin(t)
	doReq(t, http.MethodPost, srv.URL+"/pet/config",
		`{"binary":"`+bin+`","skin":"dark","autostart":true}`, nil)
	_, b3 := doReq(t, http.MethodGet, srv.URL+"/pet/config", "", nil)
	if !strings.Contains(b3, `"skin":"dark"`) || !strings.Contains(b3, `"autostart":true`) {
		t.Fatalf("config body %s", b3)
	}

	// cross-origin POST /pet/launch → 403
	status4, _ := doReq(t, http.MethodPost, srv.URL+"/pet/launch", "",
		http.Header{"Origin": {"https://evil.example"}})
	if status4 != 403 {
		t.Fatalf("cross-origin launch status %d want 403", status4)
	}
}

// doReq は body を必ず閉じ、エラーは t.Fatal にする HTTP ヘルパ（noctx/bodyclose 対策）。
func doReq(t *testing.T, method, url, body string, hdr http.Header) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body %s %s: %v", method, url, err)
	}
	return res.StatusCode, string(b)
}
