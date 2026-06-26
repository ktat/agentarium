// Slack タブ: 接続状態の表示 + 「Slack 連携」ボタン + 接続済み workspace 一覧。
// シェルは /plugins/slack/assets/index.js を動的 import し render(root) を呼ぶ。
function esc(s) {
  return String(s).replace(/[<>&"']/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;', "'": '&#39;' }[c]));
}

export async function render(root) {
  root.innerHTML =
    '<div class="slack-panel">' +
    '<p><a id="slackConnect" href="/plugins/slack/start" target="_blank" rel="noopener">Slack 連携</a></p>' +
    '<div id="slackStatus"></div>' +
    '<div id="slackList"></div>' +
    '</div>';

  const status = root.querySelector('#slackStatus');
  const list = root.querySelector('#slackList');

  async function refresh() {
    try {
      const res = await fetch('/plugins/slack/tokens');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const data = await res.json();
      if (!data.configured) {
        status.innerHTML = '<p class="empty-section">SLACK_CLIENT_ID / SLACK_CLIENT_SECRET を Settings タブで設定してください。</p>';
      } else {
        status.innerHTML = '';
      }
      const wss = data.workspaces || [];
      if (!wss.length) {
        list.innerHTML = '<p class="empty-section">接続済み workspace なし</p>';
        return;
      }
      const rows = wss.map(w => {
        const at = (w.obtained_at || '').replace('T', ' ').substring(0, 19);
        return '<tr><td>' + esc(w.team_name) + '</td><td>' + esc(w.workspace_id) + '</td>' +
          '<td>' + esc(w.user_id) + '</td><td>' + esc(at) + '</td></tr>';
      }).join('');
      list.innerHTML = '<table class="slack-table"><thead><tr><th>Team</th><th>Workspace</th><th>User</th><th>取得日時</th></tr></thead><tbody>' + rows + '</tbody></table>';
    } catch (e) {
      status.innerHTML = '<p class="empty-section">読み込み失敗: ' + esc(e.message) + '</p>';
    }
  }

  root.querySelector('#slackConnect').addEventListener('click', () => {
    // 認証完了後に手動リロードする運用。連携クリック後の復帰時にも更新できるよう少し待って再取得。
    setTimeout(refresh, 3000);
  });

  await refresh();
}
