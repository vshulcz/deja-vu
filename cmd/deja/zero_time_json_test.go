package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `omitempty` does nothing to a struct, and time.Time is one: a field tagged
// that way is always written, and a zero time reads as January of year 1. The
// document promises `recall.since` is absent on a store with no recall history
// (#1874).
func TestStatsOmitsTheRecallWindowItNeverOpened(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","cwd":"/proj","timestamp":"2026-08-20T10:00:00Z","message":{"role":"user","content":"fix the parser"}}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "stats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Recall map[string]any `json:"recall"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if v, ok := report.Recall["since"]; ok {
		t.Errorf("a store that has served no recall reports a window opening at %v", v)
	}
	// The control: the block itself is still there, so the absence above is an
	// omitted field and not a missing section.
	if _, ok := report.Recall["recalls_served"]; !ok {
		t.Errorf("the recall block is gone entirely: %v", report.Recall)
	}
}

// The same tag on the same type is elsewhere in the emitted JSON. This walks
// for it rather than waiting to meet the next one: a new field written this way
// is a documented omission that never happens.
func TestNoEmittedTimestampReliesOnOmitemptyAloneToVanish(t *testing.T) {
	// internal/peers is deja's own file rather than a reported contract, and a
	// year-1 stamp there parses back to a zero time.Time, so nothing reads it
	// wrong. Recorded here so the exception is a decision rather than a gap.
	allowed := map[string]bool{
		"../../internal/peers/peers.go": true,
	}
	// A pointer is the fix, not the flaw: *time.Time with omitempty does
	// vanish. Only the bare struct is the bug.
	tagged := regexp.MustCompile(`[^*\w.]time\.Time\s+` + "`" + `json:"[^"]*,omitempty"`)
	var found []string
	roots := []string{".", "../../internal"}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			if allowed[filepath.ToSlash(path)] {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if m := tagged.FindString(string(src)); m != "" {
				found = append(found, path+": "+m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Errorf("these fields are written even when the time is zero, which reads as year 1:\n%s", strings.Join(found, "\n"))
	}
}
