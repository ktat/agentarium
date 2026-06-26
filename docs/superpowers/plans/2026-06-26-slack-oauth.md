# Slack OAuth プラグイン Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** agentarium に Slack OAuth(v2) でユーザートークンを取得・暗号化保存し、そのトークンで Slack API（履歴/ユーザー取得）を呼べるバンドルプラグイン `plugins/slack/` を追加する。

**Architecture:** カーネルには触れず `plugins/slack/`（public パッケージ）を新設。`slack.New(*secrets.Store)` を消費者が `Register` する。OAuth フロー（authorize→callback→token 交換）、トークンの暗号化保存（secrets ストア）、Slack API クライアント、接続状態タブを提供する。redirect_uri はリクエスト Host から動的生成する。

**Tech Stack:** Go 1.26、標準ライブラリ（net/http, encoding/json, crypto/rand）、`httptest` でテスト。既存 `kernel/plugin`・`kernel/secrets`・`kernel/settings`・`kernel/server` の規約に従う。移植元は `backlog-worker/management-server/internal/{slack,slackhttp}`。

## Global Constraints

- 言語は Go 1.26 系。
- 1 ファイル 700 行以内。
- OSS 前提: 環境固有値（フルパス・社内固有名）をコード/設定に書かない。資格情報は secrets 経由。
- `127.0.0.1` バインドのローカル実行前提。
- TDD（Red → Green → Refactor）。Conventional Commits（日本語・scope 付き）。
- 各コミットメッセージ末尾に `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` を付ける。
- アクセストークン文字列は `String()` で `***` にマスクし、実値取得は `Reveal()` のみ。
- プラグイン ID は `slack`。ルートは `/plugins/slack/...` に自動マウントされる。

---

## File Structure

```
plugins/slack/
├── types.go        # WorkspaceID / AccessToken(マスク) / OAuthState / Token / Message / User
├── urlparser.go    # MessageRef / ParseMessageURL / normalizeTS
├── oauth.go        # OAuthClient: AuthorizeURL / Exchange
├── tokenstore.go   # SecretTokenStore: secrets ストアへ slack.tokens(JSON) 保存
├── client.go       # APIClient: GetMessage / GetThread / ListMessagesSince / GetUser
├── slack.go        # Plugin: New / Meta / Assets / SettingsSchema / Routes / Client()
├── handler.go      # handleStart / handleCallback / handleTokens / stateStore / redirect_uri 生成
├── assets/index.js # 接続状態 + 「Slack 連携」ボタン + workspace 一覧タブ
├── types_test.go
├── urlparser_test.go
├── oauth_test.go
├── tokenstore_test.go
├── client_test.go
└── handler_test.go
```

すべて `package slack`。

---

### Task 1: 値オブジェクトと構造体 (types.go)

**Files:**
- Create: `plugins/slack/types.go`
- Test: `plugins/slack/types_test.go`

**Interfaces:**
- Consumes: なし
- Produces:
  - `type WorkspaceID string`; `func NewWorkspaceID(s string) (WorkspaceID, error)`; `func (WorkspaceID) String() string`
  - `type AccessToken string`; `func NewAccessToken(s string) (AccessToken, error)`; `func (AccessToken) String() string`（`"***"`）; `func (AccessToken) Reveal() string`
  - `type OAuthState string`; `func NewOAuthState() (OAuthState, error)`; `func (OAuthState) String() string`
  - `type Token struct { WorkspaceID WorkspaceID; TeamName string; UserID string; AccessToken AccessToken; Scope string; ObtainedAt time.Time }`
  - `type Message struct { ChannelID, TS, User, BotID, Subtype, Text, ThreadTS string }`
  - `type User struct { ID, Name, RealName, DisplayName string }`; `func (u *User) BestName() string`

- [ ] **Step 1: Write the failing test**

```go
// plugins/slack/types_test.go
package slack

import "testing"

func TestAccessTokenMasksString(t *testing.T) {
	at, err := NewAccessToken("xoxp-secret")
	if err != nil {
		t.Fatalf("NewAccessToken: %v", err)
	}
	if at.String() != "***" {
		t.Errorf("String() = %q, want ***", at.String())
	}
	if at.Reveal() != "xoxp-secret" {
		t.Errorf("Reveal() = %q, want xoxp-secret", at.Reveal())
	}
	if _, err := NewAccessToken(""); err == nil {
		t.Error("empty token should error")
	}
}

func TestNewOAuthStateUnique(t *testing.T) {
	s1, err := NewOAuthState()
	if err != nil {
		t.Fatalf("NewOAuthState: %v", err)
	}
	s2, _ := NewOAuthState()
	if s1.String() == "" || s1 == s2 {
		t.Errorf("states should be non-empty and unique: %q %q", s1, s2)
	}
}

func TestUserBestName(t *testing.T) {
	cases := []struct {
		u    User
		want string
	}{
		{User{DisplayName: "disp", RealName: "real", Name: "name"}, "disp"},
		{User{RealName: "real", Name: "name"}, "real"},
		{User{Name: "name"}, "name"},
	}
	for _, c := range cases {
		if got := c.u.BestName(); got != c.want {
			t.Errorf("BestName() = %q, want %q", got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/slack/ -run 'AccessToken|OAuthState|BestName' -v`
