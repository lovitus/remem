package guard

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
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

func snapshotProcesses() ([]Proc, map[int32]Proc, map[int32][]int32, error) {
	ps, err := process.Processes()
	if err != nil {
		return nil, nil, nil, err
	}

	out := make([]Proc, 0, len(ps))
	byPID := make(map[int32]Proc, len(ps))
	children := make(map[int32][]int32, len(ps))

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
		mem, err := p.MemoryInfo()
		if err != nil || mem == nil {
			continue
		}
		exe, _ := p.Exe()

		proc := Proc{
			PID:      pid,
			PPID:     ppid,
			Name:     name,
			NameNorm: normalizeProcName(name),
			Exe:      exe,
			ExeNorm:  normalizePath(exe),
			RSSBytes: mem.RSS,
		}
		out = append(out, proc)
		byPID[pid] = proc
		children[ppid] = append(children[ppid], pid)
	}
	return out, byPID, children, nil
}

func normalizeProcName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	return n
}

func normalizePath(path string) string {
	if path == "" {
		return ""
	}
	n := strings.ToLower(strings.TrimSpace(path))
	if runtime.GOOS == "windows" {
		n = filepath.ToSlash(n)
	}
	return n
}
