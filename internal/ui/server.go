package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
			in = config.NormalizeFileConfig(in)

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
    --panel: #141d29;
    --panel2: #0f1722;
    --line: #2b3a4f;
    --text: #ebf2fc;
    --muted: #9bb0ca;
    --ok: #36c66b;
    --btn: #1f8e5e;
    --btn2: #5d6e82;
    --danger: #ce4f4f;
    --focus: #58a0ff;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: radial-gradient(circle at top right, #1a2a44 0%, #0b1017 46%);
    color: var(--text);
    font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  }
  .page { max-width: 1160px; margin: 18px auto; padding: 0 16px 24px; }
  .topbar {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 12px;
    align-items: start;
  }
  .head-left { min-width: 0; }
  h1 { margin: 0 0 8px; font-size: 24px; letter-spacing: .2px; }
  .sub { color: var(--muted); font-size: 13px; }
  .tips {
    margin-top: 10px;
    border: 1px solid #33506f;
    border-radius: 10px;
    background: #122033;
    color: #c7d8ec;
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
  .card h3 { margin: 0 0 8px; font-size: 14px; color: #ddecff; letter-spacing: .2px; }
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

  .limits { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
  .limit-row {
    display: grid;
    grid-template-columns: 1fr 130px;
    gap: 8px;
    align-items: center;
    background: var(--panel2);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 8px;
  }

  .token-box {
    background: var(--panel2);
    border: 1px solid var(--line);
    border-radius: 10px;
    min-height: 220px;
    padding: 8px;
    display: flex;
    flex-direction: column;
    gap: 7px;
  }
  .token-row {
    display: grid;
    grid-template-columns: 1fr 110px 34px;
    gap: 8px;
    align-items: center;
  }
  .token-row.add {
    border-top: 1px dashed #3b4d66;
    padding-top: 9px;
    margin-top: 2px;
  }

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
  .input:focus { border-color: var(--focus); box-shadow: 0 0 0 2px rgba(88, 160, 255, .2); }
  .num {
    text-align: right;
    font-family: ui-monospace, Menlo, Consolas, monospace;
  }

  .xbtn, .plus {
    border-radius: 8px;
    border: 1px solid #5a3036;
    background: #2c1a20;
    color: #ffb1b9;
    height: 32px;
    cursor: pointer;
    font-size: 18px;
    line-height: 1;
  }
  .plus {
    border-color: #255b41;
    background: #173324;
    color: #98f0bf;
  }
  .xbtn:disabled { opacity: .35; cursor: not-allowed; }

  .ops {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    justify-content: flex-end;
    position: fixed;
    top: 12px;
    right: max(16px, calc((100vw - 1160px) / 2 + 16px));
    z-index: 60;
    background: linear-gradient(180deg, rgba(16,23,34,.95), rgba(16,23,34,.78));
    border: 1px solid #2d4160;
    border-radius: 10px;
    padding: 8px;
    max-width: 460px;
    box-shadow: 0 8px 22px rgba(0, 0, 0, .35);
  }
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
  .warn { color: #ffca66; }
  a { color: #8fd3ff; }

  @media (max-width: 900px) {
    .topbar { grid-template-columns: 1fr; }
    .ops {
      position: static;
      justify-content: flex-start;
      max-width: none;
    }
    .grid-2 { grid-template-columns: 1fr; }
    .limits { grid-template-columns: 1fr; }
    .token-row { grid-template-columns: 1fr 92px 32px; }
  }
</style>
</head>
<body>
<div class="page">
  <div class="topbar">
    <div class="head-left">
      <h1>Rules Editor</h1>
      <div class="sub" id="meta"></div>
    </div>
    <div class="ops">
      <button class="btn save" id="saveBtn">保存并立即生效</button>
      <button class="btn reload" id="reloadBtn">重新加载当前生效内容</button>
      <button class="btn restore" id="restoreBtn">恢复默认并生效</button>
    </div>
  </div>

  <div class="tips">
    支持两层内存限制：<b>全局上限</b> + <b>单项自定义上限</b>。<br/>
    命令规则和程序组规则都在一个大框内编辑：每项后面 <b>x</b> 删除；末尾空项后面 <b>+</b> 添加。<br/>
    保存后立刻热加载生效。
  </div>

  <div class="grid-2">
    <div class="card">
      <h3>当前生效: 命令规则（含限制）</h3>
      <div class="list" id="effectiveCommands"></div>
    </div>
    <div class="card">
      <h3>当前生效: 程序组规则（含限制）</h3>
      <div class="list" id="effectiveGroups"></div>
    </div>
  </div>

  <div class="card" style="margin-top:12px">
    <h3>全局上限（GiB）</h3>
    <div class="limits">
      <div class="limit-row">
        <div>命令规则全局上限</div>
        <input class="input num" id="globalCommandLimit" type="number" min="0.1" step="0.1" />
      </div>
      <div class="limit-row">
        <div>程序组规则全局上限</div>
        <input class="input num" id="globalGroupLimit" type="number" min="0.1" step="0.1" />
      </div>
    </div>
    <div class="desc" id="baseLimitsHint" style="margin-top:8px;"></div>
  </div>

  <div class="grid-2">
    <div class="card">
      <h3>命令规则编辑（名称 + 单项上限）</h3>
      <p class="desc">示例: sed / vim / grep。单项上限留空时使用全局上限。</p>
      <div class="token-box" id="cmdBox"></div>
    </div>
    <div class="card">
      <h3>程序组规则编辑（名称 + 单项上限）</h3>
      <p class="desc">示例: codex / chrome / firefox。单项上限留空时使用全局上限。</p>
      <div class="token-box" id="grpBox"></div>
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

  <div class="state" id="status"></div>
  <div class="state"><a href="/">Back to logs</a></div>
</div>

<script>
const EPS = 0.0001;
const state = {
  baseLimits: { command: 2, group: 6 },
  activeLimits: { command: 2, group: 6 },
  globalLimits: { command: 2, group: 6 },
  defaults: { commands: [], groups: [] },
  effective: { commands: [], groups: [] },
  effectiveLimits: { commands: {}, groups: {} },
  draft: { commands: [], groups: [] },
};

function normalizeName(v) {
  return (v || '').trim().toLowerCase().replace(/\.exe$/, '');
}

function parsePositive(v) {
  const n = Number(v);
  if (!Number.isFinite(n) || n <= 0) return 0;
  return n;
}

function roundGiB(v) {
  return Math.round(v * 1000) / 1000;
}

function fmtGiB(v) {
  const n = parsePositive(v);
  return (Math.round(n * 100) / 100).toFixed(2);
}

function normalizeNameList(arr) {
  const out = [];
  const seen = new Set();
  for (const item of arr || []) {
    const v = normalizeName(item);
    if (!v) continue;
    if (seen.has(v)) continue;
    seen.add(v);
    out.push(v);
  }
  return out;
}

function normalizeItems(items, globalLimit) {
  const out = [];
  const seen = new Set();
  for (const it of items || []) {
    const name = normalizeName((it || {}).name || '');
    if (!name || seen.has(name)) continue;
    seen.add(name);
    const isCustom = !!(it && it.custom);
    const parsed = parsePositive((it || {}).limit);
    const limit = isCustom ? (parsed || globalLimit) : globalLimit;
    out.push({ name, limit: roundGiB(limit), custom: isCustom });
  }
  return out;
}

function itemsFromNames(names, overrideMap, globalLimit) {
  const out = [];
  for (const n of normalizeNameList(names || [])) {
    const ov = parsePositive((overrideMap || {})[n]);
    out.push({ name: n, limit: ov > 0 ? ov : globalLimit, custom: ov > 0 });
  }
  return out;
}

function byNameMap(items) {
  const m = {};
  for (const it of items || []) {
    m[it.name] = it.limit;
  }
  return m;
}

function renderBox(kind) {
  const box = document.getElementById(kind === 'commands' ? 'cmdBox' : 'grpBox');
  const items = state.draft[kind];
  const globalLimit = kind === 'commands' ? state.globalLimits.command : state.globalLimits.group;
  const namePlaceholder = kind === 'commands' ? '输入命令名' : '输入程序组名';
  box.innerHTML = '';

  items.forEach((it, idx) => {
    const row = document.createElement('div');
    row.className = 'token-row';

    const name = document.createElement('input');
    name.className = 'input';
    name.type = 'text';
    name.value = it.name;
    name.placeholder = namePlaceholder;
    name.addEventListener('input', () => {
      state.draft[kind][idx].name = name.value;
    });

    const limit = document.createElement('input');
    limit.className = 'input num';
    limit.type = 'number';
    limit.min = '0.1';
    limit.step = '0.1';
    limit.value = it.custom ? fmtGiB(it.limit || globalLimit) : '';
    limit.placeholder = fmtGiB(globalLimit);
    limit.title = 'GiB';
    limit.addEventListener('input', () => {
      const n = parsePositive(limit.value);
      state.draft[kind][idx].custom = n > 0;
      state.draft[kind][idx].limit = n > 0 ? n : globalLimit;
    });

    const x = document.createElement('button');
    x.className = 'xbtn';
    x.textContent = 'x';
    x.title = '删除';
    x.addEventListener('click', () => {
      state.draft[kind].splice(idx, 1);
      renderEditors();
    });

    row.appendChild(name);
    row.appendChild(limit);
    row.appendChild(x);
    box.appendChild(row);
  });

  const add = document.createElement('div');
  add.className = 'token-row add';

  const addName = document.createElement('input');
  addName.className = 'input';
  addName.type = 'text';
  addName.placeholder = namePlaceholder + '（新增）';

  const addLimit = document.createElement('input');
  addLimit.className = 'input num';
  addLimit.type = 'number';
  addLimit.min = '0.1';
  addLimit.step = '0.1';
  addLimit.placeholder = 'GiB';

  const addBtn = document.createElement('button');
  addBtn.className = 'plus';
  addBtn.textContent = '+';
  addBtn.title = '添加';
  addBtn.addEventListener('click', () => {
    const name = normalizeName(addName.value);
    if (!name) return;
    const limitVal = parsePositive(addLimit.value);
    const custom = limitVal > 0;
    const limit = custom ? limitVal : globalLimit;
    const existing = state.draft[kind].find((x) => normalizeName(x.name) === name);
    if (existing) {
      existing.limit = limit;
      existing.custom = custom;
    } else {
      state.draft[kind].push({ name, limit, custom });
    }
    renderEditors();
  });
  addName.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') addBtn.click();
  });
  addLimit.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') addBtn.click();
  });

  add.appendChild(addName);
  add.appendChild(addLimit);
  add.appendChild(addBtn);
  box.appendChild(add);
}

function renderEditors() {
  renderBox('commands');
  renderBox('groups');
}

function renderLists() {
  const cmdMap = state.effectiveLimits.commands || {};
  const grpMap = state.effectiveLimits.groups || {};
  document.getElementById('effectiveCommands').textContent =
    (state.effective.commands || []).map((n) => n + '  (' + fmtGiB(cmdMap[n] || state.activeLimits.command) + 'GiB)').join('\n');
  document.getElementById('effectiveGroups').textContent =
    (state.effective.groups || []).map((n) => n + '  (' + fmtGiB(grpMap[n] || state.activeLimits.group) + 'GiB)').join('\n');
  document.getElementById('defaultCommands').textContent = (state.defaults.commands || []).join('\n');
  document.getElementById('defaultGroups').textContent = (state.defaults.groups || []).join('\n');
}

function setFromRuleState(data) {
  state.defaults.commands = normalizeNameList(data.defaultCommands || []);
  state.defaults.groups = normalizeNameList(data.defaultGroups || []);
  state.effective.commands = normalizeNameList(data.effectiveCommands || []);
  state.effective.groups = normalizeNameList(data.effectiveGroups || []);

  state.baseLimits.command = parsePositive(data.baseCommandLimitGiB) || parsePositive(data.commandLimitGiB) || 2;
  state.baseLimits.group = parsePositive(data.baseGroupLimitGiB) || parsePositive(data.groupLimitGiB) || 6;
  state.activeLimits.command = parsePositive(data.commandLimitGiB) || state.baseLimits.command;
  state.activeLimits.group = parsePositive(data.groupLimitGiB) || state.baseLimits.group;
  state.globalLimits.command = state.activeLimits.command;
  state.globalLimits.group = state.activeLimits.group;

  state.effectiveLimits.commands = data.commandLimitsGiB || {};
  state.effectiveLimits.groups = data.groupLimitsGiB || {};
  state.draft.commands = itemsFromNames(state.effective.commands, state.effectiveLimits.commands, state.globalLimits.command);
  state.draft.groups = itemsFromNames(state.effective.groups, state.effectiveLimits.groups, state.globalLimits.group);

  document.getElementById('globalCommandLimit').value = fmtGiB(state.globalLimits.command);
  document.getElementById('globalGroupLimit').value = fmtGiB(state.globalLimits.group);
  document.getElementById('baseLimitsHint').innerHTML =
    '环境默认上限: 命令 ' + fmtGiB(state.baseLimits.command) + 'GiB / 程序组 ' + fmtGiB(state.baseLimits.group) +
    'GiB。若与此不同，保存后将写入 rules.json 的 limits override。';
  renderLists();
  renderEditors();
  document.getElementById('meta').textContent = '规则文件: ' + (data.configPath || '(not set)');
}

function toSet(arr) {
  return new Set(normalizeNameList(arr));
}

function computePatchFromDraft() {
  const globalCommand = parsePositive(document.getElementById('globalCommandLimit').value) || state.baseLimits.command;
  const globalGroup = parsePositive(document.getElementById('globalGroupLimit').value) || state.baseLimits.group;

  const targetCommands = normalizeItems(state.draft.commands, globalCommand);
  const targetGroups = normalizeItems(state.draft.groups, globalGroup);

  const targetCommandNames = targetCommands.map((x) => x.name);
  const targetGroupNames = targetGroups.map((x) => x.name);
  const defaultCommands = state.defaults.commands;
  const defaultGroups = state.defaults.groups;

  const targetCommandSet = toSet(targetCommandNames);
  const targetGroupSet = toSet(targetGroupNames);
  const defaultCommandSet = toSet(defaultCommands);
  const defaultGroupSet = toSet(defaultGroups);

  const cmdLimitsGiB = {};
  for (const it of targetCommands) {
    if (it.custom && Math.abs(it.limit - globalCommand) > EPS) {
      cmdLimitsGiB[it.name] = roundGiB(it.limit);
    }
  }

  const grpLimitsGiB = {};
  for (const it of targetGroups) {
    if (it.custom && Math.abs(it.limit - globalGroup) > EPS) {
      grpLimitsGiB[it.name] = roundGiB(it.limit);
    }
  }

  return {
    limits: {
      commandGiB: Math.abs(globalCommand - state.baseLimits.command) > EPS ? roundGiB(globalCommand) : 0,
      groupGiB: Math.abs(globalGroup - state.baseLimits.group) > EPS ? roundGiB(globalGroup) : 0,
    },
    commands: {
      add: targetCommandNames.filter((v) => !defaultCommandSet.has(v)),
      remove: defaultCommands.filter((v) => !targetCommandSet.has(v)),
      limitsGiB: cmdLimitsGiB,
    },
    groups: {
      add: targetGroupNames.filter((v) => !defaultGroupSet.has(v)),
      remove: defaultGroups.filter((v) => !targetGroupSet.has(v)),
      limitsGiB: grpLimitsGiB,
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
  const patch = computePatchFromDraft();
  await savePatch(patch, '规则已保存');
});

function applyGlobalDraftLimits() {
  const gc = parsePositive(document.getElementById('globalCommandLimit').value) || state.baseLimits.command;
  const gg = parsePositive(document.getElementById('globalGroupLimit').value) || state.baseLimits.group;
  state.globalLimits.command = gc;
  state.globalLimits.group = gg;
  state.draft.commands.forEach((it) => {
    if (!it.custom) it.limit = gc;
  });
  state.draft.groups.forEach((it) => {
    if (!it.custom) it.limit = gg;
  });
  renderEditors();
}

document.getElementById('globalCommandLimit').addEventListener('input', applyGlobalDraftLimits);
document.getElementById('globalGroupLimit').addEventListener('input', applyGlobalDraftLimits);

document.getElementById('reloadBtn').addEventListener('click', async () => {
  await loadRules();
  document.getElementById('status').innerHTML =
    '<span class="oktext">已重新加载当前生效规则</span>: ' + new Date().toLocaleTimeString();
});

document.getElementById('restoreBtn').addEventListener('click', async () => {
  const patch = {
    limits: { commandGiB: 0, groupGiB: 0 },
    commands: { add: [], remove: [], limitsGiB: {} },
    groups: { add: [], remove: [], limitsGiB: {} },
  };
  await savePatch(patch, '已恢复默认规则');
});

loadRules().catch((e) => {
  document.getElementById('status').textContent = '加载失败: ' + e;
});
</script>
</body>
</html>`
