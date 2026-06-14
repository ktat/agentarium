// Package terminal はカーネルの Agent ターミナルサービスの中核型を定義する。
// バックエンド実装（xterm / wrap）はサブパッケージに置き、本パッケージの
// Agent / RunRequest / Backend / SessionInfo を共有する。
package terminal

import (
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/ktat/agentarium/kernel/plugin"
)

// RunRequest は実行時にエージェントへ渡す中立パラメータ（valueobject）。
// claude 固有の引数（--model / --resume / -n）は Agent 実装が組み立てる。
// Cols/AltRows はターミナル renderer の hint で、Agent は無視する規約
// （wrap backend のみが Process.SetInitialSize へ橋渡しする）。
type RunRequest struct {
	Model       string // "" 指定なし
	Resume      string // 再開セッション識別子。"" 新規
	SessionName string // 表示名。"" 指定なし
	Cols        int    // 初期 cols。0 なら backend 既定
	AltRows     int    // alt-screen 時の初期 rows。0 なら backend 既定
}

// SessionState はセッションの状態を表す不変 value object。
// 生 string ではなく struct にすることで、不正値を NewSessionState / UnmarshalJSON で
// 弾き、未知状態が SSE/JSON 経由で client に流れるのを防ぐ。1 フィールドなので
// == 比較・map キーに使える。
type SessionState struct{ v string }

var (
	StatePending      = SessionState{"pending"}       // 遅延復元で未起動（wrap）
	StateIdle         = SessionState{"idle"}          // 起動済み・入力待ち
	StateRunning      = SessionState{"running"}       // 実行中
	StateAwaitingUser = SessionState{"awaiting_user"} // ユーザ確認待ち
)

// knownStates は NewSessionState / UnmarshalJSON の検証に使う既知状態集合。
var knownStates = map[string]SessionState{
	StatePending.v:      StatePending,
	StateIdle.v:         StateIdle,
	StateRunning.v:      StateRunning,
	StateAwaitingUser.v: StateAwaitingUser,
}

// NewSessionState は既知の状態文字列から SessionState を構築する。未知ならエラー。
func NewSessionState(s string) (SessionState, error) {
	if st, ok := knownStates[s]; ok {
		return st, nil
	}
	return SessionState{}, fmt.Errorf("unknown session state %q", s)
}

// String は状態の文字列表現を返す（ゼロ値は ""）。
func (s SessionState) String() string { return s.v }

// MarshalJSON は状態を文字列として出力する（SessionInfo の JSON 配信用）。
func (s SessionState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.v)
}

// UnmarshalJSON は文字列から状態を復元する。未知の値はエラー。
func (s *SessionState) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	st, err := NewSessionState(raw)
	if err != nil {
		return err
	}
	*s = st
	return nil
}

// SessionInfo は Backend.List の戻り。
type SessionInfo struct {
	ID        string
	Label     string
	SessionID string // 旧 UUID 相当（汎用名）
	State     SessionState
	Running   bool
}

// ObserverHooks は Registry が起動した Process の入出力を観測する callback。
// Sessions プラグイン（S1-P3）等が 1 実装で xterm/wrap 両 backend を購読できるよう、
// backend ごとに別型を持たず terminal package に 1 つだけ定義する。
type ObserverHooks interface {
	OnInput(terminalID string, data []byte)
	OnOutput(terminalID string, data []byte)
	Forget(terminalID string)
}

// StateListener は entry の状態遷移時に呼ばれる callback。source は "hook"|"pty"|"init"。
type StateListener func(id string, prev, next SessionState, source string)

// TerminalBackend はターミナルのドメイン操作面。agent でプロセスを起動/停止/入力し、
// 一覧・セッション ID を扱う。transport（HTTP/WS/assets）からは独立。
type TerminalBackend interface {
	Name() string // "xterm" / "wrap"
	Start(id, label string, ag Agent, req RunRequest) error
	Stop(id string) error
	Inject(id, text string, enter bool) error
	SetSessionID(id, sessionID string)
	List() []SessionInfo
	AddStateListener(StateListener)
	// Restore は起動時に永続化レコードから復元する（spec §B）。canResume が false を
	// 返すレコードは skip。store を持たない backend は (0,0) を返す no-op で満たす。
	// 戻り値は (復元できた件数, レコード総件数)。
	Restore(canResume func(rec SessionRecord) bool) (restored, total int)
}

// TransportBackend はフロント結線面。renderer 名・WS/HTTP ルート・フロント資産を返す。
type TransportBackend interface {
	Renderer() string       // shell に渡すフロント識別子
	Routes() []plugin.Route // WS 等。/terminal/ 配下に自動マウント
	Assets() fs.FS          // /terminal/assets/<renderer>/ に配信
}

// Backend は domain + transport の両面を満たすターミナルバックエンド。
// 既存の xterm / wrap backend はこの合成 interface を満たす（後方互換）。
// Service は当面これを使うが、消費者は TerminalBackend / TransportBackend を
// 個別に受け取って疎結合にできる。
type Backend interface {
	TerminalBackend
	TransportBackend
}
