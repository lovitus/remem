package guard

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
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

type Monitor struct {
	cfg       config.Config
	logs      *logbuf.Buffer
	triggerCh chan string

	statsMu sync.RWMutex
	stats   Stats

	rulesMu          sync.RWMutex
	commandWatchlist map[string]struct{}
	groups           []config.GroupSpec
	commandLimit     uint64
	groupLimit       uint64
	commandLimits    map[string]uint64
	groupLimits      map[string]uint64
	customPatch      config.FileConfig
	groupCursor      int
	hotGroups        map[string]time.Time
}

func New(cfg config.Config, logs *logbuf.Buffer) *Monitor {
	customPatch := config.NormalizeFileConfig(cfg.CustomPatch)
	commandLimit, groupLimit, commandLimits, groupLimits := config.BuildLimitSet(cfg.CommandLimitBytes, cfg.GroupLimitBytes, customPatch, cfg.CommandWatchlist, cfg.Groups)
	return &Monitor{
		cfg:              cfg,
		logs:             logs,
		triggerCh:        make(chan string, 1),
		commandWatchlist: cloneCommandSet(cfg.CommandWatchlist),
		groups:           cloneGroups(cfg.Groups),
		commandLimit:     commandLimit,
		groupLimit:       groupLimit,
		commandLimits:    commandLimits,
		groupLimits:      groupLimits,
		customPatch:      customPatch,
		hotGroups:        make(map[string]time.Time),
	}
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
	commandLimit, groupLimit, commandLimits, groupLimits := config.BuildLimitSet(m.cfg.CommandLimitBytes, m.cfg.GroupLimitBytes, patch, commands, groups)
	if persist {
		if err := config.SaveFileConfig(m.cfg.ConfigPath, patch); err != nil {
			return err
		}
	}

	m.rulesMu.Lock()
	m.commandWatchlist = cloneCommandSet(commands)
	m.groups = cloneGroups(groups)
	m.commandLimit = commandLimit
	m.groupLimit = groupLimit
	m.commandLimits = cloneUint64Map(commandLimits)
	m.groupLimits = cloneUint64Map(groupLimits)
	m.customPatch = patch
	m.groupCursor = 0
	m.hotGroups = make(map[string]time.Time)
	m.rulesMu.Unlock()

	m.logs.AddActionf("rules updated: commands=%d groups=%d command_limit=%s group_limit=%s", len(commands), len(groups), formatGiB(commandLimit), formatGiB(groupLimit))
	if persist && m.cfg.ConfigPath != "" {
		m.logs.AddActionf("rules persisted: %s", m.cfg.ConfigPath)
	}
	return nil
}

func (m *Monitor) RuleState() RuleState {
	m.rulesMu.RLock()
	defer m.rulesMu.RUnlock()
	groupNames := make([]string, 0, len(m.groups))
	for _, g := range m.groups {
		groupNames = append(groupNames, g.Name)
	}
	sort.Strings(groupNames)

	commands := make([]string, 0, len(m.commandWatchlist))
	for k := range m.commandWatchlist {
		commands = append(commands, k)
	}
	sort.Strings(commands)
	return RuleState{
		ConfigPath:          m.cfg.ConfigPath,
		EnvPatch:            m.cfg.EnvPatch,
		CustomPatch:         m.customPatch,
		DefaultCommands:     config.DefaultCommandNames(),
		DefaultGroups:       config.DefaultGroupNames(),
		EffectiveCommands:   commands,
		EffectiveGroups:     groupNames,
		BaseCommandLimitGiB: bytesToGiB(m.cfg.CommandLimitBytes),
		BaseGroupLimitGiB:   bytesToGiB(m.cfg.GroupLimitBytes),
		CommandLimitGiB:     bytesToGiB(m.commandLimit),
		GroupLimitGiB:       bytesToGiB(m.groupLimit),
		CommandLimitsGiB:    bytesMapToGiBMap(m.commandLimits),
		GroupLimitsGiB:      bytesMapToGiBMap(m.groupLimits),
	}
}

