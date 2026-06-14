package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/terminal/observer"
)

// fakeBackend はテスト用の最小 Backend 実装。
type fakeBackend struct {
	name           string
	starts         int
	lastID         string
	routes         []plugin.Route
	restoreCalled  bool
	restoreApplied func(SessionRecord) bool
}

func (b *fakeBackend) Name() string                                            { return b.name }
func (b *fakeBackend) Renderer() string                                        { return b.name }
func (b *fakeBackend) Start(id, label string, ag Agent, req RunRequest) error {
	b.starts++
	b.lastID = id
	return nil
}
func (b *fakeBackend) Stop(id string) error                     { return nil }
func (b *fakeBackend) Inject(id, text string, enter bool) error { return nil }
func (b *fakeBackend) SetSessionID(id, sessionID string)        {}
func (b *fakeBackend) List() []SessionInfo                      { return nil }
func (b *fakeBackend) AddStateListener(l StateListener) {}
func (b *fakeBackend) Restore(canResume func(SessionRecord) bool) (int, int) {
	b.restoreCalled = true
	b.restoreApplied = canResume
	return 0, 0
}
func (b *fakeBackend) Routes() []plugin.Route { return b.routes }
func (b *fakeBackend) Assets() fs.FS                            { return nil }

func TestNewService_PicksActiveByName(t *testing.T) {
	x := &fakeBackend{name: "xterm"}
	w := &fakeBackend{name: "wrap"}
	agents := NewAgentRegistry("claude")
	agents.Register(ConfigAgent{AgentName: "claude", Binary: "claude"})

	svc, err := NewService(ServiceConfig{
		Agents:   agents,
		Backends: []Backend{x, w},
		Active:   "wrap",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc.Active().Name() != "wrap" {
		t.Fatalf("want active=wrap, got %q", svc.Active().Name())
	}
}

func TestNewService_DefaultsToFirstWhenActiveEmpty(t *testing.T) {
	x := &fakeBackend{name: "xterm"}
	w := &fakeBackend{name: "wrap"}
	svc, err := NewService(ServiceConfig{
		Agents:   NewAgentRegistry("claude"),
		Backends: []Backend{x, w},
		Active:   "",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc.Active().Name() != "xterm" {
		t.Fatalf("want first backend default (xterm), got %q", svc.Active().Name())
	}
}

func TestNewService_UnknownActiveErrors(t *testing.T) {
	if _, err := NewService(ServiceConfig{
		Agents:   NewAgentRegistry("claude"),
		Backends: []Backend{&fakeBackend{name: "xterm"}},
		Active:   "ghost",
	}); err == nil {
		t.Fatal("want error for unknown active backend")
	}
}

func TestNewService_NoBackendsErrors(t *testing.T) {
	if _, err := NewService(ServiceConfig{
		Agents:   NewAgentRegistry("claude"),
		Backends: nil,
	}); !errors.Is(err, ErrNoBackends) {
		t.Fatalf("want ErrNoBackends, got %v", err)
	}
}

func TestEnvActiveBackend(t *testing.T) {
	t.Setenv("AGENTARIUM_TERMINAL_RENDERER", "")
	if got := EnvActiveBackend(); got != "xterm" {
		t.Fatalf("default want xterm, got %q", got)
	}
	t.Setenv("AGENTARIUM_TERMINAL_RENDERER", "wrap")
	if got := EnvActiveBackend(); got != "wrap" {
		t.Fatalf("want wrap, got %q", got)
	}
}

func TestService_ResolveAgent(t *testing.T) {
	agents := NewAgentRegistry("claude")
	agents.Register(ConfigAgent{AgentName: "claude", Binary: "claude"})
	agents.Register(ConfigAgent{AgentName: "codex", Binary: "codex"})

	svc, _ := NewService(ServiceConfig{
		Agents:   agents,
		Backends: []Backend{&fakeBackend{name: "xterm"}},
	})

	if svc.resolveAgent("") == nil || svc.resolveAgent("").Name() != "claude" {
		t.Fatal("empty name should resolve to default (claude)")
	}
	if svc.resolveAgent("codex") == nil || svc.resolveAgent("codex").Name() != "codex" {
		t.Fatal("explicit codex resolution failed")
	}
	if svc.resolveAgent("ghost") != nil {
		t.Fatal("unknown agent should return nil")
	}
}

func newTestService(t *testing.T) (*Service, *fakeBackend) {
	t.Helper()
	x := &fakeBackend{name: "xterm"}
	agents := NewAgentRegistry("claude")
	agents.Register(ConfigAgent{AgentName: "claude", Binary: "claude"})
	svc, err := NewService(ServiceConfig{
		Agents:   agents,
		Backends: []Backend{x},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, x
}

func TestService_HandlerRendererJSON(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/terminal/renderer")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status: %d", res.StatusCode)
	}
	var body struct {
		Renderer string `json:"renderer"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Renderer != "xterm" || body.Name != "xterm" {
		t.Fatalf("body unexpected: %+v", body)
	}
}

func TestService_HandlerStartCallsBackend(t *testing.T) {
	svc, b := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/terminal/start?id=t1&label=L", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("status want 204, got %d", res.StatusCode)
	}
	if b.starts != 1 || b.lastID != "t1" {
		t.Fatalf("backend.Start not called: starts=%d lastID=%q", b.starts, b.lastID)
	}
}

func TestService_HandlerStartRequiresID(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/terminal/start", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status want 400, got %d", res.StatusCode)
	}
}

func TestService_HandlerStartUnknownAgent(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/terminal/start?id=t1&agent=ghost", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status want 400, got %d", res.StatusCode)
	}
}

func TestService_HandlerListEmpty(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/terminal/list")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status: %d", res.StatusCode)
	}
	var body struct {
		Items []SessionInfo `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestService_HandlerStopRequiresID(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/terminal/stop", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status want 400, got %d", res.StatusCode)
	}
}

func TestService_HandlerInjectRejectsCrossOrigin(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	body := `{"terminal_id":"t1","text":"hi"}`
	req, _ := http.NewRequest("POST", srv.URL+"/terminal/inject", strings.NewReader(body))
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("status want 403, got %d", res.StatusCode)
	}
}

func TestService_HandlerInjectAcceptsNoOrigin(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	body := `{"terminal_id":"t1","text":"hi"}`
	req, _ := http.NewRequest("POST", srv.URL+"/terminal/inject", strings.NewReader(body))
	// Origin / Referer 無し → 許可（curl 等）
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	// fakeBackend.Inject は常に nil を返すので 204 期待。
	if res.StatusCode != 204 {
		t.Fatalf("status want 204, got %d body=%s", res.StatusCode, string(b))
	}
}

func TestService_HandlerInjectBadJSON(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/terminal/inject", "application/json", strings.NewReader("not-json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status want 400, got %d", res.StatusCode)
	}
}

func TestService_HandlerMethodNotAllowed(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	// /start は POST のみ
	res, err := http.Get(srv.URL + "/terminal/start?id=t1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 405 {
		t.Fatalf("status want 405, got %d", res.StatusCode)
	}
}

func TestService_HandlerInjectRequiresTerminalID(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/terminal/inject", "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status want 400, got %d", res.StatusCode)
	}
}

func TestService_HandlerInjectRequiresText(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/terminal/inject", "application/json", strings.NewReader(`{"terminal_id":"t1"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status want 400, got %d", res.StatusCode)
	}
}

