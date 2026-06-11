package terminal

import (
	"fmt"
	"regexp"
	"strings"
)

// validTerminalID は terminal ID（タブ識別子）の許容文字。plugin ID と同じく
// ルート/クエリで安全な範囲に縛る（#13 の plugin ID と同じ規約）。
var validTerminalID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// TerminalID はターミナルタブ識別子の value object。NewTerminalID で構築し、
// 不正文字を 1 箇所で弾く（HTTP 境界の primitive obsession 対策。D4）。
type TerminalID struct{ v string }

// NewTerminalID は id 文字列を検証して TerminalID を返す。空・不正文字はエラー。
func NewTerminalID(s string) (TerminalID, error) {
	if !validTerminalID.MatchString(s) {
		return TerminalID{}, fmt.Errorf("invalid terminal id %q: must match [a-z0-9][a-z0-9_-]*", s)
	}
	return TerminalID{v: s}, nil
}

// String は id 文字列を返す。
func (t TerminalID) String() string { return t.v }

// SessionID は agent セッション識別子（claude UUID 等）の value object。
// 文字種はエージェント依存なので空チェックのみ（resume 引数の元）。
type SessionID struct{ v string }

// NewSessionID は空でない session id を検証して返す。
func NewSessionID(s string) (SessionID, error) {
	if strings.TrimSpace(s) == "" {
		return SessionID{}, fmt.Errorf("empty session id")
	}
	return SessionID{v: s}, nil
}

// String は session id 文字列を返す。
func (s SessionID) String() string { return s.v }
