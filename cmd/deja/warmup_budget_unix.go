//go:build !windows

package main

import (
	"os"
	"runtime"
	"strconv"
	"syscall"
)

// warmupNice is how far down the background index moves itself. 10 is the
// conventional "this can wait" step: the scheduler still runs it whenever the
// machine is idle and stops handing it a slice against the editor, the agent
// and the compiler someone is waiting on.
const warmupNice = 10

// takeWarmupBudget lowers what a detached warmup is allowed to take.
//
// The warmup is `deja index` with nothing holding it back: a compaction asks for
// one at exactly the moment a session is busy, and a full rebuild then tokenizes
// on every core it can find while the person waits for their agent (#3500).
//
// Two limits, because they bind on different things. GOMAXPROCS caps how many
// cores the parallel phases claim at once; the nice value decides who wins when
// they and the foreground want the same core. Neither makes the rebuild slower
// on an idle machine, which is where most of them run.
//
// Set in the child rather than by the parent: the parent cannot renice a process
// it has already exec'd, and a wrapper like `nice` is a binary this machine may
// not have — `timeout` is missing here, which is how that lesson was learned.
func takeWarmupBudget() {
	if os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		return
	}
	// Half the cores, at least one, and never more than the eight a bucket
	// write already uses: past that the index pass is waiting on the disk.
	if os.Getenv("GOMAXPROCS") == "" {
		n := runtime.NumCPU() / 2
		if n < 1 {
			n = 1
		}
		if n > 8 {
			n = 8
		}
		runtime.GOMAXPROCS(n)
		// For anything this process spawns of its own.
		_ = os.Setenv("GOMAXPROCS", strconv.Itoa(n))
	}
	// Best effort: a machine that refuses the call (a container with a policy,
	// a platform without the syscall) runs the warmup as before rather than
	// refusing to warm up.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, warmupNice)
}
