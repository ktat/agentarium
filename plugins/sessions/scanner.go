// Package sessions は claude セッション一覧（jsonl）を扱うバンドルプラグイン。
// kernel/terminal の Agent 抽象には触れず、独立した同梱プラグインとして実装する。
package sessions

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// summaryCache は jsonl の「先頭 user メッセージ要約」を path×mtime でキャッシュする。
// 先頭 user メッセージはセッション開始時に書かれ以降不変なので、mtime が変わらなければ
// ファイルを開き直さない（一覧の繰り返し取得での N+1 open を回避）。スレッドセーフ。
type summaryCache struct {
	mu sync.Mutex
	m  map[string]summaryEntry // path -> {modTime, summary}
}

type summaryEntry struct {
	modTime time.Time
	summary string
}

func newSummaryCache() *summaryCache {
	return &summaryCache{m: make(map[string]summaryEntry)}
}

// summary は path の要約を返す。mtime 一致のキャッシュがあればそれを使い、
// なければ読み込んでキャッシュする。
func (c *summaryCache) summary(path string, modTime time.Time) string {
	c.mu.Lock()
	if e, ok := c.m[path]; ok && e.modTime.Equal(modTime) {
		c.mu.Unlock()
		return e.summary
	}
	c.mu.Unlock()

	s := firstUserMessageSummary(path)

	c.mu.Lock()
	c.m[path] = summaryEntry{modTime: modTime, summary: s}
	c.mu.Unlock()
	return s
}

// Session は jsonl 1 ファイルに対応する claude セッションのメタ情報。
type Session struct {
	UUID    string    `json:"uuid"`
	ModTime time.Time `json:"mod_time"`
	Summary string    `json:"summary"` // 先頭 user message を最大 80 文字で truncate
}

// encodeWorkDir は workDir 絶対パスの '/' と '.' を '-' に置換して
// claude code の projects ディレクトリ名規約に変換する。
func encodeWorkDir(workDir string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(workDir)
}

// SessionsDirFor は workDir に対応する ~/.claude/projects/<encoded> を返す。
func SessionsDirFor(workDir string) (string, error) {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects", encodeWorkDir(abs)), nil
}

// ListSessions は dir 内の <uuid>.jsonl 群を走査し、Session 一覧を mtime 降順で返す。
// dir が存在しない場合は空 + nil error。要約はキャッシュせず毎回読む（純粋関数）。
func ListSessions(dir string) ([]Session, error) {
	return listSessions(dir, nil)
}

// listSessions は ListSessions の本体。cache が非 nil なら要約取得に使う
// （path×mtime キャッシュで N+1 open を回避）。cache が nil なら毎回読む。
func listSessions(dir string, cache *summaryCache) ([]Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Session, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		uuid := strings.TrimSuffix(name, ".jsonl")
		path := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		var summary string
		if cache != nil {
			summary = cache.summary(path, info.ModTime())
		} else {
			summary = firstUserMessageSummary(path)
		}
		out = append(out, Session{
			UUID:    uuid,
			ModTime: info.ModTime(),
			Summary: summary,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// firstUserMessageSummary は jsonl を行頭から走査し、最初の user メッセージ
// （type:"user" もしくは message.role:"user"）の本文を最大 80 文字 (rune 単位) で
// truncate して返す。失敗・該当なしは空文字。
//
// claude code の jsonl は 1 行 1 レコードで、メッセージ行は
//
//	{"type":"user","message":{"role":"user","content": <string|[{type,text}]>}}
//
// の形（先頭行は last-prompt 等の非メッセージ行のことがある）。content は
// 文字列の場合と {type,text} 配列の場合の両方を扱う。
func firstUserMessageSummary(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// jsonl の 1 行は長くなりうる（assistant の大きな出力等）。バッファを拡げる。
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type != "user" && rec.Message.Role != "user" {
			continue
		}
		s := contentText(rec.Message.Content)
		if s == "" {
			continue
		}
		rs := []rune(s)
		if len(rs) > 80 {
			s = string(rs[:80])
		}
		return s
	}
	// 読み取りエラー（I/O 失敗 / 1 行が 4MB 上限超過）は「該当なし」と区別して
	// ログに出す。要約は空のままだが、silent に握り潰さない（R 系レビュー S3）。
	if err := sc.Err(); err != nil {
		log.Printf("plugins/sessions: scan %s: %v", path, err)
	}
	return ""
}

// contentText は claude メッセージの content（文字列 or {type,text} 配列）から
// テキスト本文を抽出する。どちらでもなければ空。
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// 1) 文字列の場合
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	// 2) [{type,text}] 配列の場合
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		return strings.TrimSpace(sb.String())
	}
	return ""
}
