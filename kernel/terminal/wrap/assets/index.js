// wrap renderer: サーバ側 VT エミュレータが送る LineUpdate 差分を色/属性付きで描画する。
// agentarium の 1-tab モデル: render(root, ctx) が DOM・WS・入力を結線し {close, ready} を返す。
// 移植元の wrapper renderer を per-tab に適応移植したもの。

// SGR 2 (Faint) を opacity でどれだけ薄くするか。xterm.js 互換の autosuggest
// 視認性 (元の前景色は維持しつつ薄く表示) を目安に調整した値。
const FAINT_OPACITY = '0.55';

function ensureCSS() {
  if (document.getElementById('twrap-css')) return;
  const link = document.createElement('link');
  link.id = 'twrap-css';
  link.rel = 'stylesheet';
  link.href = '/terminal/assets/wrap/wrap.css';
  document.head.appendChild(link);
}

function wsUrl(id) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return proto + '//' + location.host + '/terminal/ws?id=' + encodeURIComponent(id);
}

// render は呼び出し側 (app.js) で await される契約上 async（await が無くても署名を保つ）。
// deno-lint-ignore require-await
export async function render(root, ctx) {
  ensureCSS();
  const id = ctx && ctx.id;

  root.style.position = 'relative';
  const viewportEl = document.createElement('div');
  viewportEl.className = 'twrap-viewport';
  viewportEl.tabIndex = 0;
  const gridEl = document.createElement('div');
  gridEl.className = 'twrap-grid';
  const cursorEl = document.createElement('div');
  cursorEl.className = 'twrap-cursor';
  const imeEl = document.createElement('textarea');
  imeEl.className = 'twrap-ime';
  imeEl.setAttribute('autocomplete', 'off');
  imeEl.setAttribute('autocapitalize', 'off');
  imeEl.setAttribute('autocorrect', 'off');
  imeEl.setAttribute('spellcheck', 'false');
  imeEl.setAttribute('wrap', 'off');
  imeEl.spellcheck = false;
  viewportEl.appendChild(gridEl);
  viewportEl.appendChild(cursorEl);
  root.appendChild(viewportEl);
  // imeEl は viewport 外（root 直下）に置く（viewport 内だと composition の
  // scrollIntoView 暴発で viewport が先頭に飛ぶ）。
  root.appendChild(imeEl);

  const entry = {
    ws: null, viewportEl, gridEl, cursorEl, imeEl,
    grid: new Map(), gridMaxY: -1,
    cursorX: 0, cursorY: 0, cursorHidden: false,
    mode: 'main', altRows: 40, cols: 80,
    fontMetric: null, userScrolled: false, pendingRender: 0,
    imePosScheduled: false,
  };

  function measureCell() {
    // フォントは CSS（.twrap-grid → --twrap-font）を継承して実測する。
    // ハードコードしないことで CSS とセル幅が必ず一致し、カーソル位置がずれない。
    const tmp = document.createElement('span');
    tmp.style.position = 'absolute';
    tmp.style.visibility = 'hidden';
    tmp.style.whiteSpace = 'pre';
    tmp.textContent = 'M'.repeat(100);
    entry.gridEl.appendChild(tmp);
    const w = tmp.offsetWidth / 100;
    entry.gridEl.removeChild(tmp);
    const heightProbe = document.createElement('span');
    heightProbe.className = 'twrap-line';
    heightProbe.style.visibility = 'hidden';
    heightProbe.textContent = 'M';
    entry.gridEl.appendChild(heightProbe);
    const h = heightProbe.offsetHeight;
    entry.gridEl.removeChild(heightProbe);
    return { w: w || 8, h: h || 16 };
  }

  function sendResize() {
    if (!entry.ws || entry.ws.readyState !== 1) return;
    const m = measureCell();
    entry.fontMetric = m;
    // 非アクティブ tab は display:none で viewport.clientWidth = 0 になり、
    // そのまま計算すると cols=20 (Math.max の下限) になって PTY が極狭で起動する。
    // 親 root の幅を fallback として使う。
    let vpW = entry.viewportEl.clientWidth;
    let vpH = entry.viewportEl.clientHeight;
    if (vpW < 50 || vpH < 50) {
      vpW = root.clientWidth || vpW;
      vpH = root.clientHeight || vpH;
    }
    const newCols = Math.max(20, Math.floor(vpW / m.w));
    const newAltRows = Math.max(10, Math.floor(vpH / m.h));
    entry.altRows = newAltRows;
    entry.ws.send(JSON.stringify({ type: 'resize', cols: newCols, altRows: newAltRows }));
    // fontMetric 更新後に ime 位置を追従させる (リサイズ直後の render 待ちの間
    // に composition が始まっても候補ウィンドウ位置がずれないように)。
    positionIme();
  }

  function keyToSeq(e) {
    const k = e.key;
    // Alt 単独 (ctrl/meta が併用されていない) 修飾。SPECIAL ヒット時の ESC prefix と
    // 1 文字キー時の ESC prefix の両方で同じ条件を参照するため、ここで一度束ねて
    // 名前を付けておく (誤った修飾子組合せの混入を防ぐ意図を明示する)。
    const altOnly = e.altKey && !e.ctrlKey && !e.metaKey;
    const SPECIAL = {
      'Enter': '\r', 'Backspace': '\x7f', 'Tab': '\t', 'Escape': '\x1b',
      'ArrowUp': '\x1b[A', 'ArrowDown': '\x1b[B', 'ArrowRight': '\x1b[C', 'ArrowLeft': '\x1b[D',
      'Home': '\x1b[H', 'End': '\x1b[F', 'PageUp': '\x1b[5~', 'PageDown': '\x1b[6~',
      'Insert': '\x1b[2~', 'Delete': '\x1b[3~',
      'F1': '\x1bOP', 'F2': '\x1bOQ', 'F3': '\x1bOR', 'F4': '\x1bOS',
      'F5': '\x1b[15~', 'F6': '\x1b[17~', 'F7': '\x1b[18~', 'F8': '\x1b[19~',
      'F9': '\x1b[20~', 'F10': '\x1b[21~', 'F11': '\x1b[23~', 'F12': '\x1b[24~',
    };
    if (SPECIAL[k] !== undefined) {
      // Alt+特殊キー は VT 流に ESC prefix を付ける。
      // 例: Alt+Enter → \x1b\r (claude TUI 等で改行挿入)
      if (altOnly) return '\x1b' + SPECIAL[k];
      return SPECIAL[k];
    }
    if (k === 'Shift' || k === 'Control' || k === 'Alt' || k === 'Meta') return null;
    if (e.ctrlKey && !e.altKey && !e.metaKey && k.length === 1) {
      if (e.shiftKey) {
        const lk = k.toLowerCase();
        if (lk === 'v' || lk === 'c') return null;
      }
      const code = k.toLowerCase().charCodeAt(0);
      if (code >= 97 && code <= 122) return String.fromCharCode(code - 96);
      const ctrlMap = {' ': '\x00', '@': '\x00', '[': '\x1b', '\\': '\x1c', ']': '\x1d', '^': '\x1e', '_': '\x1f'};
      if (ctrlMap[k] !== undefined) return ctrlMap[k];
      return null;
    }
    if (altOnly && k.length === 1) return '\x1b' + k;
    if (e.metaKey) return null;
    if (k.length === 1) return k;
    return null;
  }

  function sendInput(text) {
    if (!entry.ws || entry.ws.readyState !== 1) return;
    entry.ws.send(JSON.stringify({ type: 'input', data: text }));
    // 入力したら過去出力閲覧用の手動 scroll up を解除して cursor 位置に戻す。
    // xterm.js の標準挙動 (入力時に bottom へジャンプ) と同等。
    entry.userScrolled = false;
    scheduleRender();
  }

  function attachInputHandlers() {
    entry.imeEl.addEventListener('keydown', (e) => {
      if (e.isComposing || e.keyCode === 229) return;
      const seq = keyToSeq(e);
      if (seq === null) return;
      e.preventDefault();
      sendInput(seq);
    });
    entry.imeEl.addEventListener('compositionstart', () => {
      // 変換中だけ textarea を可視化して未確定文字列をカーソル位置に表示する。
      entry.imeEl.classList.add('composing');
      positionIme();
    });
    entry.imeEl.addEventListener('compositionend', (e) => {
      entry.imeEl.classList.remove('composing');
      if (e.data) sendInput(e.data);
      entry.imeEl.value = '';
      positionIme();
    });
    entry.imeEl.addEventListener('input', (e) => {
      if (e.isComposing) return;
      if (e.inputType === 'insertCompositionText') return;
      if (e.inputType === 'insertFromPaste') return;
      if (e.inputType === 'insertText') {
        if (entry.imeEl.value) {
          sendInput(entry.imeEl.value);
          entry.imeEl.value = '';
        }
      }
    });
    entry.imeEl.addEventListener('paste', (e) => {
      e.preventDefault();
      const text = (e.clipboardData || globalThis.clipboardData).getData('text');
      if (!text || !entry.ws || entry.ws.readyState !== 1) return;
      entry.ws.send(JSON.stringify({ type: 'paste', data: text }));
    });
    // mousedown では preventDefault しない (ブラウザのテキスト選択ドラッグを許可)。
    // 代わりに mouseup で「選択範囲が無いとき」のみ ime に focus を戻す。
    // 選択範囲があるときに focus すると selection が解除される実装ブラウザがあるため。
    entry.viewportEl.addEventListener('mouseup', (e) => {
      if (e.target.tagName === 'BUTTON') return;
      const t = e.target;
      if (!(t === entry.viewportEl || t.classList.contains('twrap-grid') || t.classList.contains('twrap-line') || t.tagName === 'SPAN')) return;
      const sel = globalThis.getSelection();
      if (sel && sel.toString().length > 0) return;
      // preventScroll: true を付けないと、ime (left:-9999px, top:0) に focus
      // した瞬間にブラウザが要素を viewport に見せようとして scrollTop=0 に
      // ジャンプする (click で先頭に戻る症状の原因)。
      entry.imeEl.focus({ preventScroll: true });
    });
  }

  function runsKey(runs) {
    let s = '';
    for (const r of runs) s += (r.f||'') + '|' + (r.b||'') + '|' + (r.a||0) + ':' + r.t + '\x1e';
    return s;
  }

  // http(s) URL を検出して <a> に変換する。xterm.js の WebLinksAddon と同等に
  // URL をクリック可能にするための処理。wrapper はサーバ側で ANSI を Run に
  // 分割済みのため、URL が複数 Run にまたがって着色されているとリンク化されない
  // (実用上 URL は単色 1 Run で出力されるため許容)。
  const TWRAP_URL_RE = /https?:\/\/[^\s]+/g;
  function appendTextWithLinks(parent, text) {
    if (!text || text.indexOf('http') === -1) { parent.textContent = text; return; }
    TWRAP_URL_RE.lastIndex = 0;
    let last = 0, m, any = false;
    while ((m = TWRAP_URL_RE.exec(text)) !== null) {
      let url = m[0];
      // 末尾の句読点・閉じ括弧は URL に含めない (文中の "(...url)。" 等への対応)。
      const trail = url.match(/[.,;:!?)\]}>'"」』]+$/);
      if (trail) url = url.slice(0, url.length - trail[0].length);
      if (!url) { TWRAP_URL_RE.lastIndex = m.index + 1; continue; }
      const start = m.index;
      if (start > last) parent.appendChild(document.createTextNode(text.slice(last, start)));
      const a = document.createElement('a');
      a.className = 'twrap-link';
      a.href = url;
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
      a.textContent = url;
      parent.appendChild(a);
      last = start + url.length;
      any = true;
      // trail を URL から外した分、次回 match を URL 直後から再開させる。
      TWRAP_URL_RE.lastIndex = last;
    }
    if (!any) { parent.textContent = text; return; }
    if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
  }

  function renderRuns(node, runs) {
    const key = runsKey(runs);
    if (node.dataset.key === key) return;
    node.dataset.key = key;
    node.innerHTML = '';
    for (const r of runs) {
      const sp = document.createElement('span');
      let fg = r.f, bg = r.b;
      if ((r.a || 0) & 4) {
        // inverse: 前景と背景を入れ替える。デフォルト色は xterm.js 互換の値で補完。
        const tmp = fg || '#d4d4d4';
        fg = bg || '#1e1e1e';
        bg = tmp;
      }
      let style = '';
      if (fg) style += 'color:' + fg + ';';
      if (bg) style += 'background:' + bg + ';';
      if ((r.a || 0) & 1) style += 'font-weight:bold;';
      if ((r.a || 0) & 2) style += 'font-style:italic;';
      if ((r.a || 0) & 8) style += 'text-decoration:underline;';
      // bit 16 = Faint (SGR 2)。Claude Code の autosuggest 等が薄く表示される。
      // xterm.js 互換の見た目になるよう opacity を下げる (色は維持)。
      if ((r.a || 0) & 16) style += 'opacity:' + FAINT_OPACITY + ';';
      if (style) sp.setAttribute('style', style);
      appendTextWithLinks(sp, r.t);
      node.appendChild(sp);
    }
    node.appendChild(document.createTextNode('\n'));
  }

  // renderCursor は cursorX/cursorY の位置にブロックカーソル (entry.cursorEl) を
  // 重ねる。xterm.js 経路では xterm 自身がカーソルを描画するが、wrapper の grid
  // renderer には相当物が無く、素のシェルでカーソル位置が見えない。DECTCEM
  // (\x1b[?25l) で隠されている間 (Claude TUI 等が独自カーソルを描く間) は出さない。
  // 位置は cursorEl / .twrap-line とも offsetParent が viewport なので、行の
  // offsetTop/offsetLeft + 列 × cell 幅で重なる。--twrap-font が同梱の完全等幅
  // フォント (半角 1.0× / 全角 2.0×、罫線は半角) を保証するため、列モデル
  // (cursorX × セル幅) がそのまま実描画位置に一致する。
  //
  // NOTE: 以前は「実描画テキスト右端で上限クランプ (Math.min(left, contentRight))」
  // していたが、これは ui-monospace 由来の全角ドリフトの対症療法で、(1) フォント
  // 同梱でドリフト自体が解消し不要になった上、(2) snapshotLine が行末スペースを
  // trim するため「末尾スペースを打つとカーソルが描画右端に張り付いて動かない」
  // バグの原因になっていた。サーバが送る cursorX が唯一の正なので、クランプせず
  // 列モデルをそのまま使う (cursor は trim 済み空白領域 = blank の上を正しく進む)。
  function renderCursor(maxY) {
    const el = entry.cursorEl;
    if (!el) return;
    const y = entry.cursorY;
    if (entry.cursorHidden || y < 0 || y > maxY) {
      el.style.display = 'none';
      return;
    }
    const rowEl = entry.gridEl.children[y];
    if (!rowEl) { el.style.display = 'none'; return; }
    if (!entry.fontMetric) entry.fontMetric = measureCell();
    const m = entry.fontMetric;
    el.style.display = 'block';
    el.style.left = (rowEl.offsetLeft + entry.cursorX * m.w) + 'px';
    el.style.top = rowEl.offsetTop + 'px';
    el.style.width = m.w + 'px';
    el.style.height = rowEl.offsetHeight + 'px';
  }

  function scrollToCursor() {
    const vp = entry.viewportEl;
    const gridEl = entry.gridEl;
    // grid の最終行 (= 実際に何かが書かれた最も下の行) を viewport の bottom に
    // スナップする。下方向に余計なバッファは無く、cursor より下に書かれた行
    // (claude TUI の選択肢メニュー / プレビュー等) を画面外に追いやらないため、
    // cursor 行ではなく lastElementChild を基準にする。
    const lastEl = gridEl.lastElementChild;
    if (!lastEl) { vp.scrollTop = vp.scrollHeight; return; }
    try {
      lastEl.scrollIntoView({ block: 'end', inline: 'nearest' });
    } catch {
      // viewport の padding-bottom 等が乗ると scrollHeight ベースだと
      // 余白分だけ下に寄りすぎる。try と意味的に同等になるよう
      // 「lastEl の bottom を viewport の bottom に合わせる」計算で確定する。
      const want = lastEl.offsetTop + lastEl.offsetHeight - vp.clientHeight;
      vp.scrollTop = Math.max(0, want);
    }
  }

  // positionIme は ime textarea をカーソルの画面上の見かけ位置に重ねる。
  // IME の変換候補ウィンドウは focus 中 textarea のキャレット位置に出るため、
  // composition 開始前から常時追従させる (開始時点の位置で候補ウィンドウを
  // 固定する IME があるため、compositionstart 時だけの移動では足りない)。
  // imeEl 自体は root 直下 (viewport の外) のまま動かさない: viewport 内に
  // 入れると composition 中のブラウザの scrollIntoView で viewport が先頭に飛ぶ。
  // viewportEl は root に inset:0 で重なっているため、root 座標 = viewport 座標
  // として扱える。
  function positionIme() {
    const el = entry.imeEl;
    if (!el) return;
    const vp = entry.viewportEl;
    if (!entry.fontMetric) entry.fontMetric = measureCell();
    const m = entry.fontMetric;
    const rowEl = entry.gridEl.children[entry.cursorY];
    // カーソル行がまだ DOM に構築されていない瞬間 (render 前や cursorY が
    // 描画済み行数を超えている初期化タイミング) に呼ばれると、位置を 0,0 に
    // 倒して左上へ flash する。その場合は更新せず直前の正しい位置を維持する。
    if (!rowEl) return;
    const composing = el.classList.contains('composing');
    // 「行頭 offsetLeft + 列 × セル幅」で重ねる (同梱の完全等幅フォント前提で
    // 列モデルが実描画に一致する)。旧・実描画右端クランプは末尾スペースで張り付く
    // 副作用があり廃止した (renderCursor の NOTE 参照)。
    let left = rowEl.offsetLeft + entry.cursorX * m.w;
    let top = rowEl.offsetTop - vp.scrollTop;
    // カーソルが scroll で viewport 外にある場合も候補ウィンドウが viewport
    // 近傍に出るよう範囲内にクランプする。
    left = Math.max(0, Math.min(left, vp.clientWidth - m.w));
    top = Math.max(0, Math.min(top, vp.clientHeight - m.h));
    el.style.left = left + 'px';
    el.style.top = top + 'px';
    if (composing) {
      // 変換中はカーソル位置から viewport 右端までを未確定文字列の表示域に使う。
      // wrap="off" のため右端を超えた分は textarea 内の水平スクロールに任せる。
      el.style.width = Math.max(m.w * 2, vp.clientWidth - left) + 'px';
      el.style.height = m.h + 'px';
    } else {
      el.style.width = '1em';
      el.style.height = '1em';
    }
  }

  function setupScrollHandler() {
    entry.viewportEl.addEventListener('scroll', () => {
      // positionIme は style 書き込みを行うため、直接呼ぶと直後の offsetTop
      // 読み出しで scroll イベント毎に forced reflow が発生する。rAF に逃して
      // 書き込みをフレーム境界へデバウンスする (1 フレームに 1 回で十分)。
      if (!entry.imePosScheduled) {
        entry.imePosScheduled = true;
        requestAnimationFrame(() => { entry.imePosScheduled = false; positionIme(); });
      }
      if (entry.mode === 'alt') return;
      // userScrolled の判定基準は scrollToCursor と揃えて lastElementChild。
      // ユーザーが grid の bottom より上にスクロールすると userScrolled=true になり、
      // 以降の自動 scrollToCursor がガードされて過去出力を閲覧できる。
      const lastEl = entry.gridEl.lastElementChild;
      if (!lastEl) return;
      const lastBottom = lastEl.offsetTop + lastEl.offsetHeight;
      const viewportBottom = entry.viewportEl.scrollTop + entry.viewportEl.clientHeight;
      entry.userScrolled = (viewportBottom < lastBottom - 4);
    });
  }

  function doRender() {
    // maxY は「実際に書かれた行の最大 (gridMaxY)」 + cursor が近ければそれも含める。
    // gridMaxY は init/update 受信時に O(変更行数) で増分更新しているので、ここで
    // Math.max(...grid.keys()) を毎フレーム展開しなくて済む (ストリーミング負荷低減)。
    const usedMax = entry.gridMaxY >= 0 ? entry.gridMaxY : 0;
    const cursorNearGrid = entry.cursorY <= usedMax + 100;
    const mainMax = cursorNearGrid ? Math.max(usedMax, entry.cursorY) : usedMax;
    const maxY = (entry.mode === 'alt')
      ? (entry.altRows - 1)
      : Math.max(mainMax, 0);
    const gridEl = entry.gridEl;
    while (gridEl.children.length <= maxY) {
      const node = document.createElement('span');
      node.className = 'twrap-line';
      gridEl.appendChild(node);
    }
    while (gridEl.children.length > maxY + 1) {
      gridEl.removeChild(gridEl.lastChild);
    }
    for (let y = 0; y <= maxY; y++) {
      const runs = entry.grid.get(y) || [];
      const node = gridEl.children[y];
      renderRuns(node, runs);
      node.classList.toggle('twrap-cursor-row', y === entry.cursorY);
    }
    if (entry.mode === 'alt') {
      entry.viewportEl.scrollTop = 0;
    } else if (!entry.userScrolled) {
      scrollToCursor();
    }
    // scrollToCursor で scrollTop が確定した後に ime を追従させる。
    renderCursor(maxY);
    positionIme();
  }

  function scheduleRender() {
    if (entry.pendingRender) return;
    entry.pendingRender = requestAnimationFrame(() => { entry.pendingRender = 0; doRender(); });
  }

  function applyMessage(msg) {
    if (msg.type === 'init' || msg.type === 'snapshot') {
      entry.mode = msg.mode || 'main';
      entry.altRows = msg.altRows || entry.altRows || 40;
      entry.grid.clear();
      entry.gridMaxY = -1;
      for (const ln of (msg.lines || [])) {
        if (ln.runs && ln.runs.length) {
          entry.grid.set(ln.y, ln.runs);
          if (ln.y > entry.gridMaxY) entry.gridMaxY = ln.y;
        }
      }
      entry.cursorX = msg.cursorX;
      entry.cursorY = msg.cursorY;
      entry.cursorHidden = !!msg.cursorHidden;
      entry.userScrolled = false;
      scheduleRender();
    } else if (msg.type === 'update') {
      // 空 runs は "行が clear された" 通知。set([]) で keys に残すと無駄な空行が出る。
      for (const ln of (msg.lines || [])) {
        if (ln.runs && ln.runs.length) {
          entry.grid.set(ln.y, ln.runs);
          if (ln.y > entry.gridMaxY) entry.gridMaxY = ln.y;
        } else if (entry.grid.has(ln.y)) {
          entry.grid.delete(ln.y);
          // 末尾を消したら gridMaxY を再計算 (それ以外は max は変わらない)
          if (ln.y === entry.gridMaxY) {
            entry.gridMaxY = entry.grid.size ? Math.max(...entry.grid.keys()) : -1;
          }
        }
      }
      entry.cursorX = msg.cursorX;
      entry.cursorY = msg.cursorY;
      entry.cursorHidden = !!msg.cursorHidden;
      scheduleRender();
    }
  }

  attachInputHandlers();
  setupScrollHandler();
  const ro = new ResizeObserver(() => sendResize());
  ro.observe(root);

  let resolveReady;
  const ready = new Promise((res) => { resolveReady = res; });

  // 再接続バナー（切断中だけ表示）。
  const banner = document.createElement('div');
  banner.className = 'twrap-reconnect';
  banner.textContent = '再接続中…';
  banner.style.display = 'none';
  root.appendChild(banner);

  // 再接続: 指数バックオフ（RECONNECT_BASE→RECONNECT_MAX）。close() で userClosed=true にして止める。
  const RECONNECT_BASE = 500, RECONNECT_MAX = 5000;
  let reconnectAttempt = 0, reconnectTimer = 0, userClosed = false;

  function scheduleReconnect() {
    if (userClosed) return;
    banner.style.display = '';
    const delay = Math.min(RECONNECT_BASE * Math.pow(2, reconnectAttempt), RECONNECT_MAX);
    reconnectAttempt++;
    reconnectTimer = setTimeout(connect, delay);
  }

  function connect() {
    const ws = new WebSocket(wsUrl(id));
    entry.ws = ws;
    ws.onopen = () => {
      reconnectAttempt = 0;
      banner.style.display = 'none';
      // 再接続後はサーバが init snapshot を送り applyMessage('init') が grid を
      // クリア再構築するため、ここでの明示クリアは不要。
      if (document.fonts && document.fonts.ready) document.fonts.ready.then(() => sendResize());
      else sendResize();
      resolveReady && resolveReady();
    };
    ws.onmessage = (ev) => { let msg; try { msg = JSON.parse(ev.data); } catch (_) { return; } applyMessage(msg); };
    ws.onclose = () => { scheduleReconnect(); };
    ws.onerror = () => { try { ws.close(); } catch (_) { /* 無視 */ } }; // close → onclose → scheduleReconnect
  }

  connect();

  return {
    close() {
      userClosed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (entry.pendingRender) { cancelAnimationFrame(entry.pendingRender); entry.pendingRender = 0; }
      try { entry.ws && entry.ws.close(); } catch (_) { /* 無視 */ }
      try { ro.disconnect(); } catch (_) { /* 無視 */ }
    },
    ready,
  };
}
