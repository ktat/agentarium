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
