package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthorizeURL(t *testing.T) {
	c := &OAuthClient{ClientID: "cid", UserScopes: []string{"channels:history", "users:read"}}
	got := c.AuthorizeURL(OAuthState("st8"), "http://127.0.0.1:8780/plugins/slack/callback")
	for _, want := range []string{
		"client_id=cid",
		"user_scope=channels%3Ahistory%2Cusers%3Aread",
		"state=st8",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A8780%2Fplugins%2Fslack%2Fcallback",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AuthorizeURL missing %q in %q", want, got)
		}
	}
}

func TestExchangeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code") != "thecode" {
			t.Errorf("code = %q", r.FormValue("code"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"team":{"id":"T1","name":"Acme"},"authed_user":{"id":"U1","scope":"channels:history","access_token":"xoxp-tok","token_type":"user"}}`))
	}))
	defer srv.Close()

	c := &OAuthClient{ClientID: "cid", ClientSecret: "sec", HTTPClient: srv.Client()}
	accessURLOverride = srv.URL // テスト用にエンドポイントを差し替える
	defer func() { accessURLOverride = "" }()

	tok, err := c.Exchange(context.Background(), "thecode", "http://127.0.0.1/cb")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.WorkspaceID != "T1" || tok.TeamName != "Acme" || tok.UserID != "U1" {
		t.Errorf("token meta = %+v", tok)
	}
	if tok.AccessToken.Reveal() != "xoxp-tok" {
		t.Errorf("access token = %q", tok.AccessToken.Reveal())
	}
}

func TestExchangeSlackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_code"}`))
	}))
	defer srv.Close()
	c := &OAuthClient{ClientID: "cid", ClientSecret: "sec", HTTPClient: srv.Client()}
	accessURLOverride = srv.URL
	defer func() { accessURLOverride = "" }()
	if _, err := c.Exchange(context.Background(), "x", "http://127.0.0.1/cb"); err == nil {
		t.Error("expected error on ok:false")
	}
}
