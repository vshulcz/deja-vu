package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja log --json` is the surface a script polls to see what deja fed an
// agent, and the document named it twice without saying what it emits (#1924).
// Pinned the way stats (#1710), impact (#1900) and show/last (#1911) are.
func TestLogJSONKeysMatchTheDocumentedContract(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(filepath.Dir(usage.Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	usage.RecordServedSessions(dir, usage.KindRecall, 900, 2, false, 9000, []string{"s1", "s2"})
	usage.RecordResultRaw(dir, usage.KindHook, 400, 1, false, 4000)
	usage.RecordResult(dir, usage.KindRecall, 0, 0, true)
	_ = tmp

	out, err := captureRun(t, "log", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(events) != 3 {
		t.Fatalf("wrote three events, read %d: %s", len(events), out)
	}

	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, string(doc), "## `deja log --json`")

	keys := map[string]bool{}
	for _, e := range events {
		for k := range e {
			keys[k] = true
		}
	}
	var missing []string
	for k := range keys {
		if !strings.Contains(section, "`"+k+"`") {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("deja log --json emits %v, absent from its section of docs/json-output.md", missing)
	}
}

// The two shapes the document has to keep straight: the array is newest first
// and is never null, which #733 settled for the script that polls it.
func TestLogJSONIsAnArrayNewestFirst(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(filepath.Dir(usage.Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	empty, err := captureRun(t, "log", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(empty) != "[]" {
		t.Errorf("an empty log emits %q, not an empty array", strings.TrimSpace(empty))
	}

	usage.RecordResult(dir, usage.KindRecall, 100, 1, false)
	time.Sleep(2 * time.Millisecond)
	usage.RecordResult(dir, usage.KindHook, 200, 1, false)

	out, err := captureRun(t, "log", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var events []usage.Event
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("read %d events", len(events))
	}
	if events[0].Time.Before(events[1].Time) {
		t.Errorf("the array is oldest first: %s then %s", events[0].Time, events[1].Time)
	}
}

// `--last` is a different shape and the document describes it in prose; that
// prose named Go field names rather than JSON keys until this test existed.
func TestLogLastJSONKeysMatchTheDocumentedContract(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(filepath.Dir(usage.Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	usage.SnapshotPolicy(dir, usage.KindHook, "a digest of earlier work", 2, "local-only")

	out, err := captureRun(t, "log", "--last", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, string(doc), "## `deja log --json`")
	var missing []string
	for k := range snap {
		if !strings.Contains(section, "`"+k+"`") {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("deja log --last --json emits %v, absent from the section", missing)
	}
	if len(snap) < 4 {
		t.Errorf("the snapshot emitted %d keys, too few to say anything: %v", len(snap), snap)
	}
}
