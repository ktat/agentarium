package terminal

import (
	"reflect"
	"testing"
)

func TestConfigAgent_ModelFlag(t *testing.T) {
	ag := ConfigAgent{AgentName: "codex", Binary: "codex", BaseArgs: []string{"chat"}, ModelFlag: "--model"}
	bin, args := ag.Invocation(RunRequest{Model: "o4"})
	if bin != "codex" {
		t.Fatalf("want codex, got %q", bin)
	}
	want := []string{"chat", "--model", "o4"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch: want %v, got %v", want, args)
	}
}

func TestConfigAgent_NoModelFlag_IgnoresModel(t *testing.T) {
	ag := ConfigAgent{AgentName: "x", Binary: "x", BaseArgs: []string{"run"}}
	_, args := ag.Invocation(RunRequest{Model: "ignored"})
	want := []string{"run"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch: want %v, got %v", want, args)
	}
}

func TestConfigAgent_Name(t *testing.T) {
	if (ConfigAgent{AgentName: "codex"}).Name() != "codex" {
		t.Fatal("ConfigAgent.Name mismatch")
	}
}

func TestAgentRegistry_RegisterResolveDefault(t *testing.T) {
	r := NewAgentRegistry("claude")
	r.Register(ConfigAgent{AgentName: "claude", Binary: "claude"})
	r.Register(ConfigAgent{AgentName: "codex", Binary: "codex"})

	if r.Resolve("codex") == nil {
		t.Fatal("resolve codex returned nil")
	}
	if r.Resolve("missing") != nil {
		t.Fatal("resolve missing should be nil")
	}
	if r.Default() == nil || r.Default().Name() != "claude" {
		t.Fatalf("default should be claude, got %v", r.Default())
	}
}
