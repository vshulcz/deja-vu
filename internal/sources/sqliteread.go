package sources

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// A read against a SQLite store is given up when it stops moving, not when a
// clock runs out. Ten minutes of wall time cut a Hermes store of 1,683
// sessions that was still reading on a slow disk — the sqlite3 child was
// waiting on file pages, not hung — and the store went missing from the index
// with exit 0 and nothing said (#5517).
//
// Moving is any byte from the child: a row, or the line `.progress` prints
// every thousand steps of SQLite's virtual machine. The second is what keeps a
// read alive through a sort or a scan that has no row to hand back yet, and it
// moves only while pages are arriving. sqlite3 buffers a pipe, so those lines
// land a few hundred at a time — every few hundred thousand steps, seconds
// apart on a slow disk against a budget of minutes. A sqlite3 without
// `.progress` (older than 3.22, or built without it) is judged by its rows.
const sqliteProgressArg = ".progress 1000"

// sqliteRead is one sqlite3 child under that rule. Callers use it like the
// exec.Cmd it wraps: set Stderr, then Output, or sqliteRows and Wait.
type sqliteRead struct {
	*exec.Cmd
	db      string
	budget  time.Duration
	last    atomic.Int64 // unix nanos of the last byte
	marked  atomic.Int64 // unix nanos of the last mark on the outer watch
	stalled atomic.Bool
	killed  chan struct{}
	started atomic.Bool
	waited  atomic.Bool
	quit    chan struct{}
	once    sync.Once
	pipe    io.ReadCloser
}

// sqliteReadCmd builds a read against a SQLite store under the store read
// budget. The caller defers the release, which ends a child it never waited
// for. A read given up is an ordinary read error, a *StalledRead, which every
// caller already handles: the harness reports it as unreadable and doctor
// names it, rather than the run sitting on it forever.
func sqliteReadCmd(db string, tail ...string) (*sqliteRead, func()) {
	budget := storeReadBudget()
	args := []string{"-readonly", sqliteTarget(db), ".timeout 5000"}
	if budget > 0 && sqlite3Progress() {
		args = append(args, sqliteProgressArg)
	}
	args = append(args, tail...)
	r := &sqliteRead{Cmd: exec.Command("sqlite3", args...), db: db, budget: budget, quit: make(chan struct{}), killed: make(chan struct{})}
	return r, r.release
}

// sqliteOutput is the same read for an answer wanted whole.
func sqliteOutput(db string, tail ...string) ([]byte, error) {
	cmd, stop := sqliteReadCmd(db, tail...)
	defer stop()
	return cmd.Output()
}

// stdout is the child's output with the progress lines taken out.
func (r *sqliteRead) stdout() (io.Reader, error) {
	pipe, err := r.StdoutPipe()
	if err != nil {
		return nil, err
	}
	r.pipe = pipe
	return &progressFilter{r: bufio.NewReaderSize(pipe, 64<<10), moved: r.moved, ended: r.ended}, nil
}

// Start starts the child and the watch on it.
func (r *sqliteRead) Start() error {
	if err := r.Cmd.Start(); err != nil {
		return err
	}
	r.started.Store(true)
	r.moved()
	if r.budget > 0 {
		go r.watch()
	}
	return nil
}

// Output is exec.Cmd's Output through the filter and the watch.
func (r *sqliteRead) Output() ([]byte, error) {
	// Capped as exec.Cmd.Output caps it: the head of an error is what is read.
	stderr := &cappedBuffer{max: 32 << 10}
	captured := r.Stderr == nil
	if captured {
		r.Stderr = stderr
	}
	out, err := r.stdout()
	if err != nil {
		return nil, err
	}
	if err := r.Start(); err != nil {
		return nil, err
	}
	b, rerr := io.ReadAll(out)
	if werr := r.Wait(); werr != nil {
		var ee *exec.ExitError
		if captured && errors.As(werr, &ee) {
			ee.Stderr = stderr.Bytes()
		}
		return b, werr
	}
	return b, rerr
}

