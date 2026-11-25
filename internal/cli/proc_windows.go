//go:build windows

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

// processChain 返回从父进程开始向上的进程链。
// Windows 没有可跨进程比较的终端标识，只能沿父进程链认 shell。
func processChain() []hostKey {
	parents := snapshotParents()
	if len(parents) == 0 {
		return nil
	}
	// 父进程一定比子进程创建得早；时间倒挂说明 PID 已被回收，停止向上。
	var since uint64
	if self, err := syscall.GetCurrentProcess(); err == nil {
		since = creationTime(self)
	}
	pid := os.Getppid()
	var chain []hostKey
	for len(chain) < 32 && pid > 0 {
		handle, err := syscall.OpenProcess(0x1000, false, uint32(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
		if err != nil {
			break
		}
		created := creationTime(handle)
		_ = syscall.CloseHandle(handle)
		if created == 0 || (since != 0 && created > since) {
			break
		}
		chain = append(chain, hostKey{PID: pid, Start: created})
		since = created
		next, ok := parents[pid]
		if !ok || next == pid {
			break
		}
		pid = next
	}
	return chain
}

func creationTime(handle syscall.Handle) uint64 {
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
}

func snapshotParents() map[int]int {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = syscall.CloseHandle(snapshot) }()
	parents := make(map[int]int)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := syscall.Process32First(snapshot, &entry); err != nil {
		return nil
	}
	for {
		parents[int(entry.ProcessID)] = int(entry.ParentProcessID)
		if err := syscall.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return parents
}
