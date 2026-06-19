package observer

import (
	"reflect"
	"testing"
)

func collect(feeds ...[]byte) []string {
	var got []string
	b := newLineBuffer(func(line string) { got = append(got, line) })
	for _, f := range feeds {
		b.Feed(f)
	}
	return got
}

func TestLineBuffer_SplitsLines(t *testing.T) {
	got := collect([]byte("hello\nworld\n"))
	want := []string{"hello", "world"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLineBuffer_StripsCSI(t *testing.T) {
	got := collect([]byte("\x1b[31mred\x1b[0m\n"))
	if len(got) != 1 || got[0] != "red" {
		t.Fatalf("got %v want [red]", got)
	}
}

func TestLineBuffer_StripsOSC8Hyperlink(t *testing.T) {
	got := collect([]byte("\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\\n"))
	if len(got) != 1 || got[0] != "link" {
		t.Fatalf("got %v want [link]", got)
	}
}

func TestLineBuffer_Backspace(t *testing.T) {
	got := collect([]byte("ab\x08c\n"))
	if len(got) != 1 || got[0] != "ac" {
		t.Fatalf("got %v want [ac]", got)
	}
}

func TestLineBuffer_SplitFeedAcrossCalls(t *testing.T) {
	got := collect([]byte("he"), []byte("llo\n"))
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %v want [hello]", got)
	}
}
