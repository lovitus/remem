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
    --bg: #0b1017;
    --panel: #151d28;
    --panel2: #101722;
    --line: #2a3749;
    --text: #eaf2fc;
    --muted: #9eb1c9;
    --ok: #2fc764;
    --btn: #1f8b5d;
    --btn2: #5d6a7a;
    --danger: #cd4f4f;
    --focus: #5aa0ff;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: radial-gradient(circle at top right, #152238 0%, #0b1017 45%);
    color: var(--text);
    font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  }
  .page { max-width: 1100px; margin: 18px auto; padding: 0 16px 24px; }
  h1 { margin: 0 0 8px; font-size: 24px; }
  .sub { color: var(--muted); font-size: 13px; }
  .tips {
    margin-top: 10px;
    border: 1px solid #33506f;
    border-radius: 10px;
    background: #122033;
    color: #c8d9ed;
    padding: 10px 12px;
    line-height: 1.6;
  }

  .grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 12px; }
  .card {
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 10px;
    padding: 10px;
  }
  .card h3 { margin: 0 0 8px; font-size: 14px; color: #ddecff; }
  .desc { color: var(--muted); font-size: 12px; margin: 0 0 8px; }
  .list {
    background: var(--panel2);
    border: 1px solid var(--line);
    border-radius: 8px;
    min-height: 110px;
    max-height: 220px;
    overflow: auto;
    padding: 8px;
    white-space: pre-wrap;
    font-family: ui-monospace, Menlo, Consolas, monospace;
    font-size: 12px;
  }

  .rows { display: flex; flex-direction: column; gap: 6px; }
  .row { display: grid; grid-template-columns: 24px 1fr 34px; gap: 6px; align-items: center; }
  .mark { width: 24px; text-align: center; color: #7f93ad; font-weight: 700; user-select: none; }
  .mark.ok { color: var(--ok); }
  .input {
    width: 100%;
    border-radius: 8px;
    border: 1px solid var(--line);
    background: var(--panel2);
    color: var(--text);
    padding: 8px 10px;
    font-size: 13px;
    outline: none;
  }
  .input:focus { border-color: var(--focus); box-shadow: 0 0 0 2px rgba(90, 160, 255, .2); }
  .minus {
    border-radius: 8px;
    border: 1px solid #5a3036;
    background: #2c1a20;
    color: #ffb0b8;
    height: 32px;
    cursor: pointer;
    font-size: 18px;
    line-height: 1;
  }
  .minus:disabled { opacity: .35; cursor: not-allowed; }

  .ops { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 12px; }
  .btn {
    border: 0;
    border-radius: 9px;
    color: #fff;
    padding: 9px 13px;
    cursor: pointer;
    font-size: 13px;
  }
  .save { background: var(--btn); }
  .reload { background: var(--btn2); }
  .restore { background: var(--danger); }
  .state { margin-top: 10px; color: var(--muted); font-size: 13px; line-height: 1.5; }
  .oktext { color: var(--ok); }
  a { color: #8fd3ff; }

  @media (max-width: 900px) {
    .grid-2 { grid-template-columns: 1fr; }
  }
</style>
</head>
<body>
<div class="page">
  <h1>Rules Editor</h1>
  <div class="sub" id="meta"></div>
  <div class="tips">
    直接编辑“最终生效规则”，不需要理解 Add/Remove。<br/>
    每行右侧可点 <b>-</b> 删除；末尾永远有一个空行；输入任何内容会显示绿色 <b>✓</b>。<br/>
    点击“保存并立即生效”会立刻热加载规则。
  </div>

  <div class="grid-2">
    <div class="card">
      <h3>当前生效: 命令规则</h3>
      <div class="list" id="effectiveCommands"></div>
    </div>
    <div class="card">
      <h3>当前生效: 程序组规则</h3>
      <div class="list" id="effectiveGroups"></div>
    </div>
  </div>

  <div class="grid-2">
    <div class="card">
      <h3>编辑最终生效命令规则</h3>
      <p class="desc">示例: codex, vim, nano, grep, less, more</p>
      <div class="rows" id="cmdRows"></div>
    </div>
    <div class="card">
      <h3>编辑最终生效程序组规则</h3>
      <p class="desc">示例: codex, windsurf, vscode, chrome, firefox, edge, safari</p>
      <div class="rows" id="grpRows"></div>
    </div>
  </div>

  <div class="grid-2">
    <div class="card">
      <h3>默认命令规则</h3>
      <div class="list" id="defaultCommands"></div>
    </div>
    <div class="card">
      <h3>默认程序组规则</h3>
      <div class="list" id="defaultGroups"></div>
    </div>
  </div>

  <div class="ops">
    <button class="btn save" id="saveBtn">保存并立即生效</button>
    <button class="btn reload" id="reloadBtn">重新加载当前生效内容</button>
    <button class="btn restore" id="restoreBtn">恢复默认并生效</button>
  </div>
  <div class="state" id="status"></div>
  <div class="state"><a href="/">Back to logs</a></div>
</div>

<script>
const state = {
  defaults: { commands: [], groups: [] },
  effective: { commands: [], groups: [] },
  draft: { commands: [''], groups: [''] },
};

function normalizeList(arr) {
  const out = [];
  const seen = new Set();
  for (const item of arr || []) {
    const v = (item || '').trim();
    if (!v) continue;
    const k = v.toLowerCase();
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(v);
  }
  return out;
}

function ensureTrailingEmpty(key) {
  let rows = (state.draft[key] || []).map(v => v || '');
  while (rows.length > 1 && rows[rows.length - 1].trim() === '' && rows[rows.length - 2].trim() === '') {
    rows.pop();
  }
  if (rows.length === 0 || rows[rows.length - 1].trim() !== '') {
    rows.push('');
  }
  state.draft[key] = rows;
}

function renderRows(key, containerId, placeholder) {
  ensureTrailingEmpty(key);
  const wrap = document.getElementById(containerId);
  wrap.innerHTML = '';
  const rows = state.draft[key];

  rows.forEach((val, idx) => {
    const row = document.createElement('div');
    row.className = 'row';

    const mark = document.createElement('div');
    mark.className = 'mark' + (val.trim() ? ' ok' : '');
    mark.textContent = val.trim() ? '✓' : '○';

    const input = document.createElement('input');
    input.className = 'input';
    input.type = 'text';
    input.value = val;
    input.placeholder = placeholder;
    input.addEventListener('input', () => {
      state.draft[key][idx] = input.value;
      if (idx === state.draft[key].length - 1 && input.value.trim() !== '') {
        state.draft[key].push('');
        renderEditors();
        return;
      }
      mark.className = 'mark' + (input.value.trim() ? ' ok' : '');
      mark.textContent = input.value.trim() ? '✓' : '○';
    });

    const minus = document.createElement('button');
    minus.className = 'minus';
    minus.textContent = '−';
    minus.disabled = rows.length <= 1;
    minus.addEventListener('click', () => {
      state.draft[key].splice(idx, 1);
      renderEditors();
    });

    row.appendChild(mark);
    row.appendChild(input);
    row.appendChild(minus);
    wrap.appendChild(row);
  });
}

function renderEditors() {
  renderRows('commands', 'cmdRows', '输入命令名');
  renderRows('groups', 'grpRows', '输入程序组名');
}

function renderLists() {
  document.getElementById('effectiveCommands').textContent = (state.effective.commands || []).join('\n');
  document.getElementById('effectiveGroups').textContent = (state.effective.groups || []).join('\n');
  document.getElementById('defaultCommands').textContent = (state.defaults.commands || []).join('\n');
  document.getElementById('defaultGroups').textContent = (state.defaults.groups || []).join('\n');
}

function setFromRuleState(data) {
  state.defaults.commands = normalizeList(data.defaultCommands || []);
  state.defaults.groups = normalizeList(data.defaultGroups || []);
  state.effective.commands = normalizeList(data.effectiveCommands || []);
  state.effective.groups = normalizeList(data.effectiveGroups || []);
  state.draft.commands = state.effective.commands.slice();
  state.draft.groups = state.effective.groups.slice();
  renderLists();
  renderEditors();
  document.getElementById('meta').textContent = '规则文件: ' + (data.configPath || '(not set)');
}

function toLowerSet(arr) {
  return new Set((arr || []).map(v => v.toLowerCase()));
}

function computePatchFromDraft() {
  const targetCommands = normalizeList(state.draft.commands);
  const targetGroups = normalizeList(state.draft.groups);
  const defaultCommands = state.defaults.commands;
  const defaultGroups = state.defaults.groups;

  const targetCommandSet = toLowerSet(targetCommands);
  const targetGroupSet = toLowerSet(targetGroups);
  const defaultCommandSet = toLowerSet(defaultCommands);
  const defaultGroupSet = toLowerSet(defaultGroups);

  return {
    commands: {
      add: targetCommands.filter(v => !defaultCommandSet.has(v.toLowerCase())),
      remove: defaultCommands.filter(v => !targetCommandSet.has(v.toLowerCase())),
    },
    groups: {
      add: targetGroups.filter(v => !defaultGroupSet.has(v.toLowerCase())),
      remove: defaultGroups.filter(v => !targetGroupSet.has(v.toLowerCase())),
    },
  };
}

async function loadRules() {
  const res = await fetch('/api/rules', { cache: 'no-store' });
  if (!res.ok) {
    throw new Error(await res.text());
  }
  const data = await res.json();
  setFromRuleState(data);
}

async function savePatch(patch, okText) {
  const res = await fetch('/api/rules', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  if (!res.ok) {
    document.getElementById('status').textContent = '保存失败: ' + await res.text();
    return;
  }
  const data = await res.json();
  setFromRuleState(data);
  document.getElementById('status').innerHTML =
    '<span class="oktext">' + okText + '</span>，已热加载生效: ' + new Date().toLocaleTimeString();
}

document.getElementById('saveBtn').addEventListener('click', async () => {
  await savePatch(computePatchFromDraft(), '规则已保存');
});

document.getElementById('reloadBtn').addEventListener('click', async () => {
  await loadRules();
  document.getElementById('status').innerHTML =
    '<span class="oktext">已重新加载当前生效规则</span>: ' + new Date().toLocaleTimeString();
});

document.getElementById('restoreBtn').addEventListener('click', async () => {
  const patch = { commands: { add: [], remove: [] }, groups: { add: [], remove: [] } };
  await savePatch(patch, '已恢复默认规则');
});

loadRules().catch((e) => {
  document.getElementById('status').textContent = '加载失败: ' + e;
});
</script>
</body>
</html>`
