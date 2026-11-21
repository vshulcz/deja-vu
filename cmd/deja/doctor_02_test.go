package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A peer's stamps are written by deja itself, so a copy from a machine a few
// hundred milliseconds ahead is not a broken clock; a session's stamps come
// from a transcript on some other machine, where anything ahead is unusable.
// Two rules, on purpose (#1855, #1753) — this pins the band where they differ,
// so a change to either is a change to a test rather than a silent one.
func TestThePeerRuleAndTheSessionRuleDifferByTheSlack(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	inside := now.Add(peerClockSlack / 2)
	outside := now.Add(peerClockSlack + time.Second)

	if peerStampedAhead(inside, now) {
		t.Errorf("a peer stamped inside the slack is flagged, so a clock a moment out reads as broken")
	}
	if !peerStampedAhead(outside, now) {
		t.Errorf("a peer stamped past the slack is not flagged")
	}
	if !index.StampedAhead(inside, now) {
		t.Errorf("the session rule has grown a tolerance, so the two rules no longer differ and the doc below is describing nothing")
	}
}

// The field is part of a documented contract, and the doc said the session
// rule, which is the one the peer surface does not use: someone implementing
// against it disagreed with deja over the whole slack band (#1865).
func TestTheDocumentedPeerAheadRuleNamesTheSlack(t *testing.T) {
	src, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(src)
	i := strings.Index(doc, "`stamped_ahead`")
	if i < 0 {
		t.Fatal("docs/json-output.md no longer describes stamped_ahead")
	}
	para := doc[i:]
	if end := strings.Index(para, "\n\n"); end > 0 {
		para = para[:end]
	}
	if !strings.Contains(para, "minute") {
		t.Errorf("the documented rule does not name the minute of slack the peer surface applies, so a reader implements the session rule:\n%s", para)
	}
	if strings.Contains(para, "the rule recall applies to sessions stamped ahead") {
		t.Errorf("the doc still says a peer is judged by the session rule:\n%s", para)
	}
}

func seedAheadIndex(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted while the migration held the lock"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	ahead, err := json.Marshal(map[string]any{
		"ts": time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s9", "state": "accepted", "project": "app",
		"title": "a note from next year", "text": "pgbouncer runs in transaction mode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(ahead, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// doctor is where someone looks when a store reads wrong, and it named this for
// a peer's clock since #1855 while saying nothing about a session's (#2106).
func TestDoctorNamesSessionsStampedAhead(t *testing.T) {
	seedAheadIndex(t)
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the index really holds the session ahead.
	if !strings.Contains(out, "freshness") {
		t.Fatalf("doctor did not report on the index at all: %q", out)
	}
	if !strings.Contains(out, "later than this machine's clock") {
		t.Errorf("doctor says nothing about a session dated next year:\n%s", out)
	}
}

// And the machine-readable side, which is what a script reads.
func TestDoctorJSONCarriesTheAheadCount(t *testing.T) {
	seedAheadIndex(t)
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Index struct {
			SessionsAhead int `json:"sessions_stamped_ahead"`
		} `json:"index"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json: %v: %s", err, out)
	}
	if report.Index.SessionsAhead != 1 {
		t.Errorf("doctor --json counts %d sessions stamped ahead, want 1", report.Index.SessionsAhead)
	}
}

// A store with nothing ahead says nothing: the line is a state, not a
// decoration on every run.
func TestDoctorIsQuietWhenNothingIsAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "later than this machine's clock") {
		t.Errorf("doctor reported a session ahead where there is none:\n%s", out)
	}
}

// What makes the context file reach aider is the read: entry in
// ~/.aider.conf.yml. The row looked at the file alone: wired with the entry
// gone, and missing — the word for never installed — when the entry was there
// and the file was not, which makes aider print an error on every start
// (#4327).
func TestDoctorAiderRowReadsTheConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index"))
	var row autoWiring
	for _, a := range autoWirings() {
		if a.name == "aider" {
			row = a
		}
	}
	conf := filepath.Join(home, ".aider.conf.yml")
	ctx := aiderContextPath()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	text := func() string {
		var b bytes.Buffer
		doctorAutoRecall(&b)
		for l := range strings.SplitSeq(b.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "aider ") {
				return l
			}
		}
		return ""
	}
	cases := []struct {
		name, conf string
		ctxThere   bool
		want       string
	}{
		{"installed", "model: gpt-4o\nread:\n  - " + ctx + "\n", true, "wired"},
		{"entry gone", "model: gpt-4o\n", true, "stale"},
		{"no config", "", true, "stale"},
		{"file gone", "read:\n  - " + ctx + "\n", false, "broken"},
		{"scalar entry", "read: " + ctx + "\n", true, "wired"},
		{"never installed", "", false, "missing"},
		{"another key names it", "notes:\n  - " + ctx + "\n", true, "stale"},
	}
	for _, c := range cases {
		_ = os.Remove(conf)
		_ = os.Remove(ctx)
		if c.conf != "" {
			write(conf, c.conf)
		}
		if c.ctxThere {
			write(ctx, "digest\n")
		}
		if got, _ := autoWiringState(row); got != c.want {
			t.Errorf("%s: state %q, want %q", c.name, got, c.want)
		}
		if l := text(); !strings.Contains(l, " "+c.want+" ") {
			t.Errorf("%s: row %q, want %s", c.name, l, c.want)
		}
	}
}

// A config saved with a byte order mark — PowerShell's default, and install
// writes it back — still has its read: entry on the first line.
func TestDoctorAiderReadsAConfigWithABOM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ctx := aiderContextPath()
	if err := os.MkdirAll(filepath.Dir(ctx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctx, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aiderConfPath(), []byte("\xef\xbb\xbfread:\n  - "+ctx+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := aiderWiring(true); got != "" {
		t.Errorf("a wired config with a BOM reads as %q", got)
	}
}

// Amp builds from 2026-03-31 on keep threads on ampcode.com and write nothing
// under threads/, while the data directory still holds bin, logs and pids. The
// row said `missing` with no reason, which reads as "you never used Amp"
// (#4355).
func TestDoctorSaysCurrentAmpKeepsThreadsServerSide(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	dataDir := filepath.Join(tmp, "data", "amp")

	doctorAmp := func() (string, doctorStore) {
		t.Helper()
		var buf bytes.Buffer
		if err := runDoctor(&buf, []string{"--offline"}, stubLookup("1.0.0", false), t.TempDir()); err != nil {
			t.Fatal(err)
		}
		store, _ := inspectDoctorStore(doctorCheckNamed(t, "amp"))
		return harnessRow(t, buf.String(), "amp"), store
	}

	// Control: no Amp on the machine, no sentence about it.
	if row, store := doctorAmp(); strings.Contains(row, "ampcode.com") || store.Note != "" {
		t.Fatalf("row = %q, note = %q — nothing says Amp is installed here", row, store.Note)
	}

	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	row, store := doctorAmp()
	if !strings.Contains(row, "missing") || !strings.Contains(row, "ampcode.com") {
		t.Fatalf("row = %q — want missing plus where current Amp keeps its threads", row)
	}
	if !strings.Contains(store.Note, "ampcode.com") {
		t.Fatalf("json note = %q — want the same reason as the text row", store.Note)
	}

	// A thread on disk is a store that reads, and the note goes.
	threads := filepath.Join(dataDir, "threads")
	if err := os.MkdirAll(threads, 0o755); err != nil {
		t.Fatal(err)
	}
	thread := `{"id":"T-1","created":1774950000000,"title":"x","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	if err := os.WriteFile(filepath.Join(threads, "T-1.json"), []byte(thread), 0o644); err != nil {
		t.Fatal(err)
	}
	if row, store := doctorAmp(); strings.Contains(row, "ampcode.com") || store.Note != "" {
		t.Fatalf("row = %q, note = %q — a store with threads needs no note", row, store.Note)
	}
}

// Antigravity keeps everything under `.system_generated`, and the dot-rule
// that trims codex's `.tmp` noise silenced the row completely: a transcript
// deja could not read was invisible, while the store's own bookkeeping — the
// full transcripts, the chunk files, the message records — was invisible too
// (#3377).
func TestDoctorNamesAnUnreadAntigravityTranscriptButNotItsBookkeeping(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "antigravity")
	brain := filepath.Join(root, "brain", "f017fad5", ".system_generated")
	for _, d := range []string{
		filepath.Join(brain, "logs", "chunks", "transcript"),
		filepath.Join(brain, "messages"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nwhy does the retry loop drop the last attempt\n</USER_REQUEST>"}`
	write(filepath.Join(brain, "logs", "transcript.jsonl"), line+"\n")
	write(filepath.Join(brain, "logs", "transcript_full.jsonl"), line+"\n")
	write(filepath.Join(brain, "logs", "chunks", "transcript", "0.jsonl"), line+"\n")
	write(filepath.Join(brain, "messages", "8f1c2a3e-0000-4000-8000-000000000000.json"), "{}")
	write(filepath.Join(root, "brain", "f017fad5", "task.md.metadata.json"), "{}")

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.Contains(l, "antigravity") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no antigravity file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the rest of the store is its own bookkeeping", got)
	}
	// And a transcript under a name the reader does not take is what the row
	// is for.
	write(filepath.Join(brain, "logs", "transcript.v2.jsonl"), line+"\n")
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the transcript deja did not read", got)
	}
}

// The two shapes the first cut swallowed: a transcript restored beside the
// conversations rather than inside one, and a session whose only artefact is
// the full log — the row said nothing about either (review of #3377).
func TestDoctorNamesAnAntigravityTranscriptInTheWrongPlace(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "antigravity")
	logs := filepath.Join(root, "brain", "sessA", ".system_generated", "logs")
	onlyFull := filepath.Join(root, "brain", "sessB", ".system_generated", "logs")
	restored := filepath.Join(root, "restored_backup")
	for _, d := range []string{logs, onlyFull, restored, filepath.Join(root, "cache")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)

	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nwhy does the retry loop drop the last attempt\n</USER_REQUEST>"}`
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(logs, "transcript.jsonl"))
	write(filepath.Join(logs, "transcript_full.jsonl")) // the second copy: not a loss
	write(filepath.Join(onlyFull, "transcript_full.jsonl"))
	write(filepath.Join(restored, "transcript.jsonl"))
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "antigravity") && strings.Contains(l, "file") {
			if !strings.Contains(l, "2 not recognised here") {
				t.Errorf("row = %q, want both the restored transcript and the session whose only log is the full one", l)
			}
			return
		}
	}
	t.Fatal("no antigravity file row in the report at all")
}

