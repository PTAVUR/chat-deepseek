//go:build linux

package cli

import (
	"os"
	"strconv"
	"strings"
)

// processChain 返回从父进程开始向上的进程链。
func processChain() []hostKey {
	var chain []hostKey
	pid := os.Getppid()
	for len(chain) < 32 && pid > 0 {
		ppid, start, ok := readProcStat(pid)
		if !ok {
			break
		}
		chain = append(chain, hostKey{PID: pid, Start: start})
		pid = ppid
	}
	return chain
}

// readProcStat 读取 /proc/<pid>/stat 里的父进程和启动时间。
// comm 字段可能包含空格和括号，所以从最后一个 ')' 之后再切分。
func readProcStat(pid int) (int, uint64, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, false
	}
	text := string(data)
	index := strings.LastIndexByte(text, ')')
	if index < 0 || index+2 >= len(text) {
		return 0, 0, false
	}
	fields := strings.Fields(text[index+2:])
	// state 是字段 3，ppid 是字段 4，starttime 是字段 22。
	if len(fields) < 20 {
		return 0, 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ppid, start, true
}
