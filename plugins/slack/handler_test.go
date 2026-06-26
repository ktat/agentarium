// plugins/slack/handler_test.go
package slack

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/settings"
)

func TestStartRedirectsToSlack(t *testing.T) {
	st := newTestStore(t)
	// CLIENT_ID/SECRET を plugin scoped キーに直接保存（Settings 経由と同等）。
	_ = st.SetSecret("slack.SLACK_CLIENT_ID", "cid")
	_ = st.SetSecret("slack.SLACK_CLIENT_SECRET", "sec")
	p := New(st)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8780/plugins/slack/start", nil)
	rec := httptest.NewRecorder()
	p.handleStart(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, authorizeURL) {
		t.Errorf("Location = %q", loc)
	}
	if !strings.Contains(loc, "redirect_uri=http%3A%2F%2F127.0.0.1%3A8780%2Fplugins%2Fslack%2Fcallback") {
		t.Errorf("redirect_uri not derived from host: %q", loc)
	}
}

func TestStartWithoutCredentials503(t *testing.T) {
	p := New(newTestStore(t))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/plugins/slack/start", nil)
	rec := httptest.NewRecorder()
	p.handleStart(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestCallbackInvalidState(t *testing.T) {
	st := newTestStore(t)
	_ = st.SetSecret("slack.SLACK_CLIENT_ID", "cid")
	_ = st.SetSecret("slack.SLACK_CLIENT_SECRET", "sec")
	p := New(st)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/plugins/slack/callback?state=bogus&code=x", nil)
	rec := httptest.NewRecorder()
	p.handleCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSettingsSchema(t *testing.T) {
	p := New(newTestStore(t))
	fields := p.SettingsSchema()
	if len(fields) != 2 {
		t.Fatalf("fields = %d, want 2", len(fields))
	}
	for _, f := range fields {
		if !f.Secret {
			t.Errorf("field %q should be secret", f.Key)
		}
	}
}

// settings.Reader 経由でも読めること（ref 解決を壊していない確認）。
func TestReaderReadsCredentials(t *testing.T) {
	st := newTestStore(t)
	_ = st.SetSecret("slack.SLACK_CLIENT_ID", "cid")
	r := settings.NewReader(st, "slack")
	if v, ok := r.Get("SLACK_CLIENT_ID"); !ok || v != "cid" {
		t.Errorf("reader.Get = %q,%v", v, ok)
	}
}

// TestTokensDoesNotLeakAccessToken は /tokens レスポンスに生の access token が含まれないことを確認する。
func TestTokensDoesNotLeakAccessToken(t *testing.T) {
	st := newTestStore(t)
	p := New(st)

	// 識別可能な access token を持つトークンを直接 store に保存する。
	at, err := NewAccessToken("xoxp-SECRET-DONOTLEAK")
	if err != nil {
		t.Fatalf("NewAccessToken: %v", err)
	}
	tok := &Token{
		WorkspaceID: WorkspaceID("T999"),
		TeamName:    "LeakTestTeam",
		UserID:      "U999",
		AccessToken: at,
		Scope:       "channels:history",
		ObtainedAt:  time.Now().UTC(),
	}
	if err := p.tokens.Save(tok); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8780/plugins/slack/tokens", nil)
	// Origin ヘッダーなし = IsLocalOriginOrAbsent が true を返す
	rec := httptest.NewRecorder()
	p.handleTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "xoxp-SECRET-DONOTLEAK") {
		t.Errorf("/tokens レスポンスに生 access token が含まれている: %s", body)
	}
	if !strings.Contains(body, "LeakTestTeam") {
		t.Errorf("/tokens レスポンスに team_name が含まれていない: %s", body)
	}
	if !strings.Contains(body, "T999") {
		t.Errorf("/tokens レスポンスに workspace_id が含まれていない: %s", body)
	}
}

// TestTokensCrossOriginRejected は cross-origin リクエストが 403 を返すことを確認する。
func TestTokensCrossOriginRejected(t *testing.T) {
	st := newTestStore(t)
	p := New(st)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8780/plugins/slack/tokens", nil)
	// 外部パブリック Origin を付与 → IsLocalOriginOrAbsent が false を返す
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	p.handleTokens(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestStartCallbackStateSharing は /start で発行した state が /callback で消費できることを確認する。
// 同一 Plugin インスタンスを使うことでポインタ共有契約を回帰テストとして固定する。
func TestStartCallbackStateSharing(t *testing.T) {
	st := newTestStore(t)
	_ = st.SetSecret("slack.SLACK_CLIENT_ID", "cid")
	_ = st.SetSecret("slack.SLACK_CLIENT_SECRET", "sec")
	p := New(st)

	// /start を呼んで Location から state= を取り出す。
	startReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8780/plugins/slack/start", nil)
	startRec := httptest.NewRecorder()
	p.handleStart(startRec, startReq)

	if startRec.Code != http.StatusFound {
		t.Fatalf("/start status = %d, want 302", startRec.Code)
	}
	loc := startRec.Header().Get("Location")
	// state= パラメータを URL から抽出する。
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("state not found in Location: %s", loc)
	}

	// 同一 p インスタンスで /callback を呼ぶ（state は p.states に保存済み）。
	// code は存在しない値だが state の有効性だけを確認する。
	cbReq := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1:8780/plugins/slack/callback?state="+state+"&code=dummy", nil)
	cbRec := httptest.NewRecorder()
	p.handleCallback(cbRec, cbReq)

	// state が消費できていれば "invalid or expired state" の 400 にはならない。
	// (Exchange は失敗するが別のエラーコードになる)
	if cbRec.Code == http.StatusBadRequest && strings.Contains(cbRec.Body.String(), "invalid or expired state") {
		t.Errorf("/callback returned 'invalid or expired state': state sharing via pointer is broken")
	}
}
