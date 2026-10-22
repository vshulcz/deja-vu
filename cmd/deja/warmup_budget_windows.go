//go:build windows

package main

import (
	"os"
	"runtime"
	"strconv"
)

// takeWarmupBudget is the windows half: the core cap is the same, and the
// priority class is left alone. Lowering it here means SetPriorityClass through
// the process handle, which is a syscall dependency this file does not need to
// carry — the cap is what keeps the rebuild off every core, and that is the
// half that was measured (#3500).
func takeWarmupBudget() {
	if os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		return
	}
	if os.Getenv("GOMAXPROCS") != "" {
		return
	}
	n := min(max(runtime.NumCPU()/2, 1), 8)
	runtime.GOMAXPROCS(n)
	_ = os.Setenv("GOMAXPROCS", strconv.Itoa(n))
}
