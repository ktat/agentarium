package sessions

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"

	"github.com/ktat/agentarium/kernel/plugin"
)

//go:embed assets/*
var assetsFS embed.FS

// Plugin は claude セッション一覧を提供する同梱プラグイン。
// workDir に対応する ~/.claude/projects/<encoded>/*.jsonl を走査する。
type Plugin struct {
	workDir string
	cache   *summaryCache // 要約の path×mtime キャッシュ（/list 連打の N+1 open 回避）
}

// New は workDir を引数に Plugin を構築する。consumer の main で
// `sessions.New(workDir)` を Register する想定。
func New(workDir string) Plugin { return Plugin{workDir: workDir, cache: newSummaryCache()} }

func (p Plugin) Meta() plugin.Meta {
	return plugin.Meta{ID: "sessions", Title: "Sessions", Pane: plugin.PaneLeft, Order: 0}
}

func (p Plugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/list", Handler: p.handleList},
	}
}

func (p Plugin) Assets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}

// handleList は GET /plugins/sessions/list で session 一覧を JSON 配列で返す。
// 失敗時は 500 + 汎用メッセージ。詳細（絶対パスを含む err）はサーバログにのみ出し、
// HTTP 応答には載せない（~/.claude/projects/<encoded> 等の path 漏洩防止。R4）。
func (p Plugin) handleList(w http.ResponseWriter, r *http.Request) {
	dir, err := SessionsDirFor(p.workDir)
	if err != nil {
		log.Printf("plugins/sessions: resolve sessions dir: %v", err)
		http.Error(w, "failed to resolve sessions dir", http.StatusInternalServerError)
		return
	}
	items, err := listSessions(dir, p.cache)
	if err != nil {
		log.Printf("plugins/sessions: list sessions: %v", err)
		http.Error(w, "failed to list sessions", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []Session{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}
