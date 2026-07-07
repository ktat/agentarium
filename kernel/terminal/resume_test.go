package terminal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/terminal"
)

type fakeResumableAgent struct{ artifact string }

func (fakeResumableAgent) Name() string                                      { return "fake" }
func (fakeResumableAgent) Invocation(terminal.RunRequest) (string, []string) { return "fake", nil }
func (f fakeResumableAgent) ResumeArtifact(workDir, sessionID string) string { return f.artifact }

type fakePlainAgent struct{}

func (fakePlainAgent) Name() string                                      { return "plain" }
func (fakePlainAgent) Invocation(terminal.RunRequest) (string, []string) { return "plain", nil }

func TestCanResume(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(existing, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.jsonl")

	if !terminal.CanResume(fakeResumableAgent{artifact: existing}, dir, "s1") {
		t.Fatal("existing artifact should be resumable")
	}
	if terminal.CanResume(fakeResumableAgent{artifact: missing}, dir, "nope") {
		t.Fatal("missing artifact should NOT be resumable")
	}
	if !terminal.CanResume(fakePlainAgent{}, dir, "s1") {
		t.Fatal("non-ResumableAgent should default to true")
	}
	if !terminal.CanResume(fakeResumableAgent{artifact: ""}, dir, "s1") {
		t.Fatal("empty artifact should default to true")
	}
	if !terminal.CanResume(nil, dir, "s1") {
		t.Fatal("nil agent should default to true")
	}
}
