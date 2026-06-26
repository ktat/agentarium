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
