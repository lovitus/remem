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
			Now   string          `json:"now"`
			Stats guard.Stats     `json:"stats"`
			Logs  logbuf.Snapshot `json:"logs"`
		}{
			Now:   time.Now().Format(time.RFC3339),
			Stats: monitor.Stats(),
			Logs:  logs.SnapshotByCategory(),
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
  .wrap { display: grid; grid-template-columns: 1fr; gap: 12px; }
  .card { background: #171b23; border: 1px solid #2a3240; padding: 10px; border-radius: 8px; }
  .title { color: #bcd0e6; font-size: 13px; margin-bottom: 8px; }
  .logs { white-space: pre-wrap; line-height: 1.45; min-height: 120px; max-height: 260px; overflow: auto; }
  a { color: #8fd3ff; }
</style>
</head>
<body>
  <div class="meta" id="meta"></div>
  <div class="meta"><a href="/rules">Open Rules Editor</a></div>
  <div class="wrap">
    <div class="card">
      <div class="title">Important Logs (action/error/kill, persistent in memory)</div>
      <div class="logs" id="imp">loading...</div>
    </div>
    <div class="card">
      <div class="title">Routine Scan Logs (rolling 100 lines)</div>
      <div class="logs" id="rt">loading...</div>
    </div>
  </div>
<script>
function fmt(lines){ return (lines||[]).map(l => '[' + l.time + '] [' + l.kind + '] ' + l.message).join('\n'); }

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
    document.getElementById('imp').textContent = fmt((data.logs||{}).important);
    document.getElementById('rt').textContent = fmt((data.logs||{}).routine);
  } catch (e) {
    document.getElementById('imp').textContent = 'fetch error: ' + e;
    document.getElementById('rt').textContent = '';
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
  h1 { margin: 0 0 8px 0; font-size: 20px; }
  .muted { color: #9db0c5; font-size: 13px; margin: 6px 0; }
  .help { background: #162030; border: 1px solid #29425d; border-radius: 8px; padding: 10px; margin: 10px 0; font-size: 13px; color: #c6d7ea; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  .card { background: #1a2230; border: 1px solid #2e3a4d; border-radius: 8px; padding: 10px; }
  .card h3 { margin: 0 0 8px 0; font-size: 14px; color: #d8e6f7; }
  textarea { width: 100%; min-height: 120px; resize: vertical; background: #131a25; color: #eef3fb; border: 1px solid #2e3a4d; border-radius: 8px; padding: 8px; font-family: ui-monospace, Menlo, monospace; }
  .list { background: #131a25; border: 1px solid #2e3a4d; border-radius: 8px; padding: 8px; min-height: 100px; max-height: 200px; overflow: auto; font-family: ui-monospace, Menlo, monospace; font-size: 12px; white-space: pre-wrap; }
  button { margin-top: 10px; margin-right: 8px; background: #1f9d67; border: 0; color: #fff; padding: 9px 12px; border-radius: 8px; cursor: pointer; }
  .secondary { background: #6b7280; }
  .status { margin-top: 10px; color: #9db0c5; }
  a { color: #8fd3ff; }
</style>
</head>
<body>
  <h1>Rules Editor</h1>
  <div class="muted" id="meta"></div>
  <div class="help">
    你只需要维护“自定义增删补丁”。默认规则始终存在。<br/>
    1) 在 Add 里写要新增的命令/程序名（每行一个）<br/>
    2) 在 Remove 里写要从默认规则里移除的名称（每行一个）<br/>
    3) 点击 Save And Apply Now，立即生效
  </div>

  <div class="grid">
    <div class="card">
      <h3>Default Commands</h3>
      <div class="list" id="defaultCommands"></div>
    </div>
    <div class="card">
      <h3>Effective Commands</h3>
      <div class="list" id="effectiveCommands"></div>
    </div>
    <div class="card">
      <h3>Default Groups</h3>
      <div class="list" id="defaultGroups"></div>
    </div>
    <div class="card">
      <h3>Effective Groups</h3>
      <div class="list" id="effectiveGroups"></div>
    </div>
  </div>

  <div class="grid" style="margin-top:12px;">
    <div class="card"><h3>Custom: Commands Add</h3><textarea id="cmdAdd" placeholder="例如:\nbun\ndeno"></textarea></div>
    <div class="card"><h3>Custom: Commands Remove</h3><textarea id="cmdRemove" placeholder="例如:\nless"></textarea></div>
    <div class="card"><h3>Custom: Groups Add</h3><textarea id="grpAdd" placeholder="例如:\nbrave\nopera"></textarea></div>
    <div class="card"><h3>Custom: Groups Remove</h3><textarea id="grpRemove" placeholder="例如:\nsafari"></textarea></div>
  </div>

  <button id="saveBtn">Save And Apply Now</button>
  <button class="secondary" id="resetBtn">Clear Custom Patch</button>
  <div class="status" id="status"></div>
  <div class="status"><a href="/">Back to logs</a></div>

<script>
function toLines(a){ return (a||[]).join('\n'); }
function fromLines(s){ return s.split(/\r?\n/).map(v=>v.trim()).filter(Boolean); }
function showList(id, arr){ document.getElementById(id).textContent = (arr||[]).join('\n'); }

let lastState = null;

async function loadRules() {
  const res = await fetch('/api/rules', {cache:'no-store'});
  const data = await res.json();
  lastState = data;
  const cfgPath = data.configPath || '(not set)';
  document.getElementById('meta').textContent = 'config file: ' + cfgPath;

  showList('defaultCommands', data.defaultCommands || []);
  showList('effectiveCommands', data.effectiveCommands || []);
  showList('defaultGroups', data.defaultGroups || []);
  showList('effectiveGroups', data.effectiveGroups || []);

  const p = data.customPatch || {};
  document.getElementById('cmdAdd').value = toLines((p.commands||{}).add);
  document.getElementById('cmdRemove').value = toLines((p.commands||{}).remove);
  document.getElementById('grpAdd').value = toLines((p.groups||{}).add);
  document.getElementById('grpRemove').value = toLines((p.groups||{}).remove);
}

async function saveRules(payload) {
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

document.getElementById('saveBtn').addEventListener('click', async () => {
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
  await saveRules(payload);
});

document.getElementById('resetBtn').addEventListener('click', async () => {
  await saveRules({commands:{add:[],remove:[]}, groups:{add:[],remove:[]}});
});

loadRules().catch(e => {
  document.getElementById('status').textContent = 'load failed: ' + e;
});
</script>
</body>
</html>`
