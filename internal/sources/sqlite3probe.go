package sources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SQLite3NotFound is the problem reported when no sqlite3 is on PATH.
const SQLite3NotFound = "sqlite3 CLI not found"

// sqlite3ProbeQuery exercises what the readers depend on: the argument shape
// sqliteReadCmd passes and SQLite's JSON functions.
const (
	sqlite3ProbeQuery  = "select json_object('deja',1);"
	sqlite3ProbeAnswer = `{"deja":1}`
	sqlite3ProbeBudget = 5 * time.Second
)

// A sqlite3 that is on PATH but does not answer reads every store as empty,
// with exit 0 and nothing on stderr: a wrapper that forwards only its first
// argument, a stub, a build without JSON functions. That looked exactly like
// an empty database, so the binary is asked a real question once and its
// answer is kept until the file on PATH changes.
var sqlite3Probe struct {
	mu      sync.Mutex
	key     string
	problem string
}

// SQLite3Available reports whether the sqlite3 CLI the database readers shell
// out to is on PATH and answers a query the way they need it to.
func SQLite3Available() bool {
	return SQLite3Problem() == ""
}

// SQLite3Problem says why the sqlite3 CLI cannot be used: SQLite3NotFound when
// there is none, a sentence naming the binary and what it did when it is there
// but broken, and "" when it works.
func SQLite3Problem() string {
	path, err := exec.LookPath("sqlite3")
	if err != nil {
		return SQLite3NotFound
	}
	key := path
	if fi, err := os.Stat(path); err == nil {
		key = fmt.Sprintf("%s|%d|%d", path, fi.Size(), fi.ModTime().UnixNano())
	}
	sqlite3Probe.mu.Lock()
	defer sqlite3Probe.mu.Unlock()
	if sqlite3Probe.key == key {
		return sqlite3Probe.problem
	}
	sqlite3Probe.problem = probeSQLite3(path)
	sqlite3Probe.key = key
	return sqlite3Probe.problem
}

// SQLite3Broken reports that a sqlite3 is on PATH and does not work, as
// opposed to there being none.
func SQLite3Broken() bool {
	p := SQLite3Problem()
	return p != "" && p != SQLite3NotFound
}

// ResetSQLite3Probe forgets the cached answer. Tests that swap PATH between
// stubs written in the same instant use it.
func ResetSQLite3Probe() {
	sqlite3Probe.mu.Lock()
	sqlite3Probe.key, sqlite3Probe.problem = "", ""
	sqlite3Probe.mu.Unlock()
}

func probeSQLite3(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), sqlite3ProbeBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-readonly", ":memory:", ".timeout 5000", sqlite3ProbeQuery)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	switch {
	case ctx.Err() != nil:
		return fmt.Sprintf("sqlite3 at %s did not answer a probe query within %s", path, sqlite3ProbeBudget)
	case err != nil:
		msg := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if line := firstLine(stderr.String()); line != "" {
				msg += ": " + line
			}
		}
		return fmt.Sprintf("sqlite3 at %s failed a probe query (%s)", path, msg)
	case out == sqlite3ProbeAnswer:
		return ""
	case out == "":
		return fmt.Sprintf("sqlite3 at %s did not answer a probe query (no output)", path)
	default:
		return fmt.Sprintf("sqlite3 at %s answered a probe query with %q", path, clipProbe(firstLine(out)))
	}
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

func clipProbe(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "..."
	}
	return s
}
