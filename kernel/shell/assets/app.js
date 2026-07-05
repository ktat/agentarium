// カーネルシェル。3 つの責務:
//  1) /api/plugins からタブを構築（左ペイン）
//  2) plugin タブ選択で /plugins/<id>/assets/index.js を動的 import
//  3) agentarium.openAgentTab(...) ホスト API で右ペインに Agent タブを開く
//     (renderer は /terminal/renderer で取得、JS は /terminal/assets/<renderer>/index.js)

const rightTabs = new Map(); // key → {tabEl, panelEl, instance}
const viewerTabs = new Map(); // key → {tabEl, panelEl}
const leftTabs = new Map(); // pluginId → {p, btn}
let currentLeftTab = null;
let leftBar = null; // 左タブバー（main で代入。activateLeftTab が参照）
let leftPanel = null; // 左ペインのプラグイン描画先（同上）
let rendererName = null;

// command 注入のタイミング定数。claude などの TUI は
//  (a) 起動直後は入力を受け付けられない（WS 接続 = renderer ready の時点ではまだ未起動）
//  (b) 'text\r' を一括で受けると paste 検知で \r を改行として扱い、submit されない
// ため、ready 後に少し待ってから本文を送り、Enter(\r) は別フレームで送る。
// 値は backlog-worker で実証済み（起動待ち 1500ms / Enter ギャップは余裕を見て 80ms）。
const INJECT_START_DELAY_MS = 1500;
const INJECT_ENTER_GAP_MS = 80;

async function loadRendererName() {
  try {
    const res = await fetch('/terminal/renderer');
    if (!res.ok) return null;
    const j = await res.json();
    return j.renderer;
  } catch (_) {
    return null;
  }
}

async function main() {
  rendererName = await loadRendererName();
  const res = await fetch('/api/plugins');
  const plugins = await res.json();
  leftBar = document.getElementById('left-tab-bar');
  leftPanel = document.getElementById('panel');
  for (const p of plugins) {
    if (p.pane === 'right') continue; // 右ペインプラグインは agent タブと衝突するため左に統一
    const btn = makeLeftTabButton(p);
    leftTabs.set(p.id, { p, btn });
    if (!p.hidden) leftBar.appendChild(btn); // hidden はオンデマンド表示まで bar に出さない
  }
  document.querySelectorAll('.tab-bar-wrap').forEach(initTabBarScroll);
  await focusFromHash();
  globalThis.addEventListener('hashchange', focusFromHash);
}

// makeLeftTabButton は左タブボタンを生成する。hidden プラグインには × クローズを付ける。
function makeLeftTabButton(p) {
  const btn = document.createElement('button');
  btn.className = 'left-tab';
  btn.textContent = p.title;
  btn.dataset.pluginId = p.id;
  btn.addEventListener('click', () => {
    activateLeftTab(p.id);
    if (location.hash !== '#tab=' + p.id) location.hash = '#tab=' + p.id;
  });
  if (p.hidden) {
    const x = document.createElement('span');
    x.textContent = ' ×';
    x.style.cursor = 'pointer';
    x.style.marginLeft = '6px';
    x.title = '閉じる';
    x.addEventListener('click', (e) => {
      e.stopPropagation(); // タブ切替と分離
      closeLeftTab(p.id);
    });
    btn.appendChild(x);
  }
  return btn;
}

// closeLeftTab は hidden タブを bar から取り除く（leftTabs エントリは残し、再度開けるようにする）。
function closeLeftTab(pluginId) {
  const t = leftTabs.get(pluginId);
  if (!t) return;
  t.btn.remove();
  if (currentLeftTab === pluginId) {
    currentLeftTab = null;
    if (location.hash === '#tab=' + pluginId) location.hash = '';
    const first = leftBar.querySelector('.left-tab');
    if (first) {
      activateLeftTab(first.dataset.pluginId);
    } else {
      leftPanel.replaceChildren(); // 可視タブが無ければ左ペインを空に
    }
  }
}

// activateLeftTab は pluginId の左タブをアクティブ化し params を render に渡す。未知 id は false。
async function activateLeftTab(pluginId, params) {
  const t = leftTabs.get(pluginId);
  if (!t) return false;
  if (!t.btn.isConnected) leftBar.appendChild(t.btn); // hidden タブのオンデマンド表示
  leftBar.querySelectorAll('.left-tab').forEach((b) => b.classList.remove('active'));
  t.btn.classList.add('active');
  currentLeftTab = pluginId;
  await activate(t.p, leftPanel, params);
  return true;
}

