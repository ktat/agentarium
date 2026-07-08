# reload 前に開いていた Agent タブの復元 実装プラン

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ブラウザ reload 時に、直前に開いていた Agent ターミナルタブだけを順序・アクティブタブを保って復元する。

**Architecture:** フロントエンド `kernel/shell/assets/app.js` のみ変更。開いているタブの key 列とアクティブ key を `localStorage`（キー `agentarium.rightTabs`）へ保存し、`main()` で読み戻す。復元は生存セッション（`GET /terminal/list`）に対してだけ既存の冪等 `openAgentTab` で再 attach する。Go 側は変更なし。

**Tech Stack:** バニラ JS（ES module, `embed.FS` 実ファイル）、`localStorage`、既存の `/terminal/list`・`/terminal/start`（冪等）。SPA/ビルドツールチェーンは無し（spec D1）。

## Global Constraints

- 言語/対象: `kernel/shell/assets/app.js` のみ編集。Go 側変更なし。1 ファイル 700 行以内（現 646 行 → +約40 行）
- localStorage 書き込み/読み出しは必ず `try/catch` で握りつぶす（制限環境で throw しても動作を止めない。既存 `app.js` の localStorage 利用と同じ流儀）
- 空の `catch` は必ずコメント `/* 無視 */` を入れる（`deno lint` の `no-empty` 回避。既存パターン踏襲）
- stale タブ（reload 後 `/terminal/list` に無い保存 id）は静かに破棄する
- 復元経路は `openAgentTab` に `{key, label, silent:true}` のみ渡す（`command`/`resume`/`agent`/`model` を渡さない = 再 inject も再起動も起きない）
- 公開 API `window.agentarium.openAgentTab` の外向き挙動（`silent` 省略時）は不変
- JS 単体テストのランナーは無い。検証は `go run ./cmd/agentarium` + chrome-devtools MCP による実ブラウザ駆動で行う
- lint/test: `make lint`（`lint-go` + `lint-js`=`deno lint`）と `make test`（`go test -race ./...`）がグリーンであること

---

### Task 1: タブ状態の保存（persist）

開いている Agent タブの key 列とアクティブ key を、タブ集合・アクティブが変わるたびに localStorage へ書く。

**Files:**
- Modify: `kernel/shell/assets/app.js`
  - 新設: `persistRightTabs()`（`closeAgentTab` の直前あたり、`activateRightTab` の後に置く）
  - 編集: `activateRightTab`（末尾に保存呼び出し）
  - 編集: `closeAgentTab`（末尾に保存呼び出し）

**Interfaces:**
- Produces: `persistRightTabs(): void` — `rightTabs`（挿入順）から `{keys:[...], active}` を作り `localStorage['agentarium.rightTabs']` に JSON 保存
- Consumes: 既存 `const rightTabs = new Map()`（`app.js:7`）、`activateRightTab`（`app.js:280`）、`closeAgentTab`（`app.js:296`）

> 備考: `openAgentTab` は末尾で `activateRightTab(key)` を必ず呼ぶ（新規時 `app.js:237`、既存再活性時 `app.js:194`）。したがって `activateRightTab` に保存を仕込めば「タブ追加＋activate 後」も自動的に保存される。`openAgentTab` 自体への保存呼び出し追加は不要（DRY）。

- [ ] **Step 1: `persistRightTabs()` を追加**

`app.js` の `activateRightTab` 関数（`function activateRightTab(key) { ... }`、末尾は `app.js:294` の `}`）の直後に以下を挿入する:

```js
// persistRightTabs は現在開いている Agent タブの key 列とアクティブ key を
// localStorage に保存する。reload 時に restoreRightTabs がこれを読み、直前に
// 開いていたタブだけを復元する。書き込みは制限環境で throw しても動作を止めない。
function persistRightTabs() {
  const keys = Array.from(rightTabs.keys());
  let active = null;
  for (const [k, entry] of rightTabs) {
    if (entry.tabEl.classList.contains('active')) { active = k; break; }
  }
  try {
    localStorage.setItem('agentarium.rightTabs', JSON.stringify({ keys, active }));
  } catch (_) { /* 無視 */ }
}
```

- [ ] **Step 2: `activateRightTab` の末尾で保存する**

`activateRightTab` の最後の行（`if (initial) initial.style.display = 'none';`、`app.js:293`）の直後、関数を閉じる `}` の直前に 1 行追加する:

```js
  // ペイン底に置いた empty メッセージは隠す
  const initial = document.getElementById('right-panel');
  if (initial) initial.style.display = 'none';
  persistRightTabs();
}
```

- [ ] **Step 3: `closeAgentTab` の末尾で保存する**

`closeAgentTab` の `else { ... }` ブロック（`app.js:312-315`）の直後、関数を閉じる `}` の直前に 1 行追加する。閉じてタブが 0 になった場合も storage を最新化するため、無条件に末尾で呼ぶ:

```js
  // 他にタブが残っていれば 1 件アクティブに
  const first = rightTabs.keys().next();
  if (!first.done) activateRightTab(first.value);
  else {
    const initial = document.getElementById('right-panel');
    if (initial) initial.style.display = '';
  }
  persistRightTabs();
}
```

