//go:build windows

package game

import (
	"syscall"
	"unsafe"
)

// The process list comes from the tool help snapshot, which is the same list
// the task manager reads: one call names every process, so a check costs one
// snapshot rather than a walk over the system's process information.
var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	process32First           = kernel32.NewProc("Process32FirstW")
	process32Next            = kernel32.NewProc("Process32NextW")
	closeHandle              = kernel32.NewProc("CloseHandle")
)

const (
	th32csSnapProcess = 0x00000002
	// maxPath is the most a Windows file name holds, which bounds the
	// executable name the snapshot gives.
	maxPath = 260
)

// processEntry32 is PROCESSENTRY32W, whose last field is the executable's
// name.
type processEntry32 struct {
	size            uint32
	cntUsage        uint32
	processID       uint32
	defaultHeapID   uintptr
	moduleID        uint32
	threads         uint32
	parentProcessID uint32
	priClassBase    int32
	flags           uint32
	exeFile         [maxPath]uint16
}

// running reports whether the game has a process, read from the snapshot of
// every process the system is running.
func running() bool {
	snapshot, _, _ := createToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	handle := syscall.Handle(snapshot)
	if handle == syscall.InvalidHandle {
		return false
	}
	defer closeHandle.Call(uintptr(handle))

	entry := processEntry32{size: uint32(unsafe.Sizeof(processEntry32{}))}
	ok, _, _ := process32First.Call(uintptr(handle), uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if isDeadlockName(syscall.UTF16ToString(entry.exeFile[:])) {
			return true
		}
		ok, _, _ = process32Next.Call(uintptr(handle), uintptr(unsafe.Pointer(&entry)))
	}
	return false
}
