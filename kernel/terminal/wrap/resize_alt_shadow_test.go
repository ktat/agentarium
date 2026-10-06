package wrap

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// このファイルは「alt 往復で main grid から切り詰められ、mainShadow だけが
// 内容を持つ行」(shadow 専用行) が、その後の寸法変更 resize の全行照合で
// 消されないことの回帰テスト。
//
// alt 突入時の emu.Resize は main grid を altRows 行に切り詰め、復帰時の
// emu.Resize は空行で埋め戻すだけなので、altRows 行目以降の古い履歴は
// mainShadow / lastSent にしか残らない。resize 直後の全行照合 (grid を正と
// する) がそれを「grid で消えた行」と見なすと、履歴が client からも
// mainShadow からも消え、リロードでも戻らない。

// newAltShadowProcess は y=199 に履歴行を書いて sweep し、alt 往復
// (DefaultAltRows=40 に切り詰め) を済ませた Process を返す。
func newAltShadowProcess(t *testing.T) *Process {
	t.Helper()
	withVirtualRows(t, 500)
	p := NewProcess("", "true")
	p.mu.Lock()
	p.emu = vt.NewEmulator(40, VirtualRows())
	p.altScreen = false
	p.mu.Unlock()
	p.emu.SetCallbacks(vt.Callbacks{AltScreen: func(on bool) { p.onAltScreenChange(on) }})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeLocked("\x1b[200;1Hhistory row\x1b[1;1H")
	p.sweepLocked()
	p.writeLocked("\x1b[?1049h")
	p.writeLocked("\x1b[?1049l")
	p.sweepLocked()
	if !strings.Contains(runsText(p.mainShadow[199]), "history row") {
		t.Fatal("前提: alt 復帰後の mainShadow[199] に履歴行が無い")
	}
	if got := runsKey(p.snapshotLine(199)); got != "" {
		t.Fatalf("前提: alt 往復後の grid row199 が空でない (切り詰められているはず): %q", got)
	}
	return p
}

// alt 往復の後に寸法が実際に変わる resize が来ても、shadow 専用行の履歴が
// 消えないこと (空行の update を流さず、mainShadow / lastSent / init snapshot
// に残ること)。
//
// 【守っている対策】shadow 専用行の追跡 (resweepAllRowsLocked(false) が
// shadow 専用で grid も空の行を grid 由来の消去と見なさないこと)。
func TestClientResize_afterAltRoundTrip_keepsShadowOnlyHistory(t *testing.T) {
	p := newAltShadowProcess(t)

	sub, cancel := p.Subscribe()
	defer cancel()
	if err := p.Resize(60, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
drain:
	for {
		select {
		case msg, ok := <-sub:
			if !ok {
				break drain
			}
			for _, lu := range msg.Lines {
				if lu.Y == 199 && len(lu.Runs) == 0 {
					t.Error("resize 後の update に y=199 の履歴を消す空行が流れた")
				}
			}
		default:
			break drain
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.Contains(runsText(p.mainShadow[199]), "history row") {
		t.Errorf("resize 後に mainShadow[199] の履歴が消えた: %q", runsText(p.mainShadow[199]))
	}
	if p.lastSent[199] == "" {
		t.Error("resize 後に lastSent[199] が空になった")
	}
	found := false
	for _, lu := range p.buildSnapshot("init").Lines {
		if lu.Y == 199 && strings.Contains(runsText(lu.Runs), "history row") {
			found = true
		}
	}
	if !found {
		t.Error("resize 後の init snapshot に y=199 の履歴が載っていない")
	}
}

// shadow 専用行でも、子がその行を書き換えたら grid が正に戻ること。子が消した
// (Touched 付きで空にした) 行は、以後の resize で復活しないこと。
//
// 【守っている対策】sweepLocked が Touched 行を shadow 専用の追跡から外すこと。
func TestShadowOnlyRow_childEraseIsHonored(t *testing.T) {
	p := newAltShadowProcess(t)

	p.mu.Lock()
	p.writeLocked("\x1b[200;1H\x1b[2K\x1b[1;1H") // 子が y=199 を消す
	p.sweepLocked()
	if got := runsText(p.mainShadow[199]); got != "" {
		t.Errorf("子が消した shadow 専用行が mainShadow に残った: %q", got)
	}
	p.mu.Unlock()

	_ = p.Resize(60, 40)

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := runsText(p.mainShadow[199]); got != "" {
		t.Errorf("resize 後に子が消した行が復活した: %q", got)
	}
}

// shadow 専用行に子が新しい内容を書いたら、その内容が正になり、resize 後も
// grid の内容が送られること (shadow の古い内容で上書きしないこと)。
func TestShadowOnlyRow_childRewriteWins(t *testing.T) {
	p := newAltShadowProcess(t)

	p.mu.Lock()
	p.writeLocked("\x1b[200;1Hnew content\x1b[1;1H")
	p.sweepLocked()
	p.mu.Unlock()

	_ = p.Resize(60, 40)

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := runsText(p.mainShadow[199]); !strings.Contains(got, "new content") {
		t.Errorf("子が書き直した行が grid の内容になっていない: %q", got)
	}
}
