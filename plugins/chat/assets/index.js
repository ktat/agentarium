// Chat タブ: 自由入力 → 既定エージェント起動（初期入力注入）+ chat 履歴の一覧/再開。
// シェルは /plugins/chat/assets/index.js を動的 import し render(root, {pluginId}) を呼ぶ。
function esc(s) {
  return String(s).replace(/[<>&"']/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;', "'": '&#39;' }[c]));
}

const ARCHIVE_KEY = 'chat-show-archived';

function showArchived() {
  return localStorage.getItem(ARCHIVE_KEY) === '1';
}

export async function render(root) {
  root.innerHTML =
    '<div class="chat-form">' +
    '<textarea id="chatInput" rows="3" placeholder="メッセージを入力（Enterで送信 / Shift+Enterで改行）"></textarea>' +
    '<button id="chatStart">送信</button></div>' +
    '<div id="chatHistory"></div>';

  const input = root.querySelector('#chatInput');
  const startBtn = root.querySelector('#chatStart');
  const hist = root.querySelector('#chatHistory');

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
        const resumeAttrs = r.session_id
          ? 'data-id="' + esc(r.id) + '" data-sid="' + esc(r.session_id) + '" data-summary="' + esc(r.summary) + '"'
          : 'disabled';
        return '<tr' + rowCls + '><td>' + esc(r.summary) + '</td>' +
          '<td>' + esc(created) + '</td>' +
          '<td><button class="chat-resume-btn" ' + resumeAttrs + '>↪ 再開</button> ' +
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
        window.agentarium.openAgentTab({ key: btn.dataset.id, label: label, resume: btn.dataset.sid });
      });
    });
    hist.querySelectorAll('button.chat-archive-btn:not([data-bound])').forEach(btn => {
      btn.dataset.bound = '1';
      btn.addEventListener('click', async () => {
        btn.disabled = true;
        try {
          await fetch('/plugins/chat/archive?id=' + encodeURIComponent(btn.dataset.id), { method: 'POST' });
        } catch (_) {}
        refreshHistory();
      });
    });
  }

  // 起動後 /terminal/list をポーリングして SessionID を ChatRecord に紐付ける。
  function trackSessionID(id) {
    let tries = 0;
    const timer = setInterval(async () => {
      tries++;
      if (tries > 20) { clearInterval(timer); return; } // 最大 ~20*1.5s
      try {
        const res = await fetch('/terminal/list');
        if (!res.ok) return;
        const data = await res.json();
        const row = (data.items || []).find(it => it.ID === id);
        if (row && row.SessionID) {
          clearInterval(timer);
          await fetch('/plugins/chat/update?id=' + encodeURIComponent(id) +
            '&session_id=' + encodeURIComponent(row.SessionID), { method: 'POST' });
          refreshHistory();
        }
      } catch (_) {}
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
    window.agentarium.openAgentTab({ key: id, label: label, command: text, autoEnter: true });
    input.value = '';
    trackSessionID(id);
    setTimeout(refreshHistory, 800);
  }

  startBtn.addEventListener('click', startChat);
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); startChat(); }
  });

  refreshHistory();
}
