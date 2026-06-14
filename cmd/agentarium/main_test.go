package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalStorePath(t *testing.T) {
	p, err := terminalStorePath("wrap")
	if err != nil {
		t.Fatalf("terminalStorePath: %v", err)
	}
	if filepath.Base(p) != "terminal-wrap.json" {
		t.Fatalf("unexpected file name: %s", p)
	}
	if !strings.Contains(p, "agentarium") {
		t.Fatalf("path should be under agentarium config dir: %s", p)
	}
}

func TestClaudeAgent_ResumeArtifact(t *testing.T) {
	got := claudeAgent{}.ResumeArtifact("/tmp/work", "sess-1")
	if !strings.HasSuffix(got, "sess-1.jsonl") {
		t.Fatalf("ResumeArtifact should end with <sessionID>.jsonl: %s", got)
	}
	if !strings.Contains(got, filepath.Join(".claude", "projects")) {
		t.Fatalf("ResumeArtifact should be under .claude/projects: %s", got)
	}
	if (claudeAgent{}).ResumeArtifact("/tmp/work", "") != "" {
		t.Fatal("empty sessionID should return empty path")
	}
}