// focusFromHash は location.hash が "#term=<id>" のとき該当 Agent タブを開く/アクティブ化する。
// Pet の popover クリックが xdg-open する deep-link を処理する。
async function focusFromHash() {
  // #tab=<pluginId>: 左タブの deep-link（params は持たない＝プログラム遷移は openTab を使う）
  const mt = /^#tab=(.+)$/.exec(location.hash || '');
  if (mt) {
    let tid;
    try {
      tid = decodeURIComponent(mt[1]);
    } catch (_) {
      return;
    }
    if (tid !== currentLeftTab) await activateLeftTab(tid);
    return;
  }
  const m = /^#term=(.+)$/.exec(location.hash || '');
  if (!m) return;
  let id;
  try {
    id = decodeURIComponent(m[1]);
  } catch (_) {
    return; // 不正な % エスケープ等は無視（main() を中断させない）
  }
  let label = id;
  try {
    const res = await fetch('/terminal/list');
    if (res.ok) {
      const data = await res.json();
      const items = (data && data.items) || [];
      const hit = items.find((it) => it.ID === id || it.id === id);
      if (hit) label = hit.Label || hit.label || id;
    }
  } catch (_) { /* 無視 */ }
  if (globalThis.agentarium && typeof globalThis.agentarium.openAgentTab === 'function') {
    globalThis.agentarium.openAgentTab({ key: id, label: label });
  }
}

async function activate(p, panel, params) {
  panel.innerHTML = '';
  try {
    const mod = await import('/plugins/' + p.id + '/assets/index.js');
    if (typeof mod.render === 'function') {
      await mod.render(panel, { pluginId: p.id, params });
    } else {
      panel.textContent = 'plugin ' + p.id + ' has no render()';
    }
  } catch (e) {
    panel.textContent = 'failed to load plugin ' + p.id + ': ' + e;
  }
}

// ===== agentarium ホスト API =====

// openAgentTab は右ペインに Agent タブを開く。
//   key:    タブ識別子 (同じ key を再度渡せば既存タブをアクティブ化)
//   label:  タブ見出し
//   agent:  agent 名（省略時はサーバ既定）
//   model:  RunRequest.Model
//   resume: 再開するセッション識別子（RunRequest.Resume。agent 実装が --resume 等へ変換）
//   command: タブを開いた直後に inject するテキスト（省略時は何もしない）
//   autoEnter: true なら command 末尾に \r を付与
async function openAgentTab(opts) {
  const { key, label, agent, model, resume, command, autoEnter } = opts || {};
  if (!key) { console.warn('openAgentTab: key is required'); return; }
  if (!rendererName) {
    rendererName = await loadRendererName();
    if (!rendererName) {
      alert('Terminal Service が結線されていません');
      return;
    }
  }
  // 既存タブなら activate のみ
  if (rightTabs.has(key)) {
    activateRightTab(key);
    return;
  }
  // 1) /terminal/start でサーバ側にプロセスを起こす
  const startQS = new URLSearchParams();
  startQS.set('id', key);
  if (label) startQS.set('label', label);
  if (agent) startQS.set('agent', agent);
  if (model) startQS.set('model', model);
  if (resume) startQS.set('resume', resume);
  const startRes = await fetch('/terminal/start?' + startQS.toString(), { method: 'POST' });
  if (!startRes.ok && startRes.status !== 204) {
    alert('start failed: ' + startRes.status);
    return;
  }
  // 2) 右ペインにタブとパネルを作成
  const tabBar = document.getElementById('right-tab-bar');
  const tabEl = document.createElement('button');
  tabEl.className = 'term-tab';
  tabEl.dataset.tabKey = key;
  tabEl.title = label || key; // hover で全文（ラベルは CSS で幅省略される）
  const labelSpan = document.createElement('span');
  labelSpan.className = 'tab-label';
  labelSpan.textContent = label || key;
  const closeBtn = document.createElement('span');
  closeBtn.className = 'close';
  closeBtn.textContent = '✕';
  closeBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    closeAgentTab(key);
  });
  tabEl.appendChild(labelSpan);
  tabEl.appendChild(closeBtn);
  tabEl.addEventListener('click', () => activateRightTab(key));
  tabBar.appendChild(tabEl);

  const panelEl = document.createElement('div');
  panelEl.className = 'panel';
  panelEl.style.display = 'none';
  const rightBottom = document.getElementById('rightBottom');
  rightBottom.appendChild(panelEl);

  rightTabs.set(key, { tabEl, panelEl, instance: null });
  activateRightTab(key);

  // 3) renderer JS を動的 import して render(panel, {id:key})
  try {
    const mod = await import('/terminal/assets/' + rendererName + '/index.js');
    if (typeof mod.render !== 'function') {
      panelEl.textContent = 'renderer ' + rendererName + ' has no render()';
      return;
    }
    const instance = await mod.render(panelEl, { id: key });
    const entry = rightTabs.get(key);
    if (entry) entry.instance = instance;
  } catch (e) {
    panelEl.textContent = 'failed to load renderer ' + rendererName + ': ' + e;
  }

  // 4) command が指定されていれば inject。
  //    TUI 起動を待ってから本文を送り、autoEnter の Enter(\r) は別 inject フレームで送る
  //    （一括 'text\r' だと claude の paste 検知で \r が改行扱いされ submit されないため）。
  if (command) {
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    const injectOnce = (text) =>
      fetch('/terminal/inject', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ terminal_id: key, text: text, enter: false }),
      }).catch(() => {});
    const entry = rightTabs.get(key);
    const inst = entry && entry.instance;
    const waitReady = (inst && inst.ready && typeof inst.ready.then === 'function')
      ? inst.ready
      : Promise.resolve();
    waitReady.catch(() => {}).then(async () => {
      await sleep(INJECT_START_DELAY_MS); // TUI 起動待ち
      await injectOnce(command);          // 本文（Enter は付けない）
      if (autoEnter) {
        await sleep(INJECT_ENTER_GAP_MS); // paste 検知回避のため間隔を空けて別フレーム
        await injectOnce('\r');           // Enter
      }
    });
  }
}

