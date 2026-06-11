// xterm renderer: vendored xterm.js を初期化し、WS で raw bytes を双方向にやり取りする。
// shell の app.js から動的 import され render(root, {id}) が呼ばれる。
export async function render(root, ctx) {
  // 1) xterm.js が global Terminal として読まれている前提（shell が <script> で読む）
  if (typeof Terminal === 'undefined') {
    root.textContent = 'xterm.js not loaded; shell asset wiring broken';
    return;
  }
  // 2) 容器 + xterm.css link を root にぶら下げる
  const link = document.createElement('link');
  link.rel = 'stylesheet';
  link.href = '/terminal/assets/xterm/xterm.min.css';
  root.appendChild(link);

  const termDiv = document.createElement('div');
  termDiv.style.height = '100%';
  termDiv.style.width = '100%';
  root.appendChild(termDiv);

  const term = new Terminal({
    fontFamily: 'monospace',
    fontSize: 13,
    convertEol: false,
    scrollback: 5000,
  });
  // FitAddon を使って cols/rows を viewport に合わせる
  let fit = null;
  if (typeof FitAddon !== 'undefined' && FitAddon.FitAddon) {
    fit = new FitAddon.FitAddon();
    term.loadAddon(fit);
  }
  term.open(termDiv);
  if (fit) fit.fit();

  // 3) WS 接続
  const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const ws = new WebSocket(wsProto + '//' + location.host + '/terminal/ws?id=' + encodeURIComponent(ctx.id));
  let resolveReady;
  const ready = new Promise((res) => { resolveReady = res; });
  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch (e) { return; }
    if (msg.type === 'output') {
      term.write(msg.data);
    }
  };
  ws.onopen = () => {
    if (fit) fit.fit();
    sendResize();
    resolveReady && resolveReady();
  };

  // 4) クライアント入力 → WS
  term.onData((data) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'input', data: data }));
    }
  });

  // 5) viewport resize → WS resize
  function sendResize() {
    if (ws.readyState !== WebSocket.OPEN || !fit) return;
    fit.fit();
    ws.send(JSON.stringify({ type: 'resize', rows: term.rows, cols: term.cols }));
  }
  const ro = new ResizeObserver(() => sendResize());
  ro.observe(termDiv);

  // 6) クリーンアップ用に返す
  return {
    close() {
      try { ws.close(); } catch (_) {}
      ro.disconnect();
      term.dispose();
    },
    ready,
  };
}