（`if (!first.done)` 側は `activateRightTab` 経由でも保存されるが、末尾の無条件呼び出しで二重になるだけで害は無い。else 側=タブ 0 件のケースを確実に保存するために末尾呼び出しが必要。）

- [ ] **Step 4: lint を通す**

Run: `make lint-js`
Expected: エラー無しで完了（`deno lint` がパス）

- [ ] **Step 5: ブラウザで保存を確認**

1. `AGENTARIUM_ADDR=127.0.0.1:8780 go run ./cmd/agentarium` をバックグラウンド起動
2. chrome-devtools MCP で `http://127.0.0.1:8780/` を開く
3. `evaluate_script` で 2 タブ開く:
   ```js
   await window.agentarium.openAgentTab({ key: 't1', label: 'One' });
   await window.agentarium.openAgentTab({ key: 't2', label: 'Two' });
   ```
4. `evaluate_script` で保存内容を確認:
   ```js
   return localStorage.getItem('agentarium.rightTabs');
   ```
   Expected: `{"keys":["t1","t2"],"active":"t2"}`（最後に開いた t2 が active）
5. `evaluate_script` で t1 に切替 → 再確認:
   ```js
   return (window.agentarium, document.querySelector('[data-tab-key="t1"]').click(), localStorage.getItem('agentarium.rightTabs'));
   ```
   Expected: `active` が `"t1"` に変わる
6. サーバプロセスを停止

- [ ] **Step 6: Commit**

```bash
git add kernel/shell/assets/app.js
git commit -m "feat(shell): 開いている Agent タブ状態を localStorage に保存

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: タブ状態の復元（restore）+ 復元経路の alert 抑制

reload 時に保存済み key 列を読み、生存セッションだけ再 attach し、保存アクティブタブを前面に戻す。復元中の start 失敗で alert を出さないよう `openAgentTab` に内部フラグ `silent` を足す。

**Files:**
- Modify: `kernel/shell/assets/app.js`
  - 編集: `openAgentTab`（`opts` 分割代入に `silent` 追加、2 箇所の `alert` を `silent` で分岐）
  - 新設: `restoreRightTabs()`（`persistRightTabs()` の近く、`main()` より前）
  - 編集: `main()`（`await focusFromHash();` の前に `await restoreRightTabs();`）

**Interfaces:**
- Consumes: `persistRightTabs()`（Task 1）、`openAgentTab`（`app.js:182`）、`activateRightTab`、`rightTabs`、`main()`（`app.js:35`）
- Produces:
  - `openAgentTab(opts)` の `opts` に任意 `silent: boolean`（既定 false）。true のとき start 失敗 / renderer 未結線で `alert` の代わりに `console.warn`
  - `restoreRightTabs(): Promise<void>` — localStorage の `{keys, active}` を読み、`/terminal/list` 生存分だけ順に `openAgentTab({key,label,silent:true})`、最後に保存 active を activate

- [ ] **Step 1: `openAgentTab` に `silent` を追加**

`openAgentTab` 先頭の分割代入（`app.js:183`）に `silent` を足す:

```js
  const { key, label, agent, model, resume, command, autoEnter, silent } = opts || {};
```

renderer 未結線の alert（`app.js:188`）を分岐する:

```js
  if (!rendererName) {
    rendererName = await loadRendererName();
    if (!rendererName) {
      if (silent) { console.warn('openAgentTab: terminal service not wired'); return; }
      alert('Terminal Service が結線されていません');
      return;
    }
  }
```

start 失敗の alert（`app.js:205-208`）を分岐する:

```js
  const startRes = await fetch('/terminal/start?' + startQS.toString(), { method: 'POST' });
  if (!startRes.ok && startRes.status !== 204) {
    if (silent) { console.warn('openAgentTab: start failed ' + startRes.status); return; }
    alert('start failed: ' + startRes.status);
    return;
  }
```

- [ ] **Step 2: `restoreRightTabs()` を追加**

Task 1 で追加した `persistRightTabs()` の直後に挿入する:

```js
// restoreRightTabs は reload 前に開いていた Agent タブを復元する。
// localStorage の key 列を順に走査し、/terminal/list に生存しているセッション
// だけを openAgentTab で再オープンして、保存済みのアクティブタブを前面へ戻す。
// list に無い（閉じた/失われた）id は静かに破棄する。復元中の start 失敗で
// ユーザーに alert を出さないよう silent:true で開く。
async function restoreRightTabs() {
  let saved;
  try {
    const raw = localStorage.getItem('agentarium.rightTabs');
    if (!raw) return;
    saved = JSON.parse(raw);
  } catch (_) { return; /* 壊れた値は無視 */ }
  if (!saved || !Array.isArray(saved.keys) || saved.keys.length === 0) return;

  let items;
  try {
    const res = await fetch('/terminal/list');
    if (!res.ok) return;
    const data = await res.json();
    items = (data && data.items) || [];
  } catch (_) { return; /* list 取得失敗時は復元しない */ }

  const byId = new Map();
  for (const it of items) {
    const id = it.ID || it.id;
    if (id) byId.set(id, it);
  }

  for (const key of saved.keys) {
    const it = byId.get(key);
    if (!it) continue; // list に無い = 破棄
    await openAgentTab({ key, label: it.Label || it.label || key, silent: true });
  }

  if (saved.active && rightTabs.has(saved.active)) {
    activateRightTab(saved.active);
  }
}
```

- [ ] **Step 3: `main()` で復元を呼ぶ**

`main()` 内の `await focusFromHash();`（`app.js:48`）の直前に 1 行追加する:

```js
  document.querySelectorAll('.tab-bar-wrap').forEach(initTabBarScroll);
  await restoreRightTabs();
  await focusFromHash();
  globalThis.addEventListener('hashchange', focusFromHash);
