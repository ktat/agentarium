// Package viewer はカーネルが提供するコンテンツ描画ユーティリティ。
// 右ペインビューア（shell）から POST /viewer/render で Markdown を安全な HTML に変換する。
package viewer

import (
	"io"
	"net/http"
	"regexp"

	"github.com/gomarkdown/markdown"
	mdhtml "github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"github.com/microcosm-cc/bluemonday"
)

// renderMaxBytes は /viewer/render が受け付ける body の上限。
const renderMaxBytes = 1 << 20 // 1 MiB

// policy はユーザー生成コンテンツ向けサニタイズポリシー（script/on*/javascript: を除去）。
// 絶対 URL リンクには target="_blank" + rel="nofollow noopener" を付与する
// （ビューア内クリックでシェル全体が遷移するのを防ぐ）。
var policy = newPolicy()

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AddTargetBlankToFullyQualifiedLinks(true)
	// Notion 由来のミュート表現（斜体+灰色）を Topics で再現するため、
	// em 要素の class="notion-muted" だけを通す（他の class は従来どおり除去）。
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^notion-muted$`)).OnElements("em")
	return p
}

// RenderMarkdown は markdown を HTML 化し bluemonday でサニタイズして返す。
// HTTP 非依存の純粋関数（テスト容易性のため分離）。
func RenderMarkdown(src []byte) []byte {
	p := parser.NewWithExtensions(parser.CommonExtensions | parser.AutoHeadingIDs)
	doc := p.Parse(src)
	renderer := mdhtml.NewRenderer(mdhtml.RendererOptions{Flags: mdhtml.CommonFlags})
	unsafe := markdown.Render(doc, renderer)
	return policy.SanitizeBytes(unsafe)
}

// Handler は POST /viewer/render のハンドラ。body(markdown) を描画して text/html で返す。
// cross-origin 拒否は呼び出し側（server.New の csrfGuard）が担う。
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, renderMaxBytes)
		src, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(RenderMarkdown(src))
	}
}
