package wrap

import (
	"strings"
	"testing"
)

// TestSendResize_HiddenGuard は assets/index.js の sendResize が「非表示 (祖先が
// display:none) のときは resize を送らない」ガードを備えることを構造照合する。
//
// 回帰対象: ガードが無いと非アクティブタブ (display:none) で viewportEl も root も
// clientWidth=0 になり、cols=Math.max(20,0)=20 の極狭 resize を PTY に送ってしまう。
// TUI が 20 桁に reflow し、タブ復帰時に一瞬狭い表示が見えてから通常幅へ戻る。
// JS 実行エンジンが無いため、ガードの存在を文字列で固定してドリフトを検知する。
func TestSendResize_HiddenGuard(t *testing.T) {
	js, err := assetsFS.ReadFile("assets/index.js")
	if err != nil {
		t.Fatalf("read assets/index.js: %v", err)
	}
	src := string(js)

	// sendResize 本体に offsetParent による非表示ガードがあること。
	if !strings.Contains(src, "offsetParent === null") {
		t.Error("sendResize に offsetParent による非表示ガードが無い (非表示中に cols=20 の極狭 resize を送る恐れ)")
	}
	// レイアウト前の極小実寸を弾くガードがあること。
	if !strings.Contains(src, "if (vpW < 20 || vpH < 20) return;") {
		t.Error("実寸が取れないときに無効な resize を弾くガードが無い")
	}
}