func (m *Monitor) runScan(source string) {
	m.setRunning(true)
	defer m.setRunning(false)
	m.scan(source)
}

func (m *Monitor) scan(source string) {
	start := time.Now()
	commands, groupsToScan, commandLimit, groupLimit, commandLimits, groupLimits := m.snapshotRulesForScan(source)

	procs, byPID, children, err := snapshotProcesses(commands, groupsToScan)
	if err != nil {
		m.logs.AddErrorf("scan error (%s): %v", source, err)
		m.updateStats(start, source, 0, 0, fmt.Sprintf("scan error: %v", err))
		return
	}

	killedPIDs := make(map[int32]struct{})
	killed := 0

	for _, p := range procs {
		if p.PID == int32(os.Getpid()) {
			continue
		}
		if _, ok := commands[p.NameNorm]; !ok {
			continue
		}
		limit := commandLimit
		if ov, ok := commandLimits[p.NameNorm]; ok && ov > 0 {
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

	for _, g := range groupsToScan {
		roots := findRootPIDs(g, byPID)
		if len(roots) == 0 {
			m.touchGroupHeat(g.Name, 0, groupLimit)
			continue
		}

		members := collectGroupMembers(roots, children, byPID)
		if len(members) == 0 {
			m.touchGroupHeat(g.Name, 0, groupLimit)
			continue
		}

		total := uint64(0)
		for _, p := range members {
			total += p.RSSBytes
		}

		limit := groupLimit
		if ov, ok := groupLimits[normalizeProcName(g.Name)]; ok && ov > 0 {
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

	dur := time.Since(start)
	summary := fmt.Sprintf("scan ok (%s): procs=%d groups=%d killed=%d duration=%s", source, len(procs), len(groupsToScan), killed, dur.Truncate(time.Millisecond))
	m.logs.AddRoutine(summary)
	m.updateStats(start, source, len(procs), killed, summary)
}

func (m *Monitor) snapshotRulesForScan(source string) (map[string]struct{}, []config.GroupSpec, uint64, uint64, map[string]uint64, map[string]uint64) {
	m.rulesMu.Lock()
	defer m.rulesMu.Unlock()

	commands := cloneCommandSet(m.commandWatchlist)
	allGroups := cloneGroups(m.groups)
	commandLimit := m.commandLimit
	groupLimit := m.groupLimit
	commandLimits := cloneUint64Map(m.commandLimits)
	groupLimits := cloneUint64Map(m.groupLimits)
	if len(allGroups) == 0 {
		return commands, nil, commandLimit, groupLimit, commandLimits, groupLimits
	}
	if source != "ticker" {
		return commands, allGroups, commandLimit, groupLimit, commandLimits, groupLimits
	}

	now := time.Now()
	for name, exp := range m.hotGroups {
		if now.After(exp) {
			delete(m.hotGroups, name)
		}
	}

	perScan := m.cfg.GroupsPerScan
	if perScan <= 0 || perScan >= len(allGroups) {
		return commands, allGroups, commandLimit, groupLimit, commandLimits, groupLimits
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

	return commands, selected, commandLimit, groupLimit, commandLimits, groupLimits
}

func (m *Monitor) touchGroupHeat(groupName string, total uint64, limit uint64) {
	m.rulesMu.Lock()
	defer m.rulesMu.Unlock()
	if m.cfg.GroupHotThresholdRate <= 0 || m.cfg.GroupHotTTL <= 0 {
		return
	}
	if limit == 0 {
		limit = m.groupLimit
	}
	threshold := uint64(float64(limit) * m.cfg.GroupHotThresholdRate)
	if total >= threshold {
		m.hotGroups[groupName] = time.Now().Add(m.cfg.GroupHotTTL)
	}
}

func findRootPIDs(group config.GroupSpec, byPID map[int32]Proc) []int32 {
	matched := make(map[int32]Proc)
	for pid, p := range byPID {
		if groupRootMatch(group, p) {
			matched[pid] = p
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
