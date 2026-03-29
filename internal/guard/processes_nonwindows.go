//go:build !windows

package guard

import (
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func listProcessMetadata() ([]Proc, ProcSnapshotStats, error) {
	var stats ProcSnapshotStats

	listStart := time.Now()
	ps, err := process.Processes()
	stats.List = time.Since(listStart)
	if err != nil {
		return nil, stats, err
	}
	stats.ProcessesSeen = len(ps)

	out := make([]Proc, 0, len(ps))
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
	}
	stats.Metadata = time.Since(metaStart)
	return out, stats, nil
}
