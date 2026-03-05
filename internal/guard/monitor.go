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

	"rememguard/internal/config"
	"rememguard/internal/logbuf"
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

type Monitor struct {
	cfg     config.Config
	logs    *logbuf.Buffer
	running atomic.Bool

	statsMu sync.RWMutex
	stats   Stats
}

func New(cfg config.Config, logs *logbuf.Buffer) *Monitor {
	return &Monitor{cfg: cfg, logs: logs}
}

func (m *Monitor) Start(ctx context.Context) {
	m.TriggerScan("startup")

	ticker := time.NewTicker(m.cfg.ScanInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.TriggerScan("ticker")
			}
		}
	}()
}

func (m *Monitor) TriggerScan(source string) {
	if !m.running.CompareAndSwap(false, true) {
		m.logs.Addf("scan skipped (%s): previous scan still running", source)
		m.setRunning(true)
		return
	}
	m.setRunning(true)

	go func() {
		defer m.running.Store(false)
		defer m.setRunning(false)
		m.scan(source)
	}()
}

func (m *Monitor) scan(source string) {
	start := time.Now()
	procs, byPID, children, err := snapshotProcesses()
	if err != nil {
		m.logs.Addf("scan error (%s): %v", source, err)
		m.updateStats(start, source, 0, 0, fmt.Sprintf("scan error: %v", err))
		return
	}

	killedPIDs := make(map[int32]struct{})
	killed := 0

	// Rule 1: lightweight command hard cap (2 GiB by default).
	for _, p := range procs {
		if p.PID == int32(os.Getpid()) {
			continue
		}
		if _, ok := m.cfg.CommandWatchlist[p.NameNorm]; !ok {
			continue
		}
		if p.RSSBytes <= m.cfg.CommandLimitBytes {
			continue
		}
		reason := fmt.Sprintf("command cap: %s pid=%d rss=%s limit=%s", p.Name, p.PID, formatGiB(p.RSSBytes), formatGiB(m.cfg.CommandLimitBytes))
		if m.killPID(p.PID, reason, killedPIDs) {
			killed++
		}
	}

	// Rule 2: app-group cap (6 GiB by default), kill largest child to preserve UI.
	for _, g := range m.cfg.Groups {
		roots := findRootPIDs(g, byPID)
		if len(roots) == 0 {
			continue
		}

		members := collectGroupMembers(roots, children, byPID)
		if len(members) == 0 {
			continue
		}

		total := uint64(0)
		for _, p := range members {
			total += p.RSSBytes
		}
		if total <= m.cfg.GroupLimitBytes {
			continue
		}

		candidate, ok := largestKillableChild(g, roots, members)
		if !ok {
			m.logs.Addf("group cap hit but no kill candidate: group=%s total=%s", g.Name, formatGiB(total))
			continue
		}

		reason := fmt.Sprintf("group cap: %s total=%s > limit=%s, kill child=%s pid=%d rss=%s", g.Name, formatGiB(total), formatGiB(m.cfg.GroupLimitBytes), candidate.Name, candidate.PID, formatGiB(candidate.RSSBytes))
		if m.killPID(candidate.PID, reason, killedPIDs) {
			killed++
		}
	}

	dur := time.Since(start)
	summary := fmt.Sprintf("scan ok (%s): procs=%d killed=%d duration=%s", source, len(procs), killed, dur.Truncate(time.Millisecond))
	m.logs.Add(summary)
	m.updateStats(start, source, len(procs), killed, summary)
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
		m.logs.Addf("kill skipped pid=%d: cannot open process (%v)", pid, err)
		return false
	}
	if err := p.Kill(); err != nil {
		m.logs.Addf("kill failed pid=%d: %v", pid, err)
		return false
	}
	killedPIDs[pid] = struct{}{}
	m.logs.Addf("killed pid=%d: %s", pid, reason)
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