function activateRightTab(key) {
  for (const [k, entry] of rightTabs) {
    if (k === key) {
      entry.tabEl.classList.add('active');
      entry.panelEl.style.display = '';
      scrollTabIntoView(entry.tabEl); // 溢れて隠れたタブを選んだとき可視範囲へ
    } else {
      entry.tabEl.classList.remove('active');
      entry.panelEl.style.display = 'none';
    }
  }
  // ペイン底に置いた empty メッセージは隠す
  const initial = document.getElementById('right-panel');
  if (initial) initial.style.display = 'none';
}

async function closeAgentTab(key) {
  const entry = rightTabs.get(key);
  if (!entry) return;
  // サーバ側を Stop
  const qs = new URLSearchParams({ id: key });
  try { await fetch('/terminal/stop?' + qs.toString(), { method: 'POST' }); } catch (_) { /* 無視 */ }
  // renderer instance のクリーンアップ
  if (entry.instance && typeof entry.instance.close === 'function') {
    try { entry.instance.close(); } catch (_) { /* 無視 */ }
  }
  entry.tabEl.remove();
  entry.panelEl.remove();
  rightTabs.delete(key);
  // 他にタブが残っていれば 1 件アクティブに
  const first = rightTabs.keys().next();
  if (!first.done) activateRightTab(first.value);
  else {
    const initial = document.getElementById('right-panel');
    if (initial) initial.style.display = '';
  }
}

// ===== agentarium ビューア API（右ペイン上部・タブ式） =====

