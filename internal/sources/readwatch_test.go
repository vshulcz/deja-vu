//go:build unix

package sources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// countLines parses a JSONL file through the shared line reader and reports
// one session holding a message per line.
func countLines(p string) ([]model.Session, error) {
	s := model.Session{ID: filepath.Base(p)}
	err := scanJSONL(p, func(map[string]any) { s.Messages = append(s.Messages, model.Message{}) })
	return []model.Session{s}, err
}

func claudeTurn(id string, n int) string {
	return fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/tmp/p","timestamp":"2026-01-02T03:04:%02dZ","message":{"role":"user","content":"stuckneedle turn %d"}}`+"\n", id, n%60, n)
}

// A read that never returns held the whole run: the store printed "still
// reading" and nothing bounded it. With no progress for the store read budget
// the file is skipped and named, and the rest of the store is read (#5396).
func TestAStuckReadIsSkippedAndTheRestIsRead(t *testing.T) {
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	DiagSnapshot()
	dir := t.TempDir()
	stuck := filepath.Join(dir, "stuck.jsonl")
	if err := syscall.Mkfifo(stuck, 0o600); err != nil {
		t.Skip("no FIFO here:", err)
	}
	// Open for writing and never write: the reader's open returns, its first
	// read blocks for good.
	go func() {
		if f, err := os.OpenFile(stuck, os.O_WRONLY, 0); err == nil {
			t.Cleanup(func() { _ = f.Close() })
		}
	}()
	good := filepath.Join(dir, "good.jsonl")
	if err := os.WriteFile(good, []byte(claudeTurn("g1", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- len(parseFiles([]string{stuck, good}, countLines)) }()
	select {
	case n := <-done:
		if n != 1 {
			t.Errorf("read %d sessions, want the readable one", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read with no progress held the store past its budget")
	}
	_, failed := DiagSnapshot()
	if failed[stuck] == "" {
		t.Errorf("the stuck file was not named: %v", failed)
	}
	if failed[good] != "" {
		t.Errorf("the readable file was named: %v", failed)
	}
}

// A read that keeps moving is left alone however long it takes: the budget is
// on progress, not on total time.
func TestASlowReadThatMovesIsNotCut(t *testing.T) {
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	DiagSnapshot()
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow.jsonl")
	if err := syscall.Mkfifo(slow, 0o600); err != nil {
		t.Skip("no FIFO here:", err)
	}
	const turns = 8
	go func() {
		f, err := os.OpenFile(slow, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		for i := range turns {
			_, _ = f.WriteString(claudeTurn("s1", i))
			time.Sleep(150 * time.Millisecond)
		}
	}()
	ss, err := ReadWatched(slow, func() ([]model.Session, error) { return countLines(slow) })
	if err != nil {
		t.Fatalf("a read that moved every 150ms was cut after %v: %v", 8*150*time.Millisecond, err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != turns {
		t.Fatalf("got %d sessions, want 1 with %d messages", len(ss), turns)
	}
}

// The error a stalled read leaves says what happened and which knob sets it.
func TestAStalledReadSaysWhy(t *testing.T) {
	t.Setenv("DEJA_STORE_TIMEOUT", "50ms")
	block := make(chan struct{})
	defer close(block)
	_, err := ReadWatched("/nowhere.jsonl", func() ([]model.Session, error) { <-block; return nil, nil })
	var stalled *StalledRead
	if !errors.As(err, &stalled) {
		t.Fatalf("got %v, want a stalled read", err)
	}
	DiagSnapshot()
}

// A read given up marks its directory: the next file there is skipped at once
// rather than costing the whole budget again.
func TestAStalledDirectoryIsNotWaitedOnTwice(t *testing.T) {
	t.Setenv("DEJA_STORE_TIMEOUT", "100ms")
	dir := t.TempDir()
	block := make(chan struct{})
	defer close(block)
	if _, err := Watch(filepath.Join(dir, "a.jsonl"), func() (int, error) { <-block; return 0, nil }); err == nil {
		t.Fatal("a blocked read was not given up")
	}
	start := time.Now()
	_, err := Watch(filepath.Join(dir, "b.jsonl"), func() (int, error) { <-block; return 0, nil })
	var stalled *StalledRead
	if !errors.As(err, &stalled) || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("the second file waited %v (%v)", time.Since(start), err)
	}
	other := t.TempDir()
	if n, err := Watch(filepath.Join(other, "c.jsonl"), func() (int, error) { return 7, nil }); err != nil || n != 7 {
		t.Fatalf("another directory was skipped too: %v %v", n, err)
	}
	DiagSnapshot()
}

// Once the blocked read returns, the directory is read again.
func TestAStalledDirectoryIsReadAgainOnceItAnswers(t *testing.T) {
	t.Setenv("DEJA_STORE_TIMEOUT", "100ms")
	dir := t.TempDir()
	block := make(chan struct{})
	if _, err := Watch(filepath.Join(dir, "a.jsonl"), func() (int, error) { <-block; return 0, nil }); err == nil {
		t.Fatal("a blocked read was not given up")
	}
	close(block)
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, err := Watch(filepath.Join(dir, "b.jsonl"), func() (int, error) { return 7, nil })
		if err == nil && n == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the directory stayed skipped after its read returned: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	DiagSnapshot()
}

// A read that returns just as the budget runs out is not given up, and leaves
// nothing marked behind it.
func TestAReadThatFinishesAtTheBudgetLeavesNoMark(t *testing.T) {
	t.Setenv("DEJA_STORE_TIMEOUT", "20ms")
	dir := t.TempDir()
	for i := range 200 {
		_, _ = Watch(filepath.Join(dir, fmt.Sprint(i)), func() (int, error) { time.Sleep(20 * time.Millisecond); return 0, nil })
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		watchMu.Lock()
		left := stalledDirs[dir]
		watchMu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d stalls left marked on a directory whose reads all returned", left)
		}
		time.Sleep(10 * time.Millisecond)
	}
	DiagSnapshot()
}
