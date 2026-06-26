package slack

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/secrets"
)

func newTestStore(t *testing.T) *secrets.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := secrets.NewStore(filepath.Join(dir, "d.json"), filepath.Join(dir, "k.key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return st
}

func TestSecretTokenStoreSaveGet(t *testing.T) {
	st := newTestStore(t)
	ts := NewSecretTokenStore(st)

	if _, err := ts.GetAny(); !errors.Is(err, ErrNoToken) {
		t.Errorf("empty GetAny err = %v, want ErrNoToken", err)
	}

	at, _ := NewAccessToken("xoxp-1")
	tok := &Token{WorkspaceID: "T1", TeamName: "Acme", UserID: "U1", AccessToken: at, Scope: "channels:history", ObtainedAt: time.Now().UTC()}
	if err := ts.Save(tok); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := ts.Get("T1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TeamName != "Acme" || got.AccessToken.Reveal() != "xoxp-1" {
		t.Errorf("got = %+v", got)
	}

	all, err := ts.GetAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("GetAll = %v len=%d err=%v", all, len(all), err)
	}

	// secrets ストアに暗号化保存されていること。
	if !st.IsEncrypted(tokensKey) {
		t.Errorf("%s should be stored encrypted", tokensKey)
	}
}