// openViewer は右ペイン上部にコンテンツビューアタブを開く。
//   key:     タブ識別子（同 key 再呼び出しでアクティブ化）
//   title:   タブ見出し
//   type:    'markdown'（既定・サーバ描画+サニタイズ）/ 'text'（pre 表示・安全）/ 'custom'（空パネルを返す）
//   content: インラインのソース文字列
//   url:     同一オリジンのパス（content 未指定時に fetch して取得）
// 戻り値: 当該タブのパネル要素（.viewer-content）。custom はこれに呼び出し側が自由に描画する。
async function openViewer(opts) {
  const { key, title, type = 'markdown', content, url } = opts || {};
  if (!key) { console.warn('openViewer: key is required'); return; }
  const pane = document.querySelector('.right-pane');
  pane.classList.remove('no-viewer'); // 上下分割を表示
  if (viewerTabs.has(key)) { activateViewerTab(key); return viewerTabs.get(key).panelEl; }

  const tabBar = document.getElementById('viewer-tab-bar');
  const tabEl = document.createElement('button');
  tabEl.className = 'viewer-tab';
  tabEl.dataset.viewerKey = key;
  tabEl.title = title || key; // hover で全文（ラベルは CSS で幅省略される）
  const labelSpan = document.createElement('span');
  labelSpan.className = 'tab-label';
  labelSpan.textContent = title || key;
  const closeBtn = document.createElement('span');
  closeBtn.className = 'close';
  closeBtn.textContent = '✕';
  closeBtn.addEventListener('click', (e) => { e.stopPropagation(); closeViewer(key); });
  tabEl.appendChild(labelSpan);
  tabEl.appendChild(closeBtn);
  tabEl.addEventListener('click', () => activateViewerTab(key));
  tabBar.appendChild(tabEl);

  const panelEl = document.createElement('div');
  panelEl.className = 'viewer-content';
  panelEl.style.display = 'none';
  document.getElementById('viewer-panel').appendChild(panelEl);
  viewerTabs.set(key, { tabEl, panelEl });
  activateViewerTab(key);

  // custom: パネルだけ用意して呼び出し側に渡す（プラグインが自由に描画する）
  if (type === 'custom') {
    return panelEl;
  }

  // ソース取得
  let src = content;
  if ((src === undefined || src === null) && url) {
    try {
      const r = await fetch(url);
      src = r.ok ? await r.text() : ('読み込み失敗: HTTP ' + r.status);
    } catch (e) {
      src = '読み込み失敗: ' + e;
    }
  }
  src = src || '';

  if (type === 'text') {
    const pre = document.createElement('pre');
    pre.textContent = src; // 安全
    panelEl.textContent = '';
    panelEl.appendChild(pre);
    return panelEl;
  }
  // markdown: サーバで描画+サニタイズして HTML を受け取る
  try {
    const r = await fetch('/viewer/render', {
      method: 'POST',
      headers: { 'Content-Type': 'text/markdown' },
      body: src,
    });
    if (r.ok) {
      panelEl.innerHTML = await r.text(); // サーバ側 bluemonday でサニタイズ済み
    } else {
      panelEl.textContent = '描画失敗: HTTP ' + r.status;
    }
  } catch (e) {
    panelEl.textContent = '描画失敗: ' + e;
  }
  return panelEl;
}

function activateViewerTab(key) {
  for (const [k, entry] of viewerTabs) {
    const on = k === key;
    entry.tabEl.classList.toggle('active', on);
    entry.panelEl.style.display = on ? '' : 'none';
    if (on) scrollTabIntoView(entry.tabEl); // 溢れて隠れたタブを選んだとき可視範囲へ
  }
}

function closeViewer(key) {
  const entry = viewerTabs.get(key);
  if (!entry) return;
  entry.tabEl.remove();
  entry.panelEl.remove();
  viewerTabs.delete(key);
  const first = viewerTabs.keys().next();
  if (!first.done) {
    activateViewerTab(first.value);
  } else {
    document.querySelector('.right-pane').classList.add('no-viewer'); // ターミナル全高へ
  }
}

// ===== レイアウトリサイザ（左右幅） =====
// 左右ペインの幅を mouse drag で調整する。
// leftMinPx=160, rightMinPx=480。セッション中のみ有効（localStorage 非使用）。
(function () {
  const layout = document.querySelector('.layout');
  const left = document.querySelector('.left-pane');
  const resizer = document.getElementById('layoutResizer');
  if (!layout || !left || !resizer) return;
  let dragging = false;
  resizer.addEventListener('mousedown', (e) => {
    dragging = true;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    document.body.classList.add('layout-dragging'); // iframe の pointer-events を切る
    e.preventDefault();
  });
  const leftMinPx = 160;
  // 右ペインの xterm が 60 cols (≈ 480px) を割らないようにする
  const rightMinPx = 480;
  globalThis.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const rect = layout.getBoundingClientRect();
    const leftWidth = e.clientX - rect.left;
    if (leftWidth < leftMinPx || leftWidth > rect.width - rightMinPx) return;
    left.style.flex = '0 0 ' + leftWidth + 'px';
  });
  globalThis.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    document.body.classList.remove('layout-dragging');
  });
})();

