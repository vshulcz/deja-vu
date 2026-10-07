//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

var errNoAiderLive = errors.New("no named pipes here")

// aiderLiveSettle is how long the server waits after answering one read
// before it opens the pipe again. Opened while the last reader is still
// draining, the next answer would land in that read as a second copy.
const aiderLiveSettle = 25 * time.Millisecond

// serveAiderLive makes a named pipe and answers every open of it with body().
func serveAiderLive(body func() string) (string, func(), error) {
	d, err := os.MkdirTemp("", "deja-aider-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(d, "deja-recall.md")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		_ = os.RemoveAll(d)
		return "", nil, err
	}
	var stopped atomic.Bool
	go func() {
		for !stopped.Load() {
			// Blocks until aider opens the file to read it.
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				if stopped.Load() {
					return
				}
				time.Sleep(aiderLiveSettle)
				continue
			}
			if !stopped.Load() {
				_, _ = f.WriteString(body())
			}
			_ = f.Close()
			time.Sleep(aiderLiveSettle)
		}
	}()
	stop := func() {
		stopped.Store(true)
		// Opening the read end lets a server waiting for a reader return.
		if f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		_ = os.RemoveAll(d)
	}
	return path, stop, nil
}