func TestClampedAtoi(t *testing.T) {
	cases := map[string]int{
		"":                     0,
		"0":                    0,
		"42":                   42,
		"65535":                65535,
		"65536":                65535, // upper clamp
		"100000":               65535,
		"-1":                   0, // 負値は 0
		"abc":                  0,
		"12x":                  0,
		"99999999999999999999": 0, // overflow → strconv.Atoi が ErrRange → 0
	}
	for in, want := range cases {
		if got := clampedAtoi(in); got != want {
			t.Errorf("clampedAtoi(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNewService_DuplicateBackendName(t *testing.T) {
	a := &fakeBackend{name: "x"}
	b := &fakeBackend{name: "x"}
	if _, err := NewService(ServiceConfig{
		Agents:   NewAgentRegistry("claude"),
		Backends: []Backend{a, b},
	}); err == nil {
		t.Fatal("want error for duplicate backend name")
	}
}

func TestService_HandlerStartRejectsCrossOrigin(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/terminal/start?id=t1", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("status want 403, got %d", res.StatusCode)
	}
}

func TestService_HandlerStopRejectsCrossOrigin(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/terminal/stop?id=t1", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("status want 403, got %d", res.StatusCode)
	}
}

func TestService_HandlerInjectRejectsOversizeBody(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	huge := strings.Repeat("x", 70*1024) // 70 KiB > 64 KiB cap
	body := `{"terminal_id":"t1","text":"` + huge + `"}`
	res, err := http.Post(srv.URL+"/terminal/inject", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("oversize body should be rejected, got status %d", res.StatusCode)
	}
}

func TestService_MountOnExternalMux(t *testing.T) {
	svc, _ := newTestService(t)
	mux := http.NewServeMux()
	svc.MountOn(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/terminal/renderer")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status want 200, got %d", res.StatusCode)
	}
}

