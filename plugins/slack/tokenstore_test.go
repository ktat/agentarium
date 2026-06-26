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

// TestGetAnyReturnsNewest は GetAny() が ObtainedAt が最新のトークンを返すことを検証する。
func TestGetAnyReturnsNewest(t *testing.T) {
	st := newTestStore(t)
	ts := NewSecretTokenStore(st)

	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	at1, _ := NewAccessToken("xoxp-old")
	tok1 := &Token{WorkspaceID: "T_OLD", TeamName: "OldTeam", UserID: "U1", AccessToken: at1, Scope: "channels:history", ObtainedAt: older}
	if err := ts.Save(tok1); err != nil {
		t.Fatalf("Save tok1: %v", err)
	}

	at2, _ := NewAccessToken("xoxp-new")
	tok2 := &Token{WorkspaceID: "T_NEW", TeamName: "NewTeam", UserID: "U2", AccessToken: at2, Scope: "channels:history", ObtainedAt: newer}
	if err := ts.Save(tok2); err != nil {
		t.Fatalf("Save tok2: %v", err)
	}

	got, err := ts.GetAny()
	if err != nil {
		t.Fatalf("GetAny: %v", err)
	}
	if got.WorkspaceID != "T_NEW" {
		t.Errorf("GetAny WorkspaceID = %q, want T_NEW (newest ObtainedAt)", got.WorkspaceID)
	}
	if got.TeamName != "NewTeam" {
		t.Errorf("GetAny TeamName = %q, want NewTeam", got.TeamName)
	}
}
