package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// mcpOrphanGrace is the window a quiet but live client gets after its launcher
// exits before deja can mistake it for an orphan. Five minutes is longer than
// any request/response gap a real session should produce, but short enough that
// leaked servers do not accumulate across a working day. A client that closes
// cleanly reaches EOF within seconds; this only bounds how long an inherited
// write handle can keep the server alive after its parent is gone (#2397).
const mcpOrphanGrace = 5 * time.Minute

const mcpOrphanTick = 30 * time.Second

// startOrphanWatch stops the leaked-handle orphan from #2397. A dead parent
// alone means nothing because launchers legitimately exit, and silence alone
// means nothing because quiet clients stay connected. Only both together mark
// the orphan; a client that dies without leaking a handle already ends the
// server through EOF within seconds.
func startOrphanWatch(ppid int, lastRead func() time.Time, grace, tick time.Duration, exit func()) (stop func()) {
	ticker := time.NewTicker(tick)
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !processAlive(ppid) && time.Since(lastRead()) > grace {
					exit()
					return
				}
			case <-done:
				return
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
		})
	}
}

type mcpStampReader struct {
	r        io.Reader
	lastRead *atomic.Int64
}

func (r *mcpStampReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 || err == nil {
		r.lastRead.Store(time.Now().UnixNano())
	}
	return n, err
}

func serveMCPProcess(dir string, r io.Reader, w io.Writer) error {
	var lastRead atomic.Int64
	lastRead.Store(time.Now().UnixNano())

	// Read once, deliberately: on unix an orphaned process is reparented — to
	// init or a subreaper — and a live Getppid answers that adopter from then
	// on, so only the pid recorded at start can ever go dead. A later call
	// would watch a process that is alive by construction, and never fire.
	//
	// Windows reports -1 when the parent cannot be read; processAlive calls
	// that dead, and the AND would quietly collapse into the idle timeout the
	// design rejected. No recorded parent, no watch — EOF still works.
	ppid := os.Getppid()
	if ppid <= 0 {
		return serveMCP(dir, r, w)
	}
	stop := startOrphanWatch(
		ppid,
		func() time.Time {
			return time.Unix(0, lastRead.Load())
		},
		mcpOrphanGrace,
		mcpOrphanTick,
		func() {
			fmt.Fprintf(os.Stderr, "deja: mcp parent pid %d is gone and nothing has been read for %s; exiting the orphaned server (#2397)\n", ppid, mcpOrphanGrace)
			os.Exit(0)
		},
	)
	defer stop()

	return serveMCP(dir, &mcpStampReader{r: r, lastRead: &lastRead}, w)
}
