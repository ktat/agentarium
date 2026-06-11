package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncodeWorkDir(t *testing.T) {
	if got := encodeWorkDir("/home/u/proj"); got != "-home-u-proj" {
		t.Fatalf("want -home-u-proj, got %q", got)
	}
	if got := encodeWorkDir("/a/b.c/d"); got != "-a-b-c-d" {
		t.Fatalf("want -a-b-c-d, got %q", got)
	}
}

func TestListSessions_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	got, err := ListSessions(tmp)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0, got %d", len(got))
	}
}

func TestListSessions_ParsesJSONLFiles(t *testing.T) {
	tmp := t.TempDir()
	a := filepath.Join(tmp, "uuid-aaa.jsonl")
	// 実 claude jsonl 形式: 先頭は非メッセージ行、続いて type:user の message.content。
	// content は文字列形式（content:array 形式は別テストで担保）。
	aBody := `{"type":"last-prompt","sessionId":"uuid-aaa"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"hello world from user"}}` + "\n"
	if err := os.WriteFile(a, []byte(aBody), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	b := filepath.Join(tmp, "uuid-bbb.jsonl")
	if err := os.WriteFile(b, []byte{}, 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}

	got, err := ListSessions(tmp)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d (%+v)", len(got), got)
	}

	byID := map[string]Session{}
	for _, s := range got {
		byID[s.UUID] = s
	}
	if byID["uuid-aaa"].Summary != "hello world from user" {
		t.Fatalf("uuid-aaa summary: %q", byID["uuid-aaa"].Summary)
	}
	if byID["uuid-bbb"].Summary != "" {
		t.Fatalf("uuid-bbb summary should be empty: %q", byID["uuid-bbb"].Summary)
	}
	if byID["uuid-aaa"].ModTime.IsZero() || time.Since(byID["uuid-aaa"].ModTime) > time.Minute {
		t.Fatalf("uuid-aaa modtime: %v", byID["uuid-aaa"].ModTime)
	}
}

func TestListSessions_TruncatesLongSummary(t *testing.T) {
	tmp := t.TempDir()
	long := strings.Repeat("a", 200)
	// content:array 形式（{type,text}）の truncate を担保する。
	body := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"` + long + `"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "uuid-c.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ListSessions(tmp)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || len([]rune(got[0].Summary)) > 80 {
		t.Fatalf("summary not truncated to 80 chars: len=%d", len([]rune(got[0].Summary)))
	}
}

func TestListSessions_MissingDirReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	got, err := ListSessions(filepath.Join(tmp, "no-such-dir"))
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0, got %d", len(got))
	}
}

func TestListSessions_SortedByModTimeDesc(t *testing.T) {
	tmp := t.TempDir()
	older := filepath.Join(tmp, "uuid-old.jsonl")
	if err := os.WriteFile(older, []byte{}, 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	newer := filepath.Join(tmp, "uuid-new.jsonl")
	if err := os.WriteFile(newer, []byte{}, 0o644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	got, err := ListSessions(tmp)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].UUID != "uuid-new" || got[1].UUID != "uuid-old" {
		t.Fatalf("not sorted desc by mtime: %+v", got)
	}
}

// TestSummaryCache_ReusesByModTime は path×mtime キャッシュが、mtime 不変なら
// ファイル内容が変わっても再読込せず（先頭 user メッセージは不変前提）、
// mtime が進めば読み直すことを検証する（N+1 open 回避）。
func TestSummaryCache_ReusesByModTime(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "uuid-cache.jsonl")
	write := func(text string) {
		body := `{"type":"user","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("first")
	cache := newSummaryCache()

	got, err := listSessions(tmp, cache)
	if err != nil || len(got) != 1 || got[0].Summary != "first" {
		t.Fatalf("initial: %+v err=%v", got, err)
	}

	// 内容を変えるが mtime を据え置く → キャッシュヒットで "first" のまま。
	fixed := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, fixed, fixed); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	got1, _ := listSessions(tmp, cache)
	if got1[0].Summary != "first" {
		t.Fatalf("expected cache hit 'first', got %q", got1[0].Summary)
	}
	write("second")
	if err := os.Chtimes(path, fixed, fixed); err != nil { // 内容変更後も mtime 据え置き
		t.Fatalf("chtimes: %v", err)
	}
	got2, _ := listSessions(tmp, cache)
	if got2[0].Summary != "first" {
		t.Fatalf("mtime 不変なら再読込しない想定。got %q", got2[0].Summary)
	}

	// mtime を進める → 読み直して "second"。
	later := time.Now()
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	got3, _ := listSessions(tmp, cache)
	if got3[0].Summary != "second" {
		t.Fatalf("mtime 更新後は再読込想定。got %q", got3[0].Summary)
	}
}
