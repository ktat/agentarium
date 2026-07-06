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

// TestActivate_StaleGuard は左ペイン activate が世代トークンで stale な非同期 render を破棄し、
// 未接続の staging へ描画してから panel へ挿す構造（タブ高速切替での内容積み重なり対策）を検証する。
func TestActivate_StaleGuard(t *testing.T) {
	js := readAsset(t, "app.js")
	for _, frag := range []string{
		"leftActivationSeq",                     // 世代トークン
		"const gen = ++leftActivationSeq",       // activate 開始で世代を採番
		"if (gen !== leftActivationSeq) return", // stale なら破棄
		"createElement('div')",                  // 未接続 staging へ描画
		"panel.replaceChildren(staging)",        // 最新のみ、staging(=root) ごと挿す
	} {
		if !strings.Contains(js, frag) {
			t.Errorf("app.js に %q が無い (activate の stale ガード欠落)", frag)
		}
	}
	// 旧実装の「panel へ直接クリア＆描画」は積み重なりの原因なので残っていないこと。
	if strings.Contains(js, "panel.innerHTML = ''") {
		t.Errorf("app.js に旧 activate の panel.innerHTML='' が残存（stale render が積み重なる）")
	}
	// staging を detach したまま childNodes だけ移す実装は、render 後に root へ再描画する
	// プラグイン（settings の ⚙→showForm 等）で画面が更新されない回帰を招くので残っていないこと。
	if strings.Contains(js, "staging.childNodes") {
		t.Errorf("app.js が childNodes だけ移している（root が detach され再描画が画面に出ない回帰）")
	}
}
