// wrap renderer の仮想スクロール部品。長いセッション (数千行) で全行を DOM 化
// すると、1 打鍵ごとの描画で全行の差分照合・class 切替と、その直後のレイアウト
// 読み出し (offsetTop 等) による全行 reflow が走り、ページが固まる。可視域 ±
// バッファだけを DOM 化し、範囲外は上下の spacer の高さだけで表す。
// index.js から import する (同じ assets FS から /terminal/assets/wrap/ で配信)。

// 可視域の上下に余分に DOM 化しておく行数。スクロール直後の描画 (次フレーム)
// までの空白を隠すのと、ブラウザのテキスト選択を可視域の外まで少し伸ばせる
// ようにするための余裕。DOM から外れた行の選択は失われるため、描画コストが
// 問題にならない範囲で多めに取る。
export const OVERSCAN_ROWS = 60;

// visibleRange は scroll 位置から DOM 化すべき行の範囲 (可視域 ± buffer、両端
// 含む) を返す純関数。全行が同じ高さ (lineH) であることが前提 (white-space: pre
// + overflow-x: hidden で折り返しが起きない)。maxY < minY が渡されても
// first <= last を保つ (呼び出し側の削除ループが空の host で例外にならないため)。
export function visibleRange(scrollTop, clientHeight, lineH, minY, maxY, buffer) {
  if (!(lineH > 0) || maxY < minY) return { first: minY, last: Math.max(minY, maxY) };
  const firstVisible = minY + Math.floor(Math.max(0, scrollTop) / lineH);
  const visibleCount = Math.ceil(Math.max(0, clientHeight) / lineH);
  let first = firstVisible - buffer;
  let last = firstVisible + visibleCount + buffer;
  if (first < minY) first = minY;
  if (first > maxY) first = maxY;
  if (last > maxY) last = maxY;
  if (last < first) last = first;
  return { first, last };
}

// syncRows は host の子 span を [first, last] の行に同期する。rows (行 → span の
// Map) を正典とし、範囲に残る行は同じノードを使い続ける。ブラウザの Selection
// は DOM ノードとオフセットに結び付くため、ノードを「位置」で使い回して中身
// だけ差し替えると、選択したままスクロールしたとき選択の帯が画面上の物理位置
// に貼り付き、別の行の文字を指してしまう。並び替えは insertBefore による移動
// のみで、ノードは作り直さない。renderRow(node, y) は行の内容更新 (呼び出し側)。
//
// pinned は範囲外でも DOM に残す行 (選択の端点の行)。端点の行を捨てると選択の
// 端点が行の容れ物へ移り、末尾追従で数十行流れただけで選択が潰れる (コピーが
// 空になる)。残した行は本来の位置に置けないので範囲の直前 / 直後に並べ、その
// 行数 { above, below } を返す (呼び出し側が spacer の高さから差し引く)。
export function syncRows(host, rows, first, last, renderRow, pinned) {
  const keep = new Set();
  for (const y of pinned || []) {
    if ((y < first || y > last) && rows.has(y)) keep.add(y);
  }
  for (const [y, node] of rows) {
    if ((y < first || y > last) && !keep.has(y)) {
      node.remove();
      rows.delete(y);
    }
  }
  const above = [...keep].filter((y) => y < first).sort((a, b) => a - b);
  const below = [...keep].filter((y) => y > last).sort((a, b) => a - b);
  // DOM 順は行番号の昇順に保つ (選択・コピーのテキスト順と縦の並びのため)。
  let ref = host.firstChild;
  const place = (y) => {
    let node = rows.get(y);
    if (!node) {
      node = document.createElement('span');
      node.className = 'twrap-line';
      node.dataset.y = String(y);
      rows.set(y, node);
      host.insertBefore(node, ref);
    } else if (node !== ref) {
      host.insertBefore(node, ref);
    }
    ref = node.nextSibling;
    renderRow(node, y);
  };
  for (const y of above) place(y);
  for (let y = first; y <= last; y++) place(y);
  for (const y of below) place(y);
  return { above: above.length, below: below.length };
}

