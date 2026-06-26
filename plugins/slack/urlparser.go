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
