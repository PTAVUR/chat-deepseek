//go:build !linux && !darwin && !windows

package cli

import "os"

// processChain 在未知平台上只能退化到直接父进程。
func processChain() []hostKey {
	pid := os.Getppid()
	if pid <= 0 {
		return nil
	}
	return []hostKey{{PID: pid}}
}
