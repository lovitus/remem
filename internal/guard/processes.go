package guard

import (
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"remem/internal/config"
)

type Proc struct {
	PID      int32
	PPID     int32
	Name     string
	NameNorm string
	Exe      string
	ExeNorm  string
	RSSBytes uint64
}

type procNameIndex map[string][]int32

type ProcSnapshotStats struct {
	ProcessesSeen int
	RelevantCount int
	List          time.Duration
	Metadata      time.Duration
	Index         time.Duration
	Relevant      time.Duration
	RSS           time.Duration
	Total         time.Duration
}

func snapshotProcesses(commandWatchlist map[string]struct{}, groups []config.GroupSpec) ([]Proc, map[int32]Proc, map[int32][]int32, procNameIndex, ProcSnapshotStats, error) {
	start := time.Now()
	procs, stats, err := listProcessMetadata()
	if err != nil {
		stats.Total = time.Since(start)
		return nil, nil, nil, nil, stats, err
	}

	byPID := make(map[int32]Proc, len(procs))
	children := make(map[int32][]int32, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}

	indexStart := time.Now()
	nameIndex := buildProcNameIndex(byPID)
	stats.Index = time.Since(indexStart)

	relevantStart := time.Now()
	relevant := make(map[int32]struct{}, len(byPID)/2)
	for pid, p := range byPID {
		if _, ok := commandWatchlist[p.NameNorm]; ok {
			relevant[pid] = struct{}{}
		}
	}
	for _, g := range groups {
		roots := findRootPIDs(g, byPID, nameIndex)
		for _, root := range roots {
			markSubtreeRelevant(root, children, relevant)
		}
	}
	stats.RelevantCount = len(relevant)
	stats.Relevant = time.Since(relevantStart)

	rssStart := time.Now()
	for pid := range relevant {
		h, err := process.NewProcess(pid)
		if err != nil || h == nil {
			continue
		}
		mem, err := h.MemoryInfo()
		if err != nil || mem == nil {
			continue
		}
		p := byPID[pid]
		p.RSSBytes = mem.RSS
		byPID[pid] = p
	}
	for i := range procs {
		if p, ok := byPID[procs[i].PID]; ok {
			procs[i] = p
		}
	}
	stats.RSS = time.Since(rssStart)
	stats.Total = time.Since(start)
	return procs, byPID, children, nameIndex, stats, nil
}

func markSubtreeRelevant(root int32, children map[int32][]int32, relevant map[int32]struct{}) {
	queue := []int32{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, seen := relevant[pid]; seen {
			continue
		}
		relevant[pid] = struct{}{}
		for _, ch := range children[pid] {
			queue = append(queue, ch)
		}
	}
}

func normalizeProcName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	return n
}

func buildProcNameIndex(byPID map[int32]Proc) procNameIndex {
	if len(byPID) == 0 {
		return nil
	}
	index := make(procNameIndex, len(byPID))
	for pid, p := range byPID {
		if p.NameNorm == "" {
			continue
		}
		index[p.NameNorm] = append(index[p.NameNorm], pid)
	}
	return index
}
