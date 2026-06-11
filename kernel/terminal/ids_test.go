package terminal

import "testing"

func TestNewTerminalID(t *testing.T) {
	good := []string{"a", "t1", "session-068ad7b1", "tab_2", "claude-demo-123"}
	for _, s := range good {
		id, err := NewTerminalID(s)
		if err != nil {
			t.Errorf("%q should be valid: %v", s, err)
		}
		if id.String() != s {
			t.Errorf("String() = %q, want %q", id.String(), s)
		}
	}
	bad := []string{"", "A", "a b", "a/b", "a.b", "{x}", "a:b"}
	for _, s := range bad {
		if _, err := NewTerminalID(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestNewSessionID(t *testing.T) {
	// SessionID は空を許さないが、文字種は claude UUID 等を想定して緩め（空でなければ可）。
	if _, err := NewSessionID(""); err == nil {
		t.Fatal("empty SessionID should be rejected")
	}
	sid, err := NewSessionID("068ad7b1-7f4c-4e8e-a987-610faa07d9dd")
	if err != nil {
		t.Fatalf("uuid should be valid: %v", err)
	}
	if sid.String() != "068ad7b1-7f4c-4e8e-a987-610faa07d9dd" {
		t.Fatal("SessionID String mismatch")
	}
}