```

- [ ] **Step 4: lint を通す**

Run: `make lint-js`
Expected: エラー無しで完了

- [ ] **Step 5: ブラウザで復元を end-to-end 確認**

1. `AGENTARIUM_ADDR=127.0.0.1:8780 go run ./cmd/agentarium` をバックグラウンド起動
2. chrome-devtools MCP で `http://127.0.0.1:8780/` を開く
3. `evaluate_script` で 3 タブ開き、2 番目をアクティブにする:
   ```js
   await window.agentarium.openAgentTab({ key: 'a', label: 'Alpha' });
   await window.agentarium.openAgentTab({ key: 'b', label: 'Bravo' });
   await window.agentarium.openAgentTab({ key: 'c', label: 'Charlie' });
   document.querySelector('[data-tab-key="b"]').click();
   return localStorage.getItem('agentarium.rightTabs');
   ```
   Expected: `{"keys":["a","b","c"],"active":"b"}`
4. ページを reload（`navigate_page` で同 URL、または `evaluate_script` で `location.reload()`）
5. `evaluate_script` で復元結果を確認:
   ```js
   return {
     tabs: Array.from(document.querySelectorAll('[data-tab-key]')).map((e) => e.dataset.tabKey),
     active: (document.querySelector('.term-tab.active') || {}).dataset?.tabKey,
   };
   ```
   Expected: `tabs` が `["a","b","c"]`（同順）、`active` が `"b"`（reload 前のアクティブ）
6. **stale 破棄の確認**: 1 タブをサーバ側で終了させる。`evaluate_script` で `await fetch('/terminal/stop?id=c', {method:'POST'})` を実行 → reload → Step 5 の確認で `tabs` が `["a","b"]`（c が静かに消える）
7. **再 inject 無しの確認**: 復元されたタブの端末に、`command` 由来の余計な入力文字列が流れ込んでいないこと（`take_snapshot` で端末内容を目視）。復元は `{key,label,silent}` のみ渡すため command 経路に入らない
8. サーバプロセスを停止

- [ ] **Step 6: フルの lint/test を通す**

Run: `make lint && make test`
Expected: lint（go + deno）と test（`go test -race ./...`）がすべてパス。Go 側は無変更のため test はグリーン維持

- [ ] **Step 7: 行数確認**

Run: `wc -l kernel/shell/assets/app.js`
Expected: 700 行以内

- [ ] **Step 8: Commit**

```bash
git add kernel/shell/assets/app.js
git commit -m "feat(shell): reload 時に直前に開いていた Agent タブを復元

- localStorage の key 列を /terminal/list 生存分だけ再 attach
- 保存アクティブタブを前面に戻す。list に無い id は静かに破棄
- 復元経路の start 失敗で alert を出さない silent フラグを追加

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage:**
- spec §1 保存データ（`agentarium.rightTabs`, `{keys, active}`, ラベル非保存）→ Task 1 Step 1 ✓
- spec §2 保存タイミング（openAgentTab/closeAgentTab/activateRightTab）→ Task 1 Step 2-3（openAgentTab は activateRightTab 経由で DRY にカバー）✓
- spec §3 復元フロー（parse→list→生存分 open→active 復元→掃除不要）→ Task 2 Step 2 ✓
- spec §4 silent 追加 → Task 2 Step 1 ✓
- spec §5 focusFromHash 順序・pending・再 inject 無し → Task 2 Step 3（restore→focusFromHash 順）、Step 5-7 で検証 ✓
- spec §6 ファイルサイズ → Task 2 Step 7 ✓
- spec テスト方針（ブラウザ駆動 1〜5）→ Task 1 Step 5 / Task 2 Step 5（項目 3=手動close相当は Task1 Step5、項目 4=stale は Task2 Step6、項目 5=再inject無しは Task2 Step7）✓

**2. Placeholder scan:** TBD/TODO/「適切に」等なし。全コードブロックは実コード ✓

**3. Type consistency:** `persistRightTabs`（保存キー `agentarium.rightTabs`、形 `{keys, active}`）と `restoreRightTabs`（同キー・同形を読む）で一致。`openAgentTab` の `silent` は Task 2 Step 1 で定義し Task 2 Step 2 で使用、一致 ✓
