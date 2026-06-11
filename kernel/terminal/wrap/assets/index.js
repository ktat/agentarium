// wrap renderer (最小版): WSMessage の init/snapshot/update を <pre> ベースに反映する。
// 色・属性・wide char は未対応（runs[].T のテキストだけを並べる）。本格差分描画は後続調整。
export async function render(root, ctx) {
  // 1) DOM: <pre> をスクロール領域に
  const pre = document.createElement('pre');
  pre.style.margin = '0';
  pre.style.padding = '4px 8px';
  pre.style.font = '13px monospace';
  pre.style.whiteSpace = 'pre';
  pre.style.overflow = 'auto';
  pre.style.height = '100%';
  pre.style.background = '#1e1e1e';
  pre.style.color = '#e5e5e5';
  root.appendChild(pre);

  // 2) Y 行毎の textContent を Map で管理し、毎フレーム最大行までを join して描画
  const lines = new Map();
  let cols = 80, rows = 0;
  function repaint() {
    const ys = [...lines.keys()].sort((a, b) => a - b);
    const buf = [];
    for (const y of ys) buf.push(lines.get(y));
    pre.textContent = buf.join('\n');
  }
  function applyLineUpdate(lu) {
    // runs[].T を結合（色は無視）
    const text = (lu.runs || []).map((r) => r.t || '').join('');
    if (text.length === 0) {
      lines.delete(lu.y);
    } else {
      lines.set(lu.y, text);
    }
  }
  function applyMessage(msg) {
    if (msg.cols) cols = msg.cols;
    if (msg.rows) rows = msg.rows;
    if (msg.type === 'init' || msg.type === 'snapshot') {
      lines.clear();
      for (const lu of msg.lines || []) applyLineUpdate(lu);
      repaint();
    } else if (msg.type === 'update') {
      for (const lu of msg.lines || []) applyLineUpdate(lu);
      repaint();
    }
  }

  // 3) WS 接続
  const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const ws = new WebSocket(wsProto + '//' + location.host + '/terminal/ws?id=' + encodeURIComponent(ctx.id));
  let resolveReady;
  const ready = new Promise((res) => { resolveReady = res; });
  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch (_) { return; }
    applyMessage(msg);
  };

  // 4) viewport resize は ClientInput resize で通知（最小版は 1 度だけ）
  ws.onopen = () => {
    const c = Math.max(40, Math.floor(pre.clientWidth / 8) | 0);
    const r = Math.max(10, Math.floor(pre.clientHeight / 16) | 0);
    ws.send(JSON.stringify({ type: 'resize', cols: c, altRows: r }));
    resolveReady && resolveReady();
  };

  // 5) クライアント入力（input/paste）はキーイベントを最小限拾う
  root.tabIndex = 0;
  root.style.outline = 'none';
  root.addEventListener('keydown', (e) => {
    if (ws.readyState !== WebSocket.OPEN) return;
    let data = '';
    if (e.key.length === 1 && !e.ctrlKey && !e.altKey && !e.metaKey) {
      data = e.key;
    } else if (e.key === 'Enter') data = '\r';
    else if (e.key === 'Backspace') data = '\x7f';
    else if (e.key === 'Tab') data = '\t';
    else if (e.key === 'Escape') data = '\x1b';
    else if (e.key === 'ArrowUp') data = '\x1b[A';
    else if (e.key === 'ArrowDown') data = '\x1b[B';
    else if (e.key === 'ArrowRight') data = '\x1b[C';
    else if (e.key === 'ArrowLeft') data = '\x1b[D';
    if (!data) return;
    e.preventDefault();
    ws.send(JSON.stringify({ type: 'input', data: data }));
  });

  return {
    close() { try { ws.close(); } catch (_) {} },
    ready,
  };
}