// `agy plugin disable deja` keeps the hook file and records the switch in
// config.json, so doctor read the row as wired while Antigravity loaded
// nothing from the plugin (#4359).
func TestDoctorMarksTheAntigravityPluginSwitchedOff(t *testing.T) {
	hermeticEnv(t)
	bin := filepath.Join(os.Getenv("HOME"), "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installAntigravityAuto(bin, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(antigravityConfigHome(), "config.json")
	plugin := filepath.Join(antigravityConfigHome(), "plugins", "deja", "plugin.json")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	row := func() doctorAutoStatus {
		t.Helper()
		out, err := captureRun(t, "doctor", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			AutoRecall []doctorAutoStatus `json:"auto_recall"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("doctor --json: %v: %s", err, out)
		}
		for _, r := range report.AutoRecall {
			if r.Name == "antigravity" {
				return r
			}
		}
		t.Fatalf("no antigravity row:\n%s", out)
		return doctorAutoStatus{}
	}

	if r := row(); r.State != "wired" || r.SwitchedOff {
		t.Fatalf("freshly installed: %+v, want wired and on", r)
	}

	// What agy 1.2.5 writes for `agy plugin disable deja`.
	write(config, "{\n  \"plugins\": {\n    \"deja\": {\n      \"enabled\": false\n    }\n  }\n}\n")
	if r := row(); r.State != "wired" || !r.SwitchedOff {
		t.Errorf("after plugin disable: %+v, want wired and switched_off", r)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "agy plugin enable deja") {
		t.Errorf("the text report does not say the plugin is off:\n%s", text)
	}

	// config.json wins over plugin.json either way: switched back on, a
	// plugin.json that ships disabled does not turn it off.
	write(config, `{"plugins":{"deja":{"enabled":true}}}`)
	write(plugin, `{"name":"deja","disabled":true}`)
	if r := row(); r.SwitchedOff {
		t.Errorf("enabled in config.json: %+v, want on", r)
	}
	// With no entry, plugin.json's own declaration decides.
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if r := row(); !r.SwitchedOff {
		t.Errorf("plugin.json disabled, no config entry: %+v, want switched_off", r)
	}
}

// A store root deja was *told* about is not a store that is there. Antigravity
// is the one harness whose roots come back unchecked — the env override is
// returned as given — so a machine with the variable pointing at a directory
// that does not exist reported `found` with nothing in it, where every other
// row says `missing`. doctor's whole job is saying which of those it is.
func TestDoctorSaysMissingForAnAntigravityRootThatIsNotThere(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-such-antigravity"))

	var buf bytes.Buffer
	if err := runDoctor(&buf, []string{"--offline"}, stubLookup("1.0.0", false), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	row := harnessRow(t, buf.String(), "antigravity")
	if !strings.Contains(row, "missing") {
		t.Fatalf("row = %q — a root that is not on disk is missing, not found", row)
	}

	// And it flips the moment the directory is there, so this is about the
	// store rather than about the variable.
	root := filepath.Join(tmp, "antigravity-here")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	buf.Reset()
	if err := runDoctor(&buf, []string{"--offline"}, stubLookup("1.0.0", false), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if row := harnessRow(t, buf.String(), "antigravity"); !strings.Contains(row, "found") {
		t.Fatalf("row = %q — the directory exists, so the store is there", row)
	}
}

// The MCP install writes the same config the hooks live in, so doctor found a
// file without the hook marker and told the reader to reinstall — which writes
// the MCP entry again and no hook. The hook comes from the -auto target
// (#3313).
func TestDoctorNamesTheAutoTargetForAnMCPOnlyInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installCrushMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install crush: %v", err)
	}
	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	var line string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "crush ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no crush row:\n%s", buf.String())
	}
	if !strings.Contains(line, "deja install crush-auto") {
		t.Errorf("the row does not name the command that adds the hook: %q", line)
	}
	if strings.Contains(line, "reinstall)") {
		t.Errorf("the row still advises the install that cannot fix it: %q", line)
	}
	_ = os.Remove(filepath.Join(home, ".config", "crush", "crush.json"))
}

// doctor is where someone looks when recall goes quiet. A harness deja can
// wire for auto-recall but doctor does not report is a hole exactly there —
// this pins the two lists together so a new harness cannot open one.
func TestDoctorCoversEveryAutoTarget(t *testing.T) {
	covered := map[string]bool{
		// The two that predate the table and print their own lines.
		"claude": true, "codex": true,
	}
	for _, a := range autoWirings() {
		covered[a.name] = true
	}
	for _, target := range installTargetNames() {
		name, ok := strings.CutSuffix(target, "-auto")
		if !ok {
			continue
		}
		if !covered[name] {
			t.Fatalf("install target %q has no doctor row: add it to autoWirings()", target)
		}
	}
}

// A file that exists but no longer calls deja is the shape of every dead
// integration: the install looks done and nothing ever fires.
func TestDoctorReportsStaleWiring(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// an old extension that calls nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	out := buf.String()
	line := ""
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, " pi ") {
			line = l
		}
	}
	if !strings.Contains(line, "stale") {
		t.Fatalf("an extension with no deja call is not reported stale:\n%s", out)
	}
	// Missing and stale must not read the same: one is "never installed",
	// the other "installed and broken", and they need different fixes.
	if !strings.Contains(out, "missing") {
		t.Fatalf("nothing reported missing in an otherwise empty home:\n%s", out)
	}
}

func TestDoctorReportsWiredWhenTheMarkerIsThere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if _, err := installPiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, " pi ") && !strings.Contains(l, "wired") {
			t.Fatalf("a fresh install is not reported wired: %q", l)
		}
	}
}

// Every target the installer accepts must be reachable from the shell too:
// the fish script had fallen seven harnesses behind before this was shared.
func TestCompletionListsEveryInstallTarget(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out := captureStdout(t, func() {
			if err := runCompletion([]string{shell}); err != nil {
				t.Fatalf("%s: %v", shell, err)
			}
		})
		if strings.Contains(out, "%INSTALL_TARGETS%") {
			t.Fatalf("%s script still has the placeholder", shell)
		}
		for _, target := range []string{"cline-auto", "goose-auto", "hermes-auto", "roo", "aider", "openclaw-auto"} {
			if !strings.Contains(out, target) {
				t.Fatalf("%s completion never offers %q", shell, target)
			}
		}
	}
}

// Codex gates hooks behind a trust store of its own, keyed per hook in
// config.toml. Measured on codex 0.142.4: a home whose hooks.json is perfectly
// wired but which codex has never been shown runs no hook at all under
// `codex exec` — no marker — while the same run with
// --dangerously-bypass-hook-trust runs it. That is the state that looks
// healthiest and works least, and deja used to call it "wired".
//
// The pin itself is not checkable from here. Codex's trusted_hash is not the
// sha256 of the hook file, and deja comparing the two called every working
// install untrusted — on the machine this was written on, doctor said untrusted
// while codex was demonstrably delivering deja's recall to the model.
func TestCodexHookTrustSectionStopsAtTheNextTable(t *testing.T) {
	hooks := map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{
		map[string]any{"type": "command", "command": "/usr/local/bin/deja hook-context"},
	}}}}
	cfg := `[hooks.state."/h/hooks.json:session_start:0:0"]
trusted_hash = "sha256:abc"

[projects."/some/other"]
enabled = false
`
	got := codexDejaPins(cfg, "/h/hooks.json", hooks)["SessionStart"]
	if !strings.Contains(got, "trusted_hash") {
		t.Fatalf("the hook's own pin is missing from its section: %q", got)
	}
	if strings.Contains(got, "enabled = false") {
		t.Errorf("the section ran on into an unrelated table, whose enabled flag would decide our status: %q", got)
	}
	if codexDejaPins("[projects.\"/x\"]\nenabled = true\n", "/h/hooks.json", hooks)["SessionStart"] != "" {
		t.Error("a config that never mentions the hook reported a section for it")
	}
}

// Goose and Hermes keep MCP servers in YAML and nest ours under a key, so a
// plain "deja appears in the file" check passes on a disabled or unrelated
// entry — these two probes exist for that and had no test.
func TestYAMLWiringProbes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name  string
		body  string
		probe func(string) bool
		want  bool
	}{
		{"goose wired", "extensions:\n  deja:\n    enabled: true\n", doctorGooseWired, true},
		{"goose absent", "extensions:\n  memory:\n    enabled: true\n", doctorGooseWired, false},
		{"goose mentions deja elsewhere", "# installed by deja\nextensions:\n  memory:\n    enabled: true\n", doctorGooseWired, false},
		{"hermes wired", "mcp_servers:\n  deja:\n    command: /bin/deja\n", doctorHermesWired, true},
		{"hermes absent", "mcp_servers:\n  other:\n    command: /bin/other\n", doctorHermesWired, false},
		{"hermes plugin only", "plugins:\n  enabled:\n    - deja\n", doctorHermesWired, false},
	}
	for _, tc := range cases {
		path := filepath.Join(dir, tc.name+".yaml")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := tc.probe(path); got != tc.want {
			t.Fatalf("%s: probe = %v, want %v", tc.name, got, tc.want)
		}
	}
	// A missing file is not wired, and must not panic.
	if doctorGooseWired(filepath.Join(dir, "absent.yaml")) || doctorHermesWired(filepath.Join(dir, "absent.yaml")) {
		t.Fatal("a missing config reported as wired")
	}
}

func TestDoctorReportsOutdatedPluginScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// An older generated plugin that calls hook-context and legacy `deja search --json`,
	// missing hook-prompt and hook-tool-after (#5770).
	legacy := "// generated by deja install — safe to delete\nconst raw = exec('deja hook-context && deja search --json')\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	out := buf.String()
	if !strings.Contains(out, "outdated plugin script — run `deja install pi-auto` to update") {
		t.Fatalf("expected outdated plugin script warning in doctorAutoRecall:\n%s", out)
	}
	for _, a := range autoWirings() {
		if a.name == "pi" {
			if st, _ := autoWiringState(a); st != "stale" {
				t.Fatalf("autoWiringState(pi) = %q, want stale", st)
			}
		}
	}
}

// doctor is what people run when memory looks absent, and during the first
// build it called the index missing and told them to start the build that was
// already running (#873).
func TestDoctorSaysWhenTheIndexIsBeingBuilt(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	status := warmupStatus{Phase: "reading sessions", Total: 100, Done: 42, Started: time.Now().UnixNano(), Updated: time.Now().UnixNano()}
	b, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warmupStatusPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "building now (reading sessions 42%)") {
		t.Errorf("doctor does not mention the build in flight:\n%s", got)
	}
	if strings.Contains(got, "run `deja warmup`") {
		t.Errorf("doctor still tells the reader to start what is running:\n%s", got)
	}

	// With no build running, the advice is the right one.
	if err := os.Remove(warmupStatusPath(dir)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	if got := out.String(); !strings.Contains(got, "not built (run `deja warmup`)") {
		t.Errorf("an index that is really missing lost its advice:\n%s", got)
	}
}

// Kilo CLI and ZCode keep their sessions in a database beside the files the
// JSON row was built from, and the row never looked at it: with the CLI alone,
// `doctor --json` said "missing" with no paths about a store the text row
// called found and the index held whole (#4397).
func TestDoctorJSONCountsTheCLIDatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "absent"))
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent-too"))
	dbs := map[string]string{
		"kilocode": filepath.Join(home, "kilo.db"),
		"zcode":    filepath.Join(home, "db.sqlite"),
	}
	t.Setenv("DEJA_KILO_DB", dbs["kilocode"])
	t.Setenv("DEJA_ZCODE_DB", dbs["zcode"])
	const seed = `
create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/proj',1767322800000,1767322800000);
insert into message values ('m1','s1','{"role":"user","time":{"created":1767322800000}}',1767322800000);
insert into part values ('p1','m1',json_object('type','text','text','fix the retry loop','time',json_object('start',1767322800000)));
`
	for _, db := range dbs {
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, seed).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3 seed: %v %s", err, out)
		}
	}
	if _, err := os.Stat(dbs["kilocode"]); err != nil {
		t.Fatal(err)
	}

	report := collectDoctorReport(nil, t.TempDir())
	for name, db := range dbs {
		var row *doctorStore
		for i := range report.Stores {
			if report.Stores[i].Name == name {
				row = &report.Stores[i]
			}
		}
		if row == nil {
			t.Errorf("no %s row", name)
			continue
		}
		if row.State != "ok" {
			t.Errorf("%s: state %q about a database holding a session, want ok", name, row.State)
		}
		if !slices.Contains(row.Paths, db) {
			t.Errorf("%s: paths %v do not name %s", name, row.Paths, db)
		}
		if row.Files != 1 {
			t.Errorf("%s: files = %d, want 1", name, row.Files)
		}
	}
}

// Kimi, Qwen, Cursor, Crush, ZCode and Command Code keep deja's hooks in the
// client's own config, which exists whether deja ever wrote to it or not. A
// file holding no deja entry is a machine that was never wired and reads
// missing; stale is for deja's own entry gone wrong (#4275).
func TestDoctorCallsAClientConfigWithoutDejaMissing(t *testing.T) {
	const hook = "/usr/local/bin/deja hook-precompact"
	for _, name := range []string{"kimi", "qwen", "cursor", "crush", "zcode", "commandcode", "clawcode"} {
		t.Run(name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("KIMI_CODE_HOME", "")
			t.Setenv("CURSOR_CONFIG_DIR", "")
			t.Setenv("CRUSH_GLOBAL_CONFIG", "")
			var a autoWiring
			for _, w := range autoWirings() {
				if w.name == name {
					a = w
				}
			}
			path := a.path()
			if !strings.HasPrefix(path, tmp) {
				t.Fatalf("%s config %s is outside the test home", name, path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(s string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			row := func() string {
				t.Helper()
				var buf bytes.Buffer
				doctorAutoRecall(&buf)
				for l := range strings.SplitSeq(buf.String(), "\n") {
					if strings.HasPrefix(l, "  "+name+" ") {
						return l
					}
				}
				t.Fatalf("no %s row:\n%s", name, buf.String())
				return ""
			}

			// The client's own settings, and an MCP server that is not a hook.
			if name == "kimi" {
				write("default_model = \"luna\"\n")
			} else {
				write(`{"model": "luna", "mcpServers": {"deja": {"command": "/usr/local/bin/deja", "args": ["mcp"]}}}`)
			}
			if state, _ := autoWiringState(a); state != "missing" {
				t.Errorf("a config with no deja hook: state %q, want missing", state)
			}
			if l := row(); !strings.Contains(l, "missing") || strings.Contains(l, "stale") {
				t.Errorf("a config with no deja hook: %q, want missing", l)
			}
			if !nothingWired() {
				t.Error("a config with no deja hook counted as a wired agent")
			}

			// Control: deja's own entry without the hook the row looks for.
			if name == "kimi" {
				write("default_model = \"luna\"\n\n" + kimiHookEntry("PreCompact", hook) + "\n")
			} else {
				write(`{"hooks": {"PreCompact": [{"hooks": [{"type": "command", "command": "` + hook + `"}]}]}}`)
			}
			if state, _ := autoWiringState(a); state != "stale" {
				t.Errorf("deja's entry without the %s call: state %q, want stale", a.marker, state)
			}
			if l := row(); !strings.Contains(l, "stale") {
				t.Errorf("deja's entry without the %s call: %q, want stale", a.marker, l)
			}
			if nothingWired() {
				t.Error("deja's own entry counted as nothing wired")
			}
		})
	}
}

// The brief's "no agent wired yet" line reads the same rows doctor prints. A
// Kimi plugin recalls with no block in config.toml, and Claude Code's hooks
// live outside the table; either is a wired agent beside a client config deja
// never wrote to (#4275).
func TestNothingWiredCountsThePluginAndClaudeHooks(t *testing.T) {
	t.Run("kimi plugin", func(t *testing.T) {
		tmp := hermeticEnv(t)
		home := filepath.Join(tmp, "kimi")
		t.Setenv("KIMI_CODE_HOME", home)
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("default_model = \"luna\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !nothingWired() {
			t.Fatal("control: a bare config.toml and no plugin already counts as wired")
		}
		writeKimiPlugin(t, home, true)
		if nothingWired() {
			t.Error("the Kimi plugin recalls on every prompt, and the brief says no agent is wired")
		}
	})
	// Codex's hooks.json is codex's own file: one holding only the user's
	// hooks is not deja's, whatever codex's trust store says about it.
	for _, trust := range []string{"no config.toml", "untrusted", "trusted"} {
		t.Run("codex user's own hooks only/"+trust, func(t *testing.T) {
			hermeticEnv(t)
			hooks := filepath.Join(sources.CodexHome(), "hooks.json")
			if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
				t.Fatal(err)
			}
			own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/my-notes --start"}]}]}}`
			if err := os.WriteFile(hooks, []byte(own), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := ""
			switch trust {
			case "untrusted":
				cfg = "model = \"luna\"\n"
			case "trusted":
				cfg = "[hooks.state." + strconv.Quote(hooks+":session_start:0:0") + "]\ntrusted_hash = \"sha256:abc\"\n"
			}
			if cfg != "" {
				if err := os.WriteFile(filepath.Join(sources.CodexHome(), "config.toml"), []byte(cfg), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if !nothingWired() {
				t.Error("a codex hooks.json with only the user's own hooks counted as a wired agent")
			}
		})
	}
	// The Codex plugin carries deja's hooks under its own root, so a
	// hooks.json beside it with only the user's hook leaves codex wired.
	t.Run("codex plugin beside the user's hooks", func(t *testing.T) {
		hermeticEnv(t)
		home := sources.CodexHome()
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/my-notes --start"}]}]}}`
		if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		if !nothingWired() {
			t.Fatal("control: the user's own codex hook already counts as wired")
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[plugins.\"deja-vu@deja-vu\"]\nenabled = true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if nothingWired() {
			t.Error("the Codex plugin is enabled, and the brief says no agent is wired")
		}
	})
	t.Run("codex deja hooks", func(t *testing.T) {
		hermeticEnv(t)
		if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
			t.Fatalf("install codex-auto: %v", err)
		}
		if nothingWired() {
			t.Error("deja's codex hooks are wired, and the brief says no agent is")
		}
	})
	t.Run("claude hooks", func(t *testing.T) {
		hermeticEnv(t)
		qwen := filepath.Join(sources.QwenConfigDir(), "settings.json")
		if err := os.MkdirAll(filepath.Dir(qwen), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(qwen, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !nothingWired() {
			t.Fatal("control: an empty qwen settings.json already counts as wired")
		}
		if _, err := installClaudeAuto("/usr/local/bin/deja", false); err != nil {
			t.Fatalf("install claude-auto: %v", err)
		}
		if nothingWired() {
			t.Error("Claude Code's hooks are wired, and the brief says no agent is")
		}
	})
}

// A Windows install quotes a path with a space, and TOML escapes the quotes:
// the line has to be read unescaped to see the binary (#4275).
func TestDejaHookInReadsAnEscapedQuotedPath(t *testing.T) {
	line := `command = "\"C:/Program Files/deja/deja.exe\" hook-prompt --plain"` + "\n"
	if !dejaHookIn("[[hooks]]\nevent = \"UserPromptSubmit\"\n" + line) {
		t.Errorf("a hook naming a quoted Windows path is not read as deja's: %s", line)
	}
	if dejaHookIn("[[hooks]]\n" + `command = "\"C:/Program Files/other/tool.exe\" hook-prompt"` + "\n") {
		t.Error("another tool's hook-prompt read as deja's")
	}
}

// Every client keeps its own way to turn deja off without removing it: a
// switch on the entry in a YAML or TOML config, a deny list or MCP master
// switch beside the servers, a switch for all hooks, a disable on deja's own
// extension or plugin. Doctor read only the JSON entry flag, so each of these
// left the row `wired` in text and JSON while the client ran nothing (#4466,
// #4468, #4469, #4470).
func TestDoctorReadsTheClientsOwnOffSwitches(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	editJSON := func(t *testing.T, path string, edit func(map[string]any)) {
		t.Helper()
		root := map[string]any{}
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		edit(root)
		b, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setJSON := func(path string, keys []string, v any) func(*testing.T) {
		return func(t *testing.T) {
			t.Helper()
			editJSON(t, path, func(root map[string]any) {
				m := root
				for _, k := range keys[:len(keys)-1] {
					next, _ := m[k].(map[string]any)
					if next == nil {
						next = map[string]any{}
						m[k] = next
					}
					m = next
				}
				m[keys[len(keys)-1]] = v
			})
		}
	}
	replace := func(path func() string, old, updated string) func(*testing.T) {
		return func(t *testing.T) {
			t.Helper()
			b, err := os.ReadFile(path())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), old) {
				t.Fatalf("%s has no %q:\n%s", path(), old, b)
			}
			if err := os.WriteFile(path(), []byte(strings.Replace(string(b), old, updated, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	appendTo := func(path func() string, text string) func(*testing.T) {
		return func(t *testing.T) {
			t.Helper()
			b, _ := os.ReadFile(path())
			if err := os.MkdirAll(filepath.Dir(path()), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path(), append(b, text...), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	prepend := func(path func() string, text string) func(*testing.T) {
		return func(t *testing.T) {
			t.Helper()
			b, _ := os.ReadFile(path())
			if err := os.WriteFile(path(), append([]byte(text), b...), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	lazy := func(f func() string) func() string { return f }
	codexToml := lazy(func() string { return filepath.Join(sources.CodexHome(), "config.toml") })
	grokToml := lazy(func() string { return filepath.Join(sources.GrokHome(), "config.toml") })
	// Codex runs no hook it has not been shown, so its row is wired only with
	// every event trusted — the control the switch is then measured against.
	trustCodex := func(t *testing.T) {
		t.Helper()
		var pins strings.Builder
		hooks := filepath.Join(sources.CodexHome(), "hooks.json")
		for _, h := range codexHookWiring {
			pins.WriteString(codexTrustPin(hooks, h.Event))
		}
		appendTo(codexToml, pins.String())(t)
	}

	cases := []struct {
		name, target, section, row, key string
		before                          func(*testing.T)
		off                             func(*testing.T)
	}{
		// #4466: the entry's own switch, in YAML and TOML.
		{name: "goose entry", target: "goose", section: "mcp", row: "goose", key: "extensions.deja.enabled",
			off: replace(func() string { return filepath.Join(gooseConfigDir(), "config.yaml") }, "enabled: true", "enabled: false")},
		{name: "hermes entry", target: "hermes", section: "mcp", row: "hermes", key: "mcp_servers.deja.enabled",
			off: replace(func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }, "enabled: true", "enabled: false")},
		{name: "codex entry", target: "codex", section: "mcp", row: "codex", key: "[mcp_servers.deja] enabled",
			off: appendTo(codexToml, "enabled = false\n")},
		{name: "grok entry", target: "grok", section: "mcp", row: "grok", key: "[mcp_servers.deja] enabled",
			off: appendTo(grokToml, "enabled = false\n")},
		{name: "dsh row", target: "deepseek", section: "mcp", row: "deepseek", key: "mcp-deja",
			off: replace(dshPatchPath, "- id: mcp-deja\n", "- id: mcp-deja\n      disabled: true\n")},
		// #4468: deny lists and MCP master switches outside the entry.
		{name: "gemini mcp disable", target: "gemini", section: "mcp", row: "gemini", key: "mcp-server-enablement.json",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.GeminiHome(), "mcp-server-enablement.json"), []string{"deja", "enabled"}, false)(t)
			}},
		{name: "gemini excluded", target: "gemini", section: "mcp", row: "gemini", key: "mcp.excluded",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.GeminiHome(), "settings.json"), []string{"mcp", "excluded"}, []any{"deja"})(t)
			}},
		{name: "gemini allowed", target: "gemini", section: "mcp", row: "gemini", key: "mcp.allowed",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.GeminiHome(), "settings.json"), []string{"mcp", "allowed"}, []any{"other"})(t)
			}},
		{name: "qwen excluded", target: "qwen", section: "mcp", row: "qwen", key: "mcp.excluded",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), []string{"mcp", "excluded"}, []any{"deja"})(t)
			}},
		{name: "copilot", target: "copilot", section: "mcp", row: "copilot", key: "disabledMcpServers",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(filepath.Dir(copilotMCPConfigPath()), "settings.json"), []string{"disabledMcpServers"}, []any{"deja"})(t)
			}},
		{name: "grok disable", target: "grok", section: "mcp", row: "grok", key: "disabled_mcp_servers",
			off: prepend(grokToml, "disabled_mcp_servers = [\"deja\"]\n\n")},
		{name: "omp", target: "omp", section: "mcp", row: "omp", key: "disabledServers",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.OmpConfigDir(), "mcp.json"), []string{"disabledServers"}, []any{"deja"})(t)
			}},
		{name: "gjc", target: "gjc", section: "mcp", row: "gjc", key: "disabledServers",
			off: func(t *testing.T) { t.Helper(); setJSON(gjcMCPPath(), []string{"disabledServers"}, []any{"deja"})(t) }},
		{name: "vscode", target: "vscode", section: "mcp", row: "vscode", key: "chat.mcp.access",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(filepath.Dir(doctorVSCodeMCPPath()), "settings.json"), []string{"chat.mcp.access"}, "none")(t)
			}},
		{name: "zcode", target: "zcode", section: "mcp", row: "zcode", key: "features.mcp",
			off: func(t *testing.T) { t.Helper(); setJSON(zcodeConfigPath(), []string{"features", "mcp"}, false)(t) }},
		{name: "openclaw", target: "openclaw", section: "mcp", row: "openclaw", key: "tools.deny",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"tools", "deny"}, []any{"bundle-mcp"})(t)
			}},
		{name: "opencode", target: "opencode", section: "mcp", row: "opencode", key: "tools",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(doctorOpencodeConfigPath(), []string{"tools", "deja*"}, false)(t)
			}},
		{name: "amp", target: "amp", section: "mcp", row: "amp", key: "amp.tools.disable",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(sources.AmpSettingsFile(), []string{"amp.tools.disable"}, []any{"mcp__deja__*"})(t)
			}},
		{name: "claude project", target: "claude-code", section: "mcp", row: "claude-code", key: "disabledMcpServers",
			off: func(t *testing.T) {
				t.Helper()
				// The repository's main root, which TestPerProjectListsAreReadForTheDirectoryTheClientKeys pins.
				setJSON(sources.ClaudeJSONPath(), []string{"projects", doctorProjectDir(true), "disabledMcpServers"}, []any{"deja"})(t)
			}},
		{name: "cursor project", target: "cursor", section: "mcp", row: "cursor", key: "mcp-disabled.json",
			off: func(t *testing.T) {
				t.Helper()
				root := gitRootOf(filepath.Join(cwd, "x"))
				if root == "" {
					root = cwd
				}
				// cursor-agent's slug: every run of non-alphanumerics one dash.
				slug := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(root, "-"), "-")
				path := filepath.Join(os.Getenv("HOME"), ".cursor", "projects", slug, "mcp-disabled.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`["deja"]`), 0o644); err != nil {
					t.Fatal(err)
				}
			}},
		// #4469: the client's switch for every hook.
		{name: "claude hooks", target: "claude-auto", section: "auto_recall", row: "claude-code", key: "disableAllHooks",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.ClaudeConfigDir(), "settings.json"), []string{"disableAllHooks"}, true)(t)
			}},
		{name: "qwen hooks", target: "qwen-auto", section: "auto_recall", row: "qwen", key: "disableAllHooks",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), []string{"disableAllHooks"}, true)(t)
			}},
		{name: "gemini hooks", target: "gemini-auto", section: "auto_recall", row: "gemini", key: "hooksConfig.enabled",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.GeminiHome(), "settings.json"), []string{"hooksConfig", "enabled"}, false)(t)
			}},
		{name: "codex hooks", target: "codex-auto", section: "auto_recall", row: "codex-hook", key: "[features] hooks",
			before: trustCodex, off: appendTo(codexToml, "\n[features]\nhooks = false\n")},
		{name: "openclaw internal hooks", target: "openclaw-auto", section: "auto_recall", row: "openclaw", key: "hooks.internal.enabled",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"hooks", "internal", "enabled"}, false)(t)
			}},
		// #4470: deja's own extension or plugin, disabled through the client.
		{name: "gemini extension", target: "gemini-auto", section: "auto_recall", row: "gemini", key: "extension-enablement.json",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.GeminiHome(), "extensions", "extension-enablement.json"),
					[]string{"deja", "overrides"}, []any{"!" + geminiWorkspaceRule(cwd) + "*"})(t)
			}},
		{name: "openclaw hook entry", target: "openclaw-auto", section: "auto_recall", row: "openclaw", key: "hooks.internal.entries.deja-recall.enabled",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"hooks", "internal", "entries", "deja-recall", "enabled"}, false)(t)
			}},
		{name: "openclaw plugin", target: "openclaw-auto", section: "auto_recall", row: "openclaw", key: "plugins.entries",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), []string{"plugins", "entries", "deja", "enabled"}, false)(t)
			}},
		{name: "goose plugin", target: "goose-auto", section: "auto_recall", row: "goose", key: "disabledPlugins",
			off: func(t *testing.T) {
				t.Helper()
				// ~/.config/goose on every OS, Windows included, where
				// config.yaml is under %APPDATA% (discovery.rs user_settings_path).
				setJSON(filepath.Join(sources.Home(), ".config", "goose", "settings.json"), []string{"disabledPlugins"}, []any{"deja"})(t)
			}},
		{name: "copilot hooks", target: "copilot-auto", section: "auto_recall", row: "copilot", key: "disableAllHooks",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.CopilotHome(), "settings.json"), []string{"disableAllHooks"}, true)(t)
			}},
		{name: "pi extension", target: "pi-auto", section: "auto_recall", row: "pi", key: "-extensions/deja.ts",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.PiConfigDir(), "settings.json"), []string{"extensions"}, []any{"-extensions/deja.ts"})(t)
			}},
		{name: "senpi extension", target: "senpi-auto", section: "auto_recall", row: "senpi", key: "-extensions/deja.ts",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.SenpiConfigDir(), "settings.json"), []string{"extensions"}, []any{"-extensions/deja.ts"})(t)
			}},
		{name: "omp extension", target: "omp-auto", section: "auto_recall", row: "omp", key: "disabledExtensions",
			off: appendTo(func() string { return filepath.Join(sources.OmpConfigDir(), "config.yml") },
				"disabledExtensions:\n  - extension-module:deja\n")},
		{name: "gjc extension", target: "gjc-auto", section: "auto_recall", row: "gjc", key: "disabledExtensions",
			off: appendTo(func() string { return filepath.Join(sources.GjcConfigDir(), "config.yml") },
				"disabledExtensions: [extension-module:deja]\n")},
		{name: "cline plugin", target: "cline-auto", section: "auto_recall", row: "cline", key: "disabledPlugins",
			off: func(t *testing.T) {
				t.Helper()
				setJSON(filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), []string{"disabledPlugins"},
					[]any{filepath.Join(sources.ClinePluginsDir(), "deja", "index.js")})(t)
			}},
		{name: "hermes plugin", target: "hermes-auto", section: "auto_recall", row: "hermes", key: "plugins.enabled",
			off: replace(func() string { return filepath.Join(sources.HermesHome(), "config.yaml") },
				"  enabled:\n    - deja\n", "  enabled: []\n  disabled:\n    - deja\n")},
		{name: "dsh auto row", target: "deepseek-auto", section: "auto_recall", row: "deepseek", key: "deja-auto",
			off: replace(dshPatchPath, "- id: deja-auto\n", "- id: deja-auto\n      disabled: true\n")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			bin := filepath.Join(os.Getenv("HOME"), "bin", "deja")
			if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(c.target, bin, false); err != nil {
				t.Fatal(err)
			}
			if c.before != nil {
				c.before(t)
			}
			row := func() (state string, off bool) {
				t.Helper()
				out, err := captureRun(t, "doctor", "--json")
				if err != nil {
					t.Fatal(err)
				}
				var report map[string]json.RawMessage
				var rows []struct {
					Name        string `json:"name"`
					State       string `json:"state"`
					SwitchedOff bool   `json:"switched_off"`
				}
				if err := json.Unmarshal([]byte(out), &report); err != nil {
					t.Fatalf("doctor --json: %v", err)
				}
				if err := json.Unmarshal(report[c.section], &rows); err != nil {
					t.Fatalf("doctor --json %s: %v", c.section, err)
				}
				for _, r := range rows {
					if r.Name == c.row {
						return r.State, r.SwitchedOff
					}
				}
				t.Fatalf("no %s row in %s:\n%s", c.row, c.section, out)
				return "", false
			}
			if state, off := row(); state != "wired" || off {
				t.Fatalf("installed: %s switched_off=%v, want wired and on", state, off)
			}
			c.off(t)
			if state, off := row(); state != "wired" || !off {
				t.Errorf("after the client's switch: %s switched_off=%v, want wired and switched_off", state, off)
			}
			text, err := captureRun(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, c.key) {
				t.Errorf("the text report does not name %s:\n%s", c.key, text)
			}
		})
	}
}

