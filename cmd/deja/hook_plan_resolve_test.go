package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolvePlanTextBoundsAndConcurrentGrowth(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "valid.md")
	const validBody = "# Valid plan\nstep 1\n"
	if err := os.WriteFile(valid, []byte(validBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolvePlanText(dir, "", "valid.md"); got != validBody {
		t.Fatalf("resolvePlanText(valid) = %q, want %q", got, validBody)
	}

	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolvePlanText(dir, "", empty); got != "" {
		t.Fatalf("resolvePlanText(empty) = %q, want empty", got)
	}

	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolvePlanText(dir, "", subdir); got != "" {
		t.Fatalf("resolvePlanText(dir) = %q, want empty", got)
	}

	oversize := filepath.Join(dir, "oversize.md")
	if err := os.WriteFile(oversize, bytes.Repeat([]byte("x"), maxPlanFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolvePlanText(dir, "", oversize); got != "" {
		t.Fatalf("resolvePlanText(oversize) = len %d, want empty", len(got))
	}

	// Race Stat against in-place growth past maxPlanFileBytes.
	racePath := filepath.Join(dir, "race.md")
	small := strings.Repeat("#", 16)
	suffix := bytes.Repeat([]byte("y"), maxPlanFileBytes+4096-len(small))
	if err := os.WriteFile(racePath, []byte(small), 0o600); err != nil {
		t.Fatal(err)
	}
	wf, err := os.OpenFile(racePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wf.Close() }()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			_, _ = wf.WriteAt(suffix, int64(len(small)))
			_ = wf.Truncate(int64(len(small)))
		}
	})
	defer func() {
		close(done)
		wg.Wait()
	}()

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		got := resolvePlanText(dir, "", racePath)
		if len(got) > maxPlanFileBytes {
			t.Fatalf("resolvePlanText returned %d bytes > maxPlanFileBytes (%d)", len(got), maxPlanFileBytes)
		}
	}
}
