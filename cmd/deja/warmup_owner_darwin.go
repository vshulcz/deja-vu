//go:build darwin

package main

import (
	"encoding/binary"
	"strconv"
	"syscall"
	"unsafe"
)

// processStart reads the start time of pid from kern.proc.pid.<pid>, the
// kinfo_proc ps reads. syscall.Sysctl cannot name it: the pid is a mib
// component, not a name, so the mib is built by hand.
//
// kinfo_proc opens with extern_proc, whose first member is p_starttime (a
// timeval: 8-byte seconds, 4-byte microseconds) and whose p_stat sits at byte
// 36. SZOMB is 5: a warmup spawned by a long-running MCP server and killed is a
// zombie until the server exits, and its pid still answers.
func processStart(pid int) (start string, alive, known bool) {
	if pid <= 0 {
		return "", false, true
	}
	const (
		ctlKern     = 1
		kernProc    = 14
		kernProcPID = 1
		sysSysctl   = 202
		statOffset  = 36
		zombie      = 5
	)
	mib := [4]int32{ctlKern, kernProc, kernProcPID, int32(pid)} //nolint:gosec // pid > 0 is checked above and darwin pids fit in an int32
	buf := make([]byte, 1024)
	n := uintptr(len(buf))
	_, _, errno := syscall.Syscall6(sysSysctl, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0, 0) //nolint:gosec // sysctl takes its mib and buffers by pointer
	if errno != 0 {
		return "", false, false
	}
	// No such process: sysctl succeeds and writes nothing.
	if n == 0 {
		return "", false, true
	}
	if n <= statOffset {
		return "", false, false
	}
	if buf[statOffset] == zombie {
		return "", false, true
	}
	sec := binary.LittleEndian.Uint64(buf[0:8])
	usec := binary.LittleEndian.Uint32(buf[8:12])
	return strconv.FormatUint(sec, 10) + "." + strconv.FormatUint(uint64(usec), 10), true, true
}
