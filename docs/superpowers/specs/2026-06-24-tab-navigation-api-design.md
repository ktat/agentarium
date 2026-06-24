# 左タブのプログラム遷移 API（openTab + #tab deep-link）— 設計

## 背景・目的

カーネルシェルには現在、左ペインのプラグインタブを**プログラムから切り替える手段**も、
**タブ起動時にパラメータを渡す手段**もない（`globalThis.agentarium` は openAgentTab /
openViewer 等のみ、左タブは click で `render(root,{pluginId})` を都度呼ぶだけ、ハッシュは
`#term=` のみ）。消費側で「あるタブのUIから別タブを対象付きで開く」導線を作れるよう、
最小・汎用なタブ遷移 API を追加する。

## スコープ

やること（`kernel/shell/assets/app.js` のみ。Go 変更なし）:
- `globalThis.agentarium.openTab(pluginId, params)` を追加: 左タブをアクティブ化し、`params` を
  そのプラグインの `render` に渡す。
- 左タブのハッシュ deep-link `#tab=<pluginId>` を `focusFromHash` に追加（既存 `#term=` は不変）。
- `render(root, opts)` の `opts` に `params` を追加（`{pluginId, params}`）。click 起動時は
  `params` は `undefined`。**後方互換**（既存プラグインは params を無視するだけ）。
- タブ click でも `location.hash = '#tab=<id>'` を更新し、リロードでタブ復元。

やらないこと:
- 左タブのデフォルト自動アクティブ化（現状＝ハッシュ無しは空。挙動変更しない）。
- `params` のハッシュ/URL 永続化（メモリ保持のみ。リロードで params は失われる＝プラグインは
  params 不在を許容して描画する）。
- Go 側・プラグイン IF の Go 契約変更。openAgentTab/openViewer 等の既存挙動変更。

## 設計

`main()` でタブ生成時に lookup を保持:
```js
const leftTabs = new Map(); // id → {p, btn}
let currentLeftTab = null;
```

中核関数:
```js
// activateLeftTab は pluginId の左タブをアクティブ化し params を render に渡す。未知 id は false。
async function activateLeftTab(pluginId, params) {
  const t = leftTabs.get(pluginId);
  if (!t) return false;
  leftBar.querySelectorAll('.left-tab').forEach((b) => b.classList.remove('active'));
  t.btn.classList.add('active');
  currentLeftTab = pluginId;
  await activate(t.p, panel, params);
  return true;
}
```
`activate(p, panel, params)` は `render(panel, {pluginId: p.id, params})` を呼ぶ。

click ハンドラ:
```js
btn.addEventListener('click', () => {
  activateLeftTab(p.id);
  if (location.hash !== '#tab=' + p.id) location.hash = '#tab=' + p.id; // hashchange は同 id で skip
});
```

`focusFromHash` に追記（`#term=` 判定の前に）:
```js
const mt = /^#tab=(.+)$/.exec(location.hash || '');
if (mt) {
  let id;
  try { id = decodeURIComponent(mt[1]); } catch (_) { return; }
  if (id !== currentLeftTab) await activateLeftTab(id);
  return;
}
```

公開 API:
```js
globalThis.agentarium = {
  openAgentTab, closeAgentTab, openViewer, closeViewer,
  openTab,                      // 追加
  fetch: (path) => fetch(path),
};
// openTab は左タブをアクティブ化し params を渡す。
async function openTab(pluginId, params) {
  const ok = await activateLeftTab(pluginId, params);
  if (ok && location.hash !== '#tab=' + pluginId) location.hash = '#tab=' + pluginId;
  return ok;
}
```

二重起動の回避: activateLeftTab が `currentLeftTab` を更新済みなので、直後の hashchange は
`id === currentLeftTab` で skip。openTab で同一タブを params 違いで再呼び出しした場合は hash が
変わらず hashchange が発火しないため、activateLeftTab の再描画（params 反映）が一度だけ走る。

## エラーハンドリング
- 未知 pluginId / pane=right: `activateLeftTab` が false（openTab も false）。例外は投げない。
- 不正な % エスケープのハッシュ: 既存同様 return（main を止めない）。

## テスト
- JS テストランナーなし。検証は `go build ./...`（embed 取り込み）+ 手動:
  - タブ click で URL が `#tab=<id>` になり、リロードで同タブが復元。
  - 別プラグインから `globalThis.agentarium.openTab('<id>', {foo:1})` → 該当タブがアクティブ化し
    `render` の `opts.params` に `{foo:1}` が届く。
  - 既存プラグイン（hello/chat/sessions）が params 無しで従来どおり描画。
  - `#term=` の Agent タブ deep-link が従来どおり動く。

## リスク・トレードオフ
1. click で hash を更新するため URL 挙動が変わる（タブが履歴に乗る）。deep-link 一貫性のため許容。
2. params はメモリのみ。リロードで失われ、プラグインは params 不在時の描画を用意する必要がある。
3. `currentLeftTab` による skip で二重 render を防ぐが、外部から手で同 hash を再設定しても再描画
   されない（同 id では no-op）。プログラム遷移は openTab を使う前提。
