// plugins/slack/handler_test.go
package slack

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