Expected: コンパイルエラー（型未定義）。

- [ ] **Step 3: Write minimal implementation**

```go
// plugins/slack/types.go
package slack

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

type WorkspaceID string

func NewWorkspaceID(s string) (WorkspaceID, error) {
	if s == "" {
		return "", errors.New("workspace id is empty")
	}
	return WorkspaceID(s), nil
}

func (w WorkspaceID) String() string { return string(w) }

type AccessToken string

func NewAccessToken(s string) (AccessToken, error) {
	if s == "" {
		return "", errors.New("access token is empty")
	}
	return AccessToken(s), nil
}

// String は意図的にマスクする。log / fmt 経由でのトークン漏洩を防ぐ。実値は Reveal()。
func (a AccessToken) String() string { return "***" }

func (a AccessToken) Reveal() string { return string(a) }

type OAuthState string

func NewOAuthState() (OAuthState, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return OAuthState(hex.EncodeToString(b)), nil
}

func (s OAuthState) String() string { return string(s) }

// Token は OAuth で取得した 1 workspace 分のユーザートークン。
type Token struct {
	WorkspaceID WorkspaceID
	TeamName    string
	UserID      string
	AccessToken AccessToken
	Scope       string
	ObtainedAt  time.Time
}

// Message は Slack の 1 メッセージ。
type Message struct {
	ChannelID string
	TS        string
	User      string
	BotID     string
	Subtype   string
	Text      string
	ThreadTS  string
}

// User は users.info の必要項目。
type User struct {
	ID          string
	Name        string
	RealName    string
	DisplayName string
}

// BestName は表示に最適な名前を返す (display_name > real_name > name)。
func (u *User) BestName() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	if u.RealName != "" {
		return u.RealName
	}
	return u.Name
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/slack/ -run 'AccessToken|OAuthState|BestName' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/slack/types.go plugins/slack/types_test.go
git commit -m "feat(slack): 値オブジェクトと Token/Message/User 構造体を追加" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: メッセージ URL パーサ (urlparser.go)

**Files:**
- Create: `plugins/slack/urlparser.go`
- Test: `plugins/slack/urlparser_test.go`

**Interfaces:**
- Consumes: なし
- Produces:
  - `type MessageRef struct { Workspace, ChannelID, TS, ThreadTS string }`; `func (*MessageRef) IsThread() bool`
  - `func ParseMessageURL(s string) (*MessageRef, error)`

- [ ] **Step 1: Write the failing test**

```go
// plugins/slack/urlparser_test.go
package slack

import "testing"

func TestParseMessageURL(t *testing.T) {
	ref, err := ParseMessageURL("https://acme.slack.com/archives/C123ABC/p1700000000123456?thread_ts=1699999999.000100")
	if err != nil {
		t.Fatalf("ParseMessageURL: %v", err)
	}
	if ref.Workspace != "acme" || ref.ChannelID != "C123ABC" {
		t.Errorf("workspace/channel = %q/%q", ref.Workspace, ref.ChannelID)
	}
	if ref.TS != "1700000000.123456" {
		t.Errorf("ts = %q, want 1700000000.123456", ref.TS)
	}
	if !ref.IsThread() || ref.ThreadTS != "1699999999.000100" {
		t.Errorf("thread = %v / %q", ref.IsThread(), ref.ThreadTS)
	}
}

