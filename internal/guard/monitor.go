package guard

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"remem/internal/config"
	"remem/internal/logbuf"
)

type Stats struct {
	LastRunAt       time.Time `json:"lastRunAt"`
	LastDurationMs  int64     `json:"lastDurationMs"`
	LastSource      string    `json:"lastSource"`
	LastProcessSeen int       `json:"lastProcessSeen"`
	LastKilled      int       `json:"lastKilled"`
	LastSummary     string    `json:"lastSummary"`
	Running         bool      `json:"running"`
}

type RuleState struct {
	ConfigPath          string             `json:"configPath"`
	EnvPatch            config.FileConfig  `json:"envPatch"`
	CustomPatch         config.FileConfig  `json:"customPatch"`
	DefaultCommands     []string           `json:"defaultCommands"`
	DefaultGroups       []string           `json:"defaultGroups"`
	EffectiveCommands   []string           `json:"effectiveCommands"`
	EffectiveGroups     []string           `json:"effectiveGroups"`
	BaseCommandLimitGiB float64            `json:"baseCommandLimitGiB"`
	BaseGroupLimitGiB   float64            `json:"baseGroupLimitGiB"`
	CommandLimitGiB     float64            `json:"commandLimitGiB"`
	GroupLimitGiB       float64            `json:"groupLimitGiB"`
	CommandLimitsGiB    map[string]float64 `json:"commandLimitsGiB"`
	GroupLimitsGiB      map[string]float64 `json:"groupLimitsGiB"`
}

type ruleSnapshot struct {
	commandWatchlist map[string]struct{}
	groups           []config.GroupSpec
	commandLimit     uint64
	groupLimit       uint64
	commandLimits    map[string]uint64
	groupLimits      map[string]uint64
	ruleState        RuleState
}

type Monitor struct {
	cfg       config.Config
	logs      *logbuf.Buffer
	triggerCh chan string

	statsMu sync.RWMutex
	stats   Stats

	rules atomic.Pointer[ruleSnapshot]

	heatMu      sync.Mutex
	groupCursor int
	hotGroups   map[string]time.Time

	perfMu    sync.Mutex
	perfUntil time.Time
}

func New(cfg config.Config, logs *logbuf.Buffer) *Monitor {
	customPatch := config.NormalizeFileConfig(cfg.CustomPatch)
	snap := buildRuleSnapshot(cfg, customPatch, cfg.CommandWatchlist, cfg.Groups)
	m := &Monitor{
		cfg:       cfg,
		logs:      logs,
		triggerCh: make(chan string, 1),
		hotGroups: make(map[string]time.Time),
	}
	m.rules.Store(snap)
	return m
}

func (m *Monitor) Start(ctx context.Context) {
	go m.loop(ctx)
}

