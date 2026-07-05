// Chat タブ: 自由入力 → 既定エージェント起動（初期入力注入）+ chat 履歴の一覧/再開。
// シェルは /plugins/chat/assets/index.js を動的 import し render(root, {pluginId}) を呼ぶ。
function esc(s) {
  return String(s).replace(/[<>&"']/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;', "'": '&#39;' }[c]));
}

const ARCHIVE_KEY = 'chat-show-archived';

// 端末生存/状態を購読する SSE 接続。render は再活性化ごとに呼ばれるため、
// 接続をモジュール変数で1本に保ち、再 render 時は旧接続を閉じてから開き直す。
let sseConn = null;

function showArchived() {
  return localStorage.getItem(ARCHIVE_KEY) === '1';
}

// render は呼び出し側 (app.js) で await される契約上 async（await が無くても署名を保つ）。
// deno-lint-ignore require-await
export async function render(root) {
  root.innerHTML =
    '<div class="chat-form">' +
    '<textarea id="chatInput" rows="3" placeholder="メッセージを入力（Enterで送信 / Shift+Enterで改行）"></textarea>' +
    '<button id="chatStart">送信</button></div>' +
    '<div id="chatHistory"></div>';

  const input = root.querySelector('#chatInput');
  const startBtn = root.querySelector('#chatStart');
  const hist = root.querySelector('#chatHistory');

  // liveStates: 生存端末の id → state（"running"/"idle"/"awaiting_user" 等）。
  // /terminal/events(SSE) の state イベントで更新する。id は chat レコードの id
  // （= terminal key）と一致するため、履歴行の「実行中」判定に使える。
  const liveStates = new Map();

  async function refreshHistory() {
    try {
      const res = await fetch('/plugins/chat/list');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const data = await res.json();
      const all = data.items || [];
      const items = all.filter(r => showArchived() || !r.archived_at);
      const archivedCount = all.filter(r => r.archived_at).length;
      const toggle = '<label class="chat-archive-toggle"><input type="checkbox" id="chatArchiveToggle"' +
        (showArchived() ? ' checked' : '') + '> archive 済み (' + archivedCount + ') を表示</label>';
      if (!items.length) {
        hist.innerHTML = toggle + '<p class="empty-section">履歴なし</p>';
        bindToggle();
        return;
      }
      const rows = items.map(r => {
        const created = (r.started_at || '').replace('T', ' ').substring(0, 19);
        const rowCls = r.archived_at ? ' class="row-archived"' : '';
        // 生存端末（pending=遅延復元未起動を除く）は「実行中」。クリックで
        // その PTY タブを activate する。そうでなければ従来の「再開」。
        const st = liveStates.get(r.id);
        const isLive = !!st && st !== 'pending';
        let actionBtn;
        if (isLive) {
          actionBtn = '<button class="chat-active-btn" data-id="' + esc(r.id) +
            '" data-summary="' + esc(r.summary) + '">● 実行中</button>';
        } else {
          const resumeAttrs = r.session_id
            ? 'data-id="' + esc(r.id) + '" data-sid="' + esc(r.session_id) + '" data-summary="' + esc(r.summary) + '"'
            : 'disabled';
          actionBtn = '<button class="chat-resume-btn" ' + resumeAttrs + '>↪ 再開</button>';
        }
        return '<tr' + rowCls + '><td>' + esc(r.summary) + '</td>' +
          '<td>' + esc(created) + '</td>' +
          '<td>' + actionBtn + ' ' +
          '<button class="chat-archive-btn" data-id="' + esc(r.id) + '">' +
          (r.archived_at ? '戻す' : 'archive') + '</button></td></tr>';
      }).join('');
      hist.innerHTML = toggle +
        '<table class="task-table"><thead><tr><th>テキスト</th><th>作成</th><th></th></tr></thead><tbody>' +
        rows + '</tbody></table>';
      bindToggle();
      bindRowButtons();
    } catch (err) {
      hist.innerHTML = '<p class="empty-section">取得失敗: ' + esc(String(err)) + '</p>';
    }
  }

  function bindToggle() {
    const t = hist.querySelector('#chatArchiveToggle');
    if (t && !t.dataset.bound) {
      t.dataset.bound = '1';
      t.addEventListener('change', () => {
        localStorage.setItem(ARCHIVE_KEY, t.checked ? '1' : '0');
        refreshHistory();
      });
    }
  }

  function bindRowButtons() {
    hist.querySelectorAll('button.chat-resume-btn[data-sid]:not([data-bound])').forEach(btn => {
      btn.dataset.bound = '1';
      btn.addEventListener('click', () => {
        const summary = btn.dataset.summary || '';
        const label = '↪ ' + (summary.length > 28 ? summary.slice(0, 28) + '…' : summary);
        globalThis.agentarium.openAgentTab({ key: btn.dataset.id, label: label, resume: btn.dataset.sid });
      });
    });
    hist.querySelectorAll('button.chat-active-btn:not([data-bound])').forEach(btn => {
      btn.dataset.bound = '1';
      btn.addEventListener('click', () => {
        const summary = btn.dataset.summary || '';
        const label = '↪ ' + (summary.length > 28 ? summary.slice(0, 28) + '…' : summary);
        // 生存端末: openAgentTab は既存の右タブなら activate する（新規起動しない）。
        globalThis.agentarium.openAgentTab({ key: btn.dataset.id, label: label });
      });
    });
    hist.querySelectorAll('button.chat-archive-btn:not([data-bound])').forEach(btn => {
      btn.dataset.bound = '1';
      btn.addEventListener('click', async () => {
        btn.disabled = true;
        try {
          await fetch('/plugins/chat/archive?id=' + encodeURIComponent(btn.dataset.id), { method: 'POST' });
        } catch (_) { /* 無視 */ }
        refreshHistory();
      });
    });
  }

  // 起動後 /terminal/list をポーリングして SessionID を ChatRecord に紐付ける。
  function trackSessionID(id) {
    let tries = 0;
    const timer = setInterval(async () => {
      tries++;
      if (tries > 40) { clearInterval(timer); return; } // 最大 ~40*1.5s（カーネルの検出窓 45s に合わせる）
      try {
        const res = await fetch('/terminal/list');
        if (!res.ok) return;
        const data = await res.json();
        const row = (data.items || []).find(it => it.ID === id);
        if (row && row.SessionID) {
          const upd = await fetch('/plugins/chat/update?id=' + encodeURIComponent(id) +
            '&session_id=' + encodeURIComponent(row.SessionID), { method: 'POST' });
          if (!upd.ok) return; // 更新失敗時は clearInterval せず次の tick で再試行（tries 上限まで）
          clearInterval(timer);
          refreshHistory();
        }
      } catch (_) { /* 無視 */ }
    }, 1500);
  }

  async function startChat() {
    const text = (input.value || '').trim();
    if (!text) { alert('テキストを入力してください'); return; }
    let id;
    try {
      const res = await fetch('/plugins/chat/start', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ summary: text }),
      });
      if (!res.ok) { alert('start failed (' + res.status + '): ' + await res.text()); return; }
      id = (await res.json()).id;
    } catch (err) { alert('start error: ' + err); return; }

    const label = text.length > 28 ? text.slice(0, 28) + '…' : text;
    globalThis.agentarium.openAgentTab({ key: id, label: label, command: text, autoEnter: true });
    input.value = '';
    trackSessionID(id);
    setTimeout(refreshHistory, 800);
  }

  // 端末の生存/状態を /terminal/events(SSE) で購読し、履歴の「実行中」表示を更新する。
  // 起動→状態変化に加え、Kernel は stop 時にも再配信するため「実行中→再開」も push される。
  // 接続時に現在スナップショットが即届くので初期表示も正確。SSE 不可なら session_id ベースで動く。
  function setupLiveTracking() {
    if (sseConn) { try { sseConn.close(); } catch (_) { /* 無視 */ } sseConn = null; }
    try {
      sseConn = new EventSource('/terminal/events');
      sseConn.addEventListener('state', e => {
        let payload;
        try { payload = JSON.parse(e.data); } catch (_) { return; }
        liveStates.clear();
        for (const s of (payload.sessions || [])) {
          if (s && s.id) liveStates.set(s.id, s.state);
        }
        refreshHistory();
      });
    } catch (_) { /* SSE 不可でも履歴は session_id ベースで動作する */ }
  }

  startBtn.addEventListener('click', startChat);
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); startChat(); }
  });

  setupLiveTracking();
  refreshHistory();
}
