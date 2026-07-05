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
