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

  // 3) WS 接続（自動再接続つき）
  const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  let ws;
  let resolveReady;
  const ready = new Promise((res) => { resolveReady = res; });

  // 再接続バナー（切断中だけ表示）。root を基準に上部固定。
  if (!root.style.position) root.style.position = 'relative';
  const banner = document.createElement('div');
  banner.textContent = '再接続中…';
  Object.assign(banner.style, {
    position: 'absolute', top: '0', left: '0', right: '0', padding: '2px 8px',
    textAlign: 'center', font: '12px monospace', background: '#6a5acd', color: '#fff',
    zIndex: '20', display: 'none', pointerEvents: 'none',
  });
  root.appendChild(banner);

  // 再接続: 指数バックオフ（RECONNECT_BASE→RECONNECT_MAX）。close() で userClosed=true にして止める。
  const RECONNECT_BASE = 500, RECONNECT_MAX = 5000;
  let reconnectAttempt = 0, reconnectTimer = 0, userClosed = false, firstConnect = true;

  function scheduleReconnect() {
    if (userClosed) return;
    banner.style.display = '';
    const delay = Math.min(RECONNECT_BASE * Math.pow(2, reconnectAttempt), RECONNECT_MAX);
    reconnectAttempt++;
    reconnectTimer = setTimeout(connect, delay);
  }

  function connect() {
    ws = new WebSocket(wsProto + '//' + location.host + '/terminal/ws?id=' + encodeURIComponent(ctx.id));
    ws.onmessage = (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      if (msg.type === 'output') term.write(msg.data);
    };
    ws.onopen = () => {
      // 再接続時はサーバが ReplayBuffer を再送するため、端末をクリアしてから受ける（重複回避）。
      if (!firstConnect) term.reset();
      firstConnect = false;
      reconnectAttempt = 0;
      banner.style.display = 'none';
      if (fit) fit.fit();
      sendResize();
      resolveReady && resolveReady();
    };
    ws.onclose = () => { scheduleReconnect(); };
    ws.onerror = () => { try { ws.close(); } catch (_) {} }; // close → onclose → scheduleReconnect
  }
  connect();

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
      userClosed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      try { ws && ws.close(); } catch (_) {}
      ro.disconnect();
      term.dispose();
    },
    ready,
  };
}
