package terminal

import (
	"encoding/json"
	"testing"
)

func TestSessionState_KnownValuesString(t *testing.T) {
	cases := map[SessionState]string{
		StateIdle:         "idle",
		StateRunning:      "running",
		StateAwaitingUser: "awaiting_user",
		StatePending:      "pending",
	}
	for st, want := range cases {
		if st.String() != want {
			t.Errorf("String() = %q, want %q", st.String(), want)
		}
	}
}

func TestSessionState_Comparable(t *testing.T) {
	a, b := StateIdle, StateIdle
	if a != b {
		t.Fatal("same state should be ==")
	}
	if StateIdle == StateRunning {
		t.Fatal("different states should be !=")
	}
	// map キーとして使える
	m := map[SessionState]int{StateIdle: 1, StateRunning: 2}
	if m[StateIdle] != 1 || m[StateRunning] != 2 {
		t.Fatal("SessionState should work as map key")
	}
}

func TestNewSessionState(t *testing.T) {
	got, err := NewSessionState("running")
	if err != nil {
		t.Fatalf("running should be valid: %v", err)
	}
	if got != StateRunning {
		t.Fatalf("NewSessionState(running) != StateRunning")
	}
	if _, err := NewSessionState("garbage"); err == nil {
		t.Fatal("garbage should be rejected")
	}
	if _, err := NewSessionState(""); err == nil {
		t.Fatal("empty should be rejected")
	}
}

func TestSessionState_JSONRoundTrip(t *testing.T) {
	b, err := json.Marshal(StateRunning)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"running"` {
		t.Fatalf("marshal = %s, want \"running\"", b)
	}
	var s SessionState
	if err := json.Unmarshal([]byte(`"awaiting_user"`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s != StateAwaitingUser {
		t.Fatalf("unmarshal got %v", s)
	}
	// 不正値の Unmarshal はエラー
	if err := json.Unmarshal([]byte(`"bogus"`), &s); err == nil {
		t.Fatal("unmarshal of bogus should error")
	}
}

func TestSessionInfo_JSONUsesStateString(t *testing.T) {
	si := SessionInfo{ID: "t1", State: StateRunning, Running: true}
	b, err := json.Marshal(si)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// state が "running" 文字列で出る（struct の {} ではない）
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if m["State"] != "running" {
		t.Fatalf("SessionInfo.State JSON = %v, want running", m["State"])
	}
}