func (m *Monitor) loop(ctx context.Context) {
	m.runScan("startup")

	timer := time.NewTimer(m.cfg.ScanInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case src := <-m.triggerCh:
			m.runScan(src)
			resetTimer(timer, m.cfg.ScanInterval)
		case <-timer.C:
			m.runScan("ticker")
			resetTimer(timer, m.cfg.ScanInterval)
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func (m *Monitor) TriggerScan(source string) {
	select {
	case m.triggerCh <- source:
	default:
		m.logs.AddRoutinef("scan request dropped (%s): queue full", source)
	}
}

func (m *Monitor) UpdateCustomPatch(patch config.FileConfig, persist bool) error {
	patch = config.NormalizeFileConfig(patch)
	commands, groups := config.BuildRuleSet(m.cfg.EnvPatch, patch)
	if persist {
		if err := config.SaveFileConfig(m.cfg.ConfigPath, patch); err != nil {
			return err
		}
	}

	m.rules.Store(buildRuleSnapshot(m.cfg, patch, commands, groups))

	m.heatMu.Lock()
	m.groupCursor = 0
	m.hotGroups = make(map[string]time.Time)
	m.heatMu.Unlock()

	commandLimit, groupLimit, _, _ := config.BuildLimitSet(m.cfg.CommandLimitBytes, m.cfg.GroupLimitBytes, patch, commands, groups)
	m.logs.AddActionf("rules updated: commands=%d groups=%d command_limit=%s group_limit=%s", len(commands), len(groups), formatGiB(commandLimit), formatGiB(groupLimit))
	if persist && m.cfg.ConfigPath != "" {
		m.logs.AddActionf("rules persisted: %s", m.cfg.ConfigPath)
	}
	return nil
}

func (m *Monitor) RuleState() RuleState {
	snap := m.currentRules()
	return cloneRuleState(snap.ruleState)
}

func (m *Monitor) runScan(source string) {
	m.setRunning(true)
	defer m.setRunning(false)
	m.scan(source)
}

func (m *Monitor) scan(source string) {
	start := time.Now()
	perfActive := m.perfCaptureActive(start)
	rulesStart := time.Now()
	rules, groupsToScan := m.snapshotRulesForScan(source)
	rulesDur := time.Since(rulesStart)

	procStart := time.Now()
	procs, byPID, children, nameIndex, procStats, err := snapshotProcesses(rules.commandWatchlist, groupsToScan)
	procDur := time.Since(procStart)
	if err != nil {
		m.logs.AddErrorf("scan error (%s): %v", source, err)
		m.updateStats(start, source, 0, 0, fmt.Sprintf("scan error: %v", err))
		return
	}

	killedPIDs := make(map[int32]struct{})
	killed := 0

	commandStart := time.Now()
	for _, p := range procs {
		if p.PID == int32(os.Getpid()) {
			continue
		}
		if _, ok := rules.commandWatchlist[p.NameNorm]; !ok {
			continue
		}
		limit := rules.commandLimit
		if ov, ok := rules.commandLimits[p.NameNorm]; ok && ov > 0 {
			limit = ov
		}
		if p.RSSBytes <= limit {
			continue
		}
		reason := fmt.Sprintf("command cap: %s pid=%d rss=%s limit=%s", p.Name, p.PID, formatGiB(p.RSSBytes), formatGiB(limit))
		if m.killPID(p.PID, reason, killedPIDs) {
			killed++
		}
	}
	commandDur := time.Since(commandStart)

	groupStart := time.Now()
	for _, g := range groupsToScan {
		roots := findRootPIDs(g, byPID, nameIndex)
		if len(roots) == 0 {
			m.touchGroupHeat(g.Name, 0, rules.groupLimit)
			continue
		}

		members := collectGroupMembers(roots, children, byPID)
		if len(members) == 0 {
			m.touchGroupHeat(g.Name, 0, rules.groupLimit)
			continue
		}

		total := uint64(0)
		for _, p := range members {
			total += p.RSSBytes
		}

		limit := rules.groupLimit
		if ov, ok := rules.groupLimits[normalizeProcName(g.Name)]; ok && ov > 0 {
			limit = ov
		}

		m.touchGroupHeat(g.Name, total, limit)
		if total <= limit {
			continue
		}

		candidate, ok := largestKillableChild(g, roots, members)
		if !ok {
			m.logs.AddErrorf("group cap hit but no kill candidate: group=%s total=%s", g.Name, formatGiB(total))
			continue
		}

		reason := fmt.Sprintf("group cap: %s total=%s > limit=%s, kill child=%s pid=%d rss=%s", g.Name, formatGiB(total), formatGiB(limit), candidate.Name, candidate.PID, formatGiB(candidate.RSSBytes))
		if m.killPID(candidate.PID, reason, killedPIDs) {
			killed++
		}
	}
	groupDur := time.Since(groupStart)

	dur := time.Since(start)
	if perfActive {
		m.logs.AddActionf("[perf] source=%s rules=%s process_total=%s process_list=%s process_meta=%s process_index=%s process_relevant=%s process_rss=%s command_eval=%s group_eval=%s total=%s seen=%d relevant=%d groups=%d killed=%d",
			source,
			rulesDur.Truncate(time.Millisecond),
			procDur.Truncate(time.Millisecond),
			procStats.List.Truncate(time.Millisecond),
			procStats.Metadata.Truncate(time.Millisecond),
			procStats.Index.Truncate(time.Millisecond),
			procStats.Relevant.Truncate(time.Millisecond),
			procStats.RSS.Truncate(time.Millisecond),
			commandDur.Truncate(time.Millisecond),
			groupDur.Truncate(time.Millisecond),
			dur.Truncate(time.Millisecond),
			procStats.ProcessesSeen,
			len(procs),
			len(groupsToScan),
			killed,
		)
	}
	summary := fmt.Sprintf("scan ok (%s): procs=%d groups=%d killed=%d duration=%s", source, len(procs), len(groupsToScan), killed, dur.Truncate(time.Millisecond))
	m.logs.AddRoutine(summary)
	m.updateStats(start, source, len(procs), killed, summary)
}

func (m *Monitor) snapshotRulesForScan(source string) (*ruleSnapshot, []config.GroupSpec) {
	rules := m.currentRules()
	allGroups := rules.groups
	if len(allGroups) == 0 {
		return rules, nil
	}
	if source != "ticker" {
		return rules, allGroups
	}

	m.heatMu.Lock()
	defer m.heatMu.Unlock()

	now := time.Now()
	for name, exp := range m.hotGroups {
		if now.After(exp) {
			delete(m.hotGroups, name)
		}
	}

	perScan := m.cfg.GroupsPerScan
	if perScan <= 0 || perScan >= len(allGroups) {
		return rules, allGroups
	}

	selected := make([]config.GroupSpec, 0, perScan+len(m.hotGroups))
	selectedSet := make(map[string]struct{}, perScan+len(m.hotGroups))

	for i := 0; i < perScan; i++ {
		idx := (m.groupCursor + i) % len(allGroups)
		g := allGroups[idx]
		selected = append(selected, g)
		selectedSet[g.Name] = struct{}{}
	}
	m.groupCursor = (m.groupCursor + perScan) % len(allGroups)

	for _, g := range allGroups {
		if _, hot := m.hotGroups[g.Name]; !hot {
			continue
		}
		if _, exists := selectedSet[g.Name]; exists {
			continue
		}
		selected = append(selected, g)
		selectedSet[g.Name] = struct{}{}
	}

	return rules, selected
}

func (m *Monitor) touchGroupHeat(groupName string, total uint64, limit uint64) {
	m.heatMu.Lock()
	defer m.heatMu.Unlock()
	if m.cfg.GroupHotThresholdRate <= 0 || m.cfg.GroupHotTTL <= 0 {
		return
	}
	threshold := uint64(float64(limit) * m.cfg.GroupHotThresholdRate)
	if total >= threshold {
		m.hotGroups[groupName] = time.Now().Add(m.cfg.GroupHotTTL)
	}
}

func (m *Monitor) currentRules() *ruleSnapshot {
	if snap := m.rules.Load(); snap != nil {
		return snap
	}
	return &ruleSnapshot{}
}

func (m *Monitor) StartPerfCapture(d time.Duration) time.Time {
	if d <= 0 {
		d = 10 * time.Second
	}
	until := time.Now().Add(d)
	m.perfMu.Lock()
	m.perfUntil = until
	m.perfMu.Unlock()
	return until
}

func (m *Monitor) perfCaptureActive(now time.Time) bool {
	m.perfMu.Lock()
	defer m.perfMu.Unlock()
	if m.perfUntil.IsZero() {
		return false
	}
	if now.After(m.perfUntil) {
		m.perfUntil = time.Time{}
		return false
	}
	return true
}

func findRootPIDs(group config.GroupSpec, byPID map[int32]Proc, nameIndex procNameIndex) []int32 {
	matched := make(map[int32]Proc)
	for _, matcher := range group.RootMatchers {
		for _, pid := range matchProcIDs(matcher, byPID, nameIndex) {
			if p, ok := byPID[pid]; ok {
				matched[pid] = p
			}
		}
	}
	if len(matched) == 0 {
		return nil
	}

	roots := make([]int32, 0, len(matched))
	for pid, p := range matched {
		if _, parentAlsoMatched := matched[p.PPID]; !parentAlsoMatched {
			roots = append(roots, pid)
		}
	}
	if len(roots) == 0 {
		for pid := range matched {
			roots = append(roots, pid)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	return roots
}

func matchProcIDs(matcher config.Matcher, byPID map[int32]Proc, nameIndex procNameIndex) []int32 {
	seen := make(map[int32]struct{})
	matched := make([]int32, 0, 8)
	if len(nameIndex) == 0 {
		for pid, p := range byPID {
			if !matcherMatches(matcher, p) {
				continue
			}
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			matched = append(matched, pid)
		}
		return matched
	}
	for nameNorm, pids := range nameIndex {
		if !matcherMatches(matcher, Proc{NameNorm: nameNorm}) {
			continue
		}
		for _, pid := range pids {
			if _, ok := byPID[pid]; !ok {
				continue
			}
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			matched = append(matched, pid)
		}
	}
	return matched
}

func collectGroupMembers(roots []int32, children map[int32][]int32, byPID map[int32]Proc) []Proc {
	seen := make(map[int32]struct{})
	queue := make([]int32, 0, len(roots))
	queue = append(queue, roots...)

	members := make([]Proc, 0, 32)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}

		p, ok := byPID[pid]
		if !ok {
			continue
		}
		members = append(members, p)
		for _, ch := range children[pid] {
			queue = append(queue, ch)
		}
	}
	return members
}

func largestKillableChild(group config.GroupSpec, roots []int32, members []Proc) (Proc, bool) {
	rootSet := make(map[int32]struct{}, len(roots))
	for _, pid := range roots {
		rootSet[pid] = struct{}{}
	}

	var best Proc
	bestSet := false
	for _, p := range members {
		if _, isRoot := rootSet[p.PID]; isRoot {
			continue
		}
		if groupProtectMatch(group, p) {
			continue
		}
		if !bestSet || p.RSSBytes > best.RSSBytes {
			best = p
			bestSet = true
		}
	}

	if bestSet {
		return best, true
	}
	return Proc{}, false
}

func (m *Monitor) killPID(pid int32, reason string, killedPIDs map[int32]struct{}) bool {
	if _, exists := killedPIDs[pid]; exists {
		return false
	}
	if pid == int32(os.Getpid()) {
		return false
	}

	p, err := process.NewProcess(pid)
	if err != nil {
		m.logs.AddErrorf("kill skipped pid=%d: cannot open process (%v)", pid, err)
		return false
	}
	if err := p.Kill(); err != nil {
		m.logs.AddErrorf("kill failed pid=%d: %v", pid, err)
		return false
	}
	killedPIDs[pid] = struct{}{}
	m.logs.AddKillf("killed pid=%d: %s", pid, reason)
	return true
}

func (m *Monitor) Stats() Stats {
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()
	return m.stats
}

func (m *Monitor) updateStats(start time.Time, source string, procCount, killed int, summary string) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.stats.LastRunAt = time.Now()
	m.stats.LastDurationMs = time.Since(start).Milliseconds()
	m.stats.LastSource = source
	m.stats.LastProcessSeen = procCount
	m.stats.LastKilled = killed
	m.stats.LastSummary = summary
}

func (m *Monitor) setRunning(v bool) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.stats.Running = v
}

func cloneCommandSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func cloneGroups(in []config.GroupSpec) []config.GroupSpec {
	out := make([]config.GroupSpec, len(in))
	copy(out, in)
	return out
}

func cloneUint64Map(in map[string]uint64) map[string]uint64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func bytesMapToGiBMap(in map[string]uint64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = bytesToGiB(v)
	}
	return out
}

func buildRuleSnapshot(cfg config.Config, patch config.FileConfig, commands map[string]struct{}, groups []config.GroupSpec) *ruleSnapshot {
	commandLimit, groupLimit, commandLimits, groupLimits := config.BuildLimitSet(cfg.CommandLimitBytes, cfg.GroupLimitBytes, patch, commands, groups)

	commandWatchlist := cloneCommandSet(commands)
	groupSpecs := cloneGroups(groups)

	groupNames := make([]string, 0, len(groupSpecs))
	for _, g := range groupSpecs {
		groupNames = append(groupNames, g.Name)
	}
	sort.Strings(groupNames)

	commandNames := make([]string, 0, len(commandWatchlist))
	for name := range commandWatchlist {
		commandNames = append(commandNames, name)
	}
	sort.Strings(commandNames)

	return &ruleSnapshot{
		commandWatchlist: commandWatchlist,
		groups:           groupSpecs,
		commandLimit:     commandLimit,
		groupLimit:       groupLimit,
		commandLimits:    cloneUint64Map(commandLimits),
		groupLimits:      cloneUint64Map(groupLimits),
		ruleState: RuleState{
			ConfigPath:          cfg.ConfigPath,
			EnvPatch:            cfg.EnvPatch,
			CustomPatch:         patch,
			DefaultCommands:     config.DefaultCommandNames(),
			DefaultGroups:       config.DefaultGroupNames(),
			EffectiveCommands:   commandNames,
			EffectiveGroups:     groupNames,
			BaseCommandLimitGiB: bytesToGiB(cfg.CommandLimitBytes),
			BaseGroupLimitGiB:   bytesToGiB(cfg.GroupLimitBytes),
			CommandLimitGiB:     bytesToGiB(commandLimit),
			GroupLimitGiB:       bytesToGiB(groupLimit),
			CommandLimitsGiB:    bytesMapToGiBMap(commandLimits),
			GroupLimitsGiB:      bytesMapToGiBMap(groupLimits),
		},
	}
}

func cloneRuleState(in RuleState) RuleState {
	out := in
	out.DefaultCommands = append([]string(nil), in.DefaultCommands...)
	out.DefaultGroups = append([]string(nil), in.DefaultGroups...)
	out.EffectiveCommands = append([]string(nil), in.EffectiveCommands...)
	out.EffectiveGroups = append([]string(nil), in.EffectiveGroups...)
	if len(in.CommandLimitsGiB) > 0 {
		out.CommandLimitsGiB = make(map[string]float64, len(in.CommandLimitsGiB))
		for k, v := range in.CommandLimitsGiB {
			out.CommandLimitsGiB[k] = v
		}
	}
	if len(in.GroupLimitsGiB) > 0 {
		out.GroupLimitsGiB = make(map[string]float64, len(in.GroupLimitsGiB))
		for k, v := range in.GroupLimitsGiB {
			out.GroupLimitsGiB[k] = v
		}
	}
	return out
}
