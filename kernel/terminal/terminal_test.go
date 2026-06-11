package terminal

import "testing"

func TestSessionStateConstants(t *testing.T) {
	cases := map[SessionState]string{
		StateIdle:         "idle",
		StateRunning:      "running",
		StateAwaitingUser: "awaiting_user",
	}
	for st, want := range cases {
		if st.String() != want {
			t.Fatalf("state %v: want %q, got %q", st, want, st.String())
		}
	}
}

func TestSessionInfoZeroValue(t *testing.T) {
	var si SessionInfo
	if si.ID != "" || si.Running {
		t.Fatalf("zero SessionInfo unexpected: %+v", si)
	}
}

func TestRunRequest_ColsAltRowsZeroValue(t *testing.T) {
	var req RunRequest
	if req.Cols != 0 || req.AltRows != 0 {
		t.Fatalf("zero RunRequest should have Cols=AltRows=0, got %+v", req)
	}
}

func TestConfigAgent_IgnoresColsAltRows(t *testing.T) {
	// サイズ情報は Agent の関心外。Invocation の出力に影響しないことを確認する。
	ag := ConfigAgent{AgentName: "x", Binary: "x", ModelFlag: "--model"}
	_, args1 := ag.Invocation(RunRequest{Model: "m"})
	_, args2 := ag.Invocation(RunRequest{Model: "m", Cols: 80, AltRows: 40})
	if len(args1) != len(args2) {
		t.Fatalf("Cols/AltRows must not affect args: %v vs %v", args1, args2)
	}
	for i := range args1 {
		if args1[i] != args2[i] {
			t.Fatalf("args differ at %d: %q vs %q", i, args1[i], args2[i])
		}
	}
}
