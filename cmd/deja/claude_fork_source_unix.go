//go:build !windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// processAncestorArgs reads /proc where there is one, and asks ps elsewhere
// (macOS). It runs on a fork's SessionStart only.
func processAncestorArgs(depth int) [][]string {
	var out [][]string
	pid := os.Getppid()
	for i := 0; i < depth && pid > 1; i++ {
		args, ppid, ok := procArgs(pid)
		if !ok || len(args) == 0 {
			break
		}
		out = append(out, args)
		pid = ppid
	}
	return out
}

func procArgs(pid int) ([]string, int, bool) {
	p := strconv.Itoa(pid)
	if cmdline, err := os.ReadFile("/proc/" + p + "/cmdline"); err == nil {
		stat, err := os.ReadFile("/proc/" + p + "/stat")
		if err != nil {
			return nil, 0, false
		}
		// pid (comm) state ppid ...; comm may hold spaces and parens.
		i := bytes.LastIndexByte(stat, ')')
		if i < 0 {
			return nil, 0, false
		}
		f := strings.Fields(string(stat[i+1:]))
		if len(f) < 2 {
			return nil, 0, false
		}
		ppid, _ := strconv.Atoi(f[1])
		return strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00"), ppid, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "ps", "-ww", "-o", "ppid=", "-o", "args=", "-p", p).Output()
	if err != nil {
		return nil, 0, false
	}
	// ps joins argv with spaces; a session id is one word either way.
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return nil, 0, false
	}
	ppid, err := strconv.Atoi(f[0])
	if err != nil {
		return nil, 0, false
	}
	return f[1:], ppid, true
}