// ===== 右ペイン上下リサイザ（ビューア / ターミナル） =====
(function () {
  const pane = document.querySelector('.right-pane');
  const top = document.getElementById('rightTop');
  const resizer = document.getElementById('rightResizer');
  if (!pane || !top || !resizer) return;
  let dragging = false;
  resizer.addEventListener('mousedown', (e) => {
    dragging = true;
    document.body.style.cursor = 'row-resize';
    document.body.style.userSelect = 'none';
    document.body.classList.add('layout-dragging');
    e.preventDefault();
  });
  globalThis.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const rect = pane.getBoundingClientRect();
    const topH = e.clientY - rect.top;
    if (topH < 80 || topH > rect.height - 80) return;
    top.style.flex = '0 0 ' + topH + 'px';
  });
  globalThis.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    document.body.classList.remove('layout-dragging');
  });
})();

// ===== ペイン折りたたみトグル（左ペイン / 右下ターミナル） =====
// リサイザ上のボタンで開閉。状態は localStorage に永続化する。
// たたんでもリサイザの細い帯は残り、そのボタンで再展開できる。
(function () {
  const layout = document.querySelector('.layout');
  const rightPane = document.querySelector('.right-pane');
  const leftBtn = document.getElementById('leftCollapseBtn');
  const bottomBtn = document.getElementById('bottomCollapseBtn');

  // toggleEl: クラスを付け外す要素 / cls: 折りたたみクラス / btn: ラベル更新するボタン
  //  labels: [展開時に表示するラベル(=たたむ), たたみ時に表示するラベル(=ひらく)]
  //  name: アクセシブルな対象名（例: "左ペイン" / "ターミナル"）
  function setup(toggleEl, cls, btn, key, labels, name) {
    if (!toggleEl || !btn) return;
    const apply = (collapsed) => {
      toggleEl.classList.toggle(cls, collapsed);
      btn.textContent = collapsed ? labels[1] : labels[0];
      // 矢印グリフは accessible name にならないため title/aria-label を明示する
      const desc = name + 'を' + (collapsed ? 'ひらく' : 'たたむ');
      btn.title = desc;
      btn.setAttribute('aria-label', desc);
    };
    // localStorage から初期状態を復元（制限環境で getItem が throw しても初期化を止めない）
    let collapsed = false;
    try { collapsed = localStorage.getItem(key) === '1'; } catch (_) { /* 無視 */ }
    apply(collapsed);
    // ボタンの mousedown はリサイザのドラッグ開始を発火させない
    btn.addEventListener('mousedown', (e) => { e.stopPropagation(); });
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const collapsed = !toggleEl.classList.contains(cls);
      apply(collapsed);
      try { localStorage.setItem(key, collapsed ? '1' : '0'); } catch (_) { /* 無視 */ }
      // 表示領域の変化は xterm renderer の ResizeObserver(termDiv) が拾い、
      // 再展開時に自動で fit するため明示的な再 fit 通知は不要。
    });
  }

  setup(layout, 'left-collapsed', leftBtn, 'agentarium.layout.leftCollapsed', ['◀', '▶'], '左ペイン');
  setup(rightPane, 'bottom-collapsed', bottomBtn, 'agentarium.layout.bottomCollapsed', ['▼', '▲'], 'ターミナル');
})();

// ===== ターミナルタブ状態ポーリング =====
// /terminal/list を 2500ms 毎に取得し、term-tab の state クラスを更新する。
// rightTabs が空のときはフェッチしない。
// SessionState の JSON 値（terminal.go）:
//   "running"      → state-running
//   "awaiting_user" → state-awaiting
//   "idle" / "pending" → クラスなし
setInterval(async () => {
  if (rightTabs.size === 0) return;
  try {
    const res = await fetch('/terminal/list');
    if (!res.ok) return;
    const data = await res.json();
    const items = data.items;
    if (!Array.isArray(items)) return;
    for (const item of items) {
      const entry = rightTabs.get(item.ID);
      if (!entry) continue;
      const el = entry.tabEl;
      el.classList.remove('state-running', 'state-waiting', 'state-awaiting');
      const state = item.State; // JSON 文字列 e.g. "running"
      if (state === 'running') {
        el.classList.add('state-running');
      } else if (state === 'awaiting_user') {
        el.classList.add('state-awaiting');
      }
      // "idle" / "pending" はクラスなし
    }
  } catch (_) { /* 無視 */ }
}, 2500);