// An install over a client that keeps deja off by a switch install does not
// touch said only `unchanged` or `updated`, which reads as working (#4468,
// #4469).
func TestInstallSaysTheClientHasDejaSwitchedOff(t *testing.T) {
	hermeticEnv(t)
	settings := filepath.Join(sources.GeminiHome(), "settings.json")
	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	for path, body := range map[string]string{
		settings: `{"mcp":{"excluded":["deja"]}}`,
		claude:   `{"disableAllHooks":true}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureRun(t, "install", "gemini", "claude-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`mcp.excluded` lists deja", "`disableAllHooks: true`"} {
		if !strings.Contains(out, want) {
			t.Errorf("install does not say %s:\n%s", want, out)
		}
	}
	// Control: with the switches gone, install has nothing to add.
	for _, path := range []string{settings, claude} {
		if err = os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err = captureRun(t, "install", "gemini", "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "switched off") {
		t.Errorf("install calls an enabled client switched off:\n%s", out)
	}
}

// The readers answer "off" only for what the client would read as off.
func TestClientOffReadersStayOnWhenUnsure(t *testing.T) {
	if _, _, found := yamlLookup("plugins:\n  enabled: [\n    deja ]\n", "plugins", "enabled"); found {
		t.Error("a flow list over several lines read as an empty one")
	}
	if _, items, _ := yamlLookup("plugins:\n  enabled:\n  - spotify\n  - deja\n", "plugins", "enabled"); !anyDeja(items) {
		t.Errorf("items at the key's own indent: %q", items)
	}
	if !geminiExtensionEnabled([]string{"!/elsewhere/*"}, "/work/app") {
		t.Error("a rule for another directory turned the extension off")
	}
	if geminiExtensionEnabled([]string{"!/work/*"}, "/work/app") {
		t.Error("a subdirectory rule did not reach /work/app")
	}
	if !geminiExtensionEnabled([]string{"!/work/*", "/work/app/"}, "/work/app") {
		t.Error("the later rule did not win")
	}
	if got := piExtensionExcluded([]string{"!*", "+extensions/deja.ts"}, "/home/x/.pi/agent"); got != "" {
		t.Errorf("a force-include lost to a glob exclude: %q", got)
	}
	if !dshRowDisabled("- insert:\n    - id: mcp-deja\n      disabled: true\n    - id: deja-auto\n", "mcp-deja") ||
		dshRowDisabled("- insert:\n    - id: mcp-deja\n      disabled: true\n    - id: deja-auto\n", "deja-auto") {
		t.Error("a row's switch read for the wrong row")
	}
	if tomlDejaEntriesOff("[mcp_servers.deja]\ncommand = \"deja\"\n\n[other]\nenabled = false\n") {
		t.Error("another table's enabled read as deja's")
	}
	// Text inside a string is not a key, and YAML after a second document
	// marker is one the clients' loaders refuse rather than read.
	for _, y := range []string{
		"extensions:\n  deja:\n    description: \"two\n    enabled: false\"\n",
		"extensions:\n  deja:\n    description: 'two\n    enabled: false'\n",
		"extensions:\n  deja:\n    enabled: true\n---\nextensions:\n  deja:\n    enabled: false\n",
	} {
		if v, _, _ := yamlLookup(y, "extensions", "deja", "enabled"); v == "false" {
			t.Errorf("read as off: %q", y)
		}
	}
	for _, q := range []string{`"""`, `'''`} {
		toml := "[mcp_servers.deja]\ncommand = \"deja\"\nnote = " + q + "\nenabled = false\n" + q + "\n"
		if tomlDejaEntriesOff(toml) {
			t.Errorf("a line inside a %s string read as the switch", q)
		}
		if tomlTableValue("[features]\nnote = "+q+"\nhooks = false\n"+q+"\n", "features", "hooks") != "" {
			t.Errorf("a line inside a %s string read as [features] hooks", q)
		}
	}
}

// `deja install --all` adds the -auto sibling of a wired target, and both
// lines carried the same note about the same file (#4468).
func TestInstallSaysAClientSwitchOnce(t *testing.T) {
	hermeticEnv(t)
	settings := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"mcp":{"excluded":["deja"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "gemini", "gemini-auto", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "`mcp.excluded` lists deja"); n != 1 {
		t.Errorf("the note is said %d times, want once:\n%s", n, out)
	}
}

// Gemini starts every server when mcp.allowed is empty and compares names
// exactly; qwen reads both lists as `*` and `?` patterns and an empty allow
// list as none. Each case is how the client itself answers.
func TestGeminiAndQwenServerListsReadAsTheClientReadsThem(t *testing.T) {
	cases := []struct {
		name, client, settings, enablement string
		off                                bool
	}{
		{"gemini empty allowed", "gemini", `{"mcp":{"allowed":[]}}`, "", false},
		{"gemini allowed without deja", "gemini", `{"mcp":{"allowed":["other"]}}`, "", true},
		{"gemini excluded in another case", "gemini", `{"mcp":{"excluded":["Deja"]}}`, "", false},
		{"gemini enablement key in another case", "gemini", `{}`, `{"Deja":{"enabled":false}}`, false},
		{"gemini enablement", "gemini", `{}`, `{"deja":{"enabled":false}}`, true},
		{"qwen empty allowed", "qwen", `{"mcp":{"allowed":[]}}`, "", true},
		{"qwen allowed by pattern", "qwen", `{"mcp":{"allowed":["de*"]}}`, "", false},
		{"qwen excluded by pattern", "qwen", `{"mcp":{"excluded":["d?ja"]}}`, "", true},
		{"qwen excluded another pattern", "qwen", `{"mcp":{"excluded":["dej"]}}`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			home := sources.GeminiHome()
			if c.client == "qwen" {
				home = sources.QwenConfigDir()
			}
			write := func(p, body string) {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(home, "settings.json"), c.settings)
			if c.enablement != "" {
				write(filepath.Join(home, "mcp-server-enablement.json"), c.enablement)
			}
			if got := clientMCPDenied(c.client) != ""; got != c.off {
				t.Errorf("switched off = %v, want %v (%q)", got, c.off, clientMCPDenied(c.client))
			}
		})
	}
}

// Each client matches deja's id its own way, and a near miss is not an off
// switch: omp and gjc key extensions as extension-module:<name>, cline lists
// the module file, goose reads its plugin settings from ~/.config/goose
// whatever XDG says and lets a project's list overrule the user's, and every
// list compares exactly.
func TestClientPluginAndServerListsMatchOnlyWhatTheClientMatches(t *testing.T) {
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userGoose := func() string { return filepath.Join(os.Getenv("HOME"), ".config", "goose", "settings.json") }
	cases := []struct {
		name, client string
		hooks        bool
		setup        func(t *testing.T)
		off          bool
	}{
		{"omp bare name", "omp", true, func(t *testing.T) {
			t.Helper()
			write(t, filepath.Join(sources.OmpConfigDir(), "config.yml"), "disabledExtensions:\n  - deja\n")
		}, false},
		{"omp module id", "omp", true, func(t *testing.T) {
			t.Helper()
			write(t, filepath.Join(sources.OmpConfigDir(), "config.yml"), "disabledExtensions:\n  - extension-module:deja\n")
		}, true},
		{"cline bare name", "cline", true, func(t *testing.T) {
			t.Helper()
			write(t, filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), `{"disabledPlugins":["deja"]}`)
		}, false},
		{"cline plugin dir", "cline", true, func(t *testing.T) {
			t.Helper()
			b := mustMarshal(t, map[string]any{"disabledPlugins": []string{filepath.Join(sources.ClinePluginsDir(), "deja")}})
			write(t, filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), string(b))
		}, false},
		{"cline module file", "cline", true, func(t *testing.T) {
			t.Helper()
			b := mustMarshal(t, map[string]any{"disabledPlugins": []string{filepath.Join(sources.ClinePluginsDir(), "deja", "index.js")}})
			write(t, filepath.Join(sources.ClineConfigDir(), "settings", "global-settings.json"), string(b))
		}, true},
		{"copilot other case", "copilot", false, func(t *testing.T) {
			t.Helper()
			write(t, filepath.Join(filepath.Dir(copilotMCPConfigPath()), "settings.json"), `{"disabledMcpServers":["Deja"]}`)
		}, false},
		{"goose under XDG", "goose", true, func(t *testing.T) {
			t.Helper()
			xdg := filepath.Join(os.Getenv("HOME"), "xdg")
			t.Setenv("XDG_CONFIG_HOME", xdg)
			write(t, filepath.Join(xdg, "goose", "settings.json"), `{"disabledPlugins":["deja"]}`)
		}, false},
		{"goose user file with XDG set", "goose", true, func(t *testing.T) {
			t.Helper()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), "xdg"))
			write(t, userGoose(), `{"disabledPlugins":["deja"]}`)
		}, true},
		{"goose project enables it", "goose", true, func(t *testing.T) {
			t.Helper()
			write(t, userGoose(), `{"disabledPlugins":["deja"]}`)
			project := filepath.Join(os.Getenv("HOME"), "work")
			write(t, filepath.Join(project, ".config", "goose", "settings.json"), `{"enabledPlugins":["deja"]}`)
			t.Chdir(project)
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Chdir(t.TempDir())
			c.setup(t)
			note := clientMCPDenied(c.client)
			if c.hooks {
				note = clientHooksOff(c.client)
			}
			if got := note != ""; got != c.off {
				t.Errorf("switched off = %v, want %v (%q)", got, c.off, note)
			}
		})
	}
}

// opencode turns its `tools` map into rules and the last key that matches
// decides, in the order the file lists them (#4468).
func TestOpencodeToolsLastMatchingKeyWins(t *testing.T) {
	for body, off := range map[string]bool{
		`{"tools":{"*":false,"deja_deja":true}}`:  false,
		`{"tools":{"deja_deja":true,"*":false}}`:  true,
		`{"tools":{"deja*":false}}`:               true,
		`{"tools":{"deja_[d]eja":false}}`:         false,
		`{"tools":{"deja?deja":false,"x":true}}`:  true,
		`{"tools":{"other_*":false}}`:             false,
		`{"tools":{"deja_*":false,"deja*":true}}`: false,
	} {
		hermeticEnv(t)
		p := doctorOpencodeConfigPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := clientMCPDenied("opencode") != ""; got != off {
			t.Errorf("%s: switched off = %v, want %v", body, got, off)
		}
	}
}

// claude-code keys its per-project lists by the repository's main root and
// cursor-agent by the nearest root with a .git, both by the real path; a
// subdirectory's own entry is one neither reads (#4468).
func TestPerProjectListsAreReadForTheDirectoryTheClientKeys(t *testing.T) {
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claudeOff := func(t *testing.T, dir string) {
		t.Helper()
		b := mustMarshal(t, map[string]any{"projects": map[string]any{dir: map[string]any{"disabledMcpServers": []string{"deja"}}}})
		write(t, sources.ClaudeJSONPath(), string(b))
	}
	cursorOff := func(t *testing.T, dir string) {
		t.Helper()
		write(t, filepath.Join(cursorDataDir(), "projects", cursorProjectSlug(dir), "mcp-disabled.json"), `["deja"]`)
	}
	setup := func(t *testing.T) (main, sub, wt string) {
		t.Helper()
		hermeticEnv(t)
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		main = filepath.Join(base, "repo")
		sub = filepath.Join(main, "pkg")
		if err := os.MkdirAll(filepath.Join(main, ".git", "worktrees", "wt"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
		wt = filepath.Join(base, "wt")
		write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
		return main, sub, wt
	}
	cases := []struct {
		name   string
		client string
		run    func(t *testing.T, main, sub, wt string) string // the dir doctor runs in
		off    bool
	}{
		{"claude subdir entry", "claude-code", func(t *testing.T, _, sub, _ string) string { t.Helper(); claudeOff(t, sub); return sub }, false},
		{"claude repo entry from subdir", "claude-code", func(t *testing.T, main, sub, _ string) string { t.Helper(); claudeOff(t, main); return sub }, true},
		{"claude main entry from worktree", "claude-code", func(t *testing.T, main, _, wt string) string { t.Helper(); claudeOff(t, main); return wt }, true},
		{"claude worktree entry", "claude-code", func(t *testing.T, _, _, wt string) string { t.Helper(); claudeOff(t, wt); return wt }, false},
		{"cursor subdir entry", "cursor", func(t *testing.T, _, sub, _ string) string { t.Helper(); cursorOff(t, sub); return sub }, false},
		{"cursor repo entry from subdir", "cursor", func(t *testing.T, main, sub, _ string) string { t.Helper(); cursorOff(t, main); return sub }, true},
		{"cursor worktree entry", "cursor", func(t *testing.T, _, _, wt string) string { t.Helper(); cursorOff(t, wt); return wt }, true},
		{"claude real path through a link", "claude-code", func(t *testing.T, main, _, _ string) string {
			t.Helper()
			claudeOff(t, main)
			link := filepath.Join(filepath.Dir(main), "link")
			if err := os.Symlink(main, link); err != nil {
				t.Skip(err)
			}
			return link
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			main, sub, wt := setup(t)
			t.Chdir(c.run(t, main, sub, wt))
			if got := clientMCPDenied(c.client) != ""; got != c.off {
				t.Errorf("switched off = %v, want %v (%q)", got, c.off, clientMCPDenied(c.client))
			}
		})
	}
	// Gemini matches its extension rules against the real path too.
	t.Run("gemini extension rule through a link", func(t *testing.T) {
		main, _, _ := setup(t)
		link := filepath.Join(filepath.Dir(main), "link")
		if err := os.Symlink(main, link); err != nil {
			t.Skip(err)
		}
		b, _ := json.Marshal(map[string]any{"deja": map[string]any{"overrides": []string{"!" + geminiWorkspaceRule(main) + "*"}}})
		write(t, filepath.Join(sources.GeminiHome(), "extensions", "extension-enablement.json"), string(b))
		t.Chdir(link)
		if clientHooksOff("gemini") == "" {
			t.Error("a rule over the real path did not reach a directory entered through a link")
		}
	})
}

// A project's own settings file overrides the user's for the hooks switch in
// claude-code, qwen and gemini, so a user-wide off with the project turning
// hooks back on runs them there (#4469).
func TestProjectSettingsTurnTheHooksSwitchBackOn(t *testing.T) {
	cases := []struct {
		client, user, projectFile, project string
	}{
		{"claude-code", `{"disableAllHooks":true}`, ".claude/settings.json", `{"disableAllHooks":false}`},
		{"claude-code", `{"disableAllHooks":true}`, ".claude/settings.local.json", `{"disableAllHooks":false}`},
		{"qwen", `{"disableAllHooks":true}`, ".qwen/settings.json", `{"disableAllHooks":false}`},
		{"gemini", `{"hooksConfig":{"enabled":false}}`, ".gemini/settings.json", `{"hooksConfig":{"enabled":true}}`},
	}
	for _, c := range cases {
		t.Run(c.client+" "+c.projectFile, func(t *testing.T) {
			hermeticEnv(t)
			dir := map[string]string{"claude-code": sources.ClaudeConfigDir(), "qwen": sources.QwenConfigDir(), "gemini": sources.GeminiHome()}[c.client]
			write := func(p, body string) {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(dir, "settings.json"), c.user)
			project := t.TempDir()
			t.Chdir(project)
			if clientHooksOff(c.client) == "" {
				t.Fatal("control: the user's switch alone does not read as off")
			}
			write(filepath.Join(project, filepath.FromSlash(c.projectFile)), c.project)
			if note := clientHooksOff(c.client); note != "" {
				t.Errorf("the project turned hooks back on and doctor still says %q", note)
			}
		})
	}
}

// Two switches the first pass missed: codex still reads `codex_hooks`, the
// feature's old key, and VS Code's `registry` access blocks a server it has
// no gallery entry for, which deja's never has (#4468, #4469).
func TestCodexLegacyHooksKeyAndVSCodeRegistryAccess(t *testing.T) {
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("codex_hooks", func(t *testing.T) {
		hermeticEnv(t)
		p := filepath.Join(sources.CodexHome(), "config.toml")
		write(t, p, "[features]\ncodex_hooks = false\n")
		if clientHooksOff("codex-hook") == "" {
			t.Error("`codex_hooks = false` does not read as off")
		}
		write(t, p, "[features]\ncodex_hooks = false\nhooks = true\n")
		if note := clientHooksOff("codex-hook"); note != "" {
			t.Errorf("the current key set on still reads as off: %q", note)
		}
	})
	t.Run("vscode registry", func(t *testing.T) {
		hermeticEnv(t)
		p := filepath.Join(filepath.Dir(doctorVSCodeMCPPath()), "settings.json")
		write(t, p, `{"chat.mcp.access":"registry"}`)
		if clientMCPDenied("vscode") == "" {
			t.Error("`registry` access does not read as off")
		}
		write(t, p, `{"chat.mcp.access":"all"}`)
		if note := clientMCPDenied("vscode"); note != "" {
			t.Errorf("`all` reads as off: %q", note)
		}
	})
}

// geminiWorkspaceRule is the rule `gemini extensions disable` writes for dir:
// every `\` made `/`, a `/` at both ends (extensionEnablement.ts
// ensureLeadingAndTrailingSlash). Gemini reads a rule from the file as it
// stands, so `C:/x/*` or `C:\x/*` disables nothing there, and the fixtures
// that wrote those failed on Windows (#4509).
func geminiWorkspaceRule(dir string) string {
	dir = strings.ReplaceAll(dir, `\`, "/")
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir
}

// codexTrustPin is a trust pin for deja's hook under event, keyed the way
// codex writes it: a literal '…' key for a path with a backslash, which a
// basic "…" string would read as escapes (#4509).
func codexTrustPin(hooks, event string) string {
	key := hooks + ":" + codexEventKey(event) + ":0:0"
	return "\n[hooks.state." + quoteTOMLKey(key, strings.Contains(key, `\`)) + "]\ntrusted_hash = \"sha256:abc\"\n"
}

// The fixtures above stand in for what gemini and codex write on Windows;
// these pin that a Windows path survives them, which macOS and Linux runs
// would not otherwise check (#4509).
func TestOffSwitchFixturesHoldAWindowsPath(t *testing.T) {
	dir := `C:\Users\x\app`
	if geminiExtensionEnabled([]string{"!" + geminiWorkspaceRule(dir) + "*"}, dir+`\pkg`) {
		t.Errorf("gemini's own rule for %s did not reach a subdirectory: %q", dir, geminiWorkspaceRule(dir))
	}
	hooks := `C:\Users\x\.codex\hooks.json`
	pin := strings.TrimSpace(codexTrustPin(hooks, "SessionStart"))
	key, _, _, _, ok := codexTrustKey(strings.SplitN(pin, "\n", 2)[0])
	if !ok || !strings.HasPrefix(key, hooks+":") {
		t.Errorf("the pin's key does not read back as %s: %q -> %q, %v", hooks, pin, key, ok)
	}
}

// The cline row printed a count and nothing else, so a transcript deja could
// not read was invisible there while every other file-listing harness named it
// (#3360). A session directory keeps `<id>.json` beside `<id>.messages.json` —
// the manifest the reader opens itself for the title, the working directory
// and the timestamps — and that is not a failure either.
func TestDoctorNamesAnUnreadClineTranscriptButNotItsManifest(t *testing.T) {
	tmp := hermeticEnv(t)
	sessions := filepath.Join(tmp, "cline", "data", "sessions")
	dir := filepath.Join(sessions, "1757000000_ab12c")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOT", sessions)

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1757000000_ab12c.messages.json", `[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]}]`)
	write("1757000000_ab12c.json", `{"session_id":"1757000000_ab12c","cwd":"/w/api","metadata":{"title":"retry loop"}}`)

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.Contains(l, "cline") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no cline file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the manifest is the reader's own", got)
	}
	// And the note is there for the thing it exists for: a transcript under a
	// name the reader does not take, which is what a format change looks like.
	write("conversation.json", `[{"role":"user","content":[{"type":"text","text":"anything"}]}]`)
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the transcript deja did not read", got)
	}
}

// The row names the legacy VS Code roots on the same line, so they are walked
// too: a stray file under a legacy task was invisible while the line said the
// root was covered (review of #3360).
func TestDoctorCountsAStrayFileUnderAClineLegacyRoot(t *testing.T) {
	tmp := hermeticEnv(t)
	legacy := filepath.Join(tmp, "vscode", "globalStorage", "saoudrizwan.claude-dev")
	task := filepath.Join(legacy, "tasks", "1757000000")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOTS", legacy)
	t.Setenv("DEJA_CLINE_ROOT", filepath.Join(tmp, "cline", "data", "sessions"))

	if err := os.WriteFile(filepath.Join(task, "api_conversation_history.json"),
		[]byte(`[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task, "ui_messages.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "cline") && strings.Contains(l, "file") {
			if !strings.Contains(l, "1 not recognised here") {
				t.Errorf("row = %q, want the stray file under the legacy root counted", l)
			}
			return
		}
	}
	t.Fatal("no cline file row in the report at all")
}

// The machine form kept its own path per harness, and cline's named only the
// CLI store: on a machine whose transcripts are all in the VS Code extension
// tree the report counted files under a directory it never named, and the
// directory it did name did not exist (#3399).
func TestDoctorJSONNamesTheClineRootTheFilesCameFrom(t *testing.T) {
	legacy := clineLegacyOnlyStore(t)

	check := doctorCheckNamed(t, "cline")
	if len(check.files) != 1 {
		t.Fatalf("the walk found %d files, want the one legacy task", len(check.files))
	}
	tasks := filepath.Join(legacy, "tasks")
	found := false
	for _, p := range check.paths {
		if p == tasks {
			found = true
		}
	}
	if !found {
		t.Errorf("paths = %v, want the legacy tasks tree the transcript came from", check.paths)
	}
}

// And because paths is what the permission walk covers, a legacy tree deja may
// not read was reported as a harness nobody had used.
func TestDoctorJSONSaysDeniedForALockedClineLegacyTree(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	legacy := clineLegacyOnlyStore(t)
	tasks := filepath.Join(legacy, "tasks")
	if err := os.Chmod(tasks, 0o000); err != nil {
		t.Skipf("cannot lock a directory here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(tasks, 0o755) })
	if f, err := os.Open(tasks); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	store, _ := inspectDoctorStore(doctorCheckNamed(t, "cline"))
	if store.State != "denied" {
		t.Errorf("a locked legacy tree reports state %q, want denied", store.State)
	}
	if !strings.Contains(store.Denied, "tasks") {
		t.Errorf("doctor names %q, not the directory it could not read", store.Denied)
	}
}

// clineLegacyOnlyStore is the shape of a VS Code Cline user: one task under the
// extension's globalStorage and no CLI store at all.
func clineLegacyOnlyStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	legacy := filepath.Join(tmp, "vscode", "globalStorage", "saoudrizwan.claude-dev")
	task := filepath.Join(legacy, "tasks", "1757000000000")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOTS", legacy)
	t.Setenv("DEJA_CLINE_ROOT", filepath.Join(tmp, "cline", "data", "sessions"))
	body := `[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"the last attempt is not counted"}]}]`
	if err := os.WriteFile(filepath.Join(task, "api_conversation_history.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return legacy
}

// seedSessionsAhead indexes n transcripts stamped a year from now.
func seedSessionsAhead(t *testing.T, n int) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": fmt.Sprintf("ahead%d", i), "cwd": "/tmp/app",
			"timestamp": time.Now().AddDate(1, 0, i).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": "the pool was exhausted while the migration held the lock"},
		})
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("ahead%d.jsonl", i)), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// The clock row is the sentence a reader lands on when a store reads wrong, and
// the pronoun in it came from the helper `deja last` uses — a helper whose word
// carries its own verb ("that one is at the top of this list"), so spliced after
// "leads with" it read "leads with that one is".
func TestTheClockRowReadsAsASentence(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		want string
	}{
		{"one", 1, "`deja last` leads with it"},
		{"several", 2, "`deja last` leads with one of them"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedSessionsAhead(t, tc.n)
			out, err := captureRun(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			var line string
			for l := range strings.SplitSeq(out, "\n") {
				if strings.Contains(l, "later than this machine's clock") {
					line = strings.TrimSpace(l)
					break
				}
			}
			if line == "" {
				t.Fatalf("doctor said nothing about %d session(s) stamped ahead:\n%s", tc.n, out)
			}
			if !strings.Contains(line, tc.want) {
				t.Errorf("clock row: %q\nwant it to end with %q", line, tc.want)
			}
			for _, broken := range []string{"with that one is", "with those are"} {
				if strings.Contains(line, broken) {
					t.Errorf("clock row splices a verb into the pronoun: %q", line)
				}
			}
		})
	}
}

// A Codex store keeps its plugin cache, model cache, hooks and credentials
// beside the sessions directory; doctor walked the whole root and reported all
// of it as transcripts it could not read — 29 of them on this machine (#3321).
func TestDoctorDoesNotCallCodexCachesUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	day := filepath.Join(root, "sessions", "2026", "09", "07")
	if err := os.MkdirAll(filepath.Join(root, "plugins", "cache", "openai-templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sid := "2f7c1b90-0000-4000-8000-000000000001"
	write(filepath.Join(day, "rollout-2026-09-07T10-00-00-"+sid+".jsonl"),
		`{"type":"session_meta","payload":{"id":"`+sid+`","timestamp":"2026-09-07T10:00:00Z","cwd":"/w/api"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"user_message","message":"why does the retry loop drop the last attempt"}}`+"\n")
	write(filepath.Join(root, "history.jsonl"), `{"session_id":"`+sid+`","text":"why does the retry loop drop the last attempt"}`+"\n")
	for _, name := range []string{"version.json", "models_cache.json", "hooks.json", "auth.json"} {
		write(filepath.Join(root, name), "{}")
	}
	write(filepath.Join(root, "plugins", "cache", "openai-templates", "manifest.json"), "{}")

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.Contains(l, "codex") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no codex file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the caches and the credentials are not transcripts", got)
	}
	// The note still does what it exists for: a rollout under a name the
	// reader does not take.
	write(filepath.Join(day, "rollout.v2.jsonl"), "{}\n")
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the one file in sessions deja did not read", got)
	}
	// And a transcript in the wrong place is exactly what the row is for: a
	// rollout restored to the store root, outside the sessions tree, must not
	// disappear from the count (review of #3321).
	write(filepath.Join(root, "rollout-2026-09-07T11-00-00-"+sid+".jsonl"), "{}\n")
	if got := row(); !strings.Contains(got, "2 not recognised here") {
		t.Errorf("row = %q, want the stray rollout at the store root counted too", got)
	}
	// Neither does the cache swallow a transcript: a .jsonl under it, and a
	// .json at the root under a name deja does not know, are both files it
	// cannot account for (review of #3321).
	write(filepath.Join(root, "plugins", "cache", "openai-templates", "queue.jsonl"), "{}\n")
	write(filepath.Join(root, "newformat-transcript.json"), "{}")
	if got := row(); !strings.Contains(got, "4 not recognised here") {
		t.Errorf("row = %q, want the cache jsonl and the unknown root json counted as well", got)
	}
}

// Codex pins trust per hook, so a machine that approved deja's one hook and
// has five now runs one of them — and its own first screen says so while this
// row said `wired` (#3654).
func TestCodexApprovedHooksCountsPinsPerEvent(t *testing.T) {
	cfg := `
[hooks.state."/Users/x/.codex/hooks.json:session_start:0:0"]
trusted_hash = "sha256:abc"
enabled = true
`
	hooks := map[string]any{}
	for _, h := range codexHookWiring {
		hooks[h.Event] = []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "/usr/local/bin/deja " + h.Sub},
		}}}
	}
	approved, pinned := codexApprovedHooks(codexDejaPins(cfg, "/Users/x/.codex/hooks.json", hooks), codexHookWiring)
	if pinned != len(codexHookWiring) {
		t.Errorf("pinned = %d, want the %d events deja writes", pinned, len(codexHookWiring))
	}
	if approved != 1 {
		t.Errorf("approved = %d, want the one pin in this config", approved)
	}

	// Every event pinned is the state that may call itself wired without a
	// caveat.
	var all strings.Builder
	for _, h := range codexHookWiring {
		all.WriteString("[hooks.state.\"/Users/x/.codex/hooks.json:" + codexEventKey(h.Event) + ":0:0\"]\n")
		all.WriteString("trusted_hash = \"sha256:abc\"\n")
	}
	if approved, pinned = codexApprovedHooks(codexDejaPins(all.String(), "/Users/x/.codex/hooks.json", hooks), codexHookWiring); approved != pinned {
		t.Errorf("approved = %d of %d with every event pinned", approved, pinned)
	}
}

// The key is snake_case, which is what makes the count match codex's store
// rather than never matching it.
func TestCodexEventKeyIsSnakeCase(t *testing.T) {
	for event, want := range map[string]string{
		"SessionStart":     "session_start",
		"UserPromptSubmit": "user_prompt_submit",
		"PreToolUse":       "pre_tool_use",
		"PostToolUse":      "post_tool_use",
		"PreCompact":       "pre_compact",
	} {
		if got := codexEventKey(event); got != want {
			t.Errorf("codexEventKey(%q) = %q, want %q", event, got, want)
		}
	}
}

// codexHome writes a hooks.json holding the given events and the trust entry
// codex needs before it runs any of them.
func codexHome(t *testing.T, events ...string) {
	t.Helper()
	home := sources.CodexHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := func(event string) string {
		for _, h := range codexHookWiring {
			if h.Event == event {
				return h.Sub
			}
		}
		return "hook-unknown"
	}
	entries := make([]string, 0, len(events))
	for _, e := range events {
		entries = append(entries, `"`+e+`":[{"matcher":"","hooks":[{"type":"command","command":"/usr/local/bin/deja `+
			sub(e)+`"}]}]`)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"),
		[]byte(`{"hooks":{`+strings.Join(entries, ",")+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[hooks.state."+strconv.Quote(filepath.Join(home, "hooks.json")+":session_start:0:0")+"]\ntrusted_hash = \"sha256:abc\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Codex trusts a hooks.json by its hash, and the one it approved may be the one
// an older deja wrote. Trusted said "wired" whatever was in it, so the events
// added since ran nowhere and nothing said so.
func TestDoctorNamesTheCodexEventsAnOlderInstallLacks(t *testing.T) {
	withStatsStores(t)
	codexHome(t, "SessionStart")

	var out bytes.Buffer
	doctorCodexHook(&out)
	got := out.String()
	if !strings.Contains(got, "out of date") {
		t.Errorf("a one-of-three wiring was not called out:\n%s", got)
	}
	for _, want := range []string{"PreToolUse", "PostToolUse", "deja install"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not name %q:\n%s", want, got)
		}
	}
}

func TestDoctorIsQuietWhenCodexHasEveryEvent(t *testing.T) {
	withStatsStores(t)
	all := make([]string, 0, len(codexHookWiring))
	for _, h := range codexHookWiring {
		all = append(all, h.Event)
	}
	codexHome(t, all...)

	var out bytes.Buffer
	doctorCodexHook(&out)
	if got := out.String(); strings.Contains(got, "out of date") {
		t.Errorf("a complete wiring was reported as stale:\n%s", got)
	}
	if st := codexHookWiringState(); st.state != "wired" {
		t.Errorf("a complete, approved wiring reads %q, want wired", st.state)
	}
}

// Codex users keep their own hooks in hooks.json. One that holds no entry of
// deja's is a machine deja never wired, whatever codex's trust store says
// about the user's hooks: the row reads missing (#4297).
func TestDoctorCodexHookWithOnlyTheUsersHooksIsMissing(t *testing.T) {
	for _, trust := range []string{"no config.toml", "untrusted", "trusted"} {
		t.Run(trust, func(t *testing.T) {
			hermeticEnv(t)
			home := sources.CodexHome()
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			hooks := filepath.Join(home, "hooks.json")
			own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/my-notes --start"}]}]}}`
			if err := os.WriteFile(hooks, []byte(own), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := ""
			switch trust {
			case "untrusted":
				cfg = "model = \"gpt-5\"\n"
			case "trusted":
				cfg = "[hooks.state." + strconv.Quote(hooks+":session_start:0:0") + "]\ntrusted_hash = \"sha256:abc\"\n"
			}
			if cfg != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if st := codexHookWiringState(); st.state != "missing" {
				t.Errorf("state = %q, want missing", st.state)
			}
			var buf bytes.Buffer
			doctorCodexHook(&buf)
			if out := buf.String(); !strings.HasPrefix(strings.TrimSpace(out), "codex-hook   missing") || strings.Contains(out, "approve") {
				t.Errorf("row = %q, want missing and no advice to approve someone else's hook", out)
			}

			// Control: deja's own hook beside the user's is read as before.
			if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install codex-auto: %v", err)
			}
			if st := codexHookWiringState(); st.state == "missing" {
				t.Errorf("deja's hooks beside the user's: state %q", st.state)
			}
		})
	}
}

// A hooks.json that does not parse says nothing about deja's entries, so the
// trust store is not asked about it either: the row reads unreadable, as
// Claude's does (#4297).
func TestDoctorCodexHookUnparseableIsUnreadable(t *testing.T) {
	hermeticEnv(t)
	home := sources.CodexHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(home, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":{"SessionStart":[{"hooks":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := "[hooks.state." + strconv.Quote(hooks+":session_start:0:0") + "]\ntrusted_hash = \"sha256:abc\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := codexHookWiringState(); st.state != "unreadable" {
		t.Errorf("state = %q, want unreadable", st.state)
	}
	var buf bytes.Buffer
	doctorCodexHook(&buf)
	if out := buf.String(); !strings.Contains(out, "unreadable") || strings.Contains(out, "events wired") {
		t.Errorf("row = %q, want unreadable and no event count", out)
	}
}

// With the Codex plugin enabled, deja's hooks ride the plugin, and a
// hooks.json holding only the user's hook leaves the row at plugin (#4297).
func TestDoctorCodexHookWithThePluginBesideTheUsersHooks(t *testing.T) {
	hermeticEnv(t)
	home := sources.CodexHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/my-notes --start"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[plugins.\"deja-vu@deja-vu\"]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := codexHookWiringState(); st.state != "plugin" {
		t.Errorf("state = %q, want plugin", st.state)
	}
}

func TestDoctorCodexHookApprovedViaLaterEventOrExecutionEvidence(t *testing.T) {
	t.Run("later event pinned when SessionStart unpinned", func(t *testing.T) {
		hermeticEnv(t)
		home := sources.CodexHome()
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		hooks := filepath.Join(home, "hooks.json")
		cfg := "[hooks.state." + strconv.Quote(hooks+":user_prompt_submit:0:0") + "]\ntrusted_hash = \"sha256:abc\"\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		st := codexHookWiringState()
		if st.state != "wired" || st.approved != 1 || st.pinned != len(codexHookWiring) {
			t.Fatalf("state = %+v, want wired with 1/%d approved", st, len(codexHookWiring))
		}
	})

	t.Run("hook execution evidence when only user hooks pinned", func(t *testing.T) {
		hermeticEnv(t)
		home := sources.CodexHome()
		sessDir := filepath.Join(home, "sessions", "2026", "04", "07")
		if err := os.MkdirAll(sessDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sid := "019dd5a0-1234-7000-8000-000000000001"
		rollout := filepath.Join(sessDir, "rollout-2026-04-07T00-00-00-"+sid+".jsonl")
		if err := os.WriteFile(rollout, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		hooks := filepath.Join(home, "hooks.json")
		own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/other-hook"}]}]}}`
		if err := os.WriteFile(hooks, []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		cfg := "[hooks.state." + strconv.Quote(hooks+":session_start:0:0") + "]\ntrusted_hash = \"sha256:abc\"\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		if st := codexHookWiringState(); st.state != "untrusted" {
			t.Fatalf("before execution evidence: state = %q, want untrusted", st.state)
		}
		if err := os.MkdirAll(filepath.Dir(index.DefaultDir()), 0o755); err != nil {
			t.Fatal(err)
		}
		markSessionLive(index.DefaultDir(), sid)
		st := codexHookWiringState()
		if st.state != "wired" {
			t.Fatalf("after execution evidence: state = %q, want wired", st.state)
		}
		if !codexHasSeenItsHook() {
			t.Errorf("codexHasSeenItsHook() = false after hook ran")
		}
		var buf bytes.Buffer
		doctorCodexHook(&buf)
		if out := buf.String(); strings.Contains(out, "untrusted") || strings.Contains(out, "hooks approved") {
			t.Errorf("unexpected warning after hook ran:\n%s", out)
		}
	})
}

// Codex pins trust per entry, by where it sits in hooks.json. With the user's
// own SessionStart hook ahead of deja's, the pin at session_start:0:0 is
// theirs: codex runs none of deja's hooks, and the row has to say so (#4313).
func TestDoctorCodexTrustIsReadAtDejasOwnEntry(t *testing.T) {
	hermeticEnv(t)
	home := sources.CodexHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(home, "hooks.json")
	own := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/my-notes --start"}]}]}}`
	if err := os.WriteFile(hooks, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfg, []byte("model = \"gpt-5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install codex-auto: %v", err)
	}
	pin := func(pos string) string {
		return "\n[hooks.state." + strconv.Quote(hooks+":"+pos) + "]\ntrusted_hash = \"sha256:00\"\n"
	}
	write := func(pins ...string) {
		t.Helper()
		if err := os.WriteFile(cfg, []byte("model = \"gpt-5\"\n"+strings.Join(pins, "")), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Only the user's hook approved.
	write(pin("session_start:0:0"))
	st := codexHookWiringState()
	if st.state != "untrusted" {
		t.Errorf("only the user's hook pinned: state %q, want untrusted", st.state)
	}
	if codexHasSeenItsHook() {
		t.Error("only the user's hook pinned, and install would say codex has seen deja's")
	}

	// Control: deja's own entries approved, the user's not.
	write(pin("session_start:1:0"), pin("user_prompt_submit:0:0"), pin("pre_tool_use:0:0"), pin("post_tool_use:0:0"), pin("pre_compact:0:0"), pin("session_end:0:0"))
	st = codexHookWiringState()
	if st.state != "wired" || st.approved != st.pinned {
		t.Errorf("deja's entries pinned: state %q, %d of %d approved, want wired and all", st.state, st.approved, st.pinned)
	}
	if !codexHasSeenItsHook() {
		t.Error("deja's hook pinned, and install would say codex has not seen it")
	}

	// deja's SessionStart approved and the rest not: one of six runs.
	write(pin("session_start:0:0"), pin("session_start:1:0"))
	if st = codexHookWiringState(); st.state != "wired" || st.approved != 1 {
		t.Errorf("deja's SessionStart pinned: state %q, %d approved, want wired and 1", st.state, st.approved)
	}

	// Pins at deja's positions in another hooks file — a project's own, the
	// plugin's — say nothing about the file deja wrote.
	other := func(file, pos string) string {
		return "\n[hooks.state." + strconv.Quote(file+":"+pos) + "]\ntrusted_hash = \"sha256:00\"\n"
	}
	for _, file := range []string{"/work/repo/.codex/hooks.json", filepath.Join(home, "plugins", "cache", "deja-vu", "hooks", "hooks.json")} {
		write(other(file, "session_start:1:0"), other(file, "user_prompt_submit:0:0"), other(file, "pre_tool_use:0:0"))
		if st = codexHookWiringState(); st.state != "untrusted" || st.approved != 0 {
			t.Errorf("pins for %s: state %q, %d approved, want untrusted and none", file, st.state, st.approved)
		}
		if codexHasSeenItsHook() {
			t.Errorf("pins for %s, and install would say codex has seen deja's hook", file)
		}
	}
}

// Every harness whose install writes a `/deja` command has a row, and the row
// names the file — including the ones whose command is not called `deja`, which
// is the part a reader needs and the part that was wrong for a release (#3655).
func TestEveryCommandFileHasADoctorRow(t *testing.T) {
	hermeticEnv(t)
	rows := map[string]doctorCommandFile{}
	for _, c := range doctorCommandFiles() {
		if c.path == "" {
			t.Errorf("%s: row has no path", c.name)
		}
		rows[c.name] = c
	}
	for _, name := range installTargetNames() {
		h := guidanceHarness(name)
		want := commandFilePath(h)
		if want == "" {
			continue
		}
		if got, ok := rows[h]; !ok {
			t.Errorf("`deja install %s` writes %s and doctor has no row for it", name, want)
		} else if got.path != want {
			t.Errorf("%s: row names %s, install writes %s", h, got.path, want)
		}
	}
	// And the harnesses with no file of deja's still have a row, saying the
	// skill is the command. An omitted row read as "deja has no command here"
	// when the truth is that it is installed under another name (#3667).
	for _, name := range skillIsTheCommandHarnesses() {
		got, ok := rows[name]
		if !ok {
			t.Errorf("%s has no row; its skill is its command", name)
			continue
		}
		if !got.skill {
			t.Errorf("%s: row is not marked as a skill command", name)
		}
		if got.path != commandSkillPath(name) {
			t.Errorf("%s: row names %s, the skill is at %s", name, got.path, commandSkillPath(name))
		}
		if !strings.Contains(got.path, "skills") {
			t.Errorf("%s: row names %s, which is not a skill file", name, got.path)
		}
		if commandFilePath(name) != "" {
			t.Errorf("%s: deja writes a command file at %s as well as a skill", name, commandFilePath(name))
		}
	}
}

// Four states, and only one of them is a reason to leave a file alone.
func TestCommandFileStateSeparatesMissingFromSomebodyElses(t *testing.T) {
	dir := t.TempDir()
	if got := (doctorCommandFile{path: filepath.Join(dir, "none.md")}).state(); got != "missing" {
		t.Errorf("no file: %q, want missing", got)
	}
	ours := filepath.Join(dir, "deja.md")
	if err := os.WriteFile(ours, []byte(markdownCommand("/bin/deja")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (doctorCommandFile{path: ours}).state(); got != "written" {
		t.Errorf("our own file: %q, want written", got)
	}
	if got := (doctorCommandFile{path: ours, skill: true}).state(); got != "skill" {
		t.Errorf("a skill row: %q, want skill", got)
	}
	theirs := filepath.Join(dir, "mine.md")
	if err := os.WriteFile(theirs, []byte("# my own command\nrun the thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (doctorCommandFile{path: theirs}).state(); got != "someone else's" {
		t.Errorf("a stranger's file: %q, want someone else's", got)
	}
}

// The section is printed, with its heading, on a machine with nothing wired.
func TestDoctorPrintsTheCommandsSection(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	doctorCommands(&out)
	text := out.String()
	if !strings.HasPrefix(text, "Commands:\n") {
		t.Fatalf("no heading:\n%s", text)
	}
	for _, name := range []string{"claude-code", "cursor", "copilot-chat", "gemini", "codex"} {
		if !strings.Contains(text, "  "+name) {
			t.Errorf("no row for %s:\n%s", name, text)
		}
	}
	// No row says `skill`, so there is nothing to explain.
	if strings.Contains(text, "the skill is the command there") {
		t.Errorf("the note explains a state no row shows:\n%s", text)
	}
	// Once one does, what it means is said, or the word is a state nobody
	// can read.
	skill := commandSkillPath("codex")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: deja-history\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorCommands(&out)
	if !strings.Contains(out.String(), "the skill is the command there") {
		t.Errorf("the skill state is never explained:\n%s", out.String())
	}
}

// Continue's and goose's commands are items in a config.yaml rather than files
// of their own, and doctor had no row for either: a removed or renamed /deja
// looked the same as a working one (#4374). The state comes from the item.
func TestDoctorReadsConfigYAMLCommands(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_CONTINUE_ROOT", "")
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	t.Setenv("GOOSE_PATH_ROOT", "")
	row := func(name string) doctorCommandFile {
		t.Helper()
		for _, c := range doctorCommandFiles() {
			if c.name == name {
				return c
			}
		}
		t.Fatalf("no %s row", name)
		return doctorCommandFile{}
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, path string
		install    func() error
		other      string
	}{
		{"continue", continueConfigPath(),
			func() error { _, err := installContinue("/bin/deja", false); return err },
			"prompts:\n  - name: deja\n    description: my own\n    prompt: grep my notes\n"},
		{"goose", filepath.Join(gooseConfigDir(), "config.yaml"),
			func() error { _, err := installGooseCommand("/bin/deja", false); return err },
			"slash_commands:\n  - command: \"deja\"\n    recipe_path: /home/me/notes-recipe.yaml\n"},
	}
	for _, c := range cases {
		// A config with no item of that name: missing, though the file is there.
		write(c.path, "models: []\n")
		if got := row(c.name).state(); got != "missing" {
			t.Errorf("%s with no item: %q, want missing", c.name, got)
		}
		if row(c.name).path != c.path {
			t.Errorf("%s: row names %s, install writes %s", c.name, row(c.name).path, c.path)
		}
		write(c.path, c.other)
		if got := row(c.name).state(); got != "someone else's" {
			t.Errorf("%s with the reader's own /deja: %q, want someone else's", c.name, got)
		}
		write(c.path, "models: []\n")
		if err := c.install(); err != nil {
			t.Fatal(err)
		}
		if got := row(c.name).state(); got != "written" {
			t.Errorf("%s after install: %q, want written", c.name, got)
		}
	}
}

// Continue keeps sessions.json beside the session documents as their list;
// the reader reads it for titles and dates and skips it as a transcript on
// purpose, and doctor counted it as "1 not recognised here" on every Continue
// store (#3297).
func TestDoctorDoesNotCallContinuesListUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "continue", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "8f1c2a3e-0000-4000-8000-000000000000"
	if err := os.WriteFile(filepath.Join(dir, sid+".json"), []byte(`{"sessionId":"`+sid+`","title":"retry loop","workspaceDirectory":"/w/api","history":[{"message":{"role":"user","content":"why does the retry loop drop the last attempt"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`[{"sessionId":"`+sid+`","title":"retry loop","dateCreated":"2026-08-02T10:00:00.000Z","workspaceDirectory":"/w/api"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CONTINUE_ROOT", filepath.Join(tmp, "continue"))
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(line, "continue") && strings.Contains(line, "not recognised") {
			t.Errorf("the list file is counted as a transcript deja failed to read: %q", line)
		}
	}
}

// The Copilot CLI row read the guidance skill, so it said wired with no deja
// server in mcp-config.json and could never see a moved binary (#4232).
func TestDoctorReadsCopilotsMCPConfig(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "copilot", "--no-index"); err != nil {
		t.Fatal(err)
	}
	var row doctorMCPConfig
	for _, c := range doctorMCPConfigs() {
		if c.name == "copilot" {
			row = c
		}
	}
	want := filepath.Join(sources.Home(), ".copilot", "mcp-config.json")
	if row.path != want {
		t.Fatalf("copilot row reads %s, want %s", row.path, want)
	}
	if !row.wired(row.path) {
		t.Fatal("not wired right after install")
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	delete(cfg["mcpServers"], "deja")
	b = mustMarshal(t, cfg)
	if err := os.WriteFile(want, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if row.wired(row.path) {
		t.Error("still wired with no deja server in mcp-config.json")
	}
}

// Copilot CLI keeps vscode.metadata.json beside each session's events.jsonl;
// it is the IDE's bookkeeping, not a transcript, and doctor reported every
// session as a file it could not read (#3303).
func TestDoctorDoesNotCallCopilotsMetadataUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "copilot", "session-state", "7dad4ddc-5bc8-4ded-bead-2bcad486028d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(`{"type":"user.message","data":{"content":"why does the retry loop drop the last attempt"},"timestamp":"2026-08-02T10:00:00.000Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vscode.metadata.json"), []byte(`{"workspaceFolder":"/w/api"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Copilot CLI 1.0.79's rewind snapshots are its own state too (#4233).
	if err := os.MkdirAll(filepath.Join(dir, "rewind-file-snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.json", "tracking.json"} {
		if err := os.WriteFile(filepath.Join(dir, "rewind-file-snapshots", f), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_COPILOT_ROOT", filepath.Join(tmp, "copilot", "session-state"))
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(line, "copilot") && strings.Contains(line, "not recognised") {
			t.Errorf("the IDE's metadata is counted as a transcript deja failed to read: %q", line)
		}
	}
}

// Crush keeps a store per project and doctor prints a row for the registry and
// one per store. Each row ended with the harness-wide counts, so an empty
// crush.db read "1 indexed session" and N projects looked like N times the
// sessions (#4379). The total belongs to the first row of a harness only.
func TestDoctorCountsAHarnessOnceAcrossItsRows(t *testing.T) {
	tmp := hermeticEnv(t)
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	schema := `create table sessions (id text primary key, parent_session_id text, title text not null,
  message_count integer not null default 0, prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0, cost real not null default 0.0,
  updated_at integer not null, created_at integer not null, summary_message_id text, todos text);
create table messages (id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', model text, created_at integer not null, updated_at integer not null,
  finished_at integer, provider text, is_summary_message integer default 0 not null);
`
	store := func(project, sql string) {
		db := filepath.Join(tmp, project, ".crush", "crush.db")
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "sqlite3", db)
		cmd.Stdin = strings.NewReader(schema + sql)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build store: %v: %s", err, out)
		}
	}
	store("full", `insert into sessions values ('s1',null,'retry',1,0,0,0,1790000000,1790000000,null,null);
insert into messages values ('m1','s1','user','[{"type":"text","data":{"text":"fix the retry loop"}}]','m',1790000000,1790000000,null,'p',0);
`)
	store("empty", "")
	root := filepath.Join(tmp, "crush-data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"projects":[{"path":"` + filepath.ToSlash(filepath.Join(tmp, "full")) + `"},{"path":"` + filepath.ToSlash(filepath.Join(tmp, "empty")) + `"}]}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CRUSH_ROOT", root)

	rows := func(dir string) []string {
		var buf bytes.Buffer
		doctorHarnesses(&buf, dir)
		var out []string
		for line := range strings.SplitSeq(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "crush ") {
				out = append(out, line)
			}
		}
		if len(out) != 3 {
			t.Fatalf("want the registry and two store rows, got:\n%s", buf.String())
		}
		return out
	}
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got := rows(dir)
	if !strings.Contains(got[0], "1 indexed session") {
		t.Errorf("the registry row lost the total: %q", got[0])
	}
	for _, row := range got[1:] {
		if strings.Contains(row, "indexed session") || strings.Contains(row, "never read") {
			t.Errorf("a store row repeats the harness total: %q", row)
		}
	}
}

// The row exists to be read on the day Cursor switches stores, so it has to
// appear on a machine where the only Cursor files are the unread ones, and it
// has to stay quiet while every chat still has a transcript (#3772).
func TestDoctorNamesCursorChatsNothingCanRead(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(root, "no-ide"))
	id := "cf555555-6666-4777-8888-999999999999"
	dir := filepath.Join(root, "chats", "ws", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "store.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !doctorCursorPresent() {
		t.Fatal("doctor dropped the Cursor row on a machine that has only the unread store")
	}
	got := doctorCursorDetail(true)
	if !strings.Contains(got, "1 CLI chat with no transcript") {
		t.Fatalf("doctorCursorDetail = %q, want it to name the chat nothing can read", got)
	}

	tdir := filepath.Join(root, "projects", "-Users-me-app", "agent-transcripts", id)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := doctorCursorDetail(true); strings.Contains(got, "not readable") {
		t.Fatalf("doctorCursorDetail = %q, want silence once the transcript is there", got)
	}
}

// doctor's job is answering "is my setup right", and it said "built, up to
// date" about a store that could not return a single result (#735).
func TestDoctorReportsADamagedIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"s1","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the retry storm"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorIndex(&buf, doctorIndexReport{State: "fresh", Path: dir}, dir)
	if strings.Contains(buf.String(), "integrity") {
		t.Errorf("healthy index reported damage:\n%s", buf.String())
	}

	if err := os.RemoveAll(filepath.Join(dir, "buckets")); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorIndex(&buf, doctorIndexReport{State: "fresh", Path: dir}, dir)
	out := buf.String()
	if !strings.Contains(out, "integrity damaged") {
		t.Errorf("damaged index reported as healthy:\n%s", out)
	}
	// The reader has not lost memory, only this build — say so, or the line
	// reads as data loss.
	if !strings.Contains(out, "the next search rebuilds") {
		t.Errorf("no recovery named:\n%s", out)
	}
	// And which file: one sentence for four breakages named records and
	// postings for two that were neither (#2695).
	if !strings.Contains(out, "postings") {
		t.Errorf("the line does not say what broke:\n%s", out)
	}
	// "up to date" must not sit under a damage line.
	if strings.Contains(out, "freshness up to date") {
		t.Errorf("still claims freshness:\n%s", out)
	}
}

// Kilo and ZCode each keep half their history in a SQLite database, and reading
// it needs the sqlite3 CLI. Those two rows named the database and said nothing
// about the tool, so on a machine without sqlite3 the sessions were missing
// from recall while the row reported the store present (#3679).
func TestTheTwoDatabaseHalvesNameTheirPrereq(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "kilo.db"))
	t.Setenv("DEJA_ZCODE_DB", filepath.Join(home, "db.sqlite"))
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "absent"))
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent-too"))
	for _, db := range []string{filepath.Join(home, "kilo.db"), filepath.Join(home, "db.sqlite")} {
		if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// No sqlite3 on the PATH is the state the note exists for.
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	for _, name := range []string{"kilocode", "zcode"} {
		row := ""
		for line := range strings.SplitSeq(out.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
				row = line
				break
			}
		}
		if row == "" {
			t.Errorf("no %s row:\n%s", name, out.String())
			continue
		}
		if !strings.Contains(row, "CLI store present") {
			t.Errorf("%s: row does not name the database: %s", name, row)
		}
		if !strings.Contains(row, "sqlite3 CLI is missing") {
			t.Errorf("%s: row promises a store it cannot read: %s", name, row)
		}
	}
}

// A config can name a deja binary that is not there — a restored backup, a hand
// edit, a machine where deja moved and one file was fixed by hand. The harness
// then starts a server that cannot run, no memory arrives, and doctor reported
// the wiring as healthy: its only check asks whether the exe in deja's own
// record exists, and that one still did (#2216).
func TestDoctorNamesAConfigPointingAtAMissingBinary(t *testing.T) {
	hermeticEnv(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = installTarget("claude", exe, false); err != nil {
		t.Fatal(err)
	}
	recordWiring([]string{"claude"}, false)

	// The premise: nothing to report while the binary is where the config says.
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not there") {
		t.Fatalf("a healthy wiring was reported broken:\n%s", out)
	}

	p := sources.ClaudeJSONPath()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Absolute the way this platform spells absolute: the check stays quiet on
	// a relative path, and "/gone/deja" is one on Windows.
	gone := filepath.Join(t.TempDir(), "gone", "deja")
	marshalled, err := json.Marshal(gone)
	if err != nil {
		t.Fatal(err)
	}
	quoted := string(marshalled[1 : len(marshalled)-1])
	if err = os.WriteFile(p, []byte(strings.ReplaceAll(string(b), jsonEscaped(t, exe), quoted)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, gone) {
		t.Errorf("doctor does not name the binary the config points at:\n%s", out)
	}
	// The report contracts a home path to ~ (#2360), so the row names the file
	// in that form. What matters here is that it names it at all.
	if !strings.Contains(out, reportPath(p)) {
		t.Errorf("doctor does not name the config that points there:\n%s", out)
	}
}

// Where the check must stay quiet, and where it must speak. A bare command is
// the PATH's business; a directory that happens to be named deja is not a
// binary; and the three config shapes deja writes — JSON, TOML, YAML — plus the
// JSONC that will not parse as JSON all have to be read (#2216).
func TestTheMissingBinaryCheckReadsEveryConfigShapeAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	resolved := filepath.Join(dir, "deja")
	if err := os.WriteFile(resolved, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Both spelled the way this platform spells an absolute path, and escaped
	// for the format they are pasted into: a Windows path is backslashes.
	gone := filepath.Join(dir, "gone", "deja")
	goneJSON := jsonEscaped(t, gone)
	realJSON := jsonEscaped(t, resolved)
	for _, c := range []struct {
		name string
		body string
		want string
	}{
		{"a bare name is the PATH's", `{"mcpServers":{"deja":{"command":"deja","args":["mcp"]}}}`, ""},
		{"a binary that is there", `{"mcpServers":{"deja":{"command":"` + realJSON + `","args":["mcp"]}}}`, ""},
		// A quoted path in a file that does not parse as JSON: the escapes
		// belong to the format, and reading them raw named a path that exists
		// nowhere — doctor then called a working install broken.
		{"a quoted path in toml", "[mcp_servers.deja]\ncommand = \"" + realJSON + "\"\n", ""},
		// TOML's literal string, where a backslash is a backslash and nothing
		// is escaped — which is how a Windows path is written there.
		{"a literal path in toml", "[mcp_servers.deja]\ncommand = '" + resolved + "'\n", ""},
		{"a literal path that is gone", "[mcp_servers.deja]\ncommand = '" + gone + "'\n", gone},
		// YAML's double-quoted scalar escapes like JSON does.
		{"a quoted path in yaml", "extensions:\n  deja:\n    cmd: \"" + goneJSON + "\"\n", gone},
		{"no deja entry", `{"mcpServers":{"other":{"command":"/usr/bin/other"}}}`, ""},
		{"a directory named deja", `{"mcpServers":{"deja":{"command":"deja","cwd":"` + goneJSON + `"}}}`, ""},
		{"an unrelated path in toml", "[tool]\ndata_dir = \"" + goneJSON + "\"\n", ""},
		// Both resolve against something this check does not know: the reader's
		// working directory, and a shell that is not running.
		{"a relative path", `{"mcpServers":{"deja":{"command":"./deja"}}}`, ""},
		{"an unexpanded tilde", `{"mcpServers":{"deja":{"command":"~/bin/deja"}}}`, ""},
		{"json", `{"mcpServers":{"deja":{"command":"` + goneJSON + `"}}}`, gone},
		{"toml", "[mcp_servers.deja]\ncommand = \"" + goneJSON + "\"\n", gone},
		{"yaml", "extensions:\n  deja:\n    cmd: " + gone + "\n", gone},
		{"jsonc with comments", "{\n // deja\n \"mcpServers\":{\"deja\":{\"command\":\"" + goneJSON + "\"}}\n}", gone},
	} {
		p := filepath.Join(t.TempDir(), "config")
		if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := dejaCommandMissing(p); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// jsonEscaped is a path as it appears inside a JSON string: on Windows that is
// every backslash doubled, and pasting the raw path instead produces escapes
// the decoder refuses.
func jsonEscaped(t *testing.T, path string) string {
	t.Helper()
	b, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b[1 : len(b)-1])
}

// A machine that upgraded is not in the healthy state: the entries were written
// by the version before, so the row comes out stale, or untrusted — and the note
// naming the binary that is gone printed only under a row that came out wired.
// The rows most likely to be pointing at nothing were the ones saying nothing
// about it (#3502).
func TestDoctorNamesADeadBinaryUnderAStaleRow(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "Cellar", "deja-vu", "0.19.3", "bin", "deja")

	// Qwen's row looks for `hook-prompt`. This file has deja's own binary in it
	// and an older event, so the row reads stale — the state a settings.json
	// written by an earlier version lands in.
	qwen := filepath.Join(sources.QwenConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(qwen), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qwen, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorAutoRecall(&out)
	got := out.String()
	if !strings.Contains(got, "stale") {
		t.Fatalf("the fixture did not produce a stale row, so this measures nothing:\n%s", got)
	}
	if !strings.Contains(got, "runs "+gone+", which is not there") {
		t.Errorf("a stale row said nothing about the binary it names being gone:\n%s", got)
	}
}

// Codex's own row has a third state: trusted, disabled, or never shown to codex
// at all. An upgraded machine lands in the last one, and that was the only thing
// the row said.
func TestDoctorNamesADeadBinaryUnderAnUntrustedCodexRow(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "Cellar", "deja-vu", "0.19.3", "bin", "deja")

	home := sources.CodexHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// A config with no trust entry for the hook: codex has never been shown it.
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[mcp_servers.deja]\ncommand = \""+gone+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorCodexHook(&out)
	got := out.String()
	if !strings.Contains(got, "untrusted") {
		t.Fatalf("the fixture did not produce an untrusted row, so this measures nothing:\n%s", got)
	}
	if !strings.Contains(got, "runs "+gone+", which is not there") {
		t.Errorf("an untrusted row said nothing about the binary it names being gone:\n%s", got)
	}
}

// DeepVerify compares per-session message counts and the structure around
// them; it does not compare content, so a same-length edit inside a record
// passes every check while the session becomes unreachable. The status line
// claimed more than that — "index matches sources — no memory lost" is what a
// reader takes to the bank when deciding whether to rebuild (#1712).
func TestDeepStatusSaysWhatItCompared(t *testing.T) {
	var buf bytes.Buffer
	doctorDeep(&buf, index.DeepReport{FilesChecked: 3, SessionsIndexed: 3, SampledFiles: 3, SampledPostings: 10})
	out := buf.String()
	if strings.Contains(out, "no memory lost") {
		t.Errorf("the status promises more than a count comparison can support:\n%s", out)
	}
	if !strings.Contains(out, "count") {
		t.Errorf("the status does not say what was compared:\n%s", out)
	}
}

// A clean report that sampled nothing must not claim a comparison happened:
// nothing is sampled when every source is stale, or when the sampled tokens
// carry no postings (found in review on #1713).
func TestDeepStatusDoesNotClaimAnUnsampledComparison(t *testing.T) {
	cases := []struct {
		name    string
		report  index.DeepReport
		mustNot string
		must    string
	}{
		{"nothing sampled at all", index.DeepReport{FilesChecked: 3, SessionsIndexed: 3},
			"matches its source", "nothing to compare"},
		{"no file re-parsed", index.DeepReport{FilesChecked: 3, SessionsIndexed: 3, SampledPostings: 10},
			"message count matches", "no source was in sync"},
		{"no posting carried", index.DeepReport{FilesChecked: 3, SessionsIndexed: 3, SampledFiles: 3},
			"postings resolve;", "no sampled token carried postings"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			doctorDeep(&buf, c.report)
			out := buf.String()
			if strings.Contains(out, c.mustNot) {
				t.Errorf("claims a comparison that did not happen:\n%s", out)
			}
			if !strings.Contains(out, c.must) {
				t.Errorf("does not say what was skipped:\n%s", out)
			}
		})
	}
}

// The control: a report with findings still leads with them, unchanged.
func TestDeepStatusStillLeadsWithFindings(t *testing.T) {
	var buf bytes.Buffer
	doctorDeep(&buf, index.DeepReport{
		FilesChecked: 3,
		Findings:     []index.DeepFinding{{Kind: "torn-log", Detail: "records.bin unreadable"}},
	})
	out := buf.String()
	if !strings.Contains(out, "torn-log") || !strings.Contains(out, "records.bin unreadable") {
		t.Errorf("a finding was dropped:\n%s", out)
	}
	if strings.Contains(out, "count") {
		t.Errorf("the clean-status wording leaked into a report with findings:\n%s", out)
	}
}

// The walk's budget counted every entry, so on a store of tens of thousands of
// transcripts it was spent inside the first project and a locked directory
// later in the walk was never reached (#864).
func TestDeniedDirIsFoundPastTheFileBudget(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root := t.TempDir()
	crowd := filepath.Join(root, "aaa-crowded")
	if err := os.MkdirAll(crowd, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 6000 {
		if err := os.WriteFile(filepath.Join(crowd, fmt.Sprintf("s%d.jsonl", i)), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Join(root, "zzz-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if got, whole := firstDeniedDir([]string{root}); got != locked || !whole {
		t.Errorf("denied dir = %q whole = %v, want %q true", got, whole, locked)
	}
}

// The budget then counted directories, and a machine with a few thousand
// projects spent it before reaching a locked one — doctor called the store
// whole (#1025).
func TestDeniedDirIsFoundPastTheDirectoryBudget(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root := t.TempDir()
	for i := range 6000 {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("proj%05d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Join(root, "zzz-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if got, whole := firstDeniedDir([]string{root}); got != locked || !whole {
		t.Errorf("denied dir = %q whole = %v, want %q true", got, whole, locked)
	}
}

// A transcript deja is not allowed to open is a permission problem, not a
// harness that changed its format: the advice, and the path, have to match the
// fault (#1747).
func TestAnUnreadableFileIsDeniedAndNamed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(root, "-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w","timestamp":"2026-08-08T01:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(proj, "readable.jsonl"), []byte(rec("ok1", "flumberjack readable")), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(proj, "locked.jsonl")
	// Newer than the readable one, so it is the file doctor inspects.
	if err := os.WriteFile(locked, []byte(rec("locked1", "flumberjack locked")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot lock a file here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	var check doctorStoreCheck
	for _, c := range doctorStoreChecks() {
		if c.name == "claude" {
			check = c
		}
	}
	store, _ := inspectDoctorStore(check)
	if store.State != "denied" {
		t.Errorf("an unreadable transcript reports state %q, want denied", store.State)
	}
	if !strings.Contains(store.Denied, "locked.jsonl") {
		t.Errorf("doctor names %q, not the file it could not read", store.Denied)
	}
	if !store.Partial {
		t.Error("the rest of the store indexed, so this is a partly readable store")
	}
	_ = tmp
}

// "Partly readable" has to mean something read. A store whose files are all
// locked is not partly anything.
func TestAWhollyLockedStoreIsNotCalledPartlyReadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := make([]string, 0, 2)
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		p := filepath.Join(proj, name)
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"2026-08-08T01:00:00Z","message":{"role":"user","content":"flumberjack"}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o000); err != nil {
			t.Skipf("cannot lock a file here: %v", err)
		}
		locked = append(locked, p)
	}
	t.Cleanup(func() {
		for _, p := range locked {
			_ = os.Chmod(p, 0o600)
		}
	})
	if f, err := os.Open(locked[0]); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	var check doctorStoreCheck
	for _, c := range doctorStoreChecks() {
		if c.name == "claude" {
			check = c
		}
	}
	store, _ := inspectDoctorStore(check)
	if store.State != "denied" {
		t.Errorf("state %q, want denied", store.State)
	}
	if store.Partial {
		t.Error("every file is locked, so the store is not partly readable")
	}
}

// A store with more than one root can be half-readable: the CLI transcripts
// open and the VS Code tree refuses. The walk below says so — "sessions are
// missing from recall rather than the whole harness" (#816) — and the stat
// loop above it returned before setting the field, so the machine form called
// a half-readable store wholly denied (#3407).
func TestDoctorJSONSaysPartialWhenOnlyOneRootIsLocked(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	tmp := hermeticEnv(t)
	sessions := filepath.Join(tmp, "cline", "data", "sessions", "1757000001_aa11b")
	legacy := filepath.Join(tmp, "vscode", "globalStorage", "saoudrizwan.claude-dev")
	task := filepath.Join(legacy, "tasks", "1757000000000")
	for _, d := range []string{sessions, task} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLINE_ROOT", filepath.Dir(sessions))
	t.Setenv("DEJA_CLINE_ROOTS", legacy)
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(sessions, "1757000001_aa11b.messages.json"),
		`[{"role":"user","content":[{"type":"text","text":"why does the parser skip the first turn"}]}]`)
	write(filepath.Join(task, "api_conversation_history.json"),
		`[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]}]`)

	locked := filepath.Join(legacy, "tasks")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot lock a directory here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	store, _ := inspectDoctorStore(doctorCheckNamed(t, "cline"))
	if store.State != "denied" {
		t.Fatalf("state = %q, want denied", store.State)
	}
	if store.Files == 0 {
		t.Fatalf("no file was read at all, so this asserts nothing")
	}
	if !store.Partial {
		t.Errorf("the CLI store read %d file(s) and the report calls the harness wholly denied", store.Files)
	}
}

// A directory deja cannot open loses its sessions from recall, and the row
// people read called it `found` — the same state `doctor --json` has reported
// as `denied` since #802/#816 (#993).
func TestDoctorRowSaysDeniedForAStoreItCannotOpen(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	qwen := filepath.Join(tmp, "qwen")
	projects := filepath.Join(qwen, "projects", "p1")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "s.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", qwen)

	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	if row := harnessRow(t, out.String(), "qwen"); !strings.Contains(row, "found") {
		t.Fatalf("a readable store was not reported as found: %q", row)
	}

	if err := os.Chmod(filepath.Join(qwen, "projects"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(qwen, "projects"), 0o755) })

	out.Reset()
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	row := harnessRow(t, out.String(), "qwen")
	if strings.Contains(row, "found") {
		t.Errorf("a store deja cannot open is still called found: %q", row)
	}
	if !strings.Contains(row, "denied") {
		t.Errorf("the row does not name the state: %q", row)
	}
	// The row stays short because the warning block, which is built from the
	// same inspection, names the path and what to do about it.
	var warn bytes.Buffer
	printDoctorStoreWarnings(&warn, collectDoctorReport(func() (string, bool) { return "", false }, filepath.Join(tmp, "index.db")).Stores)
	if !strings.Contains(warn.String(), "permission denied on") {
		t.Errorf("the warning that names the path is gone:\n%s", warn.String())
	}
}

// A store deja is not allowed to read was reported as a harness that changed
// its format — or, when only a subdirectory was locked, as a store with no
// history at all (#802).
func TestDoctorSeparatesADeniedStoreFromAChangedFormat(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "sessions")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	// No files reached the collector, exactly as the walk leaves it.
	store, _ := inspectDoctorStore(doctorStoreCheck{name: "codex", paths: []string{root}, files: nil, parse: nil})
	if store.State != "denied" {
		t.Fatalf("state = %q, want denied", store.State)
	}
	if store.Denied != locked {
		t.Errorf("denied path = %q, want %q", store.Denied, locked)
	}

	var out strings.Builder
	printDoctorStoreWarnings(&out, []doctorStore{store})
	got := out.String()
	if !strings.Contains(got, "permission denied on "+locked) {
		t.Errorf("warning does not name the directory:\n%s", got)
	}
	if strings.Contains(got, "format may have changed") {
		t.Errorf("a permission problem is still reported as a format change:\n%s", got)
	}

	// Some files readable, one directory not: the store is only partly
	// readable, which is quieter than losing it all — sessions disappear from
	// recall while everything else looks whole (#816).
	readable := filepath.Join(root, "seen.jsonl")
	if err := os.WriteFile(readable, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	partial, _ := inspectDoctorStore(doctorStoreCheck{name: "codex", paths: []string{root}, files: []string{readable}, parse: nil})
	if partial.State != "denied" || !partial.Partial {
		t.Fatalf("state = %q partial = %v, want denied and partial", partial.State, partial.Partial)
	}
	var partialOut strings.Builder
	printDoctorStoreWarnings(&partialOut, []doctorStore{partial})
	if !strings.Contains(partialOut.String(), "only partly readable") {
		t.Errorf("the partial case does not say so:\n%s", partialOut.String())
	}

	// A readable store with nothing in it stays quiet: "no history" is not a
	// problem to warn about.
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if store, _ := inspectDoctorStore(doctorStoreCheck{name: "codex", paths: []string{empty}, files: nil, parse: nil}); store.State == "denied" {
		t.Errorf("an empty readable store was reported as denied")
	}

	// And a store that reads fine but fails to parse is still a format report.
	file := filepath.Join(empty, "s.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := doctorStoreCheck{
		name: "codex", paths: []string{empty}, files: []string{file},
		parse: func(string) ([]model.Session, error) { return nil, errUnreadableStore },
	}
	if store, _ := inspectDoctorStore(broken); store.State != "unreadable" {
		t.Errorf("state = %q, want unreadable", store.State)
	}
}

// The warning under the row has said "only partly readable" since #816, and
// `doctor --json` carries `partial` — the row itself folded both states into
// "cannot be read", over a store that search was still answering from (#1034).
func TestDoctorRowSeparatesAPartlyReadableStore(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the widget alignment"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	var out strings.Builder
	doctorHarnesses(&out, dir)
	row := ""
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "claude ") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("no claude row:\n%s", out.String())
	}
	if !strings.Contains(row, "partly unreadable") {
		t.Errorf("the row does not say the store is only partly unreadable: %q", row)
	}
	if strings.Contains(row, "cannot be read") {
		t.Errorf("the row still calls a partly readable store unreadable: %q", row)
	}
}

// The wiring in #3421 served one session eight identical blocks per prompt for
// a day, and nothing said so: every check read a config and answered "is the
// hook here", which a file holding eight copies answers yes to. The log deja
// already keeps had the evidence — the same session, the same second, eight
// rows.
func TestDoctorNamesASessionServedTwiceInASecond(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	// Inside one second on purpose: the row groups by the second, so three
	// events 300µs apart around a second boundary land in two of them and the
	// assertion below reads 2 where it wrote 3. A 0.06% flake, which on a
	// suite this size is a red main every few hundred runs.
	at := time.Now().UTC().Truncate(time.Second).Add(100 * time.Millisecond)
	writeInjectionLog(t, dir,
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
		usage.Event{Time: at.Add(300 * time.Microsecond), Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
		usage.Event{Time: at.Add(600 * time.Microsecond), Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
	)

	var out bytes.Buffer
	doctorDoubleInjections(&out, dir)
	got := out.String()
	if !strings.Contains(got, "served 3 dejavu injections inside a second") {
		t.Errorf("doctor did not count the repeated injections:\n%s", got)
	}
	if !strings.Contains(got, "deja install --auto") {
		t.Errorf("doctor named no way out of it:\n%s", got)
	}
}

// One injection per prompt is the normal case, and two prompts a second apart
// are not a doubled hook.
func TestDoctorSaysNothingAboutOrdinaryInjections(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	at := time.Now().UTC()
	writeInjectionLog(t, dir,
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 400, Into: "agent-1"},
		usage.Event{Time: at.Add(3 * time.Second), Kind: usage.KindDejaVu, Bytes: 400, Into: "agent-1"},
		// Two sessions served in the same second is a fleet, not a repeat.
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 400, Into: "agent-2"},
	)

	var out bytes.Buffer
	doctorDoubleInjections(&out, dir)
	if got := out.String(); got != "" {
		t.Errorf("doctor invented a repeat:\n%s", got)
	}
}

// An install collapses the entries, so a repeat from before the last one deja
// recorded is fixed history. Reading the whole log kept the row naming the
// morning before the repair, with a remedy the reader had already applied
// (#3697).
func TestDoctorStopsNamingARepeatFromBeforeTheLastInstall(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	at := time.Now().UTC().Add(-2 * time.Hour)
	writeInjectionLog(t, dir,
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
		usage.Event{Time: at.Add(300 * time.Microsecond), Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
	)
	var before bytes.Buffer
	doctorDoubleInjections(&before, dir)
	if before.String() == "" {
		t.Fatal("the row says nothing about a repeat from two hours ago, so there is nothing to bound")
	}
	// The record written since: an install has been through the file.
	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), []byte(`{"version":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	doctorDoubleInjections(&after, dir)
	if got := after.String(); got != "" {
		t.Errorf("the row still names a repeat from before the install that fixed it:\n%s", got)
	}
}

func writeInjectionLog(t *testing.T, dir string, events ...usage.Event) {
	t.Helper()
	var b bytes.Buffer
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(usage.Path(dir), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cordis.patch.yml names deja's plugin files by path, and dsh refuses the whole
// profile when one of them is gone. Doctor read the layer for the server alone
// and called it wired while dsh would not start (#4292).
func TestDoctorSaysDSHWillNotStartWhenAPluginFileIsGone(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto(bin, false); err != nil {
		t.Fatal(err)
	}
	mcpRow := func() map[string]any {
		t.Helper()
		out, err := captureRun(t, "doctor", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			MCP []map[string]any `json:"mcp"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("doctor --json: %v: %s", err, out)
		}
		for _, r := range report.MCP {
			if r["name"] == "deepseek" {
				return r
			}
		}
		t.Fatalf("no deepseek row:\n%s", out)
		return nil
	}

	// Control: everything the layer names is there.
	if got := mcpRow(); got["state"] != "wired" || got["plugin_missing"] != nil {
		t.Errorf("a complete install reads %v, want wired and no plugin_missing", got)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "dsh will not start") {
		t.Errorf("a complete install was reported as broken:\n%s", text)
	}

	if err = os.Remove(dshAutoPath()); err != nil {
		t.Fatal(err)
	}
	if got := mcpRow(); got["plugin_missing"] != true {
		t.Errorf("a layer naming a deleted plugin reads %v, want plugin_missing", got)
	}
	text, err = captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{reportPath(dshAutoPath()) + ", which dsh cannot find", "dsh will not start", "deja install deepseek-auto", "deja uninstall deepseek", "still names it"} {
		if !strings.Contains(text, want) {
			t.Errorf("doctor does not say %q:\n%s", want, text)
		}
	}
}

// The header told a reader the file could go, and dsh does not start without
// it while the layer names it (#4292).
func TestDSHPluginFilesDoNotCallThemselvesSafeToDelete(t *testing.T) {
	// auto.js goes with `deja install deepseek`, which keeps the server and
	// /deja; only the whole install comes out with uninstall.
	for name, c := range map[string]struct{ body, how string }{
		"command.js": {dshCommandJS("/bin/deja"), "deja uninstall deepseek"},
		"auto.js":    {dshAutoJS("/bin/deja"), "deja install deepseek"},
	} {
		head, _, _ := strings.Cut(c.body, "\n")
		if strings.Contains(head, "safe to delete") {
			t.Errorf("%s opens with %q", name, head)
		}
		if !strings.HasSuffix(head, c.how) {
			t.Errorf("%s does not end on %q: %q", name, c.how, head)
		}
	}
}

// A row can name its plugin other ways than by absolute path. Measured against
// dsh 0.1.1-rc.2: `./x` resolves against the profile's directory (not the
// layer's), a file:// URL is imported as the file, and `~/` is never expanded,
// so such a row fails whether or not the file is there (#4292).
func TestDSHPluginsMissingReadsEverySpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	profile := filepath.Join(home, "profiles", "headless")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "profiles", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	present := filepath.Join(home, "present.js")
	if err := os.WriteFile(present, []byte("export default () => {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "here.js"), []byte("export default () => {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(home, "cordis.patch.yml")
	body := dshBlockStart + "\n- insert:\n" +
		"    - id: a\n      name: '@deepseek-ai/dsh-mcp-client'\n" +
		"    - id: b\n      name: ./here.js\n" +
		"    - id: c\n      name: ./gone.js\n" +
		"    - id: d\n      name: 'file://" + filepath.ToSlash(present) + "'\n" +
		"    - id: e\n      name: \"file://" + filepath.ToSlash(filepath.Join(home, "gone-url.js")) + "\"\n" +
		"    - id: f\n      name: ~/.dsh/plugins/deja/auto.js\n" +
		dshBlockEnd + "\n"
	if err := os.WriteFile(layer, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dshPluginsMissing(layer)
	want := []string{filepath.Join(profile, "gone.js"), filepath.Join(home, "gone-url.js"), "~/.dsh/plugins/deja/auto.js"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("missing = %q, want %q", got, want)
	}
}

// Two files gone reads as two, not as one.
func TestDSHPluginsMissingNoteCountsWhatIsGone(t *testing.T) {
	layer := filepath.Join(t.TempDir(), "cordis.patch.yml")
	if err := os.WriteFile(layer, []byte(dshBlockStart+"\n    - id: deja-auto\n"+dshBlockEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	one := dshPluginsMissingNote(layer, []string{"/a/command.js"})
	if !strings.Contains(one, "names /a/command.js, which dsh cannot find") {
		t.Errorf("one missing: %q", one)
	}
	two := dshPluginsMissingNote(layer, []string{"/a/command.js", "/a/auto.js"})
	if !strings.Contains(two, "names /a/command.js and /a/auto.js, which dsh cannot find") ||
		!strings.Contains(two, "writes them again") {
		t.Errorf("two missing: %q", two)
	}
}

// A hand edit can leave a YAML comment after the name, and two rows, or one
// `../` row seen from every profile, can come to the same file. The comment is
// not part of the path, and a file is named once (#4292).
func TestDSHPluginsMissingReadsCommentsAndNamesAFileOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	for _, p := range []string{"headless", "web"} {
		if err := os.MkdirAll(filepath.Join(home, "profiles", p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	present := filepath.Join(home, "present.js")
	if err := os.WriteFile(present, []byte("export default () => {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(home, "cordis.patch.yml")
	body := dshBlockStart + "\n- insert:\n" +
		"    - id: a\n      name: " + present + " # the command plugin\n" +
		"    - id: b\n      name: '" + present + "'  # quoted, then a comment\n" +
		"    - id: c\n      name: ../gone.js\n" +
		"    - id: d\n      name: ../gone.js\n" +
		dshBlockEnd + "\n"
	if err := os.WriteFile(layer, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dshPluginsMissing(layer)
	want := []string{filepath.Join(home, "profiles", "gone.js")}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("missing = %q, want %q", got, want)
	}
}

// A file:// URL keeps its drive. Go's url.Parse reads file://C:/x as host "C:"
// and path /x, so the drive was dropped and doctor named \x, a path other than
// the one in the layer; file:///C:/x parses to the path /C:/x (#4438).
func TestDSHNameMissingKeepsAFileURLsDrive(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"file://C:/nowhere/gone.js", "C:/nowhere/gone.js"},
		{"file:///C:/nowhere/gone.js", "C:/nowhere/gone.js"},
		{"file:///nowhere/gone.js", "/nowhere/gone.js"},
		{"file://localhost/nowhere/gone.js", "/nowhere/gone.js"},
		{"file://localhost/C:/nowhere/gone.js", "C:/nowhere/gone.js"},
		{"file:///C:/no%20where/gone.js", "C:/no where/gone.js"},
		// A host other than localhost is a share, as Node's fileURLToPath
		// reads it on Windows; dropping it named a local path instead.
		{"file://nas/share/gone.js", "//nas/share/gone.js"},
	} {
		got := dshNameMissing(c.name)
		if want := filepath.FromSlash(c.want); len(got) != 1 || got[0] != want {
			t.Errorf("dshNameMissing(%q) = %q, want [%q]", c.name, got, want)
		}
	}
}

// A plugin written before #4937 tags its notices with the v3 "plugin" source
// kind, which dsh's v4 sessions refuse on append, so every turn it speaks in
// fails. The file still calls deja, so doctor read it as wired and the
// machine looked fine until `deja install deepseek` was run again.
func TestDoctorFlagsADshPluginWithTheRetiredSourceKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	row := func() string {
		var buf bytes.Buffer
		doctorAutoRecall(&buf)
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "deepseek ") {
				return l
			}
		}
		t.Fatalf("no deepseek row:\n%s", buf.String())
		return ""
	}
	if r := row(); !strings.Contains(r, "wired") {
		t.Fatalf("a fresh install: %q", r)
	}
	path := dshAutoPath()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(b), `source: { kind: "plugin:deja", form: "notice" }`, `source: { kind: "plugin", plugin: "deja", form: "notice" }`, 1)
	if old == string(b) {
		t.Fatal("the template no longer has the source line this test rewrites")
	}
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	r := row()
	if !strings.Contains(r, "stale") || !strings.Contains(r, "deja install deepseek-auto") {
		t.Errorf("the pre-#4937 plugin: %q", r)
	}
	if st, _ := autoWiringState(autoWiring{name: "deepseek", path: dshAutoPath, marker: "hook-prompt"}); st != "stale" {
		t.Errorf("--json state = %q", st)
	}
}

// doctor is where someone goes when semantic results stop coming. A version 1
// sidecar addresses records by offset alone, so search refuses one built for an
// earlier index, and doctor was still reporting its coverage, which reads as
// "the embeddings are there" on the one screen meant to say otherwise.
func TestDoctorReportsNoCoverageForASidecarSearchRefuses(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the scheduler retries twice"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	if _, err := embed.EmbedIndex(dir, &embed.Client{URL: ts.URL, Model: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	asVersion1Sidecar(t, dir)
	before := collectDoctorEmbed(dir)
	if before == nil || before.Coverage == 0 {
		t.Fatal("no coverage before a rebuild, so the test measures nothing")
	}

	// A rebuild replaces records.bin, so no offset in the sidecar means what
	// it did and search refuses it. A rebuild rather than an ordinary pass:
	// since #3500 a new transcript is appended and since #5387 a rewritten one
	// is too, which leaves every kept offset where it was.
	rewound := `{"type":"user","message":{"role":"user","content":"the cache eviction wiped sessions"},"timestamp":"2026-08-03T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rewound), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	s, err := embed.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !embed.Stale(dir, s) {
		t.Fatal("the rebuild left the sidecar usable, so the test measures nothing")
	}
	if r := collectDoctorEmbed(dir); r != nil && r.Coverage > 0 {
		t.Errorf("doctor reports %.1f%% coverage from a sidecar search will not use", r.Coverage)
	}
}

// A version 2 sidecar follows its records through the same rewrite, so doctor
// keeps reporting its coverage and search keeps using it.
func TestDoctorKeepsCoverageForASidecarThatFollowsARewrite(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the scheduler retries twice"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	if _, err := embed.EmbedIndex(dir, &embed.Client{URL: ts.URL, Model: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	gen, _ := index.Generation(dir)
	rewound := `{"type":"user","message":{"role":"user","content":"the scheduler retries twice"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"the cache eviction wiped sessions"},"timestamp":"2026-08-03T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rewound), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if now, _ := index.Generation(dir); now == gen {
		t.Fatal("the rebuild kept the generation, so the test measures nothing")
	}
	s, err := embed.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if embed.Stale(dir, s) {
		t.Fatal("a sidecar whose vectors carry their identity was called stale after a rebuild")
	}
	if r := collectDoctorEmbed(dir); r == nil || r.Coverage == 0 {
		t.Errorf("doctor reports no coverage for a sidecar search still uses: %+v", r)
	}
}

// A file where the index directory belongs is not a missing index: a build
// refuses to run there rather than deleting it (#3610), so the advice attached
// to `missing` — run `deja warmup` — cannot be followed. Both surfaces name the
// state instead.
func TestDoctorNamesAnIndexPathThatIsAFile(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := os.WriteFile(dir, []byte("notes somebody kept here\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := inspectDoctorIndex(dir, nil)
	if report.State != "path-is-a-file" {
		t.Errorf("doctor --json calls it %q, want path-is-a-file", report.State)
	}

	var out bytes.Buffer
	doctorIndex(&out, report, dir)
	got := out.String()
	if !strings.Contains(got, "is a file, not a directory") {
		t.Errorf("the text report does not say what is at the path:\n%s", got)
	}
	if strings.Contains(got, "deja warmup") {
		t.Errorf("the report tells the reader to run a build that will refuse:\n%s", got)
	}
}

// TeleAgent keeps one store per account under users/<account>/teleagent.db, so
// doctor's row names the store it found rather than one fixed path, and the
// store it names is the one the probe parses. An account directory a login
// created and never opened a conversation in has no store to name (#6128).
func TestDoctorTeleagentNamesTheAccountStore(t *testing.T) {
	tmp := hermeticEnv(t)
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := filepath.Join(tmp, "teleagent")
	acct := filepath.Join(root, "users", "2091696589113404")
	if err := os.MkdirAll(acct, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(acct, "teleagent.db")
	schema, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "teleagent", "teleagent.sql"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(string(schema))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(root, "users", "2091696589113405"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_TELEAGENT_ROOT", root)
	t.Setenv("TELEAGENT_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	var buf bytes.Buffer
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	doctorHarnesses(&buf, dir)
	var row string
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "teleagent ") {
			row = line
		}
	}
	if !strings.Contains(row, "found") || !strings.Contains(row, db) {
		t.Fatalf("teleagent row = %q, want the account store named\n%s", row, buf.String())
	}

	var check doctorStoreCheck
	for _, c := range doctorStoreChecks() {
		if c.name == "teleagent" {
			check = c
		}
	}
	store, _ := inspectDoctorStore(check)
	if store.State != "ok" {
		t.Errorf("store state = %q, want ok", store.State)
	}
}

func TestTeleagentIncrementalPassDoesNotDoubleTurns(t *testing.T) {
	tmp := hermeticEnv(t)
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := filepath.Join(tmp, "teleagent")
	db1 := filepath.Join(root, "users", "2091696589113404", "teleagent.db")
	db2 := filepath.Join(root, "users", "2091696589113405", "teleagent.db")
	for _, db := range []string{db1, db2} {
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "teleagent", "teleagent.sql"))
	if err != nil {
		t.Fatal(err)
	}
	runSQL := func(dbPath, sql string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "sqlite3", dbPath)
		cmd.Stdin = strings.NewReader(sql)
		out, rerr := cmd.CombinedOutput()
		if rerr != nil {
			t.Fatalf("sqlite3 %s: %v: %s", dbPath, rerr, out)
		}
	}
	runSQL(db1, string(schema))
	schema2 := strings.NewReplacer(
		"ses_teleagent_01", "ses_teleagent_02",
		"msg_teleagent_", "msg_teleagent_b_",
	).Replace(string(schema))
	runSQL(db2, schema2)

	t.Setenv("DEJA_TELEAGENT_ROOT", root)
	t.Setenv("TELEAGENT_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	dir := filepath.Join(tmp, "idx")
	if err = index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	sessions, _, _ := index.BuiltSummary(dir, nil)
	if sessions != 2 {
		t.Fatalf("pass 1 sessions = %d, want 2", sessions)
	}
	s1, ok, err := index.FindByIdentity(dir, "teleagent", "ses_teleagent_01")
	if err != nil || !ok || len(s1.Messages) != 2 {
		t.Fatalf("pass 1 ses_teleagent_01: ok=%v err=%v msgs=%d", ok, err, len(s1.Messages))
	}
	if s1.Path != db1 {
		t.Fatalf("pass 1 Path = %q, want %q", s1.Path, db1)
	}

	runSQL(db1, `
		insert into session_message (id, session_id, type, seq, time_created, time_updated, data)
		values ('msg_teleagent_03', 'ses_teleagent_01', 'user', 3, 1752915610000, 1752915610000, '{"time":{"created":1752915610000},"text":"Follow-up verify check after singleflight."}');
		update session_v2 set time_updated = 1752915610000 where id = 'ses_teleagent_01';
	`)
	future := time.Now().Add(2 * time.Second)
	if err = os.Chtimes(db1, future, future); err != nil {
		t.Fatal(err)
	}
	if err = index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	sessions, _, _ = index.BuiltSummary(dir, nil)
	if sessions != 2 {
		t.Fatalf("pass 2 sessions = %d, want 2", sessions)
	}
	s1, ok, err = index.FindByIdentity(dir, "teleagent", "ses_teleagent_01")
	if err != nil || !ok || len(s1.Messages) != 3 {
		t.Fatalf("pass 2 ses_teleagent_01: ok=%v err=%v msgs=%d (want 3)", ok, err, len(s1.Messages))
	}
	s2, ok, err := index.FindByIdentity(dir, "teleagent", "ses_teleagent_02")
	if err != nil || !ok || len(s2.Messages) != 2 {
		t.Fatalf("pass 2 ses_teleagent_02: ok=%v err=%v msgs=%d (want 2)", ok, err, len(s2.Messages))
	}
}
