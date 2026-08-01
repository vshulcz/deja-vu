package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The counters deliberately leave the future out, so the screen has to say
// those sessions exist — otherwise "recent" shows dates that have not happened
// above counters that do not mention them (#696).
func TestBriefNamesSessionsAheadOfTheClock(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := time.Now()
	write := func(sid string, at time.Time) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/p","timestamp":"` +
			at.UTC().Format(time.RFC3339) + `","message":{"role":"user","content":"work in ` + sid + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("now", now.Add(-time.Hour))
	write("ahead", now.AddDate(0, 0, 6))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "stamped later than this machine's clock") {
		t.Errorf("brief does not mention the future session:\n%s", buf.String())
	}
	// A store with none of them says nothing extra.
	if err := os.Remove(filepath.Join(root, "ahead.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "ahead") {
		t.Errorf("clean store mentions the future:\n%s", buf.String())
	}
}
