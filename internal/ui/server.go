package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"remem/internal/config"
	"remem/internal/guard"
	"remem/internal/logbuf"
)

type LogServer struct {
	URL      string
	RulesURL string
	shutdown func(context.Context) error
}

func StartLogServer(listenAddr string, logs *logbuf.Buffer, monitor *guard.Monitor) (*LogServer, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})
	mux.HandleFunc("/rules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(rulesHTML))
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
	mux.HandleFunc("/api/rules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			st := monitor.RuleState()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(st)
			return
		case http.MethodPost:
			defer r.Body.Close()
			var in config.FileConfig
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			in.Commands.Add = normalizeList(in.Commands.Add)
			in.Commands.Remove = normalizeList(in.Commands.Remove)
			in.Groups.Add = normalizeList(in.Groups.Add)
			in.Groups.Remove = normalizeList(in.Groups.Remove)

			if err := monitor.UpdateCustomPatch(in, true); err != nil {
				http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			monitor.TriggerScan("rules_saved")
			st := monitor.RuleState()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(st)
			return
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
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

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())
	return &LogServer{
		URL:      baseURL,
		RulesURL: baseURL + "/rules",
		shutdown: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	}, nil
}

func normalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
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
<title>remem logs</title>
<style>
  body { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; margin: 16px; background: #0f1115; color: #e8ebf0; }
  .meta { margin-bottom: 12px; color: #9fb0c3; font-size: 13px; }
  .ok { color: #71d17a; }
  .run { color: #f3bf44; }
  #logs { white-space: pre-wrap; line-height: 1.5; background: #171b23; border: 1px solid #2a3240; padding: 12px; border-radius: 8px; min-height: 260px; }
  a { color: #8fd3ff; }
</style>
</head>
<body>
  <div class="meta" id="meta"></div>
  <div class="meta"><a href="/rules">Open Rules Editor</a></div>
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

const rulesHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>remem rules editor</title>
<style>
  body { font-family: ui-sans-serif, -apple-system, Segoe UI, Roboto, sans-serif; margin: 16px; background: #10141a; color: #eef3fb; }
  h1 { margin: 0 0 10px 0; font-size: 20px; }
  .muted { color: #9db0c5; font-size: 13px; margin-bottom: 10px; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  textarea { width: 100%; min-height: 150px; resize: vertical; background: #1a2230; color: #eef3fb; border: 1px solid #2e3a4d; border-radius: 8px; padding: 10px; font-family: ui-monospace, Menlo, monospace; }
  button { margin-top: 12px; background: #1f9d67; border: 0; color: #fff; padding: 10px 14px; border-radius: 8px; cursor: pointer; }
  .status { margin-top: 10px; color: #9db0c5; }
  a { color: #8fd3ff; }
</style>
</head>
<body>
  <h1>Rules Editor</h1>
  <div class="muted" id="meta"></div>
  <div class="grid">
    <div><div>Commands Add</div><textarea id="cmdAdd"></textarea></div>
    <div><div>Commands Remove</div><textarea id="cmdRemove"></textarea></div>
    <div><div>Groups Add</div><textarea id="grpAdd"></textarea></div>
    <div><div>Groups Remove</div><textarea id="grpRemove"></textarea></div>
  </div>
  <button id="saveBtn">Save And Apply Now</button>
  <div class="status" id="status"></div>
  <div class="status"><a href="/">Back to logs</a></div>
<script>
function toLines(a){ return (a||[]).join('\n'); }
function fromLines(s){ return s.split(/\r?\n/).map(v=>v.trim()).filter(Boolean); }

async function loadRules() {
  const res = await fetch('/api/rules', {cache:'no-store'});
  const data = await res.json();
  document.getElementById('meta').textContent =
    'config: ' + (data.configPath || '-') +
    ' | watched commands: ' + (data.commandCount || 0) +
    ' | groups: ' + ((data.groupNames||[]).join(', '));
  const p = data.customPatch || {};
  document.getElementById('cmdAdd').value = toLines((p.commands||{}).add);
  document.getElementById('cmdRemove').value = toLines((p.commands||{}).remove);
  document.getElementById('grpAdd').value = toLines((p.groups||{}).add);
  document.getElementById('grpRemove').value = toLines((p.groups||{}).remove);
}

async function saveRules() {
  const payload = {
    commands: {
      add: fromLines(document.getElementById('cmdAdd').value),
      remove: fromLines(document.getElementById('cmdRemove').value)
    },
    groups: {
      add: fromLines(document.getElementById('grpAdd').value),
      remove: fromLines(document.getElementById('grpRemove').value)
    }
  };
  const res = await fetch('/api/rules', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify(payload)
  });
  if (!res.ok) {
    document.getElementById('status').textContent = 'save failed: ' + await res.text();
    return;
  }
  document.getElementById('status').textContent = 'saved and applied at ' + new Date().toLocaleTimeString();
  await loadRules();
}

document.getElementById('saveBtn').addEventListener('click', saveRules);
loadRules().catch(e => {
  document.getElementById('status').textContent = 'load failed: ' + e;
});
</script>
</body>
</html>`
