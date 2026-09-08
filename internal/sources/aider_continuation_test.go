package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aider prefixes its own output with "> " on the first line only: the
// --verbose dump and a multi-line commit message continue unprefixed, and the
// reader took those lines as the assistant speaking (#3311). Shape from a real
// 0.86.2 history.
func TestAiderOutputContinuationsAreNotSpeech(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.chat.history.md")
	body := strings.Join([]string{
		"",
		"# aider chat started at 2026-09-08 11:14:39",
		"",
		"> Command Line Args:   --verbose --model openai/mock-1",
		"Config File (/w/.aider.conf.yml):",
		"  read:              ['/w/.config/deja/aider-context.md']",
		"Defaults:",
		"  --map-refresh:     auto",
		"",
		"#### why does the query miss the tenant predicate",
		"",
		"The join drops it; I will add the predicate.",
		"",
		"> Applied edit to query.go",
		"> Commit 5ee830b add the tenant predicate",
		"    and keep the join order stable",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var users, assistants []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case "user":
			users = append(users, m.Text)
		case "assistant":
			assistants = append(assistants, m.Text)
		}
	}
	if len(users) != 1 || users[0] != "why does the query miss the tenant predicate" {
		t.Errorf("user turns = %q", users)
	}
	if len(assistants) != 1 || assistants[0] != "The join drops it; I will add the predicate." {
		t.Errorf("assistant turns = %q, want the reply alone — the dump and the commit tail are output", assistants)
	}
}
