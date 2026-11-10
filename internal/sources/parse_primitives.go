package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// int64Of reads an integer off a decoded JSON value (float64, json.Number, or string).
func int64Of(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return int64(f)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return int64(i)
		}
	}
	return 0
}

// exitCode reads the status a harness records for a command. sqlite3 -json
// and JSONL stores hand numbers back as json.Number, float64, or string
// depending on the shape. Signal deaths (-9 for SIGKILL) stay negative;
// out-of-range values clamp to 1.
func exitCode(v any) int {
	n := int64Of(v)
	if n < -255 || n > 255 {
		return 1
	}
	return int(n)
}

// sqliteStderrMax bounds what a failing sqlite3 is quoted saying. Its first
// line names the cause; the rest is the query it was handed, which is a
// screenful and says nothing a reader does not already have.
const sqliteStderrMax = 200

// withStderr turns "exit status 1" into what the tool actually said.
func withStderr(err error, buf *bytes.Buffer) error {
	if buf == nil {
		return err
	}
	msg := strings.TrimSpace(buf.String())
	if msg == "" {
		return err
	}
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if len(msg) > sqliteStderrMax {
		msg = strings.TrimSpace(truncateRunes(msg, sqliteStderrMax)) + "…"
	}
	return fmt.Errorf("%s (%w)", msg, err)
}
