// plugins/slack/client_test.go
package slack

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetMessageAndErrorMapping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xoxp-tok" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1.2","user":"U1","text":"hi"}]}`))
	})
	mux.HandleFunc("/users.info", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	apiBaseOverride = srv.URL
	defer func() { apiBaseOverride = "" }()

	c := &APIClient{Token: AccessToken("xoxp-tok"), HTTPClient: srv.Client()}

	msg, err := c.GetMessage(context.Background(), "C1", "1.2")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.Text != "hi" || msg.User != "U1" {
		t.Errorf("msg = %+v", msg)
	}

	if _, err := c.GetUser(context.Background(), "U1"); !errors.Is(err, ErrInvalidAuth) {
		t.Errorf("GetUser err = %v, want ErrInvalidAuth", err)
	}
}
