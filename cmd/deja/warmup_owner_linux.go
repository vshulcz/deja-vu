//go:build linux

package main

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// processStart reads the start time of pid from /proc/<pid>/stat, in clock
// ticks since boot. A zombie is not alive: a warmup spawned by a long-running
// MCP server and killed stays a zombie until the server exits, and its pid
// still answers.
func processStart(pid int) (start string, alive, known bool) {
	if pid <= 0 {
		return "", false, true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, true
	}
	if err != nil {
		return "", false, false
	}
	// The command name sits in parentheses and may hold spaces; the fields
	// after the last ')' start at field 3, the state. starttime is field 22.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return "", false, false
	}
	if f[0] == "Z" || f[0] == "X" {
		return "", false, true
	}
	return f[19], true, true
}
