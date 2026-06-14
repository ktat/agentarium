package terminal

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/server"
	"github.com/ktat/agentarium/kernel/terminal/observer"
)

// 静的 interface assertion: *Service は server.Mountable を満たす
// （kernel/server.WithTerminal が受け取るための契約）。
var _ server.Mountable = (*Service)(nil)

// ErrNoBackends は ServiceConfig に backend が 1 つも指定されなかったときに返る。
var ErrNoBackends = errors.New("terminal: no backends configured")

// ServiceConfig は Service の構築パラメータ。
type ServiceConfig struct {
	Agents    *AgentRegistry               // 必須。agent 名解決に使う
	Backends  []Backend                    // 必須。1 つ以上の backend
	Active    string                       // active backend 名。空なら Backends[0] を採用
	CanResume func(rec SessionRecord) bool // active backend の Restore に渡す復元可否判定（nil 可）
}

// Service は Agent ターミナルサービスの制御層。
//   - agents から agent 名を解決
//   - 複数 backend から active を 1 つ選び、start/stop/inject/list/renderer を提供
//   - フロント assets と WS ルートは active backend の Routes()/Assets() を経由
type Service struct {
	agents    *AgentRegistry
	backends  map[string]Backend
	active    Backend
	events    *sseHub
	lastAgg   string
	lastAggMu sync.Mutex
	detector  *detector // nil = 状態検出無効（active backend が未対応）
}

// NewService は config から Service を構築する。
//   - Backends が空なら ErrNoBackends
//   - Active が空なら Backends[0] を active に
//   - Active が backend 名と一致しなければ "unknown active backend" エラー
func NewService(cfg ServiceConfig) (*Service, error) {
	if len(cfg.Backends) == 0 {
		return nil, ErrNoBackends
	}
	backends := make(map[string]Backend, len(cfg.Backends))
	for _, b := range cfg.Backends {
		name := b.Name()
		if _, dup := backends[name]; dup {
			return nil, fmt.Errorf("terminal: duplicate backend name %q", name)
		}
		backends[name] = b
	}
	active := cfg.Backends[0]
	if cfg.Active != "" {
		b, ok := backends[cfg.Active]
		if !ok {
			return nil, fmt.Errorf("terminal: unknown active backend %q", cfg.Active)
		}
		active = b
	}
	svc := &Service{agents: cfg.Agents, backends: backends, active: active, events: newSSEHub()}
	active.AddStateListener(svc.onStateChange)
	svc.wireDetector()
	if restored, total := active.Restore(cfg.CanResume); total > 0 {
		log.Printf("terminal: restored %d/%d session(s) on backend %q", restored, total, active.Name())
	}
	return svc, nil
}

// Active は現在選択されている backend を返す。
func (s *Service) Active() Backend { return s.active }

// Close は Close() error を実装する全 backend を停止する（wrap backend の
// warmup / persist goroutine 等）。App.Shutdown から呼ばれ、library 消費者の
// graceful shutdown で goroutine leak を防ぐ（R1）。Close を持たない backend
// （xterm 等）は素通り。最初の非 nil error を返す。
func (s *Service) Close() error {
	if s.detector != nil {
		s.detector.close()
	}
	var firstErr error
	for _, b := range s.backends {
		c, ok := b.(interface{ Close() error })
		if !ok {
			continue
		}
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// wireDetector は active backend が ObserverBackend かつ StateSetter のときだけ
// PTY 状態検出を有効化する。満たさない backend では検出無効（idle 固定）。
func (s *Service) wireDetector() {
	ob, okObs := s.active.(ObserverBackend)
	setter, okSet := s.active.(StateSetter)
	if !okObs || !okSet {
		return
	}
	obs := observer.New()
	d := newDetector(setter, s.currentStates, s.patternsForTerminal, time.Now)
	d.register(obs)
	ob.SetObserver(obs)
	d.start()
	s.detector = d
}

// currentStates は active backend の List から id→state マップを作る。
func (s *Service) currentStates() map[string]SessionState {
	items := s.active.List()
	out := make(map[string]SessionState, len(items))
	for _, it := range items {
		out[it.ID] = it.State
	}
	return out
}

// patternsForTerminal は terminal id → agent → StatePatterns を解決する。
// agent が StateAware 非実装、または id が未知なら (zero,false)。
func (s *Service) patternsForTerminal(id string) (StatePatterns, bool) {
	if s.agents == nil {
		return StatePatterns{}, false
	}
	var name string
	for _, it := range s.active.List() {
		if it.ID == id {
			name = it.AgentName
			break
		}
	}
	if name == "" {
		return StatePatterns{}, false
	}
	ag := s.agents.Resolve(name)
	sa, ok := ag.(StateAware)
	if !ok {
		return StatePatterns{}, false
	}
	return sa.StatePatterns(), true
}

// Agents は agent レジストリを返す（拡張用の脱出口）。
func (s *Service) Agents() *AgentRegistry { return s.agents }

// resolveAgent は HTTP リクエストの agent パラメータから Agent を解決する。
// 空文字なら既定エージェントを返す。未登録なら nil。
func (s *Service) resolveAgent(name string) Agent {
	if s.agents == nil {
		return nil
	}
	if name == "" {
		return s.agents.Default()
	}
	return s.agents.Resolve(name)
}

// EnvActiveBackend は環境変数 AGENTARIUM_TERMINAL_RENDERER を返す。
// 空なら既定 "xterm"。
func EnvActiveBackend() string {
	if v := os.Getenv("AGENTARIUM_TERMINAL_RENDERER"); v != "" {
		return v
	}
	return "xterm"
}

// injectMaxBytes は POST /terminal/inject で受け付けるリクエストボディの上限（64 KiB）。
const injectMaxBytes = 64 * 1024

// MountOn は Service の制御 HTTP route と active backend の Routes/Assets を
// 既存 mux に登録する。kernel/server.New からも consumer の独自 mux からも
// 呼べる共通の組み込み口。
//
// 登録される route:
//
//	POST /terminal/start, /terminal/stop, /terminal/inject
//	GET  /terminal/list, /terminal/renderer
//	active backend の Routes() を /terminal 配下に
//	active backend の Assets() を /terminal/assets/<renderer>/ に
func (s *Service) MountOn(mux *http.ServeMux) {
	mux.HandleFunc("POST /terminal/start", s.handleStart)
	mux.HandleFunc("POST /terminal/stop", s.handleStop)
	mux.HandleFunc("GET /terminal/list", s.handleList)
	mux.HandleFunc("GET /terminal/renderer", s.handleRenderer)
	mux.HandleFunc("POST /terminal/inject", s.handleInject)
	mux.HandleFunc("GET /terminal/events", s.handleEvents)
	for _, rt := range s.active.Routes() {
		pattern := rt.Method + " /terminal" + rt.Path
		mux.HandleFunc(pattern, rt.Handler)
	}
	if assets := s.active.Assets(); assets != nil {
		prefix := "/terminal/assets/" + s.active.Renderer() + "/"
		mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(assets))))
	}
}