func TestService_HandlerStartRejectsInvalidID(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	// 大文字やスラッシュ入りの id は 400。
	res, err := http.Post(srv.URL+"/terminal/start?id=Bad/ID", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("invalid id should be 400, got %d", res.StatusCode)
	}
}

func TestService_EventsStreamsInitialState(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/terminal/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type=%q", ct)
	}
	buf := make([]byte, 256)
	n, _ := res.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "event: state") {
		t.Fatalf("no initial state event: %q", string(buf[:n]))
	}
}

func TestService_MountOnIncludesBackendRoutes(t *testing.T) {
	x := &fakeBackend{name: "xterm"}
	x.routes = []plugin.Route{
		{Method: "GET", Path: "/ws", Handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ws-stub"))
		}},
	}
	agents := NewAgentRegistry("claude")
	agents.Register(ConfigAgent{AgentName: "claude", Binary: "claude"})
	svc, err := NewService(ServiceConfig{Agents: agents, Backends: []Backend{x}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	mux := http.NewServeMux()
	svc.MountOn(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/terminal/ws")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status want 200, got %d", res.StatusCode)
	}
}

// closeableBackend は Close() error を持つ fakeBackend（wrap backend 相当）。
type closeableBackend struct {
	*fakeBackend
	closed int
}

func (b *closeableBackend) Close() error {
	b.closed++
	return nil
}

