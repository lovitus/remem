package guard

import (
	"strings"

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

func snapshotProcesses(commandWatchlist map[string]struct{}, groups []config.GroupSpec) ([]Proc, map[int32]Proc, map[int32][]int32, error) {
	ps, err := process.Processes()
	if err != nil {
		return nil, nil, nil, err
	}

	out := make([]Proc, 0, len(ps))
	byPID := make(map[int32]Proc, len(ps))
	children := make(map[int32][]int32, len(ps))
	byPIDHandle := make(map[int32]*process.Process, len(ps))

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

	// Decide which processes actually need expensive RSS lookup.
	relevant := make(map[int32]struct{}, len(byPID)/2)
	for pid, p := range byPID {
		if _, ok := commandWatchlist[p.NameNorm]; ok {
			relevant[pid] = struct{}{}
		}
	}
	for _, g := range groups {
		roots := findRootPIDs(g, byPID)
		for _, root := range roots {
			markSubtreeRelevant(root, children, relevant)
		}
	}

	// Fill RSS only for relevant processes.
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
	return out, byPID, children, nil
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
