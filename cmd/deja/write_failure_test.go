package main

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"testing"
)

// The index has said "no space left where the index is built" since #888,
// while every other write path handed back the syscall: the notes file, the
// sync export and the stats page all reported ENOSPC as `open /…: no space
// left on device` (#906).
func TestWriteFailureReasonSpeaksOneVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"permission", fs.ErrPermission, "permission denied"},
		{"full disk", syscall.ENOSPC, "no space left on that disk"},
		{"disk gone", syscall.EIO, "that disk is not reachable"},
		{"detached", syscall.ENXIO, "that disk is not reachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := writeFailureReason(fmt.Errorf("open /tmp/x: %w", tc.err))
			if got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "open /tmp/x") {
				t.Errorf("the syscall came through: %q", got)
			}
		})
	}
	// Anything deja does not recognise comes through whole rather than being
	// guessed at.
	other := errors.New("some other failure")
	if got := writeFailureReason(other); got != other.Error() {
		t.Errorf("unknown cause was reworded: %q", got)
	}
	// exportPromoted's own reasons still win where they are more specific.
	if got := exportFailureReason(fmt.Errorf("open x: %w", fs.ErrNotExist)); got != "its directory does not exist" {
		t.Errorf("export reason = %q", got)
	}
}
