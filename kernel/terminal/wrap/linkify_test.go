package wrap

import (
	"regexp"
	"testing"
)

var (
	// twrapURLDefRe は TWRAP_URL_RE の定義行に ASCII URI 文字クラスが
	// アンカーされていることを照合する (旧バグ [^\s]+ への退行検知)。
	twrapURLDefRe = regexp.MustCompile(`TWRAP_URL_RE\s*=\s*/https\?:\\/\\/\[A-Za-z0-9`)
	// twrapURLMirrorRe は assets/index.js の TWRAP_URL_RE と同等の Go RE2 ミラー
	// (regex literal の \/ は Go では / で表現)。終端挙動の検証に使う。
	twrapURLMirrorRe = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`)
)

// 「URL の終端が全角文字 (全角括弧・日本語) で正しく切れること」の挙動回帰テスト。
// リポジトリに JS 実行エンジンが無いため、(1) 配信される assets/index.js の
// 正規表現リテラルを構造的に照合してドリフトを検知し、(2) それと同等の Go RE2 で
// 終端挙動を検証する。JS リテラルを書き換えると (1) が落ちるので、Go ミラーの
// 更新漏れに気付ける。
func TestTwrapURLRegex_TerminatesAtNonASCII(t *testing.T) {
	js, err := assetsFS.ReadFile("assets/index.js")
	if err != nil {
		t.Fatalf("read assets/index.js: %v", err)
	}

	// (1) index.js の TWRAP_URL_RE が旧バグ ([^\s]+ = 空白以外なら何でも → 全角文字を
	// 取り込む) に戻っていないこと、かつ ASCII URI 文字クラスを使っていることを構造的に
	// 確認する。文字クラス断片を単独で探すと定義と無関係な箇所 (コメント等) にも
	// 反応しうるため、`TWRAP_URL_RE = /https?:\/\/[A-Za-z0-9` を一体で照合する。
	if !twrapURLDefRe.Match(js) {
		t.Error("TWRAP_URL_RE の定義が ASCII URI 文字クラスで始まっていない (全角文字を取り込む恐れ)")
	}

	// (2) 修正後の文字クラスと同等の Go RE2 で終端挙動を検証する。意味的な挙動は
	// こちらで担保する。
	re := twrapURLMirrorRe
	cases := []struct {
		name, in, want string
	}{
		{"全角括弧で終端", "MR: https://example.com/-/merge_requests/116（ブランチ x）", "https://example.com/-/merge_requests/116"},
		{"全角囲みの開き括弧の内側から開始", "（https://example.com/a）です", "https://example.com/a"},
		{"日本語直前で終端", "詳細は https://example.com/path です", "https://example.com/path"},
		{"半角スペースで終端", "https://x.test/p other", "https://x.test/p"},
		{"クエリ/フラグメントは含む", "https://x.test/s?a=1&b=2#frag のあと", "https://x.test/s?a=1&b=2#frag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := re.FindString(c.in); got != c.want {
				t.Errorf("FindString(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