// sqliteReapWait is how long Wait waits for a child it killed. One blocked in
// the kernel on a mount that stopped answering is not reaped until the mount
// answers, and the run must not wait on it.
var sqliteReapWait = 5 * time.Second

// Wait waits for the child; one given up reports why.
func (r *sqliteRead) Wait() error {
	done := make(chan error, 1)
	go func() { done <- r.Cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-r.killed:
		select {
		case err = <-done:
		case <-time.After(sqliteReapWait):
		}
	}
	r.waited.Store(true)
	r.once.Do(func() { close(r.quit) })
	if r.stalled.Load() {
		return r.stallError()
	}
	return err
}

func (r *sqliteRead) release() {
	r.once.Do(func() { close(r.quit) })
	if r.started.Load() && !r.waited.Load() && r.Process != nil {
		_ = r.Process.Kill()
	}
}

func (r *sqliteRead) stallError() error { return &StalledRead{Path: r.db, Idle: r.budget} }

// ended is what the output says when it ends: the stall, if the read was
// given up, rather than whatever the closed pipe said.
func (r *sqliteRead) ended(err error) error {
	if r.stalled.Load() {
		return r.stallError()
	}
	r.moved()
	return err
}

// moved records progress, and passes it to a watch on the same path at most
// once a second, or four times a budget shorter than that: a store read
// through Watch is the same read.
func (r *sqliteRead) moved() {
	now := time.Now().UnixNano()
	r.last.Store(now)
	every := time.Second
	if r.budget > 0 {
		every = min(every, r.budget/4)
	}
	if now-r.marked.Load() >= int64(every) {
		r.marked.Store(now)
		markProgress(r.db)
	}
}

func (r *sqliteRead) watch() {
	t := time.NewTicker(min(max(r.budget/10, 10*time.Millisecond), 5*time.Second))
	defer t.Stop()
	for {
		select {
		case <-r.quit:
			return
		case <-t.C:
			if time.Since(time.Unix(0, r.last.Load())) < r.budget {
				continue
			}
			r.stalled.Store(true)
			_ = r.Process.Kill()
			close(r.killed)
			// A child the kill does not reach can still hold the pipe open:
			// close it so the reader returns.
			if r.pipe != nil {
				_ = r.pipe.Close()
			}
			return
		}
	}
}

// progressFilter drops the "Progress N" lines `.progress` writes among the
// rows, and reports every byte as movement.
type progressFilter struct {
	r       *bufio.Reader
	moved   func()
	ended   func(error) error
	pending []byte
	midLine bool
	err     error
}

func (f *progressFilter) Read(p []byte) (int, error) {
	for len(f.pending) == 0 {
		if f.err != nil {
			return 0, f.err
		}
		chunk, err := f.r.ReadSlice('\n')
		if len(chunk) > 0 && f.moved != nil {
			f.moved()
		}
		atStart := !f.midLine
		f.midLine = err == bufio.ErrBufferFull
		if err != nil && err != bufio.ErrBufferFull {
			if f.ended != nil {
				err = f.ended(err)
			}
			f.err = err
		}
		if atStart && err == nil && isProgressLine(chunk) {
			continue
		}
		f.pending = chunk
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

// isProgressLine reports a whole line of the form "Progress 123".
func isProgressLine(b []byte) bool {
	rest, ok := bytes.CutPrefix(b, []byte("Progress "))
	if !ok {
		return false
	}
	rest = bytes.TrimSuffix(bytes.TrimSuffix(rest, []byte("\n")), []byte("\r"))
	if len(rest) == 0 {
		return false
	}
	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// schemaChanged is the error for a store query that failed with nothing read,
// which is most often a harness that changed its schema. A read given up for
// making no progress before its first row says that instead.
func schemaChanged(store string, err error) error {
	var st *StalledRead
	if errors.As(err, &st) {
		return err
	}
	return fmt.Errorf("%s: query failed, the store schema may have changed: %w", store, err)
}

// cappedBuffer keeps the first max bytes written to it.
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
