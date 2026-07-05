package terminal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newSessionStateForTest(s string) SessionState {
	st, err := NewSessionState(s)
	if err != nil {
		panic(err)
	}
	return st
}

func TestAggregateStates_Priority(t *testing.T) {
	mk := func(states ...string) []SessionInfo {
		var out []SessionInfo
		for _, s := range states {
			out = append(out, SessionInfo{State: newSessionStateForTest(s)})
		}
		return out
	}
	cases := []struct {
		name    string
		items   []SessionInfo
		highest string
	}{
		{"empty", nil, "idle"},
		{"idle only", mk("idle", "idle"), "idle"},
		{"pending counts as idle", mk("pending"), "idle"},
		{"running beats idle", mk("idle", "running"), "running"},
		{"awaiting beats running", mk("running", "awaiting_user"), "awaiting_user"},
	}
	for _, c := range cases {
		counts, highest := aggregateStates(c.items)
		if highest != c.highest {
			t.Errorf("%s: highest=%q want %q (counts=%v)", c.name, highest, c.highest, counts)
		}
	}
	counts, _ := aggregateStates(mk("idle", "running", "running", "awaiting_user", "pending"))
	if counts["idle"] != 2 || counts["running"] != 2 || counts["awaiting_user"] != 1 {
		t.Fatalf("counts mismatch: %v", counts)
	}
}

func TestSessionsPayload(t *testing.T) {
	items := []SessionInfo{
		{ID: "t1", Label: "alpha", State: newSessionStateForTest("running")},
		{ID: "t2", Label: "beta", State: newSessionStateForTest("idle")},
	}
	ss := sessionsPayload(items)
	if len(ss) != 2 {
		t.Fatalf("want 2, got %d", len(ss))
	}
	if ss[0]["id"] != "t1" || ss[0]["label"] != "alpha" || ss[0]["state"] != "running" {
		t.Fatalf("ss[0]=%v", ss[0])
	}
	if ss[1]["state"] != "idle" {
		t.Fatalf("ss[1]=%v", ss[1])
	}
}

func TestStateEventBytes_IncludesSessions(t *testing.T) {
	ss := []map[string]string{{"id": "t1", "label": "a", "state": "running"}}
	b := string(stateEventBytes(ss, map[string]int{"idle": 0, "running": 1, "awaiting_user": 0}, "running"))
	if !strings.HasPrefix(b, "event: state\ndata: ") || !strings.HasSuffix(b, "\n\n") {
		t.Fatalf("framing: %q", b)
	}
	for _, want := range []string{`"sessions"`, `"id":"t1"`, `"label":"a"`, `"state":"running"`, `"highest":"running"`, `"counts"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q in %q", want, b)
		}
	}
}

// stopBroadcastBackend は Stop で List から該当 id を除く最小 backend。
// handleStop が端末消滅を SSE 購読者へ配信することの検証に使う。
type stopBroadcastBackend struct {
	*fakeBackend
	mu  sync.Mutex
	ids map[string]SessionState
}

func (b *stopBroadcastBackend) List() []SessionInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]SessionInfo, 0, len(b.ids))
	for id, st := range b.ids {
		out = append(out, SessionInfo{ID: id, State: st})
	}
	return out
}

func (b *stopBroadcastBackend) Stop(id string) error {
	b.mu.Lock()
	delete(b.ids, id)
	b.mu.Unlock()
	return nil
}

// TestService_StopBroadcastsStateEvent は端末停止時に SSE の state イベントが
// 配信されることを検証する。タブを閉じた瞬間に Chat 履歴の「実行中」表示を
// 解除できるよう、Stop でも端末集合の変化を購読者へ push する必要がある。
func TestService_StopBroadcastsStateEvent(t *testing.T) {
	b := &stopBroadcastBackend{
		fakeBackend: &fakeBackend{name: "xterm"},
		ids:         map[string]SessionState{"t1": StateIdle},
	}
	agents := NewAgentRegistry("claude")
	agents.Register(ConfigAgent{AgentName: "claude", Binary: "claude"})
	svc, err := NewService(ServiceConfig{Agents: agents, Backends: []Backend{b}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ch := svc.events.add()
	defer svc.events.remove(ch)

	req := httptest.NewRequest("POST", "/terminal/stop?id=t1", nil)
	rr := httptest.NewRecorder()
	svc.handleStop(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("stop status = %d, want 204", rr.Code)
	}
	select {
	case msg := <-ch:
		if !strings.Contains(string(msg), "event: state") {
			t.Fatalf("broadcast not a state event: %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no state event broadcast after stop")
	}
}
