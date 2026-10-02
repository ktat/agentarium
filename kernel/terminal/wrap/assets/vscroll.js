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
export function syncRows(host, rows, first, last, renderRow) {
  for (const [y, node] of rows) {
    if (y < first || y > last) {
      node.remove();
      rows.delete(y);
    }
  }
  // DOM 順は行番号の昇順に保つ (選択・コピーのテキスト順と縦の並びのため)。
  let ref = host.firstChild;
  for (let y = first; y <= last; y++) {
    let node = rows.get(y);
    if (!node) {
      node = document.createElement('span');
      node.className = 'twrap-line';
      rows.set(y, node);
      host.insertBefore(node, ref);
    } else if (node !== ref) {
      host.insertBefore(node, ref);
    }
    ref = node.nextSibling;
    renderRow(node, y);
  }
}
