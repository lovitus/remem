//go:build windows

package guard

import (
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	th32csSnapProcess = 0x00000002
	maxPath           = 260
)

type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [maxPath]uint16
}

var (
	modKernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procCreateToolhelp32Snap = modKernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW      = modKernel32.NewProc("Process32FirstW")
	procProcess32NextW       = modKernel32.NewProc("Process32NextW")
)

func listProcessMetadata() ([]Proc, ProcSnapshotStats, error) {
	var stats ProcSnapshotStats

	listStart := time.Now()
	snapshot, err := createToolhelp32Snapshot(th32csSnapProcess, 0)
	stats.List = time.Since(listStart)
	if err != nil {
		return nil, stats, err
	}
	defer windows.CloseHandle(snapshot)

	out := make([]Proc, 0, 256)
	entry := processEntry32{Size: uint32(unsafe.Sizeof(processEntry32{}))}

	metaStart := time.Now()
	ok, err := process32First(snapshot, &entry)
	if err != nil {
		stats.Metadata = time.Since(metaStart)
		return nil, stats, err
	}
	if ok {
		for {
			stats.ProcessesSeen++
			name := windows.UTF16ToString(entry.ExeFile[:])
			if name != "" {
				out = append(out, Proc{
					PID:      int32(entry.ProcessID),
					PPID:     int32(entry.ParentProcessID),
					Name:     name,
					NameNorm: normalizeProcName(name),
				})
			}

			entry = processEntry32{Size: uint32(unsafe.Sizeof(processEntry32{}))}
			next, err := process32Next(snapshot, &entry)
			if err != nil {
				stats.Metadata = time.Since(metaStart)
				return nil, stats, err
			}
			if !next {
				break
			}
		}
	}
	stats.Metadata = time.Since(metaStart)
	return out, stats, nil
}

func createToolhelp32Snapshot(flags uint32, processID uint32) (windows.Handle, error) {
	r1, _, e1 := procCreateToolhelp32Snap.Call(uintptr(flags), uintptr(processID))
	handle := windows.Handle(r1)
	if handle == windows.InvalidHandle {
		if e1 != nil && e1 != windows.ERROR_SUCCESS {
			return 0, e1
		}
		return 0, syscall.EINVAL
	}
	return handle, nil
}

func process32First(snapshot windows.Handle, entry *processEntry32) (bool, error) {
	r1, _, e1 := procProcess32FirstW.Call(uintptr(snapshot), uintptr(unsafe.Pointer(entry)))
	if r1 == 0 {
		if e1 == windows.ERROR_NO_MORE_FILES {
			return false, nil
		}
		if e1 != nil && e1 != windows.ERROR_SUCCESS {
			return false, e1
		}
		return false, syscall.EINVAL
	}
	return true, nil
}

func process32Next(snapshot windows.Handle, entry *processEntry32) (bool, error) {
	r1, _, e1 := procProcess32NextW.Call(uintptr(snapshot), uintptr(unsafe.Pointer(entry)))
	if r1 == 0 {
		if e1 == windows.ERROR_NO_MORE_FILES {
			return false, nil
		}
		if e1 != nil && e1 != windows.ERROR_SUCCESS {
			return false, e1
		}
		return false, syscall.EINVAL
	}
	return true, nil
}
