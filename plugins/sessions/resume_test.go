package sessions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/terminal"
)

// fakeResumable は terminal.ResumableAgent を満たすテスト用 Agent。
type fakeResumable struct{ artifact string }

func (fakeResumable) Name() string                                      { return "fake" }
func (fakeResumable) Invocation(terminal.RunRequest) (string, []string) { return "fake", nil }
func (f fakeResumable) ResumeArtifact(workDir, sessionID string) string { return f.artifact }

// plainAgent は ResumableAgent を満たさない Agent。
type plainAgent struct{}

func (plainAgent) Name() string                                      { return "plain" }
func (plainAgent) Invocation(terminal.RunRequest) (string, []string) { return "plain", nil }

func TestCanResume(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(existing, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.jsonl")

	if !CanResume(fakeResumable{artifact: existing}, dir, "s1") {
		t.Fatal("existing artifact should be resumable")
	}
	if CanResume(fakeResumable{artifact: missing}, dir, "nope") {
		t.Fatal("missing artifact should NOT be resumable")
	}
	// ResumableAgent 非対応 → 判定材料なし → 楽観的に true
	if !CanResume(plainAgent{}, dir, "s1") {
		t.Fatal("non-ResumableAgent should default to true")
	}
	// artifact 空 → 楽観的に true
	if !CanResume(fakeResumable{artifact: ""}, dir, "s1") {
		t.Fatal("empty artifact should default to true")
	}
	// nil agent → 楽観的に true
	if !CanResume(nil, dir, "s1") {
		t.Fatal("nil agent should default to true")
	}
}
