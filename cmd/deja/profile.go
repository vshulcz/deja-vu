package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
)

// Profiling is reachable from the real binary rather than only from `go test`,
// because the interesting corpus is the one on someone's disk and a test cannot
// see it. Indexing questions — which phase owns the time, what holds the memory
// — are answered by running the command that is actually slow.
//
//	DEJA_CPUPROFILE=/tmp/cpu.out deja index --rebuild
//	DEJA_MEMPROFILE=/tmp/mem.out deja index --rebuild
//	go tool pprof -top /tmp/cpu.out
//
// Both are off unless the variable is set, so this costs a nil check per run.
func startProfiling() func() {
	var stops []func()

	if path := os.Getenv("DEJA_CPUPROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "deja: cpu profile:", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(os.Stderr, "deja: cpu profile:", err)
			_ = f.Close()
		} else {
			stops = append(stops, func() {
				pprof.StopCPUProfile()
				_ = f.Close()
				fmt.Fprintln(os.Stderr, "deja: cpu profile written to", path)
			})
		}
	}

	if path := os.Getenv("DEJA_MEMPROFILE"); path != "" {
		stops = append(stops, func() {
			f, err := os.Create(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, "deja: mem profile:", err)
				return
			}
			defer func() { _ = f.Close() }()
			// The heap profile is a snapshot, so it is taken at the end and
			// after a GC: without one it reports garbage the run had already
			// finished with.
			runtime.GC()
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintln(os.Stderr, "deja: mem profile:", err)
				return
			}
			fmt.Fprintln(os.Stderr, "deja: heap profile written to", path)
		})
	}

	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}
