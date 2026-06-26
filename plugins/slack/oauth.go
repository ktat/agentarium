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
