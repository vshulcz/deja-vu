package sources

import (
	"bytes"
	"encoding/json"
	"os/exec"
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
