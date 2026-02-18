package main

import "net/http"

const rootHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>대858기</title>
</head>
<body>
  <main>
    <h1>대858기</h1>

    <section>
      <h2>D-day</h2>
      <p id="dday">Loading...</p>
    </section>

    <section>
      <h2>Status</h2>

      <h3>Pending charges</h3>
      <p id="pending-charges">Loading...</p>

      <h3>Instance</h3>
      <dl>
        <dt>Status</dt>
        <dd id="instance-status">Loading...</dd>
        <dt>Label</dt>
        <dd id="instance-label">Loading...</dd>
        <dt>SSH</dt>
        <dd id="instance-ssh">Loading...</dd>
      </dl>
    </section>

    <section>
      <h2>Links</h2>
      <ul>
        <li><a href="https://arena.ai/?mode=direct">모든 AI모델을 무료로</a></li>
        <li><a href="https://chromewebstore.google.com/detail/ublock-origin/cjpalhdlnbpafiamejdnhcphjbkeiagm">광고 모두 제거</a></li>
        <li><a href="https://reddit.com/r/Piracy/w/megathread/movies_and_tv">영화/드라마 다운로드 모음</a></li>
        <li><a href="/static/sjb.tar.gz">Bootstrap</a></li>
      </ul>
    </section>

    <section>
      <h2>Commands</h2>
      <pre><code>curl -LsSf https://astral.sh/uv/install.sh | sh</code></pre>
      <pre><code>sudo npm i -g @openai/codex</code></pre>
      <pre><code>codex --dangerously-bypass-approvals-and-sandbox --search</code></pre>
    </section>
  </main>

  <script>
    (function () {
      var ddayEl = document.getElementById('dday');
      var target = new Date('2026-02-26T00:00:00');

      function renderDday() {
        var now = new Date();
        var diffMs = target.getTime() - now.getTime();
        if (isNaN(diffMs)) {
          ddayEl.textContent = 'Invalid date';
          return;
        }

        if (diffMs <= 0) {
          ddayEl.textContent = 'D-Day';
          return;
        }

        var days = Math.ceil(diffMs / 86400000);
        ddayEl.textContent = 'D-' + days;
      }

      renderDday();

      function renderCharges(data) {
        var el = document.getElementById('pending-charges');
        if (data && typeof data.pending_charges === 'number') {
          el.textContent = data.pending_charges.toFixed(2);
        } else {
          el.textContent = 'Unavailable';
        }
      }

      function renderInstance(data) {
        var statusEl = document.getElementById('instance-status');
        var labelEl = document.getElementById('instance-label');
        var sshEl = document.getElementById('instance-ssh');

        if (!data || !data.status || !data.ip) {
          statusEl.textContent = 'Unavailable';
          labelEl.textContent = data && data.label ? data.label : 'Unavailable';
          sshEl.textContent = 'Unavailable';
          return;
        }

        statusEl.textContent = data.status;
        labelEl.textContent = data.label || 'Unavailable';
        sshEl.textContent = 'ssh -p 443 linuxuser@' + data.ip;
      }

      fetch('/api/charges')
        .then(function (resp) { return resp.ok ? resp.json() : Promise.reject(resp); })
        .then(renderCharges)
        .catch(function () { renderCharges(null); });

      fetch('/api/instance')
        .then(function (resp) { return resp.ok ? resp.json() : Promise.reject(resp); })
        .then(renderInstance)
        .catch(function () { renderInstance(null); });
    })();
  </script>
</body>
</html>
`

func (a *app) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(rootHTML))
}
