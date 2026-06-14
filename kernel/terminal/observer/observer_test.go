package observer

import (
	"regexp"
	"testing"
)

func TestObserver_DispatchesMatchingLines(t *testing.T) {
	o := New()
	var hits []string
	o.Register(DirectionOutput, regexp.MustCompile(`(?i)proceed`), func(m MatchInfo) {
		hits = append(hits, m.TerminalID+":"+m.Line)
	})
	o.OnOutput("t1", []byte("do you want to proceed?\n"))
	o.OnOutput("t1", []byte("unrelated\n"))
	if len(hits) != 1 || hits[0] != "t1:do you want to proceed?" {
		t.Fatalf("hits=%v", hits)
	}
}

func TestObserver_CatchAllGetsLine(t *testing.T) {
	o := New()
	var lines []string
	o.Register(DirectionOutput, regexp.MustCompile(`.`), func(m MatchInfo) {
		lines = append(lines, m.Line)
	})
	o.OnOutput("t1", []byte("a\nb\n"))
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("lines=%v", lines)
	}
}

func TestObserver_NilSafe(t *testing.T) {
	var o *Observer
	o.OnOutput("t1", []byte("x\n"))
	o.Forget("t1")
}

func TestObserver_ForgetDropsBuffer(t *testing.T) {
	o := New()
	o.OnOutput("t1", []byte("partial"))
	o.Forget("t1")
	var lines []string
	o.Register(DirectionOutput, regexp.MustCompile(`.*`), func(m MatchInfo) { lines = append(lines, m.Line) })
	o.OnOutput("t1", []byte("\n"))
	if len(lines) != 1 || lines[0] != "" {
		t.Fatalf("lines=%v want [\"\"]", lines)
	}
}