// Handler は Service の制御ルートを単独 http.Handler として返す。
// standalone 利用（kernel/server を経由しない）向け。kernel/server 経由なら
// MountOn(mux) を直接呼ぶ。
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	s.MountOn(mux)
	return mux
}

// handleStart は POST /terminal/start?id=&label=&agent=&model=&session_name=&resume=&cols=&alt_rows=
// で active backend に Start を発行する。
func (s *Service) handleStart(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if _, err := NewTerminalID(id); err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	ag := s.resolveAgent(q.Get("agent"))
	if ag == nil {
		http.Error(w, "unknown or missing agent", http.StatusBadRequest)
		return
	}
	req := RunRequest{
		Model:       q.Get("model"),
		Resume:      q.Get("resume"),
		SessionName: q.Get("session_name"),
		Cols:        clampedAtoi(q.Get("cols")),
		AltRows:     clampedAtoi(q.Get("alt_rows")),
	}
	if err := s.active.Start(id, q.Get("label"), ag, req); err != nil {
		log.Printf("terminal/service start id=%s: %v", id, err)
		http.Error(w, "start failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStop は POST /terminal/stop?id= で active backend に Stop を発行する。
func (s *Service) handleStop(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if err := s.active.Stop(id); err != nil {
		log.Printf("terminal/service stop id=%s: %v", id, err)
		http.Error(w, "stop failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleList は GET /terminal/list で active backend の List を JSON で返す。
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": s.active.List()})
}

// handleRenderer は GET /terminal/renderer で active backend の renderer/name を返す。
// shell はこの値を使って /terminal/assets/<renderer>/ から JS を動的 import する。
func (s *Service) handleRenderer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"renderer": s.active.Renderer(),
		"name":     s.active.Name(),
	})
}

type injectRequest struct {
	TerminalID string `json:"terminal_id"`
	Text       string `json:"text"`
	Enter      bool   `json:"enter,omitempty"`
}

// handleInject は POST /terminal/inject でテキストを指定 terminal の PTY に流す。
// CSRF 多層防御:
//  1. localhost bind（cmd/agentarium 既定）
//  2. Origin/Referer が public な host のときは 403（handleStart/handleStop も同様）
//  3. body の最大長は injectMaxBytes に制限
func (s *Service) handleInject(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, injectMaxBytes)
	var body injectRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("terminal/service inject decode: %v", err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.TerminalID == "" {
		http.Error(w, "terminal_id is required", http.StatusBadRequest)
		return
	}
	if body.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if err := s.active.Inject(body.TerminalID, body.Text, body.Enter); err != nil {
		log.Printf("terminal/service inject id=%s: %v", body.TerminalID, err)
		http.Error(w, "inject failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clampedAtoi は s を整数として解釈し [0, 65535] にクランプする。
// 解釈失敗・空文字・負値・65535 超は 0 を返す（cols/altRows のみで使う前提）。
func clampedAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	if n > 65535 {
		return 65535
	}
	return n
}