// TestService_CloseClosesBackends は Service.Close が Close() を実装する
// backend のみを停止し、持たない backend は素通りすることを検証する（R1）。
func TestService_CloseClosesBackends(t *testing.T) {
	cb := &closeableBackend{fakeBackend: &fakeBackend{name: "wrap"}}
	plain := &fakeBackend{name: "xterm"} // Close を持たない
	agents := NewAgentRegistry("claude")
	svc, err := NewService(ServiceConfig{
		Agents:   agents,
		Backends: []Backend{plain, cb},
		Active:   "xterm",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cb.closed != 1 {
		t.Fatalf("closeable backend Close count = %d, want 1", cb.closed)
	}
	// 二重 Close でも panic しないこと（冪等性は backend 側の責務だが Service は素直に呼ぶ）。
	if err := svc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if cb.closed != 2 {
		t.Fatalf("after second Close count = %d, want 2", cb.closed)
	}
}

func TestNewService_DrivesActiveRestore(t *testing.T) {
	x := &fakeBackend{name: "xterm"}
	w := &fakeBackend{name: "wrap"}
	cr := func(SessionRecord) bool { return true }
	_, err := NewService(ServiceConfig{
		Agents:    NewAgentRegistry("xterm"),
		Backends:  []Backend{x, w},
		Active:    "wrap",
		CanResume: cr,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !w.restoreCalled {
		t.Fatal("active backend (wrap) Restore was not called")
	}
	if w.restoreApplied == nil {
		t.Fatal("CanResume was not passed to active backend Restore")
	}
	if x.restoreCalled {
		t.Fatal("non-active backend (xterm) Restore should not be called")
	}
}

func TestService_DetectorDrivesStateFromOutput(t *testing.T) {
	fb := newDetectFakeBackend("xterm")
	fb.agentName = "claude"
	agents := NewAgentRegistry("claude")
	agents.Register(detectFakeAgent{})

	svc, err := NewService(ServiceConfig{Agents: agents, Backends: []Backend{fb}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	fb.add("t1")
	fb.obs.OnOutput("t1", []byte("Do you want to proceed?\n"))

	if !waitForState(svc, "awaiting_user", time.Second) {
		t.Fatalf("expected awaiting_user in aggregated state, got %q", svc.lastAgg)
	}
}

type detectFakeAgent struct{}

func (detectFakeAgent) Name() string                              { return "claude" }
func (detectFakeAgent) Invocation(RunRequest) (string, []string) { return "claude", nil }
func (detectFakeAgent) StatePatterns() StatePatterns {
	return StatePatterns{
		Permission:       regexp.MustCompile(`(?i)do you want to proceed`),
		SustainedRunning: 2 * time.Second,
		IdleTimeout:      1500 * time.Millisecond,
		BurstGap:         time.Second,
	}
}

type detectFakeBackend struct {
	name      string
	agentName string
	obs       ObserverHooks
	mu        sync.Mutex
	ids       []string
	state     map[string]SessionState
	listeners []StateListener
}

func newDetectFakeBackend(name string) *detectFakeBackend {
	return &detectFakeBackend{name: name, state: map[string]SessionState{}}
}

func (b *detectFakeBackend) add(id string) {
	b.mu.Lock()
	b.ids = append(b.ids, id)
	b.state[id] = StateIdle
	b.mu.Unlock()
}

func (b *detectFakeBackend) Name() string                                           { return b.name }
func (b *detectFakeBackend) Start(id, label string, ag Agent, req RunRequest) error { return nil }
func (b *detectFakeBackend) Stop(id string) error                                   { return nil }
func (b *detectFakeBackend) Inject(id, text string, enter bool) error               { return nil }
func (b *detectFakeBackend) SetSessionID(id, sessionID string)                      {}
func (b *detectFakeBackend) AddStateListener(l StateListener) {
	b.mu.Lock()
	b.listeners = append(b.listeners, l)
	b.mu.Unlock()
}
func (b *detectFakeBackend) Restore(func(rec SessionRecord) bool) (int, int) { return 0, 0 }
func (b *detectFakeBackend) List() []SessionInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]SessionInfo, 0, len(b.ids))
	for _, id := range b.ids {
		out = append(out, SessionInfo{ID: id, AgentName: b.agentName, State: b.state[id]})
	}
	return out
}
func (b *detectFakeBackend) SetState(id string, s SessionState, source string) {
	b.mu.Lock()
	prev := b.state[id]
	b.state[id] = s
	ls := append([]StateListener(nil), b.listeners...)
	b.mu.Unlock()
	for _, l := range ls {
		l(id, prev, s, source)
	}
}
func (b *detectFakeBackend) SetObserver(o ObserverHooks) { b.obs = o }
func (b *detectFakeBackend) Renderer() string            { return b.name }
func (b *detectFakeBackend) Routes() []plugin.Route      { return nil }
func (b *detectFakeBackend) Assets() fs.FS               { return nil }

func waitForState(svc *Service, want string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		svc.lastAggMu.Lock()
		agg := svc.lastAgg
		svc.lastAggMu.Unlock()
		if strings.Contains(agg, want) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestForgetHook_DelegatesBoth(t *testing.T) {
	obs := observer.New()
	called := ""
	h := forgetHook{Observer: obs, also: func(id string) { called = id }}
	// ObserverHooks を満たすこと（OnInput/OnOutput/Forget）
	var _ ObserverHooks = h
	h.Forget("t1")
	if called != "t1" {
		t.Fatalf("detector forget not delegated, got %q", called)
	}
}
