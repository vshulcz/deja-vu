package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"
)

// Rows from a SQLite-backed harness come back as JSON built by SQLite's own
// json_object, not by the sqlite3 shell's -json output mode.
//
// That formatter is quadratic in the number of characters it has to escape,
// and the cost is per value rather than per row. On macOS 26.5.1 with
// /usr/bin/sqlite3 3.51.0, one 4 MB text value with a quote every sixteenth
// byte:
//
//	-json         412s   (backslashes 421s, newlines 18s, plain hex 0.03s)
//	json_object   0.04s
//
// while the same 4 MB split into 4,000 one-kilobyte rows takes -json 0.17s. A
// transcript is mostly quotes and backslashes, so a single long tool output is
// enough to stall a whole index run: a 520 MB opencode store dumped 1.7 MB of
// its 42 in ten minutes, and `deja index` looked like it had hung (#3553).
//
// json_object also escapes every newline inside the values, so the shell's
// default output mode puts one object on one line, and json.Decoder reads that
// bare stream of values as happily as it reads an array.
//
// The caller owns cmd.Wait().
func sqliteRows(cmd *exec.Cmd) (*json.Decoder, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(stdout)
	dec.UseNumber()
	return dec, nil
}

// sqliteObjects decodes the same one-object-per-line answer from output small
// enough to have been buffered — the side lookups a reader makes beside its
// main pass.
func sqliteObjects[T any](b []byte) ([]T, error) {
	var out []T
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	for dec.More() {
		var r T
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// storeReadBudget is how long one sqlite3 read may take before deja stops
// waiting for it.
//
// #3553 hung for 13m54s inside a single sqlite3 child with 0.75s of CPU in deja
// itself, and nothing in the tree set a deadline. The cap is not a guess at how
// long a read should take — it is the point past which waiting has stopped being
// the right answer. For scale, the largest store on this machine is a 3.2 GB
// opencode database that answers in 11.7s, because the queries project only
// what they need rather than scanning the file; ten minutes is fifty times that
// and still turns #3553's fourteen minutes into a bounded failure.
//
// Overridable because a budget measured on one machine's disk is a guess about
// everyone else's: DEJA_STORE_TIMEOUT takes a duration, and a zero or negative
// one turns the cap off for someone who would rather wait than lose a store. A
// value that does not parse falls back to the default rather than removing the
// bound, because a typo in an environment variable must not do that quietly.
func storeReadBudget() time.Duration {
	if v := os.Getenv("DEJA_STORE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			if d <= 0 {
				return 0
			}
			return d
		}
	}
	return 10 * time.Minute
}

// sqliteReadCmd builds a read against a SQLite store with that budget on it.
//
// The caller defers stop, because every site sets Stderr or Stdout on the
// command before starting it and a constructor that started it would take that
// away. A store that runs out of budget is then an ordinary read error, which
// is the shape every caller already handles: the harness reports as unreadable
// and doctor names it, rather than the run sitting on it forever.
func sqliteReadCmd(db string, tail ...string) (*exec.Cmd, func()) {
	args := append([]string{"-readonly", sqliteTarget(db), ".timeout 5000"}, tail...)
	budget := storeReadBudget()
	if budget <= 0 {
		return exec.Command("sqlite3", args...), func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	return exec.CommandContext(ctx, "sqlite3", args...), cancel
}

// sqliteOutput is the same budget for a read whose whole answer is wanted at
// once. Output returns when the child does, so nothing outlives the call and
// the caller has no release to remember.
func sqliteOutput(db string, tail ...string) ([]byte, error) {
	cmd, stop := sqliteReadCmd(db, tail...)
	defer stop()
	return cmd.Output()
}
