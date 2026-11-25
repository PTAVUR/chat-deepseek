//go:build darwin

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// processChain 返回从父进程开始向上的进程链。
func processChain() []hostKey {
	var chain []hostKey
	pid := os.Getppid()
	for len(chain) < 32 && pid > 0 {
		info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			break
		}
		start := uint64(info.Proc.P_starttime.Sec)*1_000_000 + uint64(info.Proc.P_starttime.Usec)
		chain = append(chain, hostKey{PID: pid, Start: start})
		pid = int(info.Eproc.Ppid)
	}
	return chain
}
