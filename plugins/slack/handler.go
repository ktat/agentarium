package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/server"
)

const (
	stateTTL               = 10 * time.Minute
	defaultExchangeTimeout = 10 * time.Second
	callbackPath           = "/plugins/slack/callback"
)

func (p Plugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/start", Handler: p.handleStart},
		{Method: "GET", Path: "/callback", Handler: p.handleCallback},
		{Method: "GET", Path: "/tokens", Handler: p.handleTokens},
	}
}

// stateStore は CSRF 用の state を TTL 付きで保持する。
type stateStore struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func newStateStore() *stateStore { return &stateStore{m: make(map[string]time.Time)} }

func (s *stateStore) add(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.m {
		if now.After(exp) {
			delete(s.m, k)
		}
	}
	s.m[state] = now.Add(stateTTL)
}

// consume は state が存在し期限内なら削除して true を返す。期限切れエントリも掃除する。
func (s *stateStore) consume(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.m {
		if now.After(exp) {
			delete(s.m, k)
		}
	}
	exp, ok := s.m[state]
	if !ok || now.After(exp) {
		return false
	}
	delete(s.m, state)
	return true
}

// redirectURL はリクエストの Host と scheme から callback の絶対 URL を組み立てる。
func redirectURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + callbackPath
}

func (p Plugin) handleStart(w http.ResponseWriter, r *http.Request) {
	oc := p.oauthClient()
	if oc == nil {
		writeError(w, http.StatusServiceUnavailable, "SLACK_CLIENT_ID / SLACK_CLIENT_SECRET が未設定です。Settings タブで設定してください。")
		return
	}
	state, err := NewOAuthState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate state: "+err.Error())
		return
	}
	p.states.add(state.String())
	http.Redirect(w, r, oc.AuthorizeURL(state, redirectURL(r)), http.StatusFound)
}

func (p Plugin) handleCallback(w http.ResponseWriter, r *http.Request) {
	oc := p.oauthClient()
	if oc == nil {
		writeError(w, http.StatusServiceUnavailable, "Slack OAuth client is not configured")
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		writeError(w, http.StatusBadRequest, "slack returned error: "+errParam)
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "state and code are required")
		return
	}
	if !p.states.consume(state) {
		writeError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultExchangeTimeout)
	defer cancel()

	token, err := oc.Exchange(ctx, code, redirectURL(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, "token exchange failed: "+err.Error())
		return
	}
	if err := p.tokens.Save(token); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save token: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, successHTML,
		html.EscapeString(token.TeamName),
		html.EscapeString(token.WorkspaceID.String()),
		html.EscapeString(token.UserID),
		html.EscapeString(token.Scope),
	)
}

// tokenDTO は /tokens が返す接続状態（access token は含めない）。
type tokenDTO struct {
	WorkspaceID string `json:"workspace_id"`
	TeamName    string `json:"team_name"`
	UserID      string `json:"user_id"`
	Scope       string `json:"scope"`
	ObtainedAt  string `json:"obtained_at"`
}

func (p Plugin) handleTokens(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	all, err := p.tokens.GetAll()
	if err != nil {
		http.Error(w, "failed to load tokens", http.StatusInternalServerError)
		return
	}
	out := make([]tokenDTO, 0, len(all))
	for _, t := range all {
		out = append(out, tokenDTO{
			WorkspaceID: t.WorkspaceID.String(),
			TeamName:    t.TeamName,
			UserID:      t.UserID,
			Scope:       t.Scope,
			ObtainedAt:  t.ObtainedAt.Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"workspaces": out, "configured": p.oauthClient() != nil})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, errorHTML, html.EscapeString(msg))
}

const successHTML = `<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><title>Slack OAuth - OK</title></head>
<body>
<h1>Slack 認証が完了しました</h1>
<ul>
  <li>Team: %s</li>
  <li>Workspace ID: %s</li>
  <li>User ID: %s</li>
  <li>Scope: %s</li>
</ul>
<p>このタブを閉じて、Slack タブをリロードしてください。</p>
</body></html>
`

const errorHTML = `<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><title>Slack OAuth - Error</title></head>
<body>
<h1>Slack 認証エラー</h1>
<pre>%s</pre>
</body></html>
`
