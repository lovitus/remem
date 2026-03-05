package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"rememguard/internal/guard"
	"rememguard/internal/logbuf"
)

type LogServer struct {
	URL      string
	shutdown func(context.Context) error
}

func StartLogServer(listenAddr string, logs *logbuf.Buffer, monitor *guard.Monitor) (*LogServer, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Now   string         `json:"now"`
			Stats guard.Stats    `json:"stats"`
			Logs  []logbuf.Entry `json:"logs"`
		}{
			Now:   time.Now().Format(time.RFC3339),
			Stats: monitor.Stats(),
			Logs:  logs.Snapshot(),
		})
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	return &LogServer{
		URL: fmt.Sprintf("http://%s", ln.Addr().String()),
		shutdown: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	}, nil
}

func (s *LogServer) Shutdown(ctx context.Context) error {
	if s == nil || s.shutdown == nil {
		return nil
	}
	return s.shutdown(ctx)
}

const indexHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Remem Guard Logs</title>
<style>
  body { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; margin: 16px; background: #0f1115; color: #e8ebf0; }
  .meta { margin-bottom: 12px; color: #9fb0c3; font-size: 13px; }
  .ok { color: #71d17a; }
  .run { color: #f3bf44; }
  #logs { white-space: pre-wrap; line-height: 1.5; background: #171b23; border: 1px solid #2a3240; padding: 12px; border-radius: 8px; min-height: 260px; }
</style>
</head>
<body>
  <div class="meta" id="meta"></div>
  <div id="logs">loading...</div>
<script>
async function refresh() {
  try {
    const res = await fetch('/api/logs', {cache: 'no-store'});
    const data = await res.json();
    const st = data.stats || {};
    const status = st.running ? '<span class="run">running</span>' : '<span class="ok">idle</span>';
    const lastAt = st.lastRunAt ? new Date(st.lastRunAt).toLocaleTimeString() : '-';
    document.getElementById('meta').innerHTML =
      'status: ' + status +
      ' | last: ' + lastAt +
      ' | source: ' + (st.lastSource || '-') +
      ' | procs: ' + (st.lastProcessSeen || 0) +
      ' | killed: ' + (st.lastKilled || 0) +
      ' | duration: ' + (st.lastDurationMs || 0) + 'ms';
    const lines = (data.logs || []).map(l => '[' + l.time + '] ' + l.message);
    document.getElementById('logs').textContent = lines.join('\n');
  } catch (e) {
    document.getElementById('logs').textContent = 'fetch error: ' + e;
  }
}
refresh();
setInterval(refresh, 1000);
</script>
</body>
</html>`
