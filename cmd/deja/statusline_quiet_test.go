package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The quiet-day line is what the bar shows all day when the week has recalls
// and today has none, and it counted in plural regardless: "1 agent recalls"
// (#1600). The line directly below it in the same file already branches at one.
func TestQuietStatuslineCountsInSingularAtOne(t *testing.T) {
	dir := quietWeekIndex(t, 1)
	var buf bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"x","cwd":"/work/one"}`), &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "1 agent recalls") {
		t.Errorf("quiet line says %q", got)
	}
	if !strings.Contains(got, "1 agent recall,") {
		t.Errorf("quiet line lost the count: %q", got)
	}

	// Two keeps the plural.
	dir = quietWeekIndex(t, 2)
	buf.Reset()
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"x","cwd":"/work/one"}`), &buf); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "2 agent recalls") {
		t.Errorf("quiet line lost its plural above one: %q", got)
	}
}

// quietWeekIndex writes n recall events two days old — inside the week, before
// today — straight into the usage log, which is the only way to have a week
// that today does not repeat.
func quietWeekIndex(t *testing.T, n int) string {
	t.Helper()
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	var lines []string
	for i := 0; i < n; i++ {
		e := usage.Event{Time: time.Now().Add(-48 * time.Hour), Kind: usage.KindRecall, Bytes: 1500, Sessions: 1}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(usage.Path(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
