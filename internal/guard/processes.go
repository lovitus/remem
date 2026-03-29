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
	List          time.Duration
	Metadata      time.Duration
	Index         time.Duration
	Relevant      time.Duration
	RSS           time.Duration
	Total         time.Duration
}

func snapshotProcesses(commandWatchlist map[string]struct{}, groups []config.GroupSpec) ([]Proc, map[int32]Proc, map[int32][]int32, procNameIndex, ProcSnapshotStats, error) {
	start := time.Now()
	var stats ProcSnapshotStats

	listStart := time.Now()
	ps, err := process.Processes()
	stats.List = time.Since(listStart)
	if err != nil {
		stats.Total = time.Since(start)
		return nil, nil, nil, nil, stats, err
	}
	stats.ProcessesSeen = len(ps)

	out := make([]Proc, 0, len(ps))
	byPID := make(map[int32]Proc, len(ps))
	children := make(map[int32][]int32, len(ps))
	byPIDHandle := make(map[int32]*process.Process, len(ps))

	metaStart := time.Now()
	for _, p := range ps {
		pid := p.Pid
		name, err := p.Name()
		if err != nil || strings.TrimSpace(name) == "" {
			continue
		}
		ppid, err := p.Ppid()
		if err != nil {
			continue
		}

		proc := Proc{
			PID:      pid,
			PPID:     ppid,
			Name:     name,
			NameNorm: normalizeProcName(name),
		}
		out = append(out, proc)
		byPID[pid] = proc
		byPIDHandle[pid] = p
		children[ppid] = append(children[ppid], pid)
	}
	stats.Metadata = time.Since(metaStart)

	indexStart := time.Now()
	nameIndex := buildProcNameIndex(byPID)
	stats.Index = time.Since(indexStart)

	// Decide which processes actually need expensive RSS lookup.
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
	stats.Relevant = time.Since(relevantStart)

	// Fill RSS only for relevant processes.
	rssStart := time.Now()
	for pid := range relevant {
		h := byPIDHandle[pid]
		if h == nil {
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

	for i := range out {
		if p, ok := byPID[out[i].PID]; ok {
			out[i] = p
		}
	}
	stats.RSS = time.Since(rssStart)
	stats.Total = time.Since(start)
	return out, byPID, children, nameIndex, stats, nil
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
