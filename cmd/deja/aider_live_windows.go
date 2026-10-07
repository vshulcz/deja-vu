//go:build windows

package main

import "errors"

var errNoAiderLive = errors.New("no named pipes here")

// serveAiderLive has no Windows form: there is no FIFO in the file system, so
// aider reads the context file written once at start.
func serveAiderLive(func() string) (string, func(), error) {
	return "", nil, errNoAiderLive
}