// rowElOf は選択の端点 (node, offset) を含む行 span を返す (host の外なら null)。
// 端点が host 自身 (行と行の境界) のときは offset 番目の子 (末尾なら最終行)。
export function rowElOf(host, node, offset) {
  if (!node || !host.contains(node)) return null;
  if (node === host) {
    const kids = host.childNodes;
    return kids[Math.min(offset, kids.length - 1)] || null;
  }
  let el = node;
  while (el && el.parentNode !== host) el = el.parentNode;
  return el;
}

// pointToRow は Range の端点を { y: 行番号, off: 行頭からの文字数 } に直す。
// 行と行の境界にある端点は、終端なら直前の行の行末 (改行込み)、始端なら次の行
// の行頭として扱う。
export function pointToRow(host, container, offset, isEnd) {
  if (!container || !host.contains(container)) return null;
  if (container === host) {
    const kids = host.childNodes;
    if (!kids.length) return null;
    if (isEnd && offset > 0) return { y: Number(kids[offset - 1].dataset.y), off: Infinity };
    if (offset >= kids.length) return { y: Number(kids[kids.length - 1].dataset.y), off: Infinity };
    return { y: Number(kids[offset].dataset.y), off: 0 };
  }
  const rowEl = rowElOf(host, container, offset);
  if (!rowEl) return null;
  // 行 span の中をテキスト順に辿り、端点の手前までの文字数を数える。
  let n = 0, done = false;
  const walk = (node) => {
    if (node === container) {
      if (node.nodeType === 3) n += offset;
      else for (let i = 0; i < offset && i < node.childNodes.length; i++) n += node.childNodes[i].textContent.length;
      done = true;
      return;
    }
    if (node.nodeType === 3) { n += node.data.length; return; }
    for (const c of node.childNodes) { walk(c); if (done) return; }
  };
  walk(rowEl);
  return { y: Number(rowEl.dataset.y), off: n };
}

// selectionText は grid (行 → runs) から [s, e] の文字列を組み立てる。行の
// テキストは renderRuns と同じく runs の連結 + 改行なので、DOM で選択したときの
// 文字列と一致する。DOM に無い (描画範囲外の) 行も grid から埋まる。
export function selectionText(grid, s, e) {
  let out = '';
  for (let y = s.y; y <= e.y; y++) {
    const text = (grid.get(y) || []).map((r) => r.t).join('') + '\n';
    const from = y === s.y ? s.off : 0;
    const to = y === e.y ? e.off : Infinity;
    out += text.slice(from, to);
  }
  return out;
}

// selectionRowYs は選択の端点 (anchor / focus) がこの host の行にあれば、その行
// 番号を返す。doRender はこの行を描画範囲外でも DOM に残す (syncRows 参照)。
export function selectionRowYs(host, sel) {
  if (!sel || !sel.rangeCount) return [];
  const ys = [];
  for (const [n, o] of [[sel.anchorNode, sel.anchorOffset], [sel.focusNode, sel.focusOffset]]) {
    const el = rowElOf(host, n, o);
    if (el && el.dataset && el.dataset.y !== undefined) ys.push(Number(el.dataset.y));
  }
  return ys;
}

// selectionCopyText は選択の両端がこの host の行にあるとき、コピーする文字列を
// grid から組み立てて返す (それ以外は null = ブラウザに任せる)。DOM に残すのは
// 端点の行だけなので、DOM の選択文字列のままだと間の描画範囲外の行が抜ける。
export function selectionCopyText(host, grid, sel) {
  if (!sel || !sel.rangeCount) return null;
  const r = sel.getRangeAt(0);
  if (r.collapsed) return null;
  const s = pointToRow(host, r.startContainer, r.startOffset, false);
  const e = pointToRow(host, r.endContainer, r.endOffset, true);
  if (!s || !e) return null;
  return selectionText(grid, s, e);
}
