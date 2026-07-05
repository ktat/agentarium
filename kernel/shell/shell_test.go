package shell

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFS_HasIndexAndApp(t *testing.T) {
	f := FS()
	for _, name := range []string{"index.html", "app.js", "app.css"} {
		b, err := fs.ReadFile(f, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

func readAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(FS(), name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// TestTabOverflow_WrapAndScrollButtons は viewer / term の両タブバーが
// .tab-bar-wrap で包まれ、両端に ‹ › スクロールボタンを備えることを構造照合する。
// 溢れたタブへ辿れなくなる退行 (wrap や button の欠落) を検知する。
func TestTabOverflow_WrapAndScrollButtons(t *testing.T) {
	html := readAsset(t, "index.html")
	for _, frag := range []string{
		`<div class="tab-bar-wrap">`,      // viewer 用ラップ
		`<div class="tab-bar-wrap term">`, // term 用ラップ (ダーク配色分岐)
		`class="tab-scroll-btn left"`,
		`class="tab-scroll-btn right"`,
		`class="viewer-tab-bar" id="viewer-tab-bar"`,
		`class="term-tab-bar" id="right-tab-bar"`,
	} {
		if !strings.Contains(html, frag) {
			t.Errorf("index.html に %q が無い (タブ横スクロール構造の欠落)", frag)
		}
	}
	// ボタンは左右 2 バー分で計 4 個。
	if got := strings.Count(html, "tab-scroll-btn"); got < 4 {
		t.Errorf("tab-scroll-btn が %d 個 (>=4 を期待)", got)
	}

	css := readAsset(t, "app.css")
	for _, frag := range []string{
		".tab-bar-wrap",
		".tab-scroll-btn",
		`.tab-bar-wrap[data-overflow="both"]`, // 両端表示の出し分け
		".tab-label",                          // #3 見出し省略
		"text-overflow: ellipsis",
	} {
		if !strings.Contains(css, frag) {
			t.Errorf("app.css に %q が無い", frag)
		}
	}

	js := readAsset(t, "app.js")
	for _, frag := range []string{
		"function initTabBarScroll",
		"dataset.overflow", // overflow 状態の追従ロジック
		"initTabBarScroll)",
		"scrollTabIntoView",
		"tabEl.title =",                     // #3 hover 全文
		"labelSpan.className = 'tab-label'", // #3 省略対象ラベル
	} {
		if !strings.Contains(js, frag) {
			t.Errorf("app.js に %q が無い", frag)
		}
	}
}