func TestParseMessageURLErrors(t *testing.T) {
	bad := []string{
		"ftp://acme.slack.com/archives/C1/p1700000000123456",
		"https://example.com/archives/C1/p1700000000123456",
		"https://acme.slack.com/foo/bar",
	}
	for _, s := range bad {
		if _, err := ParseMessageURL(s); err == nil {
			t.Errorf("%q should error", s)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/slack/ -run ParseMessageURL -v`
Expected: コンパイルエラー（`ParseMessageURL` 未定義）。

- [ ] **Step 3: Write minimal implementation**

```go
// plugins/slack/urlparser.go
package slack

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type MessageRef struct {
	Workspace string
	ChannelID string
	TS        string
	ThreadTS  string
}

func (r *MessageRef) IsThread() bool { return r.ThreadTS != "" }

var slackArchivePathRe = regexp.MustCompile(`^/archives/([A-Z][A-Z0-9]+)/p(\d+)$`)

func ParseMessageURL(s string) (*MessageRef, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("scheme must be http or https")
	}
	if !strings.HasSuffix(u.Host, ".slack.com") {
		return nil, errors.New("host must be *.slack.com")
	}
	workspace := strings.TrimSuffix(u.Host, ".slack.com")

	m := slackArchivePathRe.FindStringSubmatch(u.Path)
	if m == nil {
		return nil, errors.New("path must be /archives/{channel}/p{timestamp}")
	}
	ts, err := normalizeTS(m[2])
	if err != nil {
		return nil, err
	}
	return &MessageRef{
		Workspace: workspace,
		ChannelID: m[1],
		TS:        ts,
		ThreadTS:  u.Query().Get("thread_ts"),
	}, nil
}

// p1700000000123456 形式の ts を 1700000000.123456 に変換する。
func normalizeTS(s string) (string, error) {
	if len(s) < 7 {
		return "", errors.New("timestamp too short")
	}
	return s[:len(s)-6] + "." + s[len(s)-6:], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/slack/ -run ParseMessageURL -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/slack/urlparser.go plugins/slack/urlparser_test.go
git commit -m "feat(slack): Slack メッセージ URL パーサを追加" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: OAuth クライアント (oauth.go)

**Files:**
- Create: `plugins/slack/oauth.go`
- Test: `plugins/slack/oauth_test.go`

**Interfaces:**
- Consumes: `Token`, `AccessToken`, `WorkspaceID`, `OAuthState`（Task 1）
- Produces:
  - `var DefaultUserScopes []string`
  - `type OAuthClient struct { ClientID, ClientSecret string; UserScopes []string; HTTPClient *http.Client }`
  - `func (c *OAuthClient) AuthorizeURL(state OAuthState, redirectURL string) string`
  - `func (c *OAuthClient) Exchange(ctx context.Context, code, redirectURL string) (*Token, error)`

**Note:** 移植元は `RedirectURL` をフィールドで持っていたが、redirect_uri をリクエスト Host から都度生成する方針のため、`AuthorizeURL` / `Exchange` の引数で受け取る形に変更する。

- [ ] **Step 1: Write the failing test**

```go
// plugins/slack/oauth_test.go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/slack/ -run 'AuthorizeURL|Exchange' -v`
Expected: コンパイルエラー（`OAuthClient` 未定義）。

- [ ] **Step 3: Write minimal implementation**

```go
// plugins/slack/oauth.go
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authorizeURL = "https://slack.com/oauth/v2/authorize"
	accessURL    = "https://slack.com/api/oauth.v2.access"
)

// accessURLOverride はテスト時のみ非空にしてエンドポイントを差し替える。
var accessURLOverride string

// DefaultUserScopes は履歴/ユーザー取得に必要な user scope。
var DefaultUserScopes = []string{
	"channels:history",
	"groups:history",
	"im:history",
	"mpim:history",
	"channels:read",
	"groups:read",
	"users:read",
}

type OAuthClient struct {
	ClientID     string
	ClientSecret string
	UserScopes   []string
	HTTPClient   *http.Client
}

func (c *OAuthClient) AuthorizeURL(state OAuthState, redirectURL string) string {
	v := url.Values{}
	v.Set("client_id", c.ClientID)
	v.Set("user_scope", strings.Join(c.UserScopes, ","))
	v.Set("redirect_uri", redirectURL)
	v.Set("state", state.String())
	return authorizeURL + "?" + v.Encode()
}

type accessResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Team  struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	AuthedUser struct {
		ID          string `json:"id"`
		Scope       string `json:"scope"`
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	} `json:"authed_user"`
}

func (c *OAuthClient) Exchange(ctx context.Context, code, redirectURL string) (*Token, error) {
	endpoint := accessURL
	if accessURLOverride != "" {
		endpoint = accessURLOverride
	}
	v := url.Values{}
	v.Set("code", code)
	v.Set("client_id", c.ClientID)
	v.Set("client_secret", c.ClientSecret)
	v.Set("redirect_uri", redirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var ar accessResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	if !ar.OK {
		return nil, fmt.Errorf("slack oauth error: %s", ar.Error)
	}
	if ar.AuthedUser.AccessToken == "" {
		return nil, errors.New("authed_user.access_token is empty (user_scope might be missing)")
	}
	wsID, err := NewWorkspaceID(ar.Team.ID)
	if err != nil {
		return nil, err
	}
	at, err := NewAccessToken(ar.AuthedUser.AccessToken)
	if err != nil {
		return nil, err
	}
	return &Token{
		WorkspaceID: wsID,
		TeamName:    ar.Team.Name,
		UserID:      ar.AuthedUser.ID,
		AccessToken: at,
		Scope:       ar.AuthedUser.Scope,
		ObtainedAt:  time.Now().UTC(),
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/slack/ -run 'AuthorizeURL|Exchange' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/slack/oauth.go plugins/slack/oauth_test.go
git commit -m "feat(slack): OAuth v2 クライアント(認可URL/token交換)を追加" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: 暗号化トークンストア (tokenstore.go)

**Files:**
- Create: `plugins/slack/tokenstore.go`
- Test: `plugins/slack/tokenstore_test.go`

**Interfaces:**
- Consumes: `Token`, `WorkspaceID`, `NewAccessToken`（Task 1）, `*secrets.Store`
- Produces:
  - `var ErrNoToken error`
  - `const tokensKey = "slack.tokens"`
  - `type SecretTokenStore struct { ... }`; `func NewSecretTokenStore(store *secrets.Store) *SecretTokenStore`
  - `func (s *SecretTokenStore) Save(t *Token) error`
  - `func (s *SecretTokenStore) GetAll() ([]*Token, error)`
  - `func (s *SecretTokenStore) GetAny() (*Token, error)`（0 件は `ErrNoToken`）
  - `func (s *SecretTokenStore) Get(id WorkspaceID) (*Token, error)`

**Note:** 移植元 `FileTokenStore` の JSON 構造（`tokenFile.Workspaces map[string]storedToken`）を踏襲しつつ、永続化を os ファイルから `secrets.Store.SetSecret` / `Get`（暗号化）に置換する。

- [ ] **Step 1: Write the failing test**

```go
// plugins/slack/tokenstore_test.go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/slack/ -run SecretTokenStore -v`
Expected: コンパイルエラー（`NewSecretTokenStore` 未定義）。

- [ ] **Step 3: Write minimal implementation**

```go
// plugins/slack/tokenstore.go
package slack

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/secrets"
)

var ErrNoToken = errors.New("no token stored")

// tokensKey は secrets ストア内でトークン JSON を保持するキー。
const tokensKey = "slack.tokens"

// SecretTokenStore は workspace 単位のトークンを暗号化 secrets ストアに保存する。
type SecretTokenStore struct {
	store *secrets.Store
	mu    sync.Mutex
}

func NewSecretTokenStore(store *secrets.Store) *SecretTokenStore {
	return &SecretTokenStore{store: store}
}

type tokenFile struct {
	Workspaces map[string]storedToken `json:"workspaces"`
}

type storedToken struct {
	WorkspaceID string    `json:"workspace_id"`
	TeamName    string    `json:"team_name"`
	UserID      string    `json:"user_id"`
	AccessToken string    `json:"access_token"`
	Scope       string    `json:"scope"`
	ObtainedAt  time.Time `json:"obtained_at"`
}

func (s *SecretTokenStore) Save(t *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tf, err := s.load()
	if err != nil {
		return err
	}
	if tf.Workspaces == nil {
		tf.Workspaces = make(map[string]storedToken)
	}
	tf.Workspaces[t.WorkspaceID.String()] = storedToken{
		WorkspaceID: t.WorkspaceID.String(),
		TeamName:    t.TeamName,
		UserID:      t.UserID,
		AccessToken: t.AccessToken.Reveal(),
		Scope:       t.Scope,
		ObtainedAt:  t.ObtainedAt,
	}
	return s.write(tf)
}

func (s *SecretTokenStore) GetAll() ([]*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tf, err := s.load()
	if err != nil {
		return nil, err
	}
	tokens := make([]*Token, 0, len(tf.Workspaces))
	for _, st := range tf.Workspaces {
		tok, err := st.toToken()
		if err != nil {
			continue
		}
		tokens = append(tokens, tok)
	}
	return tokens, nil
}

func (s *SecretTokenStore) GetAny() (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tf, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, st := range tf.Workspaces {
		return st.toToken()
	}
	return nil, ErrNoToken
}

func (s *SecretTokenStore) Get(id WorkspaceID) (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tf, err := s.load()
	if err != nil {
		return nil, err
	}
	st, ok := tf.Workspaces[id.String()]
	if !ok {
		return nil, fmt.Errorf("token not found for workspace %s", id.String())
	}
	return st.toToken()
}

func (st storedToken) toToken() (*Token, error) {
	at, err := NewAccessToken(st.AccessToken)
	if err != nil {
		return nil, err
	}
	return &Token{
		WorkspaceID: WorkspaceID(st.WorkspaceID),
		TeamName:    st.TeamName,
		UserID:      st.UserID,
		AccessToken: at,
		Scope:       st.Scope,
		ObtainedAt:  st.ObtainedAt,
	}, nil
}

func (s *SecretTokenStore) load() (*tokenFile, error) {
	raw, ok := s.store.Get(tokensKey)
	if !ok || raw == "" {
		return &tokenFile{Workspaces: map[string]storedToken{}}, nil
	}
	var tf tokenFile
	if err := json.Unmarshal([]byte(raw), &tf); err != nil {
		return nil, fmt.Errorf("decode stored tokens: %w", err)
	}
	if tf.Workspaces == nil {
		tf.Workspaces = map[string]storedToken{}
	}
	return &tf, nil
}

func (s *SecretTokenStore) write(tf *tokenFile) error {
	b, err := json.Marshal(tf)
	if err != nil {
		return err
	}
	return s.store.SetSecret(tokensKey, string(b))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/slack/ -run SecretTokenStore -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/slack/tokenstore.go plugins/slack/tokenstore_test.go
git commit -m "feat(slack): トークンを暗号化 secrets ストアに保存する SecretTokenStore を追加" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Slack API クライアント (client.go)

**Files:**
- Create: `plugins/slack/client.go`
- Test: `plugins/slack/client_test.go`

**Interfaces:**
- Consumes: `AccessToken`, `Message`, `User`（Task 1）
- Produces:
  - `var ErrMessageNotFound, ErrChannelNotFound, ErrNotInChannel, ErrMissingScope, ErrInvalidAuth error`
  - `type APIClient struct { Token AccessToken; HTTPClient *http.Client }`; `func NewAPIClient(token AccessToken) *APIClient`
  - `func (c *APIClient) GetMessage(ctx, channelID, ts string) (*Message, error)`
  - `func (c *APIClient) GetThread(ctx, channelID, threadTS string) ([]Message, error)`
  - `func (c *APIClient) ListMessagesSince(ctx, channelID, oldestTS string, maxPages int) ([]Message, error)`
  - `func (c *APIClient) GetUser(ctx, userID string) (*User, error)`

**Note:** 移植元は `slackAPIBase` 定数を直接使う。テストで差し替えるため `apiBaseOverride` 変数を足し、`post` で優先する（oauth.go の `accessURLOverride` と同流儀）。

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/slack/ -run 'GetMessageAndErrorMapping' -v`
Expected: コンパイルエラー（`APIClient` 未定義）。

- [ ] **Step 3: Write minimal implementation**

```go
// plugins/slack/client.go
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const slackAPIBase = "https://slack.com/api"

// apiBaseOverride はテスト時のみ非空にして API ベース URL を差し替える。
var apiBaseOverride string

var (
	ErrMessageNotFound = errors.New("message_not_found")
	ErrChannelNotFound = errors.New("channel_not_found")
	ErrNotInChannel    = errors.New("not_in_channel")
	ErrMissingScope    = errors.New("missing_scope")
	ErrInvalidAuth     = errors.New("invalid_auth")
)

type APIClient struct {
	Token      AccessToken
	HTTPClient *http.Client
}

func NewAPIClient(token AccessToken) *APIClient {
	return &APIClient{Token: token, HTTPClient: http.DefaultClient}
}

type rawMessage struct {
	Type     string `json:"type"`
	Subtype  string `json:"subtype"`
	User     string `json:"user"`
	BotID    string `json:"bot_id"`
	Text     string `json:"text"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
}

type messagesResponse struct {
	OK               bool         `json:"ok"`
	Error            string       `json:"error,omitempty"`
	Messages         []rawMessage `json:"messages"`
	HasMore          bool         `json:"has_more"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

func (c *APIClient) GetMessage(ctx context.Context, channelID, ts string) (*Message, error) {
	v := url.Values{}
	v.Set("channel", channelID)
	v.Set("latest", ts)
	v.Set("oldest", ts)
	v.Set("inclusive", "true")
	v.Set("limit", "1")
	msgs, err := c.callMessages(ctx, "conversations.history", v)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, ErrMessageNotFound
	}
	m := rawToMessage(channelID, msgs[0])
	return &m, nil
}

func (c *APIClient) GetThread(ctx context.Context, channelID, threadTS string) ([]Message, error) {
	v := url.Values{}
	v.Set("channel", channelID)
	v.Set("ts", threadTS)
	rms, err := c.callMessages(ctx, "conversations.replies", v)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rms))
	for _, rm := range rms {
		out = append(out, rawToMessage(channelID, rm))
	}
	return out, nil
}

func (c *APIClient) callMessages(ctx context.Context, method string, v url.Values) ([]rawMessage, error) {
	body, err := c.post(ctx, method, v)
	if err != nil {
		return nil, err
	}
	var r messagesResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode %s response: %w (body=%s)", method, err, string(body))
	}
	if !r.OK {
		return nil, mapSlackError(r.Error)
	}
	return r.Messages, nil
}

// ListMessagesSince は oldestTS より新しいメッセージをページングして取得する
// ("" は下限なし)。maxPages で API 呼び出し回数を制限する。
func (c *APIClient) ListMessagesSince(ctx context.Context, channelID, oldestTS string, maxPages int) ([]Message, error) {
	if maxPages <= 0 {
		maxPages = 5
	}
	var out []Message
	cursor := ""
	for page := 0; page < maxPages; page++ {
		v := url.Values{}
		v.Set("channel", channelID)
		v.Set("limit", "100")
		if oldestTS != "" {
			v.Set("oldest", oldestTS)
		}
		if cursor != "" {
			v.Set("cursor", cursor)
		}
		body, err := c.post(ctx, "conversations.history", v)
		if err != nil {
			return nil, err
		}
		var r messagesResponse
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("decode conversations.history: %w", err)
		}
		if !r.OK {
			return nil, mapSlackError(r.Error)
		}
		for _, rm := range r.Messages {
			out = append(out, rawToMessage(channelID, rm))
		}
		if !r.HasMore || r.ResponseMetadata.NextCursor == "" {
			break
		}
		cursor = r.ResponseMetadata.NextCursor
	}
	return out, nil
}

func (c *APIClient) post(ctx context.Context, method string, v url.Values) ([]byte, error) {
	base := slackAPIBase
	if apiBaseOverride != "" {
		base = apiBaseOverride
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+method, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.Token.Reveal())
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func rawToMessage(channelID string, rm rawMessage) Message {
	return Message{
		ChannelID: channelID,
		TS:        rm.TS,
		User:      rm.User,
		BotID:     rm.BotID,
		Subtype:   rm.Subtype,
		Text:      rm.Text,
		ThreadTS:  rm.ThreadTS,
	}
}

type userResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	User  struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		Profile  struct {
			DisplayName string `json:"display_name"`
			RealName    string `json:"real_name"`
		} `json:"profile"`
	} `json:"user"`
}

func (c *APIClient) GetUser(ctx context.Context, userID string) (*User, error) {
	v := url.Values{}
	v.Set("user", userID)
	body, err := c.post(ctx, "users.info", v)
	if err != nil {
		return nil, err
	}
	var r userResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode users.info response: %w (body=%s)", err, string(body))
	}
	if !r.OK {
		return nil, mapSlackError(r.Error)
	}
	return &User{
		ID:          r.User.ID,
		Name:        r.User.Name,
		RealName:    r.User.RealName,
		DisplayName: r.User.Profile.DisplayName,
	}, nil
}

func mapSlackError(code string) error {
	switch code {
	case "message_not_found":
		return ErrMessageNotFound
	case "channel_not_found":
		return ErrChannelNotFound
	case "not_in_channel":
		return ErrNotInChannel
	case "missing_scope":
		return ErrMissingScope
	case "invalid_auth", "token_revoked", "token_expired":
		return ErrInvalidAuth
	default:
		return fmt.Errorf("slack api error: %s", code)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/slack/ -run 'GetMessageAndErrorMapping' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/slack/client.go plugins/slack/client_test.go
git commit -m "feat(slack): 履歴/ユーザー取得の Slack API クライアントを追加" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Plugin 本体とハンドラ (slack.go, handler.go)

**Files:**
- Create: `plugins/slack/slack.go`
- Create: `plugins/slack/handler.go`
- Test: `plugins/slack/handler_test.go`

**Interfaces:**
- Consumes: Task 1〜5 すべて、`*secrets.Store`, `settings.NewReader`, `plugin.Meta/Route/Field`, `server.IsLocalOriginOrAbsent`
- Produces:
  - `type Plugin struct { ... }`; `func New(store *secrets.Store) Plugin`
  - `func (p Plugin) Meta() plugin.Meta`（ID `slack`, Title `Slack`, Pane `PaneLeft`, Order 5）
  - `func (p Plugin) Assets() fs.FS`
  - `func (p Plugin) SettingsSchema() []plugin.Field`
  - `func (p Plugin) Routes() []plugin.Route`
  - `func (p Plugin) Client() (*APIClient, error)`
  - 内部: `func (p Plugin) oauthClient() *OAuthClient`、`redirectURL(r *http.Request) string`、`type stateStore`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/slack/ -run 'Start|Callback|SettingsSchema|ReaderReads' -v`
Expected: コンパイルエラー（`New`/`handleStart` 未定義）。

- [ ] **Step 3: Write minimal implementation**

```go
// plugins/slack/slack.go
// Package slack は Slack OAuth(v2) でユーザートークンを取得・暗号化保存し、
// そのトークンで Slack API を呼べる同梱プラグイン。
// 消費者 main で `slack.New(secretsStore)` を Register する想定。
package slack

import (
	"embed"
	"io/fs"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
)

//go:embed assets/*
var assetsFS embed.FS

const (
	pluginID     = "slack"
	keyClientID  = "SLACK_CLIENT_ID"
	keyClientSec = "SLACK_CLIENT_SECRET"
)

// Plugin は Slack OAuth + API クライアントを提供する同梱プラグイン。
type Plugin struct {
	store  *secrets.Store
	reader *settings.Reader
	tokens *SecretTokenStore
	states *stateStore
}

// New は secrets ストアを注入して Plugin を構築する。
func New(store *secrets.Store) Plugin {
	return Plugin{
		store:  store,
		reader: settings.NewReader(store, pluginID),
		tokens: NewSecretTokenStore(store),
		states: newStateStore(),
	}
}

func (p Plugin) Meta() plugin.Meta {
	return plugin.Meta{ID: pluginID, Title: "Slack", Pane: plugin.PaneLeft, Order: 5}
}

func (p Plugin) Assets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}

func (p Plugin) SettingsSchema() []plugin.Field {
	return []plugin.Field{
		{Key: keyClientID, Label: "Slack Client ID", Secret: true},
		{Key: keyClientSec, Label: "Slack Client Secret", Secret: true},
	}
}

// Client は保存済みトークンから API クライアントを生成して返す。0 件なら ErrNoToken。
func (p Plugin) Client() (*APIClient, error) {
	tok, err := p.tokens.GetAny()
	if err != nil {
		return nil, err
	}
	return NewAPIClient(tok.AccessToken), nil
}

// oauthClient は現在の CLIENT_ID/SECRET から OAuthClient を組み立てる。未設定なら nil。
func (p Plugin) oauthClient() *OAuthClient {
	id, ok1 := p.reader.Get(keyClientID)
	sec, ok2 := p.reader.Get(keyClientSec)
	if !ok1 || !ok2 || id == "" || sec == "" {
		return nil
	}
	return &OAuthClient{ClientID: id, ClientSecret: sec, UserScopes: DefaultUserScopes}
}
```

```go
// plugins/slack/handler.go
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
	s.m[state] = time.Now().Add(stateTTL)
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
	fmt.Fprintf(w, successHTML,
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
	fmt.Fprintf(w, errorHTML, html.EscapeString(msg))
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
```

- [ ] **Step 4: Run test to verify it passes**

まず `assets/` が空だと `//go:embed assets/*` がコンパイルエラーになるため、最小の `index.js` を置く（Task 7 で本実装）。
```bash
mkdir -p plugins/slack/assets && printf '// placeholder\nexport async function render(root){ root.textContent = "Slack"; }\n' > plugins/slack/assets/index.js
```
Run: `go test ./plugins/slack/ -run 'Start|Callback|SettingsSchema|ReaderReads' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add plugins/slack/slack.go plugins/slack/handler.go plugins/slack/handler_test.go plugins/slack/assets/index.js
git commit -m "feat(slack): OAuth フローのハンドラと Plugin 本体を追加" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: フロントエンドタブ (assets/index.js)

**Files:**
- Modify: `plugins/slack/assets/index.js`（Task 6 のプレースホルダを本実装に差し替え）

**Interfaces:**
- Consumes: `GET /plugins/slack/tokens`（`{workspaces:[{team_name,workspace_id,user_id,scope,obtained_at}], configured:bool}`）、`GET /plugins/slack/start`
- Produces: `export async function render(root)`（シェルが動的 import して呼ぶ契約）

- [ ] **Step 1: 本実装に差し替え**

```javascript
// plugins/slack/assets/index.js
// Slack タブ: 接続状態の表示 + 「Slack 連携」ボタン + 接続済み workspace 一覧。
// シェルは /plugins/slack/assets/index.js を動的 import し render(root) を呼ぶ。
function esc(s) {
  return String(s).replace(/[<>&"']/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;', "'": '&#39;' }[c]));
}

export async function render(root) {
  root.innerHTML =
    '<div class="slack-panel">' +
    '<p><a id="slackConnect" href="/plugins/slack/start" target="_blank" rel="noopener">Slack 連携</a></p>' +
    '<div id="slackStatus"></div>' +
    '<div id="slackList"></div>' +
    '</div>';

  const status = root.querySelector('#slackStatus');
  const list = root.querySelector('#slackList');

  async function refresh() {
    try {
      const res = await fetch('/plugins/slack/tokens');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const data = await res.json();
      if (!data.configured) {
        status.innerHTML = '<p class="empty-section">SLACK_CLIENT_ID / SLACK_CLIENT_SECRET を Settings タブで設定してください。</p>';
      } else {
        status.innerHTML = '';
      }
      const wss = data.workspaces || [];
      if (!wss.length) {
        list.innerHTML = '<p class="empty-section">接続済み workspace なし</p>';
        return;
      }
      const rows = wss.map(w => {
        const at = (w.obtained_at || '').replace('T', ' ').substring(0, 19);
        return '<tr><td>' + esc(w.team_name) + '</td><td>' + esc(w.workspace_id) + '</td>' +
          '<td>' + esc(w.user_id) + '</td><td>' + esc(at) + '</td></tr>';
      }).join('');
      list.innerHTML = '<table class="slack-table"><thead><tr><th>Team</th><th>Workspace</th><th>User</th><th>取得日時</th></tr></thead><tbody>' + rows + '</tbody></table>';
    } catch (e) {
      status.innerHTML = '<p class="empty-section">読み込み失敗: ' + esc(e.message) + '</p>';
    }
  }

  root.querySelector('#slackConnect').addEventListener('click', () => {
    // 認証完了後に手動リロードする運用。連携クリック後の復帰時にも更新できるよう少し待って再取得。
    setTimeout(refresh, 3000);
  });

  await refresh();
}
```

- [ ] **Step 2: ビルド確認**

Run: `go build ./plugins/slack/`
Expected: 成功（embed が解決する）。

- [ ] **Step 3: Commit**

```bash
git add plugins/slack/assets/index.js
git commit -m "feat(slack): 接続状態と連携ボタンを表示する Slack タブ UI を追加" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 8: 参照デモへの結線と README 追従

**Files:**
- Modify: `cmd/agentarium/main.go`（import に slack を追加、`app.Register(...)` に `slack.New(sec)` を追加）
- Modify: `README.md`（プラグイン一覧・環境変数・データ保存先・ルートの表を更新）

**Interfaces:**
- Consumes: `slack.New(*secrets.Store)`（Task 6）、`sec`（main.go 内の `*secrets.Store`）

**Note:** `cmd/agentarium` は参照デモ。`sec` は `app.WithSecrets(sec)` 前に生成済み。`slack.New(sec)` は他プラグインと同様に `app.Register(...)` で登録する（Register は WithSecrets より前に呼ばれているが、slack プラグインは store を直接保持するので順序に依存しない）。

- [ ] **Step 1: main.go に import と登録を追加**

`import` ブロックの plugins 群に追加:
```go
	"github.com/ktat/agentarium/plugins/sessions"
	"github.com/ktat/agentarium/plugins/slack"
```

`app.Register(` 呼び出しに追加（`manifestPlugin,` の後ろ）:
```go
		chat.New(chatStore).WithSessionLookup(svc.SessionID),
		manifestPlugin,
		slack.New(sec),
	); err != nil {
```

- [ ] **Step 2: ビルドとテスト**

Run: `go build ./... && go test ./...`
Expected: すべて PASS。

- [ ] **Step 3: README 更新**

`README.md` 内の該当箇所を更新する:
- 同梱プラグイン一覧に `slack`（Slack OAuth 連携 + API クライアント）を追記。
- 環境変数 / Settings 項目表に `SLACK_CLIENT_ID` / `SLACK_CLIENT_SECRET`（Settings タブで設定、暗号化保存）を追記。
- データ保存先に「Slack トークン: secrets ストア内 `slack.tokens`（暗号化）」を追記。
- プラグインのルート例に `/plugins/slack/{start,callback,tokens}` を追記。

（実際の表の位置・書式は README の既存フォーマットに合わせる。該当表が無い項目は新設せず既存の最も近い表へ行追加する。）

- [ ] **Step 4: Commit**

```bash
git add cmd/agentarium/main.go README.md
git commit -m "feat(slack): 参照デモに slack プラグインを結線し README を更新" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 9: 仕上げ（フルテスト + lint）

**Files:** なし（検証のみ）

- [ ] **Step 1: フルテスト**

Run: `make test`（= `go test -race ./...`）
Expected: すべて PASS。

- [ ] **Step 2: lint**

Run: `make lint`
Expected: エラーなし。

- [ ] **Step 3: 行数確認**

Run: `wc -l plugins/slack/*.go`
Expected: 各ファイル 700 行以内。

---

## Self-Review

**Spec coverage:**
- §2 配置（バンドルプラグイン）→ Task 6（slack.go）。
- §3 ファイル構成 → Task 1〜7 で全ファイル作成。
- §4 配線/公開 API（New/SettingsProvider/RouteProvider/FrontendProvider/Client）→ Task 6。
- §5 変更点（暗号化保存→Task 4、redirect_uri を Host から→Task 6 handler、state TTL→Task 6 stateStore、ルートパス→Task 6 Routes）。
- §6 データフロー → Task 6/7 で実現。
- §7 エラーハンドリング（503/400/502/ErrNoToken/マスク）→ Task 1・5・6。
- §8 テスト → 各 Task の Step 1。
- §9 ドキュメント → Task 8。
- §10 非対象 → 送信 API なし・workspace 選択 UI なし・リフレッシュなし・redirect 明示設定なしを守る。

**Placeholder scan:** Task 6 で `assets/index.js` の一時プレースホルダを置くが、Task 7 で本実装に差し替える（embed コンパイル制約のための意図的な順序）。それ以外に TODO/TBD なし。

**Type consistency:** `AuthorizeURL(state, redirectURL)` / `Exchange(ctx, code, redirectURL)` の 2 引数版を Task 3 で定義し Task 6 で同一シグネチャで呼ぶ。`SecretTokenStore.GetAny/GetAll/Get/Save`（Task 4）を Task 6 の `Client()`/`handleTokens` が一致して使用。`apiBaseOverride`（Task 5）/`accessURLOverride`（Task 3）はテスト専用変数で命名一貫。
