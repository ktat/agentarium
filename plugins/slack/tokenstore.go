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
	if len(tf.Workspaces) == 0 {
		return nil, ErrNoToken
	}
	// 最新 ObtainedAt を選ぶ。同一時刻は WorkspaceID 昇順でタイブレークし、
	// map 反復順への依存（非決定性）を排除する。
	var newest storedToken
	for _, st := range tf.Workspaces {
		if newest.WorkspaceID == "" ||
			st.ObtainedAt.After(newest.ObtainedAt) ||
			(st.ObtainedAt.Equal(newest.ObtainedAt) && st.WorkspaceID < newest.WorkspaceID) {
			newest = st
		}
	}
	return newest.toToken()
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
