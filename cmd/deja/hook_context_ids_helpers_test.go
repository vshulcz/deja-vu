package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// loggedIDsForKind reads the session ids the usage log recorded for one kind,
// through the same file `deja log` reads.
func loggedIDsForKind(t *testing.T, dir, kind string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	b, err := os.ReadFile(usage.Path(dir))
	if err != nil {
		t.Fatalf("no usage log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e struct {
			Kind string   `json:"kind"`
			IDs  []string `json:"ids"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Kind != kind {
			continue
		}
		for _, id := range e.IDs {
			out[id] = true
		}
	}
	return out
}
