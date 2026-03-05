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
      <div class="title">Important Logs (action/error/kill, rolling 100 lines)</div>
      <div class="logs" id="imp">loading...</div>
    </div>
    <div class="card">
      <div class="title">Routine Scan Logs (rolling 10 lines)</div>
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
  :root {
    --bg: #0d1117;
    --panel: #161b22;
    --panel2: #11161d;
    --line: #2a3442;
    --text: #e6edf3;
    --muted: #9baec4;
    --ok: #30c262;
    --warn: #ffcc66;
    --btn: #1f9d67;
    --btn2: #6b7280;
    --danger: #d9534f;
  }
  body { margin:0; background:var(--bg); color:var(--text); font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; }
  .page { max-width: 1100px; margin: 18px auto; padding: 0 14px 24px; }
  h1 { font-size: 22px; margin: 0 0 10px; }
  .muted { color: var(--muted); font-size: 13px; }
  .hint { margin-top:10px; background:#132033; border:1px solid #2f4b69; border-radius:10px; padding:10px 12px; line-height:1.5; }
  .grid2 { display:grid; grid-template-columns: 1fr 1fr; gap:12px; margin-top:12px; }
  .card { background:var(--panel); border:1px solid var(--line); border-radius:10px; padding:10px; }
  .card h3 { margin:0 0 8px; font-size:14px; }
  .list { background:var(--panel2); border:1px solid var(--line); border-radius:8px; padding:8px; font-family: ui-monospace, Menlo, Consolas, monospace; font-size:12px; min-height:120px; max-height:220px; overflow:auto; white-space:pre-wrap; }
  .editor { margin-top:12px; }
  .editor h2 { margin: 0 0 8px; font-size:16px; }
  .rows { display:flex; flex-direction:column; gap:6px; }
  .row { display:grid; grid-template-columns: 26px 1fr 34px; gap:6px; align-items:center; }
  .status-dot { width:22px; text-align:center; color:#7f8ea3; font-weight:600; }
  .status-dot.ok { color: var(--ok); }
  input[type=text] { width:100%; box-sizing:border-box; background:var(--panel2); border:1px solid var(--line); border-radius:8px; color:var(--text); padding:8px 10px; font-size:13px; }
  .minus { height:32px; border-radius:8px; border:1px solid #54343d; background:#2c1d22; color:#ffb3ba; cursor:pointer; }
  .minus:disabled { opacity:0.35; cursor:not-allowed; }
  .ops { margin-top:12px; display:flex; gap:8px; flex-wrap:wrap; }
  .btn { border:0; border-radius:9px; padding:9px 14px; color:#fff; cursor:pointer; }
  .save { background: var(--btn); }
  .reset { background: var(--btn2); }
  .restore { background: var(--danger); }
  .state { margin-top:10px; color:var(--muted); font-size:13px; }
  a { color:#8fd3ff; }
  @media (max-width: 900px) { .grid2 { grid-template-columns: 1fr; } }
</style>
</head>
<body>
<div class="page">
  <h1>Rules Editor</h1>
  <div class="muted" id="meta"></div>
  <div class="hint">
    操作方式：在每个列表里直接输入；有内容时左侧会显示绿色 ✓；末尾永远保留一个空行。<br/>
    点 <b>Save And Apply Now</b> 立即热加载生效。<br/>
    点 <b>恢复默认</b> 会清空所有自定义增删补丁，只保留默认规则。
  </div>

  <div class="grid2">
    <div class="card"><h3>Default Commands</h3><div class="list" id="defaultCommands"></div></div>
    <div class="card"><h3>Effective Commands (当前生效)</h3><div class="list" id="effectiveCommands"></div></div>
    <div class="card"><h3>Default Groups</h3><div class="list" id="defaultGroups"></div></div>
    <div class="card"><h3>Effective Groups (当前生效)</h3><div class="list" id="effectiveGroups"></div></div>
  </div>

  <div class="grid2 editor">
    <div class="card">
      <h2>Commands Add</h2>
      <div class="rows" id="cmdAddRows"></div>
    </div>
    <div class="card">
      <h2>Commands Remove</h2>
      <div class="rows" id="cmdRemoveRows"></div>
    </div>
    <div class="card">
      <h2>Groups Add</h2>
      <div class="rows" id="grpAddRows"></div>
    </div>
    <div class="card">
      <h2>Groups Remove</h2>
      <div class="rows" id="grpRemoveRows"></div>
    </div>
  </div>

  <div class="ops">
    <button class="btn save" id="saveBtn">Save And Apply Now</button>
    <button class="btn reset" id="clearBtn">清空自定义补丁</button>
    <button class="btn restore" id="restoreBtn">恢复默认</button>
  </div>
  <div class="state" id="status"></div>
  <div class="state"><a href="/">Back to logs</a></div>
</div>

<script>
function uniq(arr){ const s=new Set(); const out=[]; for(const x of arr){ const v=(x||'').trim(); if(!v) continue; if(!s.has(v)){s.add(v); out.push(v);} } return out; }
function listText(arr){ return (arr||[]).join('\n'); }

const editors = {
  cmdAdd: [], cmdRemove: [], grpAdd: [], grpRemove: []
};

function ensureTrailingEmpty(key) {
  let vals = editors[key] || [];
  vals = vals.map(v => (v||'').trim());
  while (vals.length > 1 && vals[vals.length-1] === '' && vals[vals.length-2] === '') vals.pop();
  if (vals.length === 0 || vals[vals.length-1] !== '') vals.push('');
  editors[key] = vals;
}

function renderRows(key, containerId) {
  ensureTrailingEmpty(key);
  const container = document.getElementById(containerId);
  container.innerHTML = '';
  const vals = editors[key];
  vals.forEach((val, idx) => {
    const row = document.createElement('div');
    row.className = 'row';

    const mark = document.createElement('div');
    mark.className = 'status-dot' + (val.trim() ? ' ok' : '');
    mark.textContent = val.trim() ? '✓' : '○';

    const input = document.createElement('input');
    input.type = 'text';
    input.value = val;
    input.placeholder = '输入名称';
    input.oninput = () => {
      editors[key][idx] = input.value;
      renderAllEditors();
    };

    const minus = document.createElement('button');
    minus.className = 'minus';
    minus.textContent = '−';
    minus.disabled = vals.length <= 1;
    minus.onclick = () => {
      editors[key].splice(idx, 1);
      renderAllEditors();
    };

    row.appendChild(mark);
    row.appendChild(input);
    row.appendChild(minus);
    container.appendChild(row);
  });
}

function renderAllEditors() {
  renderRows('cmdAdd', 'cmdAddRows');
  renderRows('cmdRemove', 'cmdRemoveRows');
  renderRows('grpAdd', 'grpAddRows');
  renderRows('grpRemove', 'grpRemoveRows');
}

function currentPatchPayload() {
  return {
    commands: {
      add: uniq(editors.cmdAdd),
      remove: uniq(editors.cmdRemove),
    },
    groups: {
      add: uniq(editors.grpAdd),
      remove: uniq(editors.grpRemove),
    },
  };
}

function applyPatchToEditors(p) {
  editors.cmdAdd = ((p.commands||{}).add || []).slice();
  editors.cmdRemove = ((p.commands||{}).remove || []).slice();
  editors.grpAdd = ((p.groups||{}).add || []).slice();
  editors.grpRemove = ((p.groups||{}).remove || []).slice();
  renderAllEditors();
}

async function loadRules() {
  const res = await fetch('/api/rules', {cache:'no-store'});
  const data = await res.json();

  const cfgPath = data.configPath || '(not set)';
  document.getElementById('meta').textContent = 'config file: ' + cfgPath;

  document.getElementById('defaultCommands').textContent = listText(data.defaultCommands || []);
  document.getElementById('effectiveCommands').textContent = listText(data.effectiveCommands || []);
  document.getElementById('defaultGroups').textContent = listText(data.defaultGroups || []);
  document.getElementById('effectiveGroups').textContent = listText(data.effectiveGroups || []);

  applyPatchToEditors(data.customPatch || {commands:{add:[],remove:[]},groups:{add:[],remove:[]}});
}

async function savePatch(patch, label) {
  const res = await fetch('/api/rules', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify(patch)
  });
  if (!res.ok) {
    document.getElementById('status').textContent = '保存失败: ' + await res.text();
    return;
  }
  document.getElementById('status').textContent = label + '，已立即生效: ' + new Date().toLocaleTimeString();
  await loadRules();
}

document.getElementById('saveBtn').onclick = async () => {
  await savePatch(currentPatchPayload(), '规则已保存');
};

document.getElementById('clearBtn').onclick = async () => {
  await savePatch({commands:{add:[],remove:[]}, groups:{add:[],remove:[]}}, '自定义补丁已清空');
};

document.getElementById('restoreBtn').onclick = async () => {
  await savePatch({commands:{add:[],remove:[]}, groups:{add:[],remove:[]}}, '已恢复默认规则');
};

loadRules().catch(e => {
  document.getElementById('status').textContent = '加载失败: ' + e;
});
</script>
</body>
</html>`
