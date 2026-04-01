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
      <div class="title">Important Logs (action/error/kill, with date, rolling 100 lines)</div>
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
    --bg2: #101827;
    --panel: #141d29;
    --panel-soft: #182335;
    --panel-deep: #0f1723;
    --line: #2c3d53;
    --line-soft: #243245;
    --text: #eaf1fb;
    --muted: #9cb1cb;
    --muted-soft: #7e93ad;
    --ok: #3ccc77;
    --warn: #ffcd62;
    --danger: #d75b63;
    --info: #71c1ff;
    --save: #24875f;
    --secondary: #42556d;
    --focus: #63a6ff;
    --shadow: 0 14px 36px rgba(0,0,0,.28);
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    color: var(--text);
    background:
      radial-gradient(circle at top right, rgba(40,74,124,.42), transparent 30%),
      radial-gradient(circle at top left, rgba(27,72,55,.25), transparent 26%),
      linear-gradient(180deg, var(--bg2), var(--bg));
    font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  }
  a { color: #9cd9ff; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .page {
    max-width: 1240px;
    margin: 0 auto;
    padding: 18px 16px 40px;
  }
  .hero {
    position: sticky;
    top: 0;
    z-index: 50;
    margin: -18px -16px 18px;
    padding: 18px 16px 14px;
    background: linear-gradient(180deg, rgba(11,16,23,.96), rgba(11,16,23,.90));
    backdrop-filter: blur(12px);
    border-bottom: 1px solid rgba(60, 82, 110, .55);
    box-shadow: 0 10px 24px rgba(0,0,0,.12);
  }
  .hero-inner {
    max-width: 1240px;
    margin: 0 auto;
    display: grid;
    gap: 12px;
  }
  .hero-top {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 14px;
    align-items: start;
  }
  .hero-title {
    min-width: 0;
  }
  h1 {
    margin: 0;
    font-size: 28px;
    letter-spacing: .2px;
  }
  .sub {
    margin-top: 6px;
    color: var(--muted);
    font-size: 13px;
    word-break: break-all;
  }
  .hero-actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    justify-content: flex-end;
  }
  .btn {
    border: 1px solid transparent;
    border-radius: 10px;
    color: #fff;
    padding: 10px 14px;
    cursor: pointer;
    font-size: 13px;
    font-weight: 600;
    min-height: 40px;
    box-shadow: inset 0 1px 0 rgba(255,255,255,.05);
  }
  .btn:disabled { opacity: .5; cursor: not-allowed; }
  .btn.save { background: var(--save); }
  .btn.secondary { background: var(--secondary); }
  .btn.linkish { background: #203044; border-color: #35506f; }
  .btn.danger { background: #60252b; border-color: #8f3942; }
  .banner {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 12px;
    align-items: center;
    border: 1px solid var(--line);
    background: linear-gradient(180deg, rgba(20,29,41,.98), rgba(17,24,35,.95));
    border-radius: 12px;
    padding: 11px 14px;
    box-shadow: var(--shadow);
  }
  .banner strong { font-size: 13px; }
  .banner-note {
    color: var(--muted);
    font-size: 12px;
  }
  .banner.saved strong { color: var(--ok); }
  .banner.dirty strong { color: var(--warn); }
  .banner.error strong { color: #ff9aa3; }
  .banner.info strong { color: var(--info); }
  .banner a {
    white-space: nowrap;
    color: #b7e2ff;
    font-size: 13px;
  }

  .intro {
    margin-bottom: 14px;
    border: 1px solid #31506f;
    border-radius: 12px;
    background: linear-gradient(180deg, #122237, #112031);
    color: #c6d8ee;
    padding: 12px 14px;
    line-height: 1.6;
  }
  .intro b { color: #eff6ff; }

  .section {
    margin-top: 14px;
    display: grid;
    gap: 12px;
  }
  .grid-2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .card {
    background: linear-gradient(180deg, rgba(20,29,41,.98), rgba(16,23,35,.96));
    border: 1px solid var(--line-soft);
    border-radius: 14px;
    padding: 14px;
    box-shadow: var(--shadow);
  }
  .card.primary {
    border-color: #35506f;
    background: linear-gradient(180deg, rgba(20,31,48,.98), rgba(16,24,36,.98));
  }
  .card.secondary {
    border-color: #26374a;
    background: linear-gradient(180deg, rgba(18,26,38,.98), rgba(14,21,31,.98));
  }
  .card h2, .card h3 {
    margin: 0;
    font-size: 15px;
    letter-spacing: .2px;
    color: #eef5ff;
  }
  .card-head {
    display: flex;
    justify-content: space-between;
    gap: 10px;
    align-items: baseline;
    margin-bottom: 8px;
  }
  .desc {
    margin: 6px 0 0;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }
  .pill {
    border-radius: 999px;
    padding: 4px 9px;
    background: #17283b;
    border: 1px solid #2d4461;
    color: #b7d8f7;
    font-size: 11px;
    white-space: nowrap;
  }
  .summary-list {
    margin-top: 10px;
    display: grid;
    gap: 8px;
  }
  .summary-item {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 12px;
    align-items: center;
    padding: 8px 10px;
    background: var(--panel-deep);
    border: 1px solid var(--line-soft);
    border-radius: 10px;
    font-family: ui-monospace, Menlo, Consolas, monospace;
    font-size: 12px;
  }
  .summary-empty {
    color: var(--muted-soft);
    padding: 16px 10px;
    text-align: center;
    border: 1px dashed #30445e;
    border-radius: 10px;
    background: rgba(15,23,35,.45);
    font-size: 12px;
  }

  .limits-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
    margin-top: 10px;
  }
  .limit-card {
    padding: 10px 12px;
    background: var(--panel-deep);
    border: 1px solid var(--line-soft);
    border-radius: 12px;
  }
  .limit-card label {
    display: block;
    margin-bottom: 8px;
    font-size: 13px;
    color: #d9e7f6;
  }
  .hint {
    margin-top: 10px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .editor-table {
    margin-top: 10px;
    border: 1px solid var(--line-soft);
    border-radius: 12px;
    overflow: hidden;
    background: rgba(13,19,29,.75);
  }
  .editor-head, .editor-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 132px 44px 28px;
    gap: 8px;
    align-items: center;
    padding: 8px 10px;
  }
  .editor-head {
    background: rgba(23,35,52,.96);
    border-bottom: 1px solid var(--line-soft);
    color: #b9cce3;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: .08em;
  }
  .editor-body {
    display: grid;
    gap: 0;
  }
  .editor-row {
    border-top: 1px solid rgba(38,55,74,.75);
  }
  .editor-row:first-child { border-top: 0; }
  .editor-row.add {
    background: rgba(18,31,28,.34);
  }
  .editor-row.invalid {
    background: rgba(90,31,38,.24);
  }
  .editor-row.duplicate {
    background: rgba(97,68,26,.22);
  }
  .input {
    width: 100%;
    border-radius: 10px;
    border: 1px solid var(--line);
    background: var(--panel-deep);
    color: var(--text);
    padding: 9px 10px;
    font-size: 13px;
    outline: none;
  }
  .input:focus {
    border-color: var(--focus);
    box-shadow: 0 0 0 2px rgba(99, 166, 255, .18);
  }
  .input.invalid {
    border-color: #c5575d;
    box-shadow: 0 0 0 2px rgba(197,87,93,.12);
  }
  .input.duplicate {
    border-color: #c89b3b;
    box-shadow: 0 0 0 2px rgba(200,155,59,.12);
  }
  .num {
    text-align: right;
    font-family: ui-monospace, Menlo, Consolas, monospace;
  }
  .row-state {
    text-align: center;
    font-weight: 700;
    font-size: 16px;
  }
  .row-state.ok { color: var(--ok); }
  .row-state.warn { color: var(--warn); }
  .row-state.bad { color: #ff9aa3; }
  .icon-btn {
    width: 34px;
    height: 34px;
    border-radius: 9px;
    border: 1px solid #4f637c;
    background: #182536;
    color: #dcecff;
    cursor: pointer;
    font-size: 18px;
    line-height: 1;
  }
  .icon-btn.add {
    border-color: #29664a;
    background: #153225;
    color: #9bf0c1;
  }
  .icon-btn.remove {
    border-color: #6e3941;
    background: #321d22;
    color: #ffb4bc;
  }
  .icon-btn:disabled {
    opacity: .35;
    cursor: not-allowed;
  }
  .validation {
    margin-top: 10px;
    min-height: 18px;
    color: var(--muted);
    font-size: 12px;
  }
  .validation.error { color: #ff9aa3; }
  .validation.warn { color: var(--warn); }

  .default-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px 12px;
    margin-top: 10px;
  }
  .default-col {
    min-width: 0;
  }
  .default-list {
    margin-top: 8px;
    display: grid;
    gap: 6px;
  }
  .default-item {
    padding: 7px 9px;
    border-radius: 10px;
    border: 1px solid #243448;
    background: rgba(15,23,35,.72);
    font-family: ui-monospace, Menlo, Consolas, monospace;
    font-size: 12px;
    color: #c7d6e8;
  }

  @media (max-width: 980px) {
    .hero {
      position: static;
      margin: 0 0 18px;
      padding: 0;
      background: none;
      backdrop-filter: none;
      border-bottom: 0;
      box-shadow: none;
    }
    .hero-top {
      grid-template-columns: 1fr;
    }
    .hero-actions {
      justify-content: flex-start;
    }
    .banner {
      grid-template-columns: 1fr;
    }
    .grid-2, .limits-grid, .default-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 760px) {
    .editor-head, .editor-row {
      grid-template-columns: minmax(0, 1fr) 110px 36px;
    }
    .editor-head .state-col,
    .editor-row .row-state {
      display: none;
    }
  }
</style>
</head>
<body>
<div class="page">
  <div class="hero">
    <div class="hero-inner">
      <div class="hero-top">
        <div class="hero-title">
          <h1>Rules Editor</h1>
          <div class="sub" id="meta"></div>
        </div>
        <div class="hero-actions">
          <a class="btn linkish" href="/">返回日志</a>
          <button class="btn secondary" id="reloadBtn">重新加载当前生效内容</button>
          <button class="btn danger" id="restoreBtn">恢复默认并生效</button>
          <button class="btn save" id="saveBtn">保存并立即生效</button>
        </div>
      </div>
      <div class="banner info" id="statusBanner">
        <div>
          <strong id="statusTitle">加载中</strong>
          <div class="banner-note" id="statusNote">正在读取当前规则状态。</div>
        </div>
        <a href="/" id="statusLink">返回日志查看运行情况</a>
      </div>
    </div>
  </div>

  <div class="intro">
    支持 <b>全局上限</b> 和 <b>单项自定义上限</b>。当前生效内容在上方对照，编辑区在中间直接修改，保存后立刻热加载生效。
  </div>

  <div class="section">
    <div class="grid-2">
      <div class="card secondary">
        <div class="card-head">
          <h2>当前生效命令规则</h2>
          <span class="pill" id="effectiveCommandCount">0 项</span>
        </div>
        <div class="summary-list" id="effectiveCommands"></div>
      </div>
      <div class="card secondary">
        <div class="card-head">
          <h2>当前生效程序组规则</h2>
          <span class="pill" id="effectiveGroupCount">0 项</span>
        </div>
        <div class="summary-list" id="effectiveGroups"></div>
      </div>
    </div>

    <div class="card secondary">
      <div class="card-head">
        <h2>全局上限</h2>
        <span class="pill">保存后立即写入当前生效配置</span>
      </div>
      <div class="limits-grid">
        <div class="limit-card">
          <label for="globalCommandLimit">命令规则全局上限（GiB）</label>
          <input class="input num" id="globalCommandLimit" type="number" min="0.1" step="0.1" />
        </div>
        <div class="limit-card">
          <label for="globalGroupLimit">程序组规则全局上限（GiB）</label>
          <input class="input num" id="globalGroupLimit" type="number" min="0.1" step="0.1" />
        </div>
      </div>
      <div class="hint" id="baseLimitsHint"></div>
    </div>

    <div class="grid-2">
      <div class="card primary">
        <div class="card-head">
          <div>
            <h2>命令规则编辑</h2>
            <p class="desc">每行一条规则。名称为空不保存；单项上限留空时使用全局上限。</p>
          </div>
          <span class="pill" id="draftCommandCount">0 行</span>
        </div>
        <div class="editor-table">
          <div class="editor-head">
            <div>名称</div>
            <div>单项上限</div>
            <div class="state-col">状态</div>
            <div></div>
          </div>
          <div class="editor-body" id="cmdBox"></div>
        </div>
        <div class="validation" id="cmdValidation"></div>
      </div>
      <div class="card primary">
        <div class="card-head">
          <div>
            <h2>程序组规则编辑</h2>
            <p class="desc">适合浏览器、IDE、工具进程组。输入内容后即视为有效候选。</p>
          </div>
          <span class="pill" id="draftGroupCount">0 行</span>
        </div>
        <div class="editor-table">
          <div class="editor-head">
            <div>名称</div>
            <div>单项上限</div>
            <div class="state-col">状态</div>
            <div></div>
          </div>
          <div class="editor-body" id="grpBox"></div>
        </div>
        <div class="validation" id="grpValidation"></div>
      </div>
    </div>

    <div class="card secondary">
      <div class="card-head">
        <div>
          <h2>默认规则</h2>
          <p class="desc">默认规则始终可见，方便对照当前生效内容，但不占用主编辑区。</p>
        </div>
        <span class="pill">只读参考</span>
      </div>
      <div class="default-grid">
        <div class="default-col">
          <h3>默认命令规则</h3>
          <div class="default-list" id="defaultCommands"></div>
        </div>
        <div class="default-col">
          <h3>默认程序组规则</h3>
          <div class="default-list" id="defaultGroups"></div>
        </div>
      </div>
    </div>
  </div>
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
  status: { kind: 'info', title: '加载中', note: '正在读取当前规则状态。' },
  dirty: false,
  restoring: false,
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
    if (!v || seen.has(v)) continue;
    seen.add(v);
    out.push(v);
  }
  return out;
}

function normalizeItems(items, globalLimit) {
  const out = [];
  const seen = new Set();
  for (const raw of items || []) {
    const name = normalizeName((raw || {}).name || '');
    if (!name || seen.has(name)) continue;
    seen.add(name);
    const custom = !!(raw && raw.custom);
    const parsed = parsePositive((raw || {}).limit);
    const limit = custom ? (parsed || globalLimit) : globalLimit;
    out.push({ name, limit: roundGiB(limit), custom });
  }
  return out;
}

function itemsFromNames(names, overrideMap, globalLimit) {
  return normalizeNameList(names || []).map((name) => {
    const ov = parsePositive((overrideMap || {})[name]);
    return { name, limit: ov > 0 ? ov : globalLimit, custom: ov > 0 };
  });
}

function summarizeLines(containerId, countId, items, limitMap, fallbackLimit) {
  const host = document.getElementById(containerId);
  document.getElementById(countId).textContent = (items.length || 0) + ' 项';
  host.innerHTML = '';
  if (!items.length) {
    const empty = document.createElement('div');
    empty.className = 'summary-empty';
    empty.textContent = '当前没有规则。';
    host.appendChild(empty);
    return;
  }
  for (const name of items) {
    const row = document.createElement('div');
    row.className = 'summary-item';
    row.innerHTML = '<div>' + name + '</div><div>' + fmtGiB((limitMap || {})[name] || fallbackLimit) + 'GiB</div>';
    host.appendChild(row);
  }
}

function renderDefaults(containerId, items) {
  const host = document.getElementById(containerId);
  host.innerHTML = '';
  if (!items.length) {
    const empty = document.createElement('div');
    empty.className = 'summary-empty';
    empty.textContent = '无默认项';
    host.appendChild(empty);
    return;
  }
  for (const name of items) {
    const row = document.createElement('div');
    row.className = 'default-item';
    row.textContent = name;
    host.appendChild(row);
  }
}

function getValidation(kind) {
  const items = state.draft[kind] || [];
  const issues = [];
  const seen = new Map();
  items.forEach((raw, idx) => {
    const name = normalizeName(raw.name);
    const limitRaw = (raw.limitValue || '').trim();
    if (!name) return;
    if (!seen.has(name)) {
      seen.set(name, []);
    }
    seen.get(name).push(idx);
    if (limitRaw !== '' && parsePositive(limitRaw) <= 0) {
      issues.push({ idx, type: 'invalid-limit', message: name + ' 的单项上限必须大于 0' });
    }
  });
  for (const [name, indexes] of seen.entries()) {
    if (indexes.length > 1) {
      indexes.forEach((idx) => issues.push({ idx, type: 'duplicate', message: '重复规则: ' + name }));
    }
  }
  return issues;
}

function hasValidationErrors() {
  return getValidation('commands').length > 0 || getValidation('groups').length > 0;
}

function updateStatus(kind, title, note) {
  state.status = { kind, title, note };
  const banner = document.getElementById('statusBanner');
  banner.className = 'banner ' + kind;
  document.getElementById('statusTitle').textContent = title;
  document.getElementById('statusNote').textContent = note;
}

function updateDirtyState() {
  if (hasValidationErrors()) {
    updateStatus('error', '存在未提交更改', '请先修正重复名称或非法上限，然后再保存。');
    state.dirty = true;
    return;
  }
  const patch = computePatchFromDraft();
  const same = JSON.stringify(patch) === JSON.stringify(computePatchFromEffective());
  state.dirty = !same;
  if (state.dirty) {
    updateStatus('dirty', '存在未提交更改', '修改已留在页面内，点击“保存并立即生效”后才会写入当前规则。');
  } else {
    updateStatus('saved', '所有更改已保存', '当前页面内容与已生效规则一致。');
  }
}

function makeDraftRow(raw) {
  return {
    name: normalizeName(raw.name || ''),
    custom: !!raw.custom,
    limitValue: raw.custom ? fmtGiB(raw.limit || 0) : '',
  };
}

function renderEditor(kind) {
  const box = document.getElementById(kind === 'commands' ? 'cmdBox' : 'grpBox');
  const validationEl = document.getElementById(kind === 'commands' ? 'cmdValidation' : 'grpValidation');
  const countEl = document.getElementById(kind === 'commands' ? 'draftCommandCount' : 'draftGroupCount');
  const items = state.draft[kind];
  const globalLimit = kind === 'commands' ? state.globalLimits.command : state.globalLimits.group;
  const placeholder = kind === 'commands' ? '输入命令名' : '输入程序组名';
  const issues = getValidation(kind);
  const issueMap = new Map();
  issues.forEach((it) => {
    if (!issueMap.has(it.idx)) issueMap.set(it.idx, []);
    issueMap.get(it.idx).push(it);
  });

  box.innerHTML = '';
  countEl.textContent = items.filter((it) => normalizeName(it.name)).length + ' 行';
  items.forEach((it, idx) => {
    const nameNorm = normalizeName(it.name);
    const row = document.createElement('div');
    const rowIssues = issueMap.get(idx) || [];
    const hasDuplicate = rowIssues.some((x) => x.type === 'duplicate');
    const hasInvalid = rowIssues.some((x) => x.type === 'invalid-limit');
    row.className = 'editor-row' + (hasDuplicate ? ' duplicate' : '') + (hasInvalid ? ' invalid' : '');

    const name = document.createElement('input');
    name.className = 'input' + (hasDuplicate ? ' duplicate' : '');
    name.type = 'text';
    name.value = it.name || '';
    name.placeholder = placeholder;
    name.addEventListener('input', () => {
      state.draft[kind][idx].name = name.value;
      renderAll();
    });

    const limit = document.createElement('input');
    limit.className = 'input num' + (hasInvalid ? ' invalid' : '');
    limit.type = 'number';
    limit.min = '0.1';
    limit.step = '0.1';
    limit.placeholder = fmtGiB(globalLimit);
    limit.value = it.limitValue || '';
    limit.addEventListener('input', () => {
      state.draft[kind][idx].limitValue = limit.value;
      renderAll();
    });

    const stateCell = document.createElement('div');
    stateCell.className = 'row-state';
    if (!nameNorm) {
      stateCell.textContent = '';
    } else if (hasDuplicate) {
      stateCell.classList.add('warn');
      stateCell.textContent = '!';
      stateCell.title = '名称重复';
    } else if (hasInvalid) {
      stateCell.classList.add('bad');
      stateCell.textContent = '!';
      stateCell.title = '上限无效';
    } else {
      stateCell.classList.add('ok');
      stateCell.textContent = '✓';
      stateCell.title = '有效';
    }

    const remove = document.createElement('button');
    remove.className = 'icon-btn remove';
    remove.type = 'button';
    remove.textContent = '×';
    remove.title = '删除';
    remove.addEventListener('click', () => {
      state.draft[kind].splice(idx, 1);
      renderAll();
    });

    row.appendChild(name);
    row.appendChild(limit);
    row.appendChild(stateCell);
    row.appendChild(remove);
    box.appendChild(row);
  });

  const addRow = document.createElement('div');
  addRow.className = 'editor-row add';

  const addName = document.createElement('input');
  addName.className = 'input';
  addName.type = 'text';
  addName.placeholder = placeholder + '（新增）';

  const addLimit = document.createElement('input');
  addLimit.className = 'input num';
  addLimit.type = 'number';
  addLimit.min = '0.1';
  addLimit.step = '0.1';
  addLimit.placeholder = fmtGiB(globalLimit);

  const addState = document.createElement('div');
  addState.className = 'row-state ' + (normalizeName(addName.value) ? 'ok' : '');
  addState.textContent = '';

  const addBtn = document.createElement('button');
  addBtn.className = 'icon-btn add';
  addBtn.type = 'button';
  addBtn.textContent = '+';
  addBtn.title = '添加';
  addBtn.addEventListener('click', () => {
    const name = normalizeName(addName.value);
    if (!name) return;
    const existing = state.draft[kind].findIndex((it) => normalizeName(it.name) === name);
    const row = {
      name,
      custom: parsePositive(addLimit.value) > 0,
      limitValue: parsePositive(addLimit.value) > 0 ? fmtGiB(parsePositive(addLimit.value)) : '',
    };
    if (existing >= 0) {
      state.draft[kind][existing] = row;
    } else {
      state.draft[kind].push(row);
    }
    renderAll();
  });

  function updateAddState() {
    const name = normalizeName(addName.value);
    addState.className = 'row-state ' + (name ? 'ok' : '');
    addState.textContent = name ? '✓' : '';
  }
  addName.addEventListener('input', updateAddState);
  addName.addEventListener('keydown', (e) => { if (e.key === 'Enter') addBtn.click(); });
  addLimit.addEventListener('keydown', (e) => { if (e.key === 'Enter') addBtn.click(); });

  addRow.appendChild(addName);
  addRow.appendChild(addLimit);
  addRow.appendChild(addState);
  addRow.appendChild(addBtn);
  box.appendChild(addRow);

  if (!issues.length) {
    validationEl.className = 'validation';
    validationEl.textContent = '当前无校验错误。';
  } else {
    validationEl.className = 'validation error';
    validationEl.textContent = issues[0].message;
  }
}

function renderAll() {
  summarizeLines('effectiveCommands', 'effectiveCommandCount', state.effective.commands, state.effectiveLimits.commands, state.activeLimits.command);
  summarizeLines('effectiveGroups', 'effectiveGroupCount', state.effective.groups, state.effectiveLimits.groups, state.activeLimits.group);
  renderDefaults('defaultCommands', state.defaults.commands);
  renderDefaults('defaultGroups', state.defaults.groups);
  renderEditor('commands');
  renderEditor('groups');
  updateDirtyState();
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
  state.draft.commands = itemsFromNames(state.effective.commands, state.effectiveLimits.commands, state.globalLimits.command).map(makeDraftRow);
  state.draft.groups = itemsFromNames(state.effective.groups, state.effectiveLimits.groups, state.globalLimits.group).map(makeDraftRow);
  document.getElementById('globalCommandLimit').value = fmtGiB(state.globalLimits.command);
  document.getElementById('globalGroupLimit').value = fmtGiB(state.globalLimits.group);
  document.getElementById('baseLimitsHint').textContent =
    '环境默认上限: 命令 ' + fmtGiB(state.baseLimits.command) + 'GiB / 程序组 ' + fmtGiB(state.baseLimits.group) +
    'GiB。输入不同值后保存，将写入 rules.json 的 limits override。';
  document.getElementById('meta').textContent = '规则文件: ' + (data.configPath || '(not set)');
  state.restoring = false;
  renderAll();
}

function toSet(arr) {
  return new Set(normalizeNameList(arr));
}

function computePatch(globalCommand, globalGroup) {
  const targetCommands = normalizeItems(state.draft.commands.map((it) => ({
    name: it.name,
    custom: parsePositive(it.limitValue) > 0,
    limit: parsePositive(it.limitValue) > 0 ? parsePositive(it.limitValue) : globalCommand,
  })), globalCommand);
  const targetGroups = normalizeItems(state.draft.groups.map((it) => ({
    name: it.name,
    custom: parsePositive(it.limitValue) > 0,
    limit: parsePositive(it.limitValue) > 0 ? parsePositive(it.limitValue) : globalGroup,
  })), globalGroup);

  const targetCommandNames = targetCommands.map((x) => x.name);
  const targetGroupNames = targetGroups.map((x) => x.name);
  const defaultCommandSet = toSet(state.defaults.commands);
  const defaultGroupSet = toSet(state.defaults.groups);
  const targetCommandSet = toSet(targetCommandNames);
  const targetGroupSet = toSet(targetGroupNames);

  const cmdLimitsGiB = {};
  targetCommands.forEach((it) => {
    if (it.custom && Math.abs(it.limit - globalCommand) > EPS) cmdLimitsGiB[it.name] = roundGiB(it.limit);
  });
  const grpLimitsGiB = {};
  targetGroups.forEach((it) => {
    if (it.custom && Math.abs(it.limit - globalGroup) > EPS) grpLimitsGiB[it.name] = roundGiB(it.limit);
  });

  return {
    limits: {
      commandGiB: Math.abs(globalCommand - state.baseLimits.command) > EPS ? roundGiB(globalCommand) : 0,
      groupGiB: Math.abs(globalGroup - state.baseLimits.group) > EPS ? roundGiB(globalGroup) : 0,
    },
    commands: {
      add: targetCommandNames.filter((v) => !defaultCommandSet.has(v)),
      remove: state.defaults.commands.filter((v) => !targetCommandSet.has(v)),
      limitsGiB: cmdLimitsGiB,
    },
    groups: {
      add: targetGroupNames.filter((v) => !defaultGroupSet.has(v)),
      remove: state.defaults.groups.filter((v) => !targetGroupSet.has(v)),
      limitsGiB: grpLimitsGiB,
    },
  };
}

function computePatchFromDraft() {
  const globalCommand = parsePositive(document.getElementById('globalCommandLimit').value) || state.baseLimits.command;
  const globalGroup = parsePositive(document.getElementById('globalGroupLimit').value) || state.baseLimits.group;
  return computePatch(globalCommand, globalGroup);
}

function computePatchFromEffective() {
  return {
    limits: {
      commandGiB: Math.abs(state.activeLimits.command - state.baseLimits.command) > EPS ? roundGiB(state.activeLimits.command) : 0,
      groupGiB: Math.abs(state.activeLimits.group - state.baseLimits.group) > EPS ? roundGiB(state.activeLimits.group) : 0,
    },
    commands: {
      add: state.effective.commands.filter((v) => !toSet(state.defaults.commands).has(v)),
      remove: state.defaults.commands.filter((v) => !toSet(state.effective.commands).has(v)),
      limitsGiB: Object.assign({}, state.effectiveLimits.commands || {}),
    },
    groups: {
      add: state.effective.groups.filter((v) => !toSet(state.defaults.groups).has(v)),
      remove: state.defaults.groups.filter((v) => !toSet(state.effective.groups).has(v)),
      limitsGiB: Object.assign({}, state.effectiveLimits.groups || {}),
    },
  };
}

async function loadRules(message) {
  const res = await fetch('/api/rules', { cache: 'no-store' });
  if (!res.ok) throw new Error(await res.text());
  const data = await res.json();
  setFromRuleState(data);
  if (message) {
    updateStatus('saved', message, '页面已刷新为当前正在生效的规则。');
  }
}

async function savePatch(patch, okTitle, okNote) {
  if (hasValidationErrors()) {
    updateStatus('error', '存在未提交更改', '请先修正重复名称或非法上限，然后再保存。');
    return;
  }
  const res = await fetch('/api/rules', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  if (!res.ok) {
    updateStatus('error', '保存失败', await res.text());
    return;
  }
  const data = await res.json();
  setFromRuleState(data);
  updateStatus('saved', okTitle, okNote);
}

function applyGlobalDraftLimits() {
  state.globalLimits.command = parsePositive(document.getElementById('globalCommandLimit').value) || state.baseLimits.command;
  state.globalLimits.group = parsePositive(document.getElementById('globalGroupLimit').value) || state.baseLimits.group;
  renderAll();
}

document.getElementById('saveBtn').addEventListener('click', async () => {
  await savePatch(computePatchFromDraft(), '所有更改已保存', '保存成功，规则已热加载生效。');
});

document.getElementById('reloadBtn').addEventListener('click', async () => {
  await loadRules('所有更改已保存');
});

document.getElementById('restoreBtn').addEventListener('click', async () => {
  state.restoring = true;
  const patch = {
    limits: { commandGiB: 0, groupGiB: 0 },
    commands: { add: [], remove: [], limitsGiB: {} },
    groups: { add: [], remove: [], limitsGiB: {} },
  };
  await savePatch(patch, '已恢复默认并生效', '页面内容已切回默认规则并立即生效。');
});

document.getElementById('globalCommandLimit').addEventListener('input', applyGlobalDraftLimits);
document.getElementById('globalGroupLimit').addEventListener('input', applyGlobalDraftLimits);

loadRules('所有更改已保存').catch((e) => {
  updateStatus('error', '加载失败', String(e));
});
</script>
</body>
</html>`
