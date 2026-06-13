// カーネルシェル。3 つの責務:
//  1) /api/plugins からタブを構築（左ペイン）
//  2) plugin タブ選択で /plugins/<id>/assets/index.js を動的 import
//  3) agentarium.openAgentTab(...) ホスト API で右ペインに Agent タブを開く
//     (renderer は /terminal/renderer で取得、JS は /terminal/assets/<renderer>/index.js)

const rightTabs = new Map(); // key → {tabEl, panelEl, instance}
const viewerTabs = new Map(); // key → {tabEl, panelEl}
let rendererName = null;

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
  const leftBar = document.getElementById('left-tab-bar');
  const panel = document.getElementById('panel');
  for (const p of plugins) {
    if (p.pane === 'right') continue; // 右ペインプラグインは agent タブと衝突するため左に統一
    const btn = document.createElement('button');
    btn.className = 'left-tab';
    btn.textContent = p.title;
    btn.dataset.pluginId = p.id;
    btn.addEventListener('click', () => {
      // active クラスを切り替え
      leftBar.querySelectorAll('.left-tab').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      activate(p, panel);
    });
    leftBar.appendChild(btn);
  }
  await focusFromHash();
  window.addEventListener('hashchange', focusFromHash);
}

// focusFromHash は location.hash が "#term=<id>" のとき該当 Agent タブを開く/アクティブ化する。
// Pet の popover クリックが xdg-open する deep-link を処理する。
async function focusFromHash() {
  const m = /^#term=(.+)$/.exec(location.hash || '');
  if (!m) return;
  const id = decodeURIComponent(m[1]);
  let label = id;
  try {
    const res = await fetch('/terminal/list');
    if (res.ok) {
      const data = await res.json();
      const items = (data && data.items) || [];
      const hit = items.find((it) => it.ID === id || it.id === id);
      if (hit) label = hit.Label || hit.label || id;
    }
  } catch (_) {}
  if (window.agentarium && typeof window.agentarium.openAgentTab === 'function') {
    window.agentarium.openAgentTab({ key: id, label: label });
  }
}

async function activate(p, panel) {
  panel.innerHTML = '';
  try {
    const mod = await import('/plugins/' + p.id + '/assets/index.js');
    if (typeof mod.render === 'function') {
      await mod.render(panel, { pluginId: p.id });
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
  const labelSpan = document.createElement('span');
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

  // 4) command が指定されていれば inject (autoEnter で \r)
  if (command) {
    const entry = rightTabs.get(key);
    const inst = entry && entry.instance;
    const send = () => {
      fetch('/terminal/inject', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ terminal_id: key, text: command, enter: !!autoEnter }),
      }).catch(() => {});
    };
    if (inst && inst.ready && typeof inst.ready.then === 'function') {
      inst.ready.then(send).catch(send);
    } else {
      setTimeout(send, 200); // ready 非対応 renderer 向けフォールバック
    }
  }
}

function activateRightTab(key) {
  for (const [k, entry] of rightTabs) {
    if (k === key) {
      entry.tabEl.classList.add('active');
      entry.panelEl.style.display = '';
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
  try { await fetch('/terminal/stop?' + qs.toString(), { method: 'POST' }); } catch (_) {}
  // renderer instance のクリーンアップ
  if (entry.instance && typeof entry.instance.close === 'function') {
    try { entry.instance.close(); } catch (_) {}
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
//   type:    'markdown'（既定・サーバ描画+サニタイズ）/ 'text'（pre 表示・安全）
//   content: インラインのソース文字列
//   url:     同一オリジンのパス（content 未指定時に fetch して取得）
async function openViewer(opts) {
  const { key, title, type = 'markdown', content, url } = opts || {};
  if (!key) { console.warn('openViewer: key is required'); return; }
  const pane = document.querySelector('.right-pane');
  pane.classList.remove('no-viewer'); // 上下分割を表示
  if (viewerTabs.has(key)) { activateViewerTab(key); return; }

  const tabBar = document.getElementById('viewer-tab-bar');
  const tabEl = document.createElement('button');
  tabEl.className = 'viewer-tab';
  tabEl.dataset.viewerKey = key;
  const labelSpan = document.createElement('span');
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
    return;
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
}

function activateViewerTab(key) {
  for (const [k, entry] of viewerTabs) {
    const on = k === key;
    entry.tabEl.classList.toggle('active', on);
    entry.panelEl.style.display = on ? '' : 'none';
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
  window.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const rect = layout.getBoundingClientRect();
    const leftWidth = e.clientX - rect.left;
    if (leftWidth < leftMinPx || leftWidth > rect.width - rightMinPx) return;
    left.style.flex = '0 0 ' + leftWidth + 'px';
  });
  window.addEventListener('mouseup', () => {
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
  window.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const rect = pane.getBoundingClientRect();
    const topH = e.clientY - rect.top;
    if (topH < 80 || topH > rect.height - 80) return;
    top.style.flex = '0 0 ' + topH + 'px';
  });
  window.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    document.body.classList.remove('layout-dragging');
  });
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
  } catch (_) {}
}, 2500);

// agentarium ホスト API を window に公開
window.agentarium = {
  openAgentTab,
  closeAgentTab,
  openViewer,
  closeViewer,
  fetch: (path) => fetch(path),
};

main();
