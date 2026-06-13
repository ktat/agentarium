package terminal

import (
	"strings"
	"testing"
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

func TestStateEventBytes_Format(t *testing.T) {
	b := stateEventBytes(map[string]int{"idle": 1, "running": 0, "awaiting_user": 0}, "idle")
	s := string(b)
	if !strings.HasPrefix(s, "event: state\ndata: ") || !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("bad SSE framing: %q", s)
	}
	if !strings.Contains(s, `"highest":"idle"`) || !strings.Contains(s, `"counts"`) {
		t.Fatalf("bad payload: %q", s)
	}
}