// openTab は左ペインのプラグインタブをアクティブ化し、params をそのプラグインの
// render(root, {pluginId, params}) に渡す。未知 pluginId / 右ペインプラグインは false。
// params はメモリ保持のみ（URL に乗らない＝リロードで失われるため、プラグインは params 不在を許容する）。
async function openTab(pluginId, params) {
  const ok = await activateLeftTab(pluginId, params);
  if (ok && location.hash !== '#tab=' + pluginId) location.hash = '#tab=' + pluginId;
  return ok;
}

// subscribe は topic の汎用イベント（/events）を購読し、各 data(JSON) を onMessage に渡す。
// 戻り値の EventSource は呼び出し側が .close() できる。
function subscribe(topic, onMessage) {
  const es = new EventSource('/events?topic=' + encodeURIComponent(topic));
  es.onmessage = (e) => {
    let data = null;
    try { data = JSON.parse(e.data); } catch (_) { data = e.data; }
    try { onMessage(data); } catch (_) { /* 購読側のエラーは握りつぶす */ }
  };
  return es;
}

// ===== タブバー横スクロール（溢れたタブへ ‹ › ボタン / ホイールで辿る） =====

// initTabBarScroll は .tab-bar-wrap 1 つに対し、両端ボタン・ホイール横スクロール・
// overflow 状態の追従を結線する。バーの内容は動的に増減するため MutationObserver で
// タブ追加/削除を、ResizeObserver でペインリサイズを拾って data-overflow を更新する。
function initTabBarScroll(wrap) {
  const scroller = wrap.querySelector('.viewer-tab-bar, .term-tab-bar');
  const btnLeft = wrap.querySelector('.tab-scroll-btn.left');
  const btnRight = wrap.querySelector('.tab-scroll-btn.right');
  if (!scroller) return;

  const updateOverflow = () => {
    const max = scroller.scrollWidth - scroller.clientWidth;
    const x = scroller.scrollLeft;
    const hasLeft = x > 1;
    const hasRight = x < max - 1;
    wrap.dataset.overflow = hasLeft && hasRight ? 'both'
      : hasLeft ? 'left'
      : hasRight ? 'right'
      : 'none';
  };

  btnLeft && btnLeft.addEventListener('click', () => scroller.scrollBy({ left: -160, behavior: 'smooth' }));
  btnRight && btnRight.addEventListener('click', () => scroller.scrollBy({ left: 160, behavior: 'smooth' }));

  // 縦ホイールを横スクロールに変換（端まで来たらページに委ねる）。
  scroller.addEventListener('wheel', (e) => {
    if (e.deltaY === 0) return;
    const max = scroller.scrollWidth - scroller.clientWidth;
    if (max <= 0) return;
    const x = scroller.scrollLeft;
    const canScroll = (e.deltaY > 0 && x < max - 1) || (e.deltaY < 0 && x > 1);
    if (!canScroll) return;
    e.preventDefault();
    scroller.scrollLeft += e.deltaY;
  }, { passive: false });

  scroller.addEventListener('scroll', updateOverflow);
  if (typeof ResizeObserver === 'function') {
    new ResizeObserver(updateOverflow).observe(scroller);
  }
  new MutationObserver(updateOverflow).observe(scroller, { childList: true, subtree: false });
  updateOverflow();
}

// scrollTabIntoView はアクティブ化したタブを可視範囲へスクロールする（溢れて隠れた
// タブを選んだときに見えるように）。
function scrollTabIntoView(tabEl) {
  if (tabEl && typeof tabEl.scrollIntoView === 'function') {
    tabEl.scrollIntoView({ inline: 'nearest', block: 'nearest' });
  }
}

// agentarium ホスト API を window に公開
globalThis.agentarium = {
  openAgentTab,
  closeAgentTab,
  openViewer,
  closeViewer,
  openTab,
  subscribe,
  fetch: (path) => fetch(path),
};

main();
