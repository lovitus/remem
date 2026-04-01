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
    --bg: #f4f7fb;
    --bg2: #edf3fa;
    --panel: rgba(255, 255, 255, .98);
    --panel-soft: rgba(248, 251, 255, .98);
    --line: #c7d6e6;
    --line-soft: #d8e3ef;
    --text: #163047;
    --muted: #587089;
    --muted-soft: #6d8297;
    --ok: #198754;
    --warn: #ab7a00;
    --danger: #c23b4a;
    --info: #0b7cc1;
    --save: #1f8a5c;
    --secondary: #5d7387;
    --focus: #1a84e8;
    --pill: #f7fbff;
    --pill-border: #bfd2e6;
    --shadow: 0 10px 24px rgba(29, 57, 85, .12);
  }
  body[data-theme="dark"] {
    --bg: #09111a;
    --bg2: #101b2b;
    --panel: rgba(18, 27, 40, .96);
    --panel-soft: rgba(22, 34, 50, .96);
    --line: #2b4057;
    --line-soft: #213243;
    --text: #edf4ff;
    --muted: #9eb2c9;
    --muted-soft: #7d91a8;
    --ok: #44ce80;
    --warn: #efbe58;
    --danger: #de6b73;
    --info: #80ceff;
    --save: #24865f;
    --secondary: #41556e;
    --focus: #5ea8ff;
    --pill: #12202f;
    --pill-border: #2a4258;
    --shadow: 0 18px 34px rgba(0, 0, 0, .28);
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    color: var(--text);
    background: linear-gradient(180deg, var(--bg2), var(--bg));
    font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  }
  a { color: #245b8f; text-decoration: none; }
  body[data-theme="dark"] a { color: #a8ddff; }
  a:hover { text-decoration: underline; }
  .page {
    max-width: 1180px;
    margin: 0 auto;
    padding: 20px 20px 44px;
  }
  .hero {
    position: sticky;
    top: 0;
    z-index: 50;
    margin: -20px -20px 18px;
    padding: 18px 20px 14px;
    background: linear-gradient(180deg, rgba(244,247,251,.96), rgba(244,247,251,.92));
    backdrop-filter: blur(12px);
    border-bottom: 1px solid rgba(174, 193, 212, .75);
    box-shadow: 0 10px 24px rgba(42, 73, 104, .10);
  }
  body[data-theme="dark"] .hero {
    background: linear-gradient(180deg, rgba(9,17,26,.97), rgba(9,17,26,.91));
    border-bottom-color: rgba(56, 79, 105, .58);
    box-shadow: 0 12px 28px rgba(0,0,0,.15);
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
  h1 {
    margin: 0;
    font-size: 30px;
    letter-spacing: .2px;
  }
  .sub {
    margin-top: 6px;
    color: var(--muted);
    font-size: 14px;
    word-break: break-all;
  }
  .hero-actions {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
    justify-content: flex-end;
  }
  .btn {
    border: 1px solid var(--line);
    border-radius: 12px;
    color: var(--text);
    background: #fff;
    padding: 10px 14px;
    cursor: pointer;
    font-size: 14px;
    font-weight: 700;
    min-height: 44px;
    box-shadow: 0 1px 2px rgba(17, 43, 66, .06);
  }
  body[data-theme="dark"] .btn {
    color: #fff;
    background: rgba(18,31,46,.9);
    border-color: #35506f;
    box-shadow: inset 0 1px 0 rgba(255,255,255,.05);
  }
  .btn:disabled { opacity: .45; cursor: not-allowed; }
  .btn.save { background: var(--save); color: #fff; border-color: transparent; }
  .btn.secondary { background: var(--panel-soft); }
  .btn.linkish { background: transparent; }
  .btn.danger { background: #fff2f3; border-color: #efc8cf; color: #972d39; }
  body[data-theme="dark"] .btn.secondary { background: var(--secondary); }
  body[data-theme="dark"] .btn.linkish { background: #213247; border-color: #35506f; color: #fff; }
  body[data-theme="dark"] .btn.danger { background: #5d272d; border-color: #8b3c45; color: #fff; }
  .banner {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 12px;
    align-items: center;
    border: 1px solid var(--line);
    background: var(--panel);
    border-radius: 12px;
    padding: 11px 14px;
  }
  body[data-theme="dark"] .banner {
    background: linear-gradient(180deg, rgba(19,29,42,.98), rgba(15,22,34,.96));
    box-shadow: var(--shadow);
  }
  .banner strong { font-size: 14px; }
  .banner-note {
    color: var(--muted);
    font-size: 13px;
  }
  .banner.saved strong { color: var(--ok); }
  .banner.dirty strong { color: var(--warn); }
  .banner.error strong { color: #ffadb4; }
  .banner.info strong { color: var(--info); }
  .banner a {
    white-space: nowrap;
    color: inherit;
    font-size: 13px;
  }
  .intro {
    margin-bottom: 14px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--panel);
    color: var(--muted);
    padding: 12px 14px;
    line-height: 1.55;
    font-size: 14px;
  }
  body[data-theme="dark"] .intro {
    border-color: #31506f;
    background: linear-gradient(180deg, #122237, #112031);
    color: #c6d8ee;
  }
  .intro b { color: var(--text); }
  .section { display: grid; gap: 14px; }
  .card {
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 16px;
    padding: 16px;
  }
  body[data-theme="dark"] .card {
    background: linear-gradient(180deg, var(--panel), rgba(13,20,31,.98));
    border-color: var(--line-soft);
    box-shadow: var(--shadow);
  }
  .card.primary { border-color: var(--line); }
  .card h2 { margin: 0; font-size: 20px; color: var(--text); }
  .card-head {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    align-items: center;
    margin-bottom: 8px;
  }
  .card-side {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .desc {
    margin: 6px 0 0;
    color: var(--muted);
    font-size: 14px;
    line-height: 1.5;
  }
  .pill-badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border-radius: 999px;
    padding: 4px 10px;
    font-size: 12px;
    color: var(--muted);
    background: #f5f8fb;
    border: 1px solid var(--line);
    white-space: nowrap;
  }
  body[data-theme="dark"] .pill-badge {
    color: #b7d8f7;
    background: #17283b;
    border-color: #2d4461;
  }
  .limits-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
    margin-top: 10px;
  }
  .limit-card {
    padding: 10px 12px;
    background: var(--panel-soft);
    border: 1px solid var(--line);
    border-radius: 12px;
  }
  body[data-theme="dark"] .limit-card {
    background: rgba(13, 19, 29, .74);
    border-color: var(--line-soft);
  }
  .limit-card label {
    display: block;
    margin-bottom: 8px;
    font-size: 15px;
    color: var(--text);
  }
  .hint {
    margin-top: 10px;
    color: var(--muted);
    font-size: 13px;
    line-height: 1.5;
  }
  .legend {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 10px;
    opacity: .88;
  }
  .legend .mini {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 9px;
    border-radius: 999px;
    font-size: 11px;
    border: 1px solid var(--pill-border);
    background: #f7fbff;
    color: var(--muted);
  }
  body[data-theme="dark"] .legend .mini { background: rgba(14,22,32,.72); }
  .legend .default { border-color: #9eb8d0; color: #5d748b; }
  .legend .added { border-color: #8dc6a2; color: #2d7a53; }
  .legend .custom { border-color: #d4c07f; color: #8c6f09; }
  .legend .removed { border-color: #e4b1b8; color: #ad4150; }
  .legend .error { border-color: #dca3aa; color: #b63f4c; }
  body[data-theme="dark"] .legend .default { border-color: #36506f; color: #cfe3f8; }
  body[data-theme="dark"] .legend .added { border-color: #2f6749; color: #abf1c5; }
  body[data-theme="dark"] .legend .custom { border-color: #5d5d2d; color: #ffe8a4; }
  body[data-theme="dark"] .legend .removed { border-color: #724249; color: #ffbec4; }
  body[data-theme="dark"] .legend .error { border-color: #91454c; color: #ffb1b7; }
  .canvas {
    margin-top: 12px;
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: flex-start;
  }
  .pill {
    position: relative;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-height: 40px;
    max-width: 100%;
    padding: 6px 10px;
    border-radius: 999px;
    border: 1px solid var(--pill-border);
    background: var(--pill);
    transition: border-color .16s ease, opacity .16s ease, background-color .16s ease;
  }
  body[data-theme="dark"] .pill {
    background: linear-gradient(180deg, rgba(18,32,47,.95), rgba(14,24,35,.94));
    box-shadow: inset 0 1px 0 rgba(255,255,255,.03);
  }
  .pill:hover { border-color: #87a5c4; }
  .pill.default-item { border-color: #bfd2e6; }
  .pill.added-item { border-color: #9fd6b3; background: #f4fcf7; }
  .pill.custom-item { background: #fff9ea; border-color: #dcc169; }
  .pill.removed-item {
    opacity: .72;
    border-color: #e2b4bb;
    background: #fff2f3;
  }
  .pill.duplicate-item {
    border-color: #d4b049;
    background: #fff8e5;
  }
  .pill.invalid-item {
    border-color: #d28a93;
    background: #fff0f2;
  }
  body[data-theme="dark"] .pill.default-item { border-color: #35506f; }
  body[data-theme="dark"] .pill.added-item { border-color: #2f6749; background: linear-gradient(180deg, rgba(19,43,35,.92), rgba(14,27,24,.94)); }
  body[data-theme="dark"] .pill.custom-item { background: linear-gradient(180deg, rgba(18,32,47,.95), rgba(14,24,35,.94)); box-shadow: inset 0 0 0 1px rgba(239,190,88,.16); }
  body[data-theme="dark"] .pill.removed-item { border-color: #7a434a; background: linear-gradient(180deg, rgba(52,26,30,.78), rgba(31,16,20,.88)); }
  body[data-theme="dark"] .pill.duplicate-item { border-color: #c89b3b; background: linear-gradient(180deg, rgba(72,53,24,.92), rgba(42,29,13,.90)); }
  body[data-theme="dark"] .pill.invalid-item { border-color: #c5575d; background: linear-gradient(180deg, rgba(73,30,36,.92), rgba(39,18,22,.90)); }
  .pill.editing {
    border-radius: 18px;
    padding: 12px;
    min-width: min(100%, 360px);
    max-width: min(100%, 420px);
    align-items: stretch;
  }
  .pill-view {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    max-width: 100%;
  }
  .pill-name {
    font-size: 14px;
    font-weight: 600;
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .pill.removed-item .pill-name { text-decoration: line-through; }
  .pill-meta {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    flex-wrap: wrap;
  }
  .tag {
    border-radius: 999px;
    padding: 1px 7px;
    font-size: 10px;
    border: 1px solid var(--line);
    background: #f4f8fc;
    color: var(--muted);
    white-space: nowrap;
  }
  body[data-theme="dark"] .tag {
    border-color: #31485f;
    background: rgba(10,18,27,.72);
    color: #cae1f7;
  }
  .tag.limit { border-color: #dcc169; color: #8d6f05; background: #fff8df; }
  .tag.default { border-color: #bfd2e6; color: #5d748b; }
  .tag.added { border-color: #9fd6b3; color: #2d7a53; background: #f2fbf6; }
  .tag.removed { border-color: #e2b4bb; color: #b0404d; background: #fff3f5; }
  .tag.error { border-color: #dca3aa; color: #b63f4c; background: #fff1f3; }
  body[data-theme="dark"] .tag.limit { border-color: #6a5e2b; color: #ffe39a; background: rgba(10,18,27,.72); }
  body[data-theme="dark"] .tag.default { border-color: #35506f; color: #cde2f8; background: rgba(10,18,27,.72); }
  body[data-theme="dark"] .tag.added { border-color: #2f6749; color: #abf1c5; background: rgba(10,18,27,.72); }
  body[data-theme="dark"] .tag.removed { border-color: #8b4850; color: #ffb1b8; background: rgba(10,18,27,.72); }
  body[data-theme="dark"] .tag.error { border-color: #91454c; color: #ffb1b8; background: rgba(10,18,27,.72); }
  .pill-edit {
    display: grid;
    gap: 8px;
    width: 100%;
  }
  .edit-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 138px;
    gap: 8px;
  }
  .input {
    width: 100%;
    border-radius: 12px;
    border: 1px solid var(--line);
    background: #fff;
    color: var(--text);
    padding: 11px 13px;
    font-size: 16px;
    outline: none;
  }
  body[data-theme="dark"] .input { background: rgba(11, 18, 28, .86); }
  .input:focus {
    border-color: var(--focus);
    box-shadow: 0 0 0 2px rgba(94,168,255,.18);
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
  .edit-actions {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    align-items: center;
  }
  .edit-tools {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .tiny-btn {
    border: 1px solid var(--line);
    background: #fff;
    color: var(--text);
    border-radius: 999px;
    padding: 8px 12px;
    font-size: 13px;
    cursor: pointer;
  }
  body[data-theme="dark"] .tiny-btn {
    border-color: #49617c;
    background: rgba(18,31,46,.92);
    color: #dbeaff;
  }
  .tiny-btn.warn { border-color: #ebd39c; color: #946900; background: #fff8e7; }
  .tiny-btn.danger { border-color: #efc8cf; color: #972d39; background: #fff2f3; }
  body[data-theme="dark"] .tiny-btn.warn { border-color: #77533b; color: #ffd7a3; background: rgba(18,31,46,.92); }
  body[data-theme="dark"] .tiny-btn.danger { border-color: #7f4249; color: #ffc0c6; background: rgba(18,31,46,.92); }
  .pill-add {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    min-height: 40px;
    padding: 6px 12px;
    border-radius: 999px;
    border: 1px dashed #3d5f80;
    background: #fff;
    color: #2c4a66;
    cursor: pointer;
    font-size: 14px;
    font-weight: 600;
  }
  .canvas-add {
    display: none;
  }
  .add-inline-btn {
    border: 1px solid var(--line);
    background: #fff;
    color: var(--text);
    border-radius: 999px;
    min-height: 38px;
    padding: 0 14px;
    font-size: 13px;
    font-weight: 700;
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }
  .add-inline-btn .plus {
    width: 22px;
    height: 22px;
    font-size: 15px;
  }
  body[data-theme="dark"] .add-inline-btn {
    background: rgba(18,31,46,.9);
    color: #fff;
    border-color: #35506f;
  }
  .pill-add:hover { border-color: #5d88b5; background: #f7fbff; }
  body[data-theme="dark"] .pill-add {
    background: rgba(18,32,47,.48);
    color: #b6d3ef;
    border-color: #3d5f80;
  }
  .pill-add .plus {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: 999px;
    border: 1px solid #2f6749;
    background: rgba(18,46,33,.88);
    color: #9bf0c1;
    font-size: 18px;
    line-height: 1;
  }
  .validation {
    margin-top: 10px;
    min-height: 18px;
    color: var(--muted);
    font-size: 13px;
  }
  .validation.error { color: #c23b4a; }
  .validation.warn { color: var(--warn); }
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
    .hero-top { grid-template-columns: 1fr; }
    .hero-actions { justify-content: flex-start; }
    .banner { grid-template-columns: 1fr; }
    .limits-grid { grid-template-columns: 1fr; }
  }
  @media (max-width: 640px) {
    .edit-grid { grid-template-columns: 1fr; }
    .pill.editing {
      min-width: 100%;
      max-width: 100%;
    }
  }
</style>
</head>
<body>
<div class="page">
  <div class="hero">
    <div class="hero-inner">
      <div class="hero-top">
        <div>
          <h1>Rules Editor</h1>
          <div class="sub" id="meta"></div>
        </div>
        <div class="hero-actions">
          <button class="btn secondary" id="themeBtn">切换日间模式</button>
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
    现在所有规则都在一套画布里 <b>原地编辑</b>。双击任意胶囊即可修改名称或单项上限；保存后立刻热加载生效。
  </div>

  <div class="section">
    <div class="card">
      <div class="card-head">
        <h2>全局上限</h2>
        <span class="pill-badge">保存后立即写入当前生效配置</span>
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
      <div class="legend">
        <span class="mini default">默认规则</span>
        <span class="mini added">新增规则</span>
        <span class="mini custom">单项上限</span>
        <span class="mini removed">删除候选</span>
        <span class="mini error">校验错误</span>
      </div>
    </div>

    <div class="card primary">
      <div class="card-head">
        <div>
          <h2>命令规则</h2>
          <p class="desc">默认规则和当前生效规则合并显示。双击胶囊即可原地修改。</p>
        </div>
        <div class="card-side">
          <span class="pill-badge" id="commandCount">0 项</span>
          <button class="add-inline-btn" id="cmdAddBtn" type="button"><span class="plus">+</span><span>新增命令规则</span></button>
        </div>
      </div>
      <div class="canvas" id="cmdCanvas"></div>
      <div class="validation" id="cmdValidation"></div>
    </div>

    <div class="card primary">
      <div class="card-head">
        <div>
          <h2>程序组规则</h2>
          <p class="desc">浏览器、IDE 和工具进程组统一在这里查看和编辑。</p>
        </div>
        <div class="card-side">
          <span class="pill-badge" id="groupCount">0 项</span>
          <button class="add-inline-btn" id="grpAddBtn" type="button"><span class="plus">+</span><span>新增程序组规则</span></button>
        </div>
      </div>
      <div class="canvas" id="grpCanvas"></div>
      <div class="validation" id="grpValidation"></div>
    </div>
  </div>
</div>

<script>
const EPS = 0.0001;
const THEME_KEY = 'remem-theme';
const state = {
  nextId: 1,
  baseLimits: { command: 2, group: 6 },
  activeLimits: { command: 2, group: 6 },
  globalLimits: { command: 2, group: 6 },
  defaults: { commands: [], groups: [] },
  effective: { commands: [], groups: [] },
  effectiveLimits: { commands: {}, groups: {} },
  draft: { commands: [], groups: [] },
  editing: null,
  theme: 'light',
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

function nextPillId(kind) {
  const id = kind + '-' + state.nextId;
  state.nextId += 1;
  return id;
}

function updateStatus(kind, title, note) {
  const banner = document.getElementById('statusBanner');
  banner.className = 'banner ' + kind;
  document.getElementById('statusTitle').textContent = title;
  document.getElementById('statusNote').textContent = note;
}

function applyTheme(theme) {
  state.theme = theme === 'dark' ? 'dark' : 'light';
  document.body.dataset.theme = state.theme;
  const btn = document.getElementById('themeBtn');
  if (btn) {
    btn.textContent = state.theme === 'light' ? '切换夜间模式' : '切换日间模式';
  }
  try {
    localStorage.setItem(THEME_KEY, state.theme);
  } catch (_) {}
}

function initializeTheme() {
  let theme = 'light';
  try {
    const saved = localStorage.getItem(THEME_KEY);
    if (saved === 'dark' || saved === 'light') theme = saved;
  } catch (_) {}
  applyTheme(theme);
}

function activeItems(kind) {
  return (state.draft[kind] || []).filter((item) => !item.removed);
}

function buildMergedDraft(kind, defaults, effective, limitMap, globalLimit) {
  const defaultSet = new Set(defaults);
  const effectiveSet = new Set(effective);
  const mergedNames = [];
  defaults.forEach((name) => mergedNames.push(name));
  effective.forEach((name) => {
    if (!defaultSet.has(name)) mergedNames.push(name);
  });
  return mergedNames.map((name) => ({
    id: nextPillId(kind),
    baselineName: name,
    name,
    sourceDefault: defaultSet.has(name),
    removed: defaultSet.has(name) && !effectiveSet.has(name),
    custom: parsePositive((limitMap || {})[name]) > 0,
    limitValue: parsePositive((limitMap || {})[name]) > 0 ? fmtGiB((limitMap || {})[name]) : '',
  }));
}

function setFromRuleState(data) {
  state.defaults.commands = (data.defaultCommands || []).map(normalizeName).filter(Boolean);
  state.defaults.groups = (data.defaultGroups || []).map(normalizeName).filter(Boolean);
  state.effective.commands = (data.effectiveCommands || []).map(normalizeName).filter(Boolean);
  state.effective.groups = (data.effectiveGroups || []).map(normalizeName).filter(Boolean);
  state.baseLimits.command = parsePositive(data.baseCommandLimitGiB) || parsePositive(data.commandLimitGiB) || 2;
  state.baseLimits.group = parsePositive(data.baseGroupLimitGiB) || parsePositive(data.groupLimitGiB) || 6;
  state.activeLimits.command = parsePositive(data.commandLimitGiB) || state.baseLimits.command;
  state.activeLimits.group = parsePositive(data.groupLimitGiB) || state.baseLimits.group;
  state.globalLimits.command = state.activeLimits.command;
  state.globalLimits.group = state.activeLimits.group;
  state.effectiveLimits.commands = data.commandLimitsGiB || {};
  state.effectiveLimits.groups = data.groupLimitsGiB || {};
  state.draft.commands = buildMergedDraft('commands', state.defaults.commands, state.effective.commands, state.effectiveLimits.commands, state.globalLimits.command);
  state.draft.groups = buildMergedDraft('groups', state.defaults.groups, state.effective.groups, state.effectiveLimits.groups, state.globalLimits.group);
  state.editing = null;
  document.getElementById('globalCommandLimit').value = fmtGiB(state.globalLimits.command);
  document.getElementById('globalGroupLimit').value = fmtGiB(state.globalLimits.group);
  document.getElementById('baseLimitsHint').textContent =
    '环境默认上限: 命令 ' + fmtGiB(state.baseLimits.command) + 'GiB / 程序组 ' + fmtGiB(state.baseLimits.group) +
    'GiB。单项上限留空时跟随当前全局上限。';
  document.getElementById('meta').textContent = '规则文件: ' + (data.configPath || '(not set)');
  renderAll();
}

function getIssues(kind) {
  const issues = [];
  const buckets = new Map();
  (state.draft[kind] || []).forEach((item, idx) => {
    if (item.removed) return;
    const name = normalizeName(item.name);
    if (!name) return;
    if (!buckets.has(name)) buckets.set(name, []);
    buckets.get(name).push(idx);
    if ((item.limitValue || '').trim() !== '' && parsePositive(item.limitValue) <= 0) {
      issues.push({ idx, type: 'invalid-limit', message: name + ' 的单项上限必须大于 0' });
    }
  });
  for (const [name, indexes] of buckets.entries()) {
    if (indexes.length > 1) {
      indexes.forEach((idx) => issues.push({ idx, type: 'duplicate', message: '重复规则: ' + name }));
    }
  }
  return issues;
}

function hasValidationErrors() {
  return getIssues('commands').length > 0 || getIssues('groups').length > 0;
}

function createCurrentPatch() {
  const defaultCommandSet = new Set(state.defaults.commands);
  const defaultGroupSet = new Set(state.defaults.groups);
  return {
    limits: {
      commandGiB: Math.abs(state.activeLimits.command - state.baseLimits.command) > EPS ? roundGiB(state.activeLimits.command) : 0,
      groupGiB: Math.abs(state.activeLimits.group - state.baseLimits.group) > EPS ? roundGiB(state.activeLimits.group) : 0,
    },
    commands: {
      add: state.effective.commands.filter((name) => !defaultCommandSet.has(name)),
      remove: state.defaults.commands.filter((name) => !state.effective.commands.includes(name)),
      limitsGiB: Object.assign({}, state.effectiveLimits.commands || {}),
    },
    groups: {
      add: state.effective.groups.filter((name) => !defaultGroupSet.has(name)),
      remove: state.defaults.groups.filter((name) => !state.effective.groups.includes(name)),
      limitsGiB: Object.assign({}, state.effectiveLimits.groups || {}),
    },
  };
}

function createDraftPatch() {
  const globalCommand = parsePositive(document.getElementById('globalCommandLimit').value) || state.baseLimits.command;
  const globalGroup = parsePositive(document.getElementById('globalGroupLimit').value) || state.baseLimits.group;
  const commandItems = activeItems('commands').map((item) => ({
    name: normalizeName(item.name),
    custom: parsePositive(item.limitValue) > 0,
    limit: parsePositive(item.limitValue) > 0 ? parsePositive(item.limitValue) : globalCommand,
  })).filter((item) => item.name);
  const groupItems = activeItems('groups').map((item) => ({
    name: normalizeName(item.name),
    custom: parsePositive(item.limitValue) > 0,
    limit: parsePositive(item.limitValue) > 0 ? parsePositive(item.limitValue) : globalGroup,
  })).filter((item) => item.name);

  const commandNames = Array.from(new Set(commandItems.map((item) => item.name)));
  const groupNames = Array.from(new Set(groupItems.map((item) => item.name)));
  const defaultCommandSet = new Set(state.defaults.commands);
  const defaultGroupSet = new Set(state.defaults.groups);
  const commandSet = new Set(commandNames);
  const groupSet = new Set(groupNames);
  const cmdLimitsGiB = {};
  commandItems.forEach((item) => {
    if (item.custom && Math.abs(item.limit - globalCommand) > EPS) cmdLimitsGiB[item.name] = roundGiB(item.limit);
  });
  const grpLimitsGiB = {};
  groupItems.forEach((item) => {
    if (item.custom && Math.abs(item.limit - globalGroup) > EPS) grpLimitsGiB[item.name] = roundGiB(item.limit);
  });
  return {
    limits: {
      commandGiB: Math.abs(globalCommand - state.baseLimits.command) > EPS ? roundGiB(globalCommand) : 0,
      groupGiB: Math.abs(globalGroup - state.baseLimits.group) > EPS ? roundGiB(globalGroup) : 0,
    },
    commands: {
      add: commandNames.filter((name) => !defaultCommandSet.has(name)),
      remove: state.defaults.commands.filter((name) => !commandSet.has(name)),
      limitsGiB: cmdLimitsGiB,
    },
    groups: {
      add: groupNames.filter((name) => !defaultGroupSet.has(name)),
      remove: state.defaults.groups.filter((name) => !groupSet.has(name)),
      limitsGiB: grpLimitsGiB,
    },
  };
}

function updateBanner() {
  if (hasValidationErrors()) {
    updateStatus('error', '存在未提交更改', '请先修正重复名称或非法上限，然后再保存。');
    document.getElementById('saveBtn').disabled = true;
    return;
  }
  const dirty = JSON.stringify(createDraftPatch()) !== JSON.stringify(createCurrentPatch());
  document.getElementById('saveBtn').disabled = false;
  if (dirty) {
    updateStatus('dirty', '存在未提交更改', '当前胶囊内容尚未写入规则文件，保存后才会立即生效。');
  } else {
    updateStatus('saved', '所有更改已保存', '当前页面内容与已生效规则一致。');
  }
}

function updateDirtyStatus() {
  updateBanner();
}

function enterEdit(kind, id) {
  if (state.editing && state.editing.kind === kind && state.editing.id === id) return;
  const item = (state.draft[kind] || []).find((it) => it.id === id);
  if (!item) return;
  state.editing = {
    kind,
    id,
    snapshot: {
      name: item.name,
      limitValue: item.limitValue,
      removed: item.removed,
    },
  };
  renderAll();
  requestAnimationFrame(() => {
    const target = document.querySelector('[data-edit-name="' + id + '"]');
    if (target) target.focus();
  });
}

function cancelEdit() {
  if (!state.editing) return;
  const { kind, id, snapshot } = state.editing;
  const item = (state.draft[kind] || []).find((it) => it.id === id);
  if (item) {
    item.name = snapshot.name;
    item.limitValue = snapshot.limitValue;
    item.removed = snapshot.removed;
    if (!normalizeName(item.name) && !item.sourceDefault && !item.baselineName) {
      state.draft[kind] = state.draft[kind].filter((it) => it.id !== id);
    }
  }
  state.editing = null;
  renderAll();
}

function commitEdit() {
  if (!state.editing) return;
  const { kind, id } = state.editing;
  const item = (state.draft[kind] || []).find((it) => it.id === id);
  if (!item) {
    state.editing = null;
    renderAll();
    return;
  }
  item.name = normalizeName(item.name);
  if (!item.name) {
    if (item.sourceDefault) {
      item.name = item.baselineName;
      item.removed = true;
      item.limitValue = '';
    } else {
      state.draft[kind] = state.draft[kind].filter((it) => it.id !== id);
    }
  }
  state.editing = null;
  renderAll();
}

function toggleRemove(kind, id) {
  const items = state.draft[kind] || [];
  const item = items.find((it) => it.id === id);
  if (!item) return;
  if (item.sourceDefault) {
    item.removed = !item.removed;
    if (item.removed) {
      item.name = item.baselineName;
      item.limitValue = '';
    }
  } else {
    state.draft[kind] = items.filter((it) => it.id !== id);
  }
  if (state.editing && state.editing.id === id && state.editing.kind === kind) {
    state.editing = null;
  }
  renderAll();
}

function addPill(kind) {
  if (state.editing) commitEdit();
  const item = {
    id: nextPillId(kind),
    baselineName: '',
    name: '',
    sourceDefault: false,
    removed: false,
    custom: false,
    limitValue: '',
  };
  state.draft[kind].unshift(item);
  enterEdit(kind, item.id);
}

function pillClasses(kind, item, idx, issues) {
  const rowIssues = issues.get(idx) || [];
  const duplicate = rowIssues.some((it) => it.type === 'duplicate');
  const invalid = rowIssues.some((it) => it.type === 'invalid-limit');
  const editing = state.editing && state.editing.kind === kind && state.editing.id === item.id;
  const classes = ['pill'];
  if (item.sourceDefault) classes.push('default-item');
  if (!item.sourceDefault) classes.push('added-item');
  if (parsePositive(item.limitValue) > 0) classes.push('custom-item');
  if (item.removed) classes.push('removed-item');
  if (duplicate) classes.push('duplicate-item');
  if (invalid) classes.push('invalid-item');
  if (editing) classes.push('editing');
  return classes.join(' ');
}

function renderCanvas(kind) {
  const canvas = document.getElementById(kind === 'commands' ? 'cmdCanvas' : 'grpCanvas');
  const countEl = document.getElementById(kind === 'commands' ? 'commandCount' : 'groupCount');
  const validationEl = document.getElementById(kind === 'commands' ? 'cmdValidation' : 'grpValidation');
  const issues = getIssues(kind);
  const issueMap = new Map();
  issues.forEach((it) => {
    if (!issueMap.has(it.idx)) issueMap.set(it.idx, []);
    issueMap.get(it.idx).push(it);
  });
  canvas.innerHTML = '';
  countEl.textContent = activeItems(kind).filter((item) => normalizeName(item.name)).length + ' 项';

  state.draft[kind].forEach((item, idx) => {
    const editing = state.editing && state.editing.kind === kind && state.editing.id === item.id;
    const rowIssues = issueMap.get(idx) || [];
    const duplicate = rowIssues.some((it) => it.type === 'duplicate');
    const invalid = rowIssues.some((it) => it.type === 'invalid-limit');
    const pill = document.createElement('div');
    pill.className = pillClasses(kind, item, idx, issueMap);
    pill.dataset.kind = kind;
    pill.dataset.id = item.id;

    if (editing) {
      const edit = document.createElement('div');
      edit.className = 'pill-edit';
      const grid = document.createElement('div');
      grid.className = 'edit-grid';

      const name = document.createElement('input');
      name.className = 'input' + (duplicate ? ' duplicate' : '');
      name.type = 'text';
      name.value = item.name;
      name.placeholder = kind === 'commands' ? '输入命令名' : '输入程序组名';
      name.dataset.editName = item.id;
      name.addEventListener('input', () => {
        item.name = name.value;
        updateDirtyStatus();
      });
      name.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          commitEdit();
        }
        if (e.key === 'Escape') {
          e.preventDefault();
          cancelEdit();
        }
      });

      const limit = document.createElement('input');
      limit.className = 'input num' + (invalid ? ' invalid' : '');
      limit.type = 'number';
      limit.min = '0.1';
      limit.step = '0.1';
      limit.placeholder = fmtGiB(kind === 'commands' ? state.globalLimits.command : state.globalLimits.group);
      limit.value = item.limitValue || '';
      limit.addEventListener('input', () => {
        item.limitValue = limit.value;
        updateDirtyStatus();
      });
      limit.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          commitEdit();
        }
        if (e.key === 'Escape') {
          e.preventDefault();
          cancelEdit();
        }
      });

      grid.appendChild(name);
      grid.appendChild(limit);

      const actions = document.createElement('div');
      actions.className = 'edit-actions';
      const tags = document.createElement('div');
      tags.className = 'pill-meta';
      if (item.sourceDefault) {
        const sourceTag = document.createElement('span');
        sourceTag.className = 'tag default';
        sourceTag.textContent = '默认';
        tags.appendChild(sourceTag);
      }

      const tools = document.createElement('div');
      tools.className = 'edit-tools';

      const reset = document.createElement('button');
      reset.className = 'tiny-btn warn';
      reset.type = 'button';
      reset.textContent = '回退全局';
      reset.addEventListener('click', (e) => {
        e.stopPropagation();
        item.limitValue = '';
        limit.value = '';
        updateDirtyStatus();
      });

      const cancel = document.createElement('button');
      cancel.className = 'tiny-btn';
      cancel.type = 'button';
      cancel.textContent = '取消';
      cancel.addEventListener('click', (e) => {
        e.stopPropagation();
        cancelEdit();
      });

      const remove = document.createElement('button');
      remove.className = 'tiny-btn danger';
      remove.type = 'button';
      remove.textContent = item.sourceDefault ? '删除候选' : '删除';
      remove.addEventListener('click', (e) => {
        e.stopPropagation();
        toggleRemove(kind, item.id);
      });

      tools.appendChild(reset);
      tools.appendChild(cancel);
      tools.appendChild(remove);
      actions.appendChild(tags);
      actions.appendChild(tools);
      edit.appendChild(grid);
      edit.appendChild(actions);
      pill.appendChild(edit);
    } else {
      pill.addEventListener('dblclick', () => enterEdit(kind, item.id));
      const view = document.createElement('div');
      view.className = 'pill-view';

      const name = document.createElement('span');
      name.className = 'pill-name';
      name.textContent = item.removed ? item.baselineName : normalizeName(item.name || item.baselineName);
      view.appendChild(name);

      const meta = document.createElement('div');
      meta.className = 'pill-meta';
      const sourceTag = document.createElement('span');
      sourceTag.className = 'tag ' + (item.sourceDefault ? 'default' : 'added');
      sourceTag.textContent = item.sourceDefault ? '默认' : '新增';
      meta.appendChild(sourceTag);

      if (parsePositive(item.limitValue) > 0 && !item.removed) {
        const limitTag = document.createElement('span');
        limitTag.className = 'tag limit';
        limitTag.textContent = fmtGiB(item.limitValue) + 'GiB';
        meta.appendChild(limitTag);
      }

      if (item.removed) {
        const removedTag = document.createElement('span');
        removedTag.className = 'tag removed';
        removedTag.textContent = '删除候选';
        meta.appendChild(removedTag);
      } else if (duplicate) {
        const dupTag = document.createElement('span');
        dupTag.className = 'tag error';
        dupTag.textContent = '重复';
        meta.appendChild(dupTag);
      } else if (invalid) {
        const badTag = document.createElement('span');
        badTag.className = 'tag error';
        badTag.textContent = '上限无效';
        meta.appendChild(badTag);
      } else if (!parsePositive(item.limitValue)) {
        const globalTag = document.createElement('span');
        globalTag.className = 'tag';
        globalTag.textContent = '跟随全局';
        meta.appendChild(globalTag);
      }

      pill.appendChild(view);
      pill.appendChild(meta);
    }

    canvas.appendChild(pill);
  });

  if (!issues.length) {
    validationEl.className = 'validation';
    validationEl.textContent = '双击胶囊直接修改，删除默认规则会先变成“删除候选”，保存后才真正生效。';
  } else {
    validationEl.className = 'validation error';
    validationEl.textContent = issues[0].message;
  }
}

function renderAll() {
  renderCanvas('commands');
  renderCanvas('groups');
  updateBanner();
}

async function loadRules(message) {
  const res = await fetch('/api/rules', { cache: 'no-store' });
  if (!res.ok) throw new Error(await res.text());
  const data = await res.json();
  setFromRuleState(data);
  if (message) updateStatus('saved', message, '页面已同步为当前正在生效的规则。');
}

async function savePatch(patch, title, note) {
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
  updateStatus('saved', title, note);
}

document.getElementById('saveBtn').addEventListener('click', async () => {
  await savePatch(createDraftPatch(), '所有更改已保存', '保存成功，规则已热加载生效。');
});

document.getElementById('reloadBtn').addEventListener('click', async () => {
  await loadRules('所有更改已保存');
});

document.getElementById('restoreBtn').addEventListener('click', async () => {
  const patch = {
    limits: { commandGiB: 0, groupGiB: 0 },
    commands: { add: [], remove: [], limitsGiB: {} },
    groups: { add: [], remove: [], limitsGiB: {} },
  };
  await savePatch(patch, '已恢复默认并生效', '所有规则已回到默认集合并立即生效。');
});

document.getElementById('themeBtn').addEventListener('click', () => {
  applyTheme(state.theme === 'light' ? 'dark' : 'light');
});
document.getElementById('cmdAddBtn').addEventListener('mousedown', (e) => {
  e.stopPropagation();
  e.preventDefault();
  addPill('commands');
});
document.getElementById('grpAddBtn').addEventListener('mousedown', (e) => {
  e.stopPropagation();
  e.preventDefault();
  addPill('groups');
});

document.getElementById('globalCommandLimit').addEventListener('input', renderAll);
document.getElementById('globalGroupLimit').addEventListener('input', renderAll);

document.addEventListener('mousedown', (e) => {
  if (!state.editing) return;
  if (e.target.closest('.pill-add')) return;
  const active = document.querySelector('.pill.editing');
  if (active && !active.contains(e.target)) {
    commitEdit();
  }
});

initializeTheme();
loadRules('所有更改已保存').catch((e) => {
  updateStatus('error', '加载失败', String(e));
});

</script>
+</body>
+</html>`
