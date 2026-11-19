package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/peers"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// doctor's store checks listed every store's files before asking whether the
// store was excluded, and `deja index` runs them, so an excluded store was
// walked on every index pass (#5394).
func TestDoctorDoesNotListAnExcludedStore(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", root)
	roll := filepath.Join(root, "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001.jsonl")
	if err := os.MkdirAll(filepath.Dir(roll), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(roll, []byte(`{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001","cwd":"/tmp/proj"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codexFiles := func() (int, bool) {
		for _, c := range doctorStoreChecks() {
			if c.name == "codex" {
				return len(c.files), true
			}
		}
		return 0, false
	}
	if n, _ := codexFiles(); n != 1 {
		t.Fatalf("control: codex lists %d files with nothing excluded, want 1", n)
	}
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "codex")
	n, row := codexFiles()
	if !row {
		t.Fatal("the excluded store lost its row, which is where doctor says `excluded`")
	}
	if n != 0 {
		t.Errorf("doctor listed %d files of an excluded store", n)
	}
}

// excludeAfterIndex indexes a store and only then writes the pattern, which is
// the sequence a reader actually performs: see a project they did not want
// indexed, write the rule, look again.
func excludeAfterIndex(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"x1", "x2", "x3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-secret", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/secret","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

func writeExclude(t *testing.T, body string) {
	t.Helper()
	path := sources.ExcludePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// doctor is the screen that says what is in force — it prints the trust policy
// per activation and the ignore rule by name. For the exclude list it said
// "N active patterns", which is false in the one state a reader is in when
// they look: pattern written, index not rebuilt, and the next search still
// serving the project they meant to hide (#2664).
func TestDoctorSaysAnExcludePatternIsNotAppliedYet(t *testing.T) {
	excludeAfterIndex(t)
	writeExclude(t, "secret\n")
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exclusions") {
		t.Fatalf("doctor no longer reports the exclude list at all:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, "exclusions") {
			continue
		}
		if strings.Contains(line, "active") && !strings.Contains(line, "rebuild") {
			t.Fatalf("doctor calls a pattern active that covers nothing already indexed:\n%s", line)
		}
		if !strings.Contains(line, "rebuild") {
			t.Fatalf("doctor does not say what would apply the pattern:\n%s", line)
		}
	}
}

// Once the index is built under the same list, the line is a plain statement
// again.
func TestDoctorSaysNothingExtraWhenTheExcludeListIsInForce(t *testing.T) {
	excludeAfterIndex(t)
	writeExclude(t, "secret\n")
	if _, err := captureRun(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "exclusions") && strings.Contains(line, "rebuild") {
			t.Fatalf("the pattern is in force; nothing should point at a rebuild:\n%s", line)
		}
	}
}

// Each client keeps files of its own beside the transcripts: goal queues,
// saved chats, workflow runs, cost caches, sub-agent metadata. None is a
// transcript deja failed to read, and a sub-agent's log is a skipped sub-agent,
// not an unknown file (#4473, #4474, #4475, #4476, #4477, #4478).
func TestAClientsOwnFilesAreNotUnreadTranscripts(t *testing.T) {
	cases := []struct {
		row     string
		env     string
		sub     string // the root below the env var's directory
		session string
		own     []string
		want    string // a fragment the row must still carry
		avoid   string // and one it must not
	}{
		{
			row: "kimi", env: "DEJA_KIMI_ROOT", sub: "sessions",
			session: "wd_api/s1/agents/main/wire.jsonl",
			own: []string{"wd_api/s1/state.json", "wd_api/s1/upcoming-goals.json", "wd_api/s1/agents/agent-1/wire.jsonl",
				"wd_api/s1/agents/main/tasks/bash-a1b2c3d4.json", "wd_api/s1/tasks/bash-e5f6a7b8.json"},
			// Read by default now, as Claude's are (#5229), and the row says how.
			want: "1 subagent transcripts read as task, answer and what they changed",
		},
		{
			row: "gemini", env: "DEJA_GEMINI_ROOT", sub: "tmp",
			session: "demo/chats/session-1.json",
			own: []string{"demo/logs.json", "demo/checkpoint-retry-loop.json",
				"demo/checkpoints/2026-10-01T10-00-00_000Z-main.go-replace.json", "demo/s1/tasks/1.json"},
			want: "(1 file",
		},
		{
			row: "qwen", env: "DEJA_QWEN_ROOT", sub: "projects",
			session: "-w-api/chats/a.jsonl",
			own: []string{"-w-api/session-organization.v1.json", "-w-api/workflows/run-1.json",
				"-w-api/workflows/run-1/journal.jsonl", "-w-api/subagents/s1/agent-a1.jsonl",
				"-w-api/subagents/s1/agent-a1.meta.json"},
			// Read by default now, as Claude's are (#5223), and the row says how.
			want: "1 subagent transcripts read as task, answer and what they changed",
		},
		{
			// Muse keeps cron.db, goals.db and spilled tool output in each
			// session directory, and its subagents' logs one level down.
			row: "muse", env: "DEJA_MUSE_ROOTS",
			session: "2026/09/18/u1/session.jsonl",
			own: []string{"2026/09/18/u1/cron.db", "2026/09/18/u1/goals.db",
				"2026/09/18/u1/tool-outputs/call_1.txt", "2026/09/18/u1/subagent/c1/session.jsonl"},
			// Read by default now, as Claude's are (#5301), and the row says how.
			want: "1 subagent transcripts read as task, answer and what they changed",
		},
		{
			row: "copilot", env: "DEJA_COPILOT_ROOT",
			session: "s1/events.jsonl",
			own:     []string{"s1/autopilot-objective.json"},
			want:    "(1 file",
		},
		{
			row: "openclaw", env: "DEJA_OPENCLAW_ROOT",
			session: "main/sessions/s1.jsonl",
			own:     []string{"main/sessions/.usage-cost-cache.json", "main/sessions/s1.trajectory.jsonl"},
			want:    "(1 file",
		},
		{
			row: "claude", env: "DEJA_CLAUDE_ROOT",
			session: "-tmp-proj/s1.jsonl",
			own:     []string{"-tmp-proj/s1/subagents/agent-a1.jsonl", "-tmp-proj/s1/subagents/agent-a1.meta.json"},
			want:    "1 subagent transcripts read as task",
			avoid:   "skipped",
		},
		{
			row: "antigravity", env: "DEJA_ANTIGRAVITY_ROOT",
			session: "brain/s1/.system_generated/logs/transcript.jsonl",
			own: []string{
				"brain/s1/.system_generated/subagents/c1.json",
				"brain/s1/scratch/poll.json",
				"brain/c1/.system_generated/logs/transcript.jsonl",
			},
			want:  "1 subagent transcripts read as task, answer and what they changed",
			avoid: "not recognised",
		},
	}
	for _, tc := range cases {
		t.Run(tc.row, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
			root := filepath.Join(tmp, tc.row+"-store")
			t.Setenv(tc.env, root)
			base := filepath.Join(root, tc.sub)
			for _, rel := range append([]string{tc.session}, tc.own...) {
				p := filepath.Join(base, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(`{"type":"user","text":"why is the retry loop slow"}`+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			doctorHarnesses(&out, t.TempDir())
			var row string
			for line := range strings.SplitSeq(out.String(), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), tc.row+" ") {
					row = strings.TrimSpace(line)
				}
			}
			if row == "" {
				t.Fatalf("no %s row in:\n%s", tc.row, out.String())
			}
			if strings.Contains(row, "not recognised") {
				t.Errorf("the client's own files are counted as unread transcripts: %s", row)
			}
			if !strings.Contains(row, tc.want) {
				t.Errorf("row lacks %q: %s", tc.want, row)
			}
			if tc.avoid != "" && strings.Contains(row, tc.avoid) {
				t.Errorf("row says %q: %s", tc.avoid, row)
			}
		})
	}
}

// A Kimi /btw fork is read, so doctor counts it as indexed; a fork that an
// Agent call with fork: true made is a sub-agent run and stays a skipped one
// (#4484).
func TestDoctorCountsAKimiBtwForkAsRead(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	root := filepath.Join(tmp, "kimi-store")
	t.Setenv("DEJA_KIMI_ROOT", root)
	session := filepath.Join(root, "sessions", "wd_api", "s1")
	for rel, body := range map[string]string{
		"state.json": `{"workDir":"/w/api","agents":{"main":{"type":"main"},` +
			`"agent-1":{"type":"sub","parentAgentId":"main","forkedFrom":"main"},` +
			`"agent-2":{"type":"sub","parentAgentId":"main","forkedFrom":"main"}}}`,
		"agents/main/wire.jsonl": `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"why is the retry loop slow"}]},"time":1782295300001}` + "\n",
		"agents/agent-1/wire.jsonl": `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"side"}],"origin":{"kind":"injection","variant":"btw"}},"time":1782295300002}` + "\n" +
			`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"what is jitter"}],"origin":{"kind":"user"}},"time":1782295300003}` + "\n",
		"agents/agent-2/wire.jsonl": `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"audit it"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1782295300004}` + "\n",
	} {
		p := filepath.Join(session, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	var row string
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "kimi ") && strings.Contains(line, "file") {
			row = strings.TrimSpace(line)
		}
	}
	for _, want := range []string{"(2 files", "1 subagent transcripts skipped"} {
		if !strings.Contains(row, want) {
			t.Errorf("kimi row lacks %q: %s", want, row)
		}
	}
}

// A files-to-sessions gap has three causes; doctor explained a parse failure
// (#861) and an id collision (#1101) and left the reader's own forget
// unexplained — on the screen someone opens to check the forget took (#1108).
func TestDoctorSaysWhatWasForgotten(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a decision about the queue"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var before bytes.Buffer
	if err := runDoctor(&before, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before.String(), "forgotten") {
		t.Errorf("a machine with nothing forgotten mentions it:\n%s", before.String())
	}

	if _, err := captureRun(t, "forget", "--session", "claude:s1"); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := runDoctor(&after, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after.String(), "1 session forgotten here") {
		t.Errorf("doctor does not say the index is empty because of a forget:\n%s", after.String())
	}
}

// An index written by a layout this binary cannot read is unreadable to it —
// the hook paths refuse it and ask for a rebuild, which is why memory goes
// quiet after an upgrade. doctor called that "up to date" (#877).
func TestDoctorNamesAnIndexFromAnOlderFormat(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// A current index says nothing about its format.
	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "ok", Path: dir}, dir)
	if got := out.String(); strings.Contains(got, "older deja") {
		t.Errorf("a current index was called old:\n%s", got)
	}

	// An index this build cannot read.
	saved := indexReadState
	indexReadState = func(string) index.ReadState { return index.ReadStateUnreadable }
	t.Cleanup(func() { indexReadState = saved })
	out.Reset()
	doctorIndex(&out, doctorIndexReport{State: "ok", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "written by an older deja") {
		t.Errorf("doctor does not mention the format:\n%s", got)
	}
	if !strings.Contains(got, "deja index") {
		t.Errorf("doctor does not say what to do:\n%s", got)
	}
}

// Four states, four sentences. The direction matters — an index from a newer
// deja means the binary was rolled back, and calling it old sends that reader
// the wrong way (#890) — and so does what the build may do with it: a store one
// content version behind reads and answers, and "cannot read it" told someone
// looking for why nothing is recalled that their index was gone (#3597).
func TestDoctorTellsWhatItMayDoWithTheIndex(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	saved := indexReadState
	t.Cleanup(func() { indexReadState = saved })

	line := func(state index.ReadState) string {
		indexReadState = func(string) index.ReadState { return state }
		var out bytes.Buffer
		doctorIndex(&out, doctorIndexReport{State: "ok", Path: dir}, dir)
		for l := range strings.SplitSeq(out.String(), "\n") {
			if strings.Contains(l, "format ") {
				return l
			}
		}
		return ""
	}

	if got := line(index.ReadStateUnreadable); !strings.Contains(got, "cannot read it") {
		t.Errorf("unreadable index: %q", got)
	}

	// Readable and answering. The row must not claim otherwise, and must not
	// leave the reader thinking nothing is pending either.
	got := line(index.ReadStateOlderRules)
	if strings.Contains(got, "cannot read") {
		t.Errorf("an index that answers was called unreadable: %q", got)
	}
	if !strings.Contains(got, "still answers") || !strings.Contains(got, "re-read") {
		t.Errorf("an older-rules index does not say what is pending: %q", got)
	}

	// Readable, but withholding until the re-read — the one state of the three
	// that does stop recall, and it is not the same sentence as unreadable.
	got = line(index.ReadStateWithheld)
	if strings.Contains(got, "cannot read") || strings.Contains(got, "still answers") {
		t.Errorf("a withheld index borrowed another state's sentence: %q", got)
	}
	if !strings.Contains(got, "mask") {
		t.Errorf("a withheld index does not say why it is quiet: %q", got)
	}

	got = line(index.ReadStateNewer)
	if !strings.Contains(got, "written by a newer deja") {
		t.Errorf("newer index: %q", got)
	}
	if strings.Contains(got, "older") {
		t.Errorf("a rolled-back binary was told its index is old: %q", got)
	}

	if got := line(index.ReadStateCurrent); got != "" {
		t.Errorf("a matching format still printed: %q", got)
	}
}

// On a new machine ~/.cache is not there yet, and doctor read that as a disk
// that may have been unmounted, with the only absolute path in the section.
// It is an index that has not been built.
func TestDoctorOnAFreshHomeSaysNotBuiltNotUnmounted(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".cache", "deja")
	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	got := out.String()
	if strings.Contains(got, "unmounted") || !strings.Contains(got, "not built") {
		t.Fatalf("fresh home reported as:\n%s", got)
	}
}

// A vanished mount point still reads as one, and its path is said the way the
// rest of the section says paths.
func TestDoctorUnreachableIndexUsesTheTildeForm(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// Under home, but below a directory that cannot be written: nothing deja
	// runs could create it, so it is not a fresh home.
	locked := filepath.Join(home, "mnt")
	if err := os.MkdirAll(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if dirWritable(locked) {
		t.Skip("cannot make a read-only directory here")
	}
	dir := filepath.Join(locked, "disk", "deja")
	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "not reachable") || !strings.Contains(got, "~"+string(filepath.Separator)+filepath.Join("mnt", "disk")) {
		t.Fatalf("unreachable index reported as:\n%s", got)
	}
}

// A note stamped next year sits at the top of `deja last` and stays there: deja
// cannot tell a skewed clock from a deliberate date, so the ordering is left
// alone — but the reader is not told either, and a typo'd year or a millisecond
// stamp read as seconds looks like nothing at all (#2063).
func TestDoctorNamesASessionStampedInTheFuture(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	next := time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339)
	now := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := `{"ts":"` + next + `","project":"tmp/app","text":"a stray note stamped next year"}` + "\n" +
		`{"ts":"` + now + `","project":"tmp/app","text":"the pgbouncer pool times out under load"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stamped in the future") {
		t.Errorf("doctor says nothing about a session dated ahead of the clock:\n%s", out.String())
	}

	// And it stays quiet when nothing is: the line is a finding, not furniture.
	if err := os.WriteFile(notes, []byte(`{"ts":"`+now+`","project":"tmp/app","text":"the pgbouncer pool times out under load"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runDoctor(&out, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "stamped in the future") {
		t.Errorf("doctor reports a future stamp with none in the store:\n%s", out.String())
	}
}

// The gemini row walked all of ~/.gemini while the reader only looks at
// tmp/<project>/chats, so Antigravity's store — a sibling directory inside the
// same root, with its own row — was counted as chats gemini failed to read:
// 55 of the 77 on this machine, the rest gemini's own configuration (#3397).
func TestDoctorDoesNotCountAnotherHarnessAsGeminisUnread(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "gemini")
	chats := filepath.Join(root, "tmp", "proj", "chats")
	brain := filepath.Join(root, "antigravity", "brain", "f017fad5", ".system_generated", "logs")
	for _, d := range []string{chats, brain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_GEMINI_ROOT", root)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(root, "antigravity"))

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(chats, "session-2026-08-22T06-32-6b36be8d.jsonl"),
		`{"sessionId":"6b36be8d","type":"user","content":"why does the retry loop drop the last attempt"}`+"\n")
	write(filepath.Join(brain, "transcript.jsonl"),
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nanything\n</USER_REQUEST>"}`+"\n")
	write(filepath.Join(root, "settings.json"), "{}")
	write(filepath.Join(root, "projects.json"), "{}")

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.Contains(l, "gemini") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no gemini file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the other store and the settings are not gemini's chats", got)
	}
	// And a chat the reader does not take is still what the row is for: the
	// shape a renamed directory has, which is what layout drift looks like.
	moved := filepath.Join(root, "tmp", "proj", "conversations")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(moved, "session-2026-08-22T06-40-6b36be8d.jsonl"), "{}\n")
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the chat deja did not read", got)
	}
}

// Without git a hit loses the line saying its files changed since, project
// names lose worktree identity, and the session-start hook loses the task
// signal — all in silence, with nowhere to ask why (#796).
func TestDoctorToolsReportsGit(t *testing.T) {
	var out strings.Builder
	doctorTools(&out)
	got := out.String()
	if !strings.Contains(got, "git ") {
		t.Errorf("git is not listed:\n%s", got)
	}
	if !strings.Contains(got, "sqlite3") {
		t.Errorf("sqlite3 lost its row:\n%s", got)
	}

	_, err := exec.LookPath("git")
	wantFound := err == nil
	line := ""
	for l := range strings.SplitSeq(got, "\n") {
		if strings.Contains(l, "git ") {
			line = l
		}
	}
	// "not found" contains "found", so a substring test passes for either
	// answer: read the status field itself.
	status := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "git"))
	status = strings.TrimSpace(strings.SplitN(status, "(", 2)[0])
	if wantFound && status != "found" {
		t.Errorf("git is on PATH but the row says %q", status)
	}
	if !wantFound && status != "not found" {
		t.Errorf("git is not on PATH but the row says %q", status)
	}
	if !strings.Contains(line, "changed-file notes") {
		t.Errorf("the row does not say what git is for: %q", line)
	}
}

// gjc 0.18 keeps a `.gjc-managed-session-scope.v2.json` in every project
// directory, and its sub-agent passes sit under `<session>/N-*.jsonl`. The row
// counted both as transcripts it could not read: one "not recognised here" per
// project, plus one per pass deja skips on purpose (#4393).
func TestDoctorGjcRowKnowsItsScopeFilesAndPasses(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "gjc-sessions")
	t.Setenv("DEJA_GJC_ROOT", root)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session := func(dir, id, cwd string) string {
		p := filepath.Join(root, dir, "2026-10-01T21-00-00-000Z_"+id+".jsonl")
		write(p, `{"type":"session","version":3,"id":"`+id+`","cwd":"`+cwd+`","timestamp":"2026-10-01T21:00:00.000Z"}`+"\n"+
			`{"type":"message","id":"a1","timestamp":"2026-10-01T21:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`+"\n")
		return p
	}
	for i, dir := range []string{"v2-xq3v", "v2-et2c"} {
		cwd := []string{"/tmp/proj", "/tmp/proj2"}[i]
		session(dir, "01a0f93c-0000-7000-8000-00000000000"+string(rune('1'+i)), cwd)
		write(filepath.Join(root, dir, ".gjc-managed-session-scope.v2.json"),
			`{"schemaVersion":1,"layoutVersion":2,"canonicalPath":"`+cwd+`","identityDigest":"x"}`)
	}
	parent := session("v2-xq3v", "01a0f93c-0000-7000-8000-000000000009", "/tmp/proj")
	write(filepath.Join(strings.TrimSuffix(parent, ".jsonl"), "0-explore.jsonl"), "{}\n")

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "gjc ") {
				return l
			}
		}
		t.Fatalf("no gjc row:\n%s", buf.String())
		return ""
	}
	got := row()
	if strings.Contains(got, "not recognised") {
		t.Errorf("row = %q: the scope files and the pass are not transcripts deja failed to read", got)
	}
	if !strings.Contains(got, "3 files") {
		t.Errorf("row = %q, want the three sessions counted", got)
	}
	if !strings.Contains(got, "1 subagent transcripts skipped") {
		t.Errorf("row = %q, want the pass named as a skip", got)
	}
	// The note still does its job for a file deja really does not know.
	write(filepath.Join(root, "v2-xq3v", "something-new.json"), "{}")
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want the unknown file counted", got)
	}
}

// A Kimchi sub-agent run is skipped the same way, and the row says so rather
// than counting it as a file deja could not read (#4401).
func TestDoctorKimchiRowNamesSkippedSubagentRuns(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "kimchi-sessions")
	t.Setenv("DEJA_KIMCHI_ROOT", root)
	dir := filepath.Join(root, "--private-tmp-proj--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := `{"type":"message","id":"m1","timestamp":"2026-10-01T21:57:05.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n"
	parent := filepath.Join(dir, "a_01a0f978-9334.jsonl")
	files := map[string]string{
		parent: `{"type":"session","version":3,"id":"01a0f978-9334","cwd":"/private/tmp/proj"}` + "\n" + msg,
		filepath.Join(dir, "b_01a0f978-95b2.jsonl"): `{"type":"session","version":3,"id":"01a0f978-95b2","cwd":"/private/tmp/proj","parentSession":"` + parent + `"}` + "\n" +
			`{"type":"custom","customType":"kimchi:subagent-session","data":{"type":"General-Purpose"}}` + "\n" + msg,
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "kimchi ") {
			continue
		}
		if !strings.Contains(l, "1 file") || !strings.Contains(l, "1 subagent transcripts skipped") || strings.Contains(l, "not recognised") {
			t.Errorf("row = %q, want one file and the run named as a skip", l)
		}
		return
	}
	t.Fatalf("no kimchi row:\n%s", buf.String())
}

// The line about @vibe-kit/grok-cli suggests `grok mcp add deja -c deja -a
// mcp`, which Grok Build rejects ("unexpected argument '-c' found"). Grok Build
// reads the server from ~/.grok/config.toml already, so the line is only for a
// machine whose `grok` is the vibe-kit CLI, or that has none on PATH (#4587).
func TestDoctorGrokCLINoteOnlyForTheCLIItIsAbout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake grok is a symlink")
	}
	for _, tc := range []struct {
		name, pkg string
		want      bool
	}{
		{"grok build", filepath.Join("@xai-official", "grok", "bin", "grok-native"), false},
		{"vibe-kit", filepath.Join("@vibe-kit", "grok-cli", "dist", "index.js"), true},
		{"none", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			bin := filepath.Join(tmp, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.pkg != "" {
				target := filepath.Join(tmp, "lib", "node_modules", tc.pkg)
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(bin, "grok")); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			note := doctorWiringNote("grok")
			if got := strings.Contains(note, "grok mcp add"); got != tc.want {
				t.Errorf("note shown = %v, want %v: %q", got, tc.want, note)
			}
		})
	}
}

// A Grok store keeps nine files of its own beside each transcript and eleven
// configuration files at the root; doctor counted every one of them as a
// transcript it could not read — 94 of them on a store with 11 sessions
// (#3319). The row's job is to say when a transcript went unread, and a number
// that size says the opposite.
func TestDoctorDoesNotCallGroksOwnFilesUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "grok")
	dir := filepath.Join(root, "sessions", "%2Fw%2Fapi", "6f0a1b2c-0000-4000-8000-000000000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", root)

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "updates.jsonl"),
		`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"why does the retry loop drop the last attempt"}}`+"\n")
	write(filepath.Join(dir, "summary.json"), `{"info":{"id":"6f0a1b2c-0000-4000-8000-000000000001"}}`)
	for _, name := range []string{
		"chat_history.jsonl", "events.jsonl", "rewind_points.jsonl", "prompt_context.json",
		"announcement_state.json", "signals.json", "resources_state.json",
		// Grok Build 1.0.41 writes two more per session (#4586).
		"usage.json", "tool_definitions.json",
	} {
		write(filepath.Join(dir, name), "{}\n")
	}
	write(filepath.Join(root, "sessions", "%2Fw%2Fapi", "prompt_history.jsonl"), "{}\n")
	for _, name := range []string{"settings.json", "user-settings.json", "auth.json", "models_cache.json"} {
		write(filepath.Join(root, name), "{}")
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	var row string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "grok") && strings.Contains(l, "file") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("no grok file row in the report at all")
	}
	if strings.Contains(row, "not recognised") {
		t.Errorf("row = %q, want no such note — every file beside the transcript is Grok's own", row)
	}

	// And the note is still there for the thing it exists for: a transcript
	// under a name the reader does not take.
	write(filepath.Join(dir, "updates.v2.jsonl"), "{}\n")
	buf.Reset()
	doctorHarnesses(&buf, t.TempDir())
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "grok") && strings.Contains(l, "file") {
			if !strings.Contains(l, "1 not recognised here") {
				t.Errorf("row = %q, want it to name the one file deja did not read", l)
			}
			break
		}
	}
}

// The row and the JSON name the same place. doctorStoreChecks keeps its own
// path per harness, and leaving grok's at the store root made `doctor --json`
// say ~/.grok where the row says ~/.grok/sessions — and, on a store deja is
// not allowed to read, turned "denied" into "missing" (review of #3319).
func TestDoctorJSONNamesTheSameGrokPathAsTheRow(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "grok")
	dir := filepath.Join(root, "sessions", "%2Fw%2Fapi", "6f0a1b2c-0000-4000-8000-000000000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", root)
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"),
		[]byte(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"why does the retry loop drop the last attempt"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "sessions")
	for _, c := range doctorStoreChecks() {
		if c.name != "grok" {
			continue
		}
		if len(c.paths) != 1 || c.paths[0] != want {
			t.Fatalf("grok paths = %v, want %q — the row says that one", c.paths, want)
		}
		return
	}
	t.Fatal("no grok entry in doctorStoreChecks at all")
}

// Hermes 0.17 keeps one state.db at the root and has no profiles/ directory.
// doctor's text row looked only at profiles/ and said missing while the store
// was indexed and --json said ok (#4244).
func TestDoctorFindsTheHermesStoreAtTheRoot(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".hermes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.db"), []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for line := range strings.SplitSeq(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "hermes" && strings.Contains(line, "store") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no hermes store row:\n%s", out)
	}
	if strings.Fields(row)[1] == "missing" || strings.Contains(row, "profiles") {
		t.Errorf("a root state.db reads as %q", strings.TrimSpace(row))
	}
}

// The control: a ~/.hermes holding only its config, with no state.db and no
// profiles/, has no store, and `deja install hermes-auto` writes there on a
// machine where hermes never ran. The row fell back to the directory and read
// found with nothing in it.
func TestDoctorDoesNotCallAHermesConfigDirAStore(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".hermes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --all keeps the row: a plain missing store folds otherwise (#4625).
	out, err := captureRun(t, "doctor", "--all")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "hermes" && strings.Contains(line, "store") {
			if f[1] != "missing" {
				t.Errorf("a config-only ~/.hermes reads as %q", strings.TrimSpace(line))
			}
			return
		}
	}
	t.Fatalf("no hermes store row:\n%s", out)
}

// The issue template asks a reporter to run `deja doctor` and redact the local
// paths before pasting. The report is the one command that fills itself with
// them, and deja already contracts a home path to ~ in its progress lines
// (#2360).
func TestDoctorContractsTheHomePath(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", "-work-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, ".claude", "projects"))

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "~/.claude/projects") && !strings.Contains(out, `~\.claude\projects`) {
		t.Errorf("doctor does not contract the home path:\n%s", storeLines(out))
	}
	if strings.Contains(out, home) {
		t.Errorf("doctor still prints the home directory:\n%s", storeLines(out))
	}

	// --json keeps the real path: a tool reading it may need one, and nobody
	// pastes JSON into an issue by hand.
	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err = json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	// As JSON writes it: a Windows path is escaped in the encoding, so looking
	// for the raw spelling is looking for something no encoder produces.
	encoded, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, strings.Trim(string(encoded), `"`)) {
		t.Errorf("doctor --json lost the real path")
	}
}

// storeLines is the part of the report that names paths, for a readable
// failure.
func storeLines(out string) string {
	var keep []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "claude") || strings.Contains(line, "index") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// The MCP rows learned in #2216 that "wired" is not "can start". The hook rows
// print the same word for a hook whose command names a binary that is gone —
// and the recorded-path check goes quiet as soon as one target is reinstalled
// from the new location, which stamps the record for every target. Doctor then
// says nothing at all about a dead recall hook (#2686).
func TestDoctorNamesHooksRunningABinaryThatIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "old", "deja")

	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-context"},
		}}},
		"PreCompact": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-precompact"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(claude, b, 0o644); err != nil {
		t.Fatal(err)
	}

	cursor := filepath.Join(sources.CursorCLIHome(), "hooks.json")
	if err = os.MkdirAll(filepath.Dir(cursor), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]any{"hooks": map[string]any{
		"sessionStart": []any{map[string]any{"command": gone + " hook-context"}},
	}}
	if b, err = json.Marshal(hooks); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursor, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	for _, want := range []string{
		"runs " + gone + ", which is not there",
		"deja install claude-auto",
		"deja install cursor-auto",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor does not say %q:\n%s", want, got)
		}
	}

	// A hook that runs a binary which is there says nothing extra.
	here := filepath.Join(tmp, "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{claude, cursor} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Both spellings: a config holds a Windows path escaped, so replacing
		// the raw one finds nothing and the file still names the binary that
		// is gone.
		swapped := bytes.ReplaceAll(b, []byte(gone), []byte(here))
		swapped = bytes.ReplaceAll(swapped, []byte(jsonEscaped(t, gone)), []byte(jsonEscaped(t, here)))
		if err := os.WriteFile(path, swapped, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("a hook running the binary that is there was called dead:\n%s", got)
	}
}

// The nine plugin-shaped wirings hold the same absolute path in JavaScript,
// TypeScript, Python, or quoted inside a command — and said "wired" while the
// binary they run is gone (#2688).
func TestDoctorNamesPluginHooksRunningABinaryThatIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "old", "deja")

	cases := []struct {
		name string
		path string
		body string
	}{
		{"opencode", filepath.Join(opencodeConfigHome(), "opencode", "plugins", "deja.js"),
			"const raw = await $`\"" + gone + "\" hook-context`.text()\n"},
		{"hermes", filepath.Join(sources.HermesHome(), "plugins", "deja", "__init__.py"),
			"DEJA = \"" + gone + "\"\nsubprocess.run([DEJA, \"hook-context\"])\n"},
		{"goose", gooseHookPath(),
			"{\"hooks\":{\"session_start\":[{\"command\":\"'" + gone + "' hook-goose\"}]}}\n"},
	}
	for _, c := range cases {
		if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	doctorAutoRecall(&out)
	got := out.String()
	if n := strings.Count(got, "runs "+gone); n != len(cases) {
		t.Errorf("%d of %d rows name the binary that is gone:\n%s", n, len(cases), got)
	}
	for _, c := range cases {
		if !strings.Contains(got, "deja install "+c.name+"-auto") {
			t.Errorf("%s is not told how to repair itself:\n%s", c.name, got)
		}
	}

	// And a binary that is there is not called dead — the marker check has
	// nothing to do with this, so a wired row stays one line.
	here := filepath.Join(tmp, "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.path, bytes.ReplaceAll(b, []byte(gone), []byte(here)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	doctorAutoRecall(&out)
	got = out.String()
	if strings.Contains(got, "which is not there") {
		t.Errorf("a plugin running the binary that is there was called dead:\n%s", got)
	}
	// And they are still wired: a check that quietly turned every row stale
	// would pass the line above.
	for _, c := range cases {
		wired := false
		for line := range strings.SplitSeq(got, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), c.name+" ") && strings.Contains(line, "wired") {
				wired = true
			}
		}
		if !wired {
			t.Errorf("%s no longer reads wired:\n%s", c.name, got)
		}
	}
}

// The files these rows read are not all deja's own code. aider's is a digest
// of past sessions, so any path a transcript once mentioned is in there, and
// the hook files themselves carry settings that end in deja without being it
// (#2688).
func TestDoctorDoesNotCallAPathInPassingADeadHook(t *testing.T) {
	tmp := hermeticEnv(t)
	nowhere := filepath.Join(tmp, "coding", "deja")

	aider := aiderContextPath()
	if err := os.MkdirAll(filepath.Dir(aider), 0o755); err != nil {
		t.Fatal(err)
	}
	// A digest of past sessions carries commands those sessions ran, deja's
	// own among them — the one shape that looks exactly like a live hook.
	digest := "- Session: ran `" + nowhere + " hook-context` after the rebuild\n"
	if err := os.WriteFile(aider, []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorAutoRecall(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("a path quoted in a recalled transcript was read as a hook:\n%s", got)
	}

	// And a setting whose name ends in deja is a directory, not the binary.
	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	here := filepath.Join(tmp, "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"DEJA_INDEX_DIR":"` + filepath.Join(tmp, "cache", "deja") + `"},` +
		`"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"` + here + ` hook-context"}]}]}}`
	if err := os.WriteFile(claude, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("an index directory was read as the binary:\n%s", got)
	}
}

// Every hook check answered "is deja's hook here", and a file holding eight
// copies of it answers yes. What the machine did instead was start eight
// processes per prompt and inject the same memory eight times, with doctor
// printing "wired" over it (#3421).
func TestDoctorCountsRepeatedHookEntries(t *testing.T) {
	withStatsStores(t)
	// A file really called deja, and really there: the count keys on the
	// binary's name, and a missing one would set the row's other note off.
	exe := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	events := make([]string, 0, len(claudeHookWiring))
	for _, h := range claudeHookWiring {
		one := `{"hooks":[{"type":"command","command":"` + jsonEscaped(t, exe) + ` ` + h.Sub + `"}]}`
		copies := []string{one}
		if h.Event == "UserPromptSubmit" {
			copies = []string{one, one, one}
		}
		events = append(events, `"`+h.Event+`":[`+strings.Join(copies, ",")+`]`)
	}
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{`+strings.Join(events, ",")+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	if !strings.Contains(got, "UserPromptSubmit ×3") {
		t.Errorf("doctor did not count the repeated hook:\n%s", got)
	}
	if !strings.Contains(got, "deja install claude-auto") {
		t.Errorf("doctor named no way out of it:\n%s", got)
	}
	// The events wired once are not the reader's problem and must not be named.
	if strings.Contains(got, "SessionStart ×") {
		t.Errorf("doctor reported an event that runs once:\n%s", got)
	}
}

// A config wired the way an install leaves it says nothing about repeats.
func TestDoctorSaysNothingAboutASingleEntry(t *testing.T) {
	withStatsStores(t)
	writeClaudeSettings(t, "SessionStart", "UserPromptSubmit")

	var out bytes.Buffer
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "more than once per event") {
		t.Errorf("doctor invented a repeat:\n%s", got)
	}
}

// Every Claude Code hook exited 127 on a Windows machine whose settings named
// `H:\pycode\Self\deja-vu\deja.exe`, and doctor printed "wired": the binary
// was there, and Git Bash could not reach it as spelled (#4116). The same
// spelling fails in any bash, so the check is exercised here with it.
func TestDoctorSaysWhenTheShellCannotRunTheHook(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	hermeticEnv(t)
	write := func(exe string) {
		t.Helper()
		var b strings.Builder
		b.WriteString(`{"hooks":{`)
		for i, h := range claudeHookWiring {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"` + h.Event + `":[{"hooks":[{"type":"command","command":` + jsonQuoted(exe+" "+h.Sub) + `}]}]`)
		}
		b.WriteString(`}}`)
		dir := sources.ClaudeConfigDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// On Windows the binary is really there, spelled the way deja used to
	// write it; elsewhere the spelling alone is enough to fail in bash, and
	// the path is not absolute there, so the missing-binary check stays out.
	broken := `H:\pycode\Self\deja-vu\deja.exe`
	if runtime.GOOS == "windows" {
		if claudeHookShell() == "" {
			t.Skip("no Git Bash")
		}
		broken = filepath.Join(t.TempDir(), "deja.exe")
		if err := os.WriteFile(broken, []byte("MZ"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(broken)
	if st := claudeHookWiringState(); !st.dead {
		t.Errorf("a hook bash cannot run reads as %q and not dead", st.state)
	}
	if note := claudeHookRunNote(claudeHookWiringState().hooks); !strings.Contains(note, "exits 127") {
		t.Errorf("no note for a hook bash cannot run: %q", note)
	}
	if out, _ := captureRun(t, "doctor"); !strings.Contains(out, "every hook exits 127") {
		t.Errorf("doctor's report does not say the hooks cannot start:\n%s", out)
	}

	// The control: the same file naming a binary bash can run says nothing.
	ok := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(hookCommandQuote(ok))
	if note := claudeHookRunNote(claudeHookWiringState().hooks); note != "" {
		t.Errorf("a runnable hook was reported: %q", note)
	}
	if st := claudeHookWiringState(); st.dead {
		t.Error("a runnable hook reads as dead")
	}
}

// A command the reader built around deja is not deja's to check: `command -v`
// would run the part after `&&`.
func TestTheHookRunCheckLeavesAWrappedCommandAlone(t *testing.T) {
	for _, exe := range []string{"cd /x && /usr/local/bin/deja", "FOO=1 /usr/local/bin/deja", "$HOME/bin/deja", "'/a b/deja' || true"} {
		if hookExeIsOneWord(exe) {
			t.Errorf("%q passed as one word", exe)
		}
	}
	for _, exe := range []string{"/usr/local/bin/deja", "'/Applications/My Tools/deja'", `"C:/Program Files/deja/deja.exe"`, `H:\pycode\Self\deja-vu\deja.exe`, `C:\Users\RUNNER~1\AppData\Local\Temp\deja.exe`} {
		if !hookExeIsOneWord(exe) {
			t.Errorf("%q, a path deja writes, was skipped", exe)
		}
	}
}

func writeClaudeSettings(t *testing.T, events ...string) {
	t.Helper()
	// A binary that is there. doctor also reports wiring that names a binary
	// which has been moved away, and a hard-coded path nobody installed made
	// every one of these fixtures look like that case.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Spelled the way the installer spells it, forward slashes on Windows
	// (#4116): the raw path there is the form Git Bash cannot run.
	writeClaudeSettingsNaming(t, hookCommandQuote(exe), events...)
}

// writeClaudeSettingsNaming is the same fixture with the binary named, for the
// state an upgrade leaves behind: entries that are perfectly wired and run a
// deja that is no longer on disk.
func writeClaudeSettingsNaming(t *testing.T, exe string, events ...string) {
	t.Helper()
	entries := make([]string, 0, len(events))
	for _, e := range events {
		// The path goes in as JSON writes it: a Windows exe carries
		// backslashes, and pasting them raw makes the fixture invalid JSON —
		// which doctor reports as an unreadable config, not as the wiring the
		// test is about.
		entries = append(entries, `"`+e+`":[{"matcher":"","hooks":[{"type":"command","command":"`+
			jsonEscaped(t, exe)+` `+subFor(e)+`"}]}]`)
	}
	body := `{"hooks":{` + strings.Join(entries, ",") + `}}`
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func subFor(event string) string {
	for _, h := range claudeHookWiring {
		if h.Event == event {
			return h.Sub
		}
	}
	return "hook-unknown"
}

// A settings.json written by an older deja keeps working and quietly lacks
// everything added since. It was reported as "wired" on the strength of one
// event out of five, so the features that arrived with the others reached
// nobody until they happened to reinstall.
func TestDoctorNamesTheHookEventsAnOlderInstallLacks(t *testing.T) {
	withStatsStores(t)
	writeClaudeSettings(t, "SessionStart", "PreCompact", "UserPromptSubmit")

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	if !strings.Contains(got, "out of date") {
		t.Errorf("a three-of-five wiring was not called out:\n%s", got)
	}
	for _, want := range []string{"PreToolUse", "PostToolUse", "deja install"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not name %q:\n%s", want, got)
		}
	}
}

// Everything the installer writes is there: no complaint, and no advice to run
// a command that would change nothing.
func TestDoctorIsQuietWhenEveryHookIsWired(t *testing.T) {
	withStatsStores(t)
	all := make([]string, 0, len(claudeHookWiring))
	for _, h := range claudeHookWiring {
		all = append(all, h.Event)
	}
	writeClaudeSettings(t, all...)

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	if strings.Contains(got, "out of date") || strings.Contains(got, "deja install") {
		t.Errorf("a complete wiring was reported as stale:\n%s", got)
	}
}

// An ignore rule is matched against the project name and the transcript's own
// path, so the natural thing to write — the directory's absolute path — matches
// neither, and doctor reported it as in force while it hid nothing. The
// activation rows above it have said what they withhold since #978; this is the
// row that did not (#3584).
func TestDoctorSaysWhatAnIgnoreRuleActuallyHides(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, tmp+"/claude/-Users-me-code-client-work/c1.jsonl", "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"2026-09-01T10:00:00Z","cwd":"/Users/me/code/client-work","message":{"role":"user","content":"the exporter drops every third retry"}}`,
	})
	writeClaudeFixture(t, tmp+"/claude/-Users-me-code-open/o1.jsonl", "o1", []string{
		`{"type":"user","sessionId":"o1","timestamp":"2026-09-01T10:00:00Z","cwd":"/Users/me/code/open","message":{"role":"user","content":"the cache key ignored the tenant id"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, pattern, says string
	}{
		{
			name:    "a rule that matches nothing",
			pattern: "/Users/me/code/client-work",
			says:    "matches no indexed session",
		},
		{
			name:    "a rule that hides something",
			pattern: "client-work",
			says:    "hides 1 of 2 indexed sessions",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			printIgnored(&out, policy.Policy{Ignore: []string{tc.pattern}}, index.DefaultDir())
			if !strings.Contains(out.String(), tc.pattern) {
				t.Fatalf("the rule is not named:\n%s", out.String())
			}
			if !strings.Contains(out.String(), tc.says) {
				t.Fatalf("output = %q, want it to say %q", out.String(), tc.says)
			}
		})
	}
}

// deja's own default is not something to explain to a machine that has never
// met an agent runtime: only a rule somebody wrote gets the effect line.
func TestTheDefaultIgnoreRuleIsNotAnnotated(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	printIgnored(&out, policy.Policy{}, index.DefaultDir())
	if out.Len() == 0 {
		t.Fatal("the default rule is not printed at all")
	}
	if strings.Contains(out.String(), "matches no indexed session") || strings.Contains(out.String(), "hides") {
		t.Fatalf("the default rule was annotated:\n%s", out.String())
	}
}

// With imports and no configured peer, the Sync section returned early on "no
// other machines yet" — while the manifest knew which machines the sessions had
// come from, and recall was answering with them (#2378).
func TestDoctorNamesTheMachinesThatSentWork(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	importFrom := func(machine string, ids ...string) {
		t.Helper()
		batch := t.TempDir()
		// Written as a batch is written on the wire: `origin` is the machine
		// that exported it, and its type is unexported here.
		lines := make([]string, 0, len(ids))
		for _, id := range ids {
			lines = append(lines, fmt.Sprintf(
				`{"harness":"claude","session_id":%q,"project":"work/app","role":"user",`+
					`"text":"work from %s","time":%q,"origin":%q}`,
				id, machine, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), machine))
		}
		if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runSync(dir, []string{"import", batch}); err != nil {
			t.Fatal(err)
		}
	}
	importFrom("laptop.local", "a0", "a1", "a2")
	importFrom("desktop", "b0", "b1")

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	sync := doctorSection(out, "Sync:")
	if strings.Contains(sync, "no other machines yet") {
		t.Errorf("doctor reports no machines while holding work from two:\n%s", sync)
	}
	for _, want := range []string{"laptop.local", "desktop"} {
		if !strings.Contains(sync, want) {
			t.Errorf("doctor does not name %q as a machine work came from:\n%s", want, sync)
		}
	}

	// A machine with neither peers nor imports still says so.
	hermeticEnv(t)
	empty := index.DefaultDir()
	if err = index.Ensure(empty, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doctorSection(out, "Sync:"), "no other machines yet") {
		t.Errorf("a machine that has exchanged nothing lost its line:\n%s", doctorSection(out, "Sync:"))
	}
}

// doctorSection returns the lines of one section of the report.
func doctorSection(out, header string) string {
	i := strings.Index(out, header)
	if i < 0 {
		return "(no " + header + " section)"
	}
	rest := out[i:]
	if j := strings.Index(rest[len(header):], "\n\n"); j >= 0 {
		return rest[:len(header)+j]
	}
	return rest
}

// The printed doctor line has carried files and indexed sessions together
// since #861, because that pair is how a collapsed store shows. --json carried
// only the files, so a script could not make the same comparison (#1092).
func TestDoctorJSONCarriesIndexedSessions(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two files, one conversation: same sessionId, different filenames.
	const turn = `{"type":"user","sessionId":"dup1","timestamp":"2026-05-01T10:00:00Z","message":{"role":"user","content":"the pool cap decision"}}`
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "a.jsonl"), "dup1", []string{turn})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "b.jsonl"), "dup1", []string{turn})
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name            string `json:"name"`
			Files           int    `json:"files"`
			IndexedSessions int    `json:"indexed_sessions"`
		} `json:"stores"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	var found bool
	for _, s := range report.Stores {
		if s.Name != "claude" {
			continue
		}
		found = true
		if s.IndexedSessions == 0 {
			t.Errorf("claude store reports %d files and no indexed_sessions — the collapse is invisible to a reader of --json", s.Files)
		}
		if s.Files <= s.IndexedSessions {
			t.Errorf("fixture did not collapse: %d files, %d sessions", s.Files, s.IndexedSessions)
		}
	}
	if !found {
		t.Fatalf("no claude store in the report:\n%s", out)
	}
}

// doctor counted files and stopped there, so a store whose transcripts share
// ids read exactly like one where every file is its own session: two files,
// one session, no difference on screen (#861).
func TestDoctorRowCountsIndexedSessions(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, text string) string {
		return `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"` + text + `"}]}}` + "\n"
	}
	for _, f := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(chats, f), []byte(line("same-id", "pool exhausted")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))

	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, dir)
	row := harnessRow(t, buf.String(), "qwen")
	if !strings.Contains(row, "2 files") || !strings.Contains(row, "1 indexed session") {
		t.Errorf("row does not show both counts: %q", row)
	}

	// A harness whose store is not on this machine but whose sessions are in
	// the index — everything arrived by `sync import` — is named too, and said
	// to have come from elsewhere (#892).
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "gone"))
	buf.Reset()
	doctorHarnesses(&buf, dir)
	// The store is not where deja looks any more, but the session in the index
	// is still this machine's own: the count is named without claiming it came
	// from another machine (#892, narrowed by #894).
	if row := harnessRow(t, buf.String(), "qwen"); !strings.Contains(row, "1 indexed session") {
		t.Errorf("a store that is only in the index says nothing: %q", row)
	} else if strings.Contains(row, "from elsewhere") {
		t.Errorf("a local session was called imported: %q", row)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))

	// A store that has both: the two are counted apart, because "2 files, 4
	// indexed sessions" reads as a miscount (#894).
	exp := filepath.Join(tmp, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch strings.Builder
	for _, id := range []string{"r1", "r2"} {
		b, err := json.Marshal(index.SyncRecord{Harness: "qwen", SessionID: id, Project: "remote", Role: "user", Text: "a remote session about the seal"})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorHarnesses(&buf, dir)
	if row := harnessRow(t, buf.String(), "qwen"); !strings.Contains(row, "1 indexed session, 2 more from elsewhere") {
		t.Errorf("mixed store does not separate the two: %q", row)
	}

	// A harness with nothing in the index says nothing rather than "0": the
	// row is about what deja found, and a zero there reads as a failure.
	buf.Reset()
	doctorHarnesses(&buf, filepath.Join(tmp, "empty"))
	if row := harnessRow(t, buf.String(), "qwen"); strings.Contains(row, "indexed session") {
		t.Errorf("unbuilt index still claims sessions: %q", row)
	}
}

func harnessRow(t *testing.T, out, name string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
			return line
		}
	}
	t.Fatalf("no %s row in:\n%s", name, out)
	return ""
}

// `imported:x` has the shape of a rule deja consults, so #941's check passes it
// even when x is a machine name — and the part after the colon is the first
// path component of the project on the machine the session came from, never a
// machine name. Such a rule reads as in force forever (#955).
func TestDoctorNamesAnImportRuleThatMatchesNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "work/api", Role: "user", Text: "peer note"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported:laptop":false,"imported:work":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	var out bytes.Buffer
	doctorPolicy(&out, dir)
	got := out.String()
	if !strings.Contains(got, `"imported:laptop" matches nothing`) {
		t.Errorf("a rule that can never fire is reported as in force:\n%s", got)
	}
	// The one that does match is not called inert.
	if strings.Contains(got, `"imported:work" matches nothing`) {
		t.Errorf("a rule that hides a session was called inert:\n%s", got)
	}
}

// doctor's ingest line ends "see `deja doctor --json`", and the JSON carried
// the same two numbers the line had just printed: a person told a line was
// skipped could not learn which file it was in. The manifest has held that per
// file since #2015 — nothing exposed it (#2189).
func TestDoctorJSONNamesTheFilesItsCountsCameFrom(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lines := []string{
		`{"ts":"` + now + `","text":"the migration held the lock"}`,
		`not json at all`,
		`{"ts":"2026-01-03","text":"a date without a time"}`,
	}
	if err := os.WriteFile(notes, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A line it could not use: the index is written, and the run says so (#5396).
	if _, err := captureRunStderr(t, "index"); !isExitStatus(err, exitPartial) {
		t.Fatalf("got %v, want exit status %d", err, exitPartial)
	}

	raw, _ := captureRun(t, "doctor", "--json")
	var report struct {
		Ingest map[string]struct {
			MalformedLines int `json:"malformed_lines"`
		} `json:"ingest_health"`
		Files map[string]struct {
			Malformed int    `json:"malformed,omitempty"`
			Error     string `json:"error,omitempty"`
			Clipped   int    `json:"clipped,omitempty"`
		} `json:"ingest_files"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json does not parse: %v", err)
	}
	// The premise: this run refused lines, so a report naming no file is
	// hiding something rather than having nothing to say.
	if report.Ingest["deja"].MalformedLines != 2 {
		t.Fatalf("the run did not refuse the two lines, so this measures nothing:\n%s", raw)
	}
	if len(report.Files) == 0 {
		t.Fatalf("doctor --json names no file for the lines it says were skipped:\n%s", raw)
	}
	got, ok := report.Files[notes]
	if !ok {
		named := make([]string, 0, len(report.Files))
		for p := range report.Files {
			named = append(named, p)
		}
		t.Fatalf("the notes file is not among the files named (%v); a person fixing it is not told it is theirs", named)
	}
	if got.Malformed != 2 {
		t.Errorf("the notes file is reported with %d refused lines, want 2", got.Malformed)
	}
	// And here, where every file belongs to a harness deja knows, the per-file
	// counts add up to the rollup printed above them — or the reader has two
	// numbers and no way to tell which one is wrong.
	sum := 0
	for _, f := range report.Files {
		sum += f.Malformed
	}
	total := 0
	for _, h := range report.Ingest {
		total += h.MalformedLines
	}
	if sum != total {
		t.Errorf("the files account for %d refused lines and the rollup says %d", sum, total)
	}
}

type autoRecallRow struct {
	Name          string `json:"name"`
	State         string `json:"state"`
	Path          string `json:"path"`
	BinaryMissing bool   `json:"binary_missing"`
}

func autoRecallRows(t *testing.T) map[string]autoRecallRow {
	t.Helper()
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		AutoRecall []autoRecallRow `json:"auto_recall"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json: %v: %s", err, out)
	}
	if len(report.AutoRecall) == 0 {
		t.Fatalf("doctor --json carries no auto_recall section:\n%s", out)
	}
	rows := map[string]autoRecallRow{}
	for _, row := range report.AutoRecall {
		rows[row.Name] = row
	}
	return rows
}

// The text report has named the state of every auto-recall wiring since the
// rows existed; the JSON a script reads carried the MCP half and not this one,
// so a machine watching its own install could see a missing sqlite3 and not a
// hook running a binary that is gone — which is what an upgrade leaves behind,
// with every hook exiting 127 (#3502, #3510).
func TestDoctorJSONCarriesTheAutoRecallRows(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	// opencode's plugin is one of those rows and the simplest to write by
	// hand: a file that is there, and a file that calls deja.
	plugin := filepath.Join(home, ".config", "opencode", "plugins", "deja.js")
	if err := os.MkdirAll(filepath.Dir(plugin), 0o755); err != nil {
		t.Fatal(err)
	}

	// Nothing written yet.
	if got := autoRecallRows(t)["opencode"]; got.State != "missing" {
		t.Errorf("with no plugin file, opencode reads %q, want missing", got.State)
	}

	// A file with no deja call in it: the install looks done and nothing runs.
	if err := os.WriteFile(plugin, []byte("export const hooks = {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := autoRecallRows(t)["opencode"]; got.State != "stale" {
		t.Errorf("with a plugin that calls nothing, opencode reads %q, want stale", got.State)
	}

	// A plugin whose binary is gone: wired, and dead. The path has to be
	// absolute on the platform running the test — `/nowhere/deja` is not one on
	// windows, and the check that reads these files skips anything relative,
	// because a relative path resolves against wherever the reader is standing.
	gone := filepath.Join(home, "nowhere", "deja")
	dead := "const DEJA = \"" + filepath.ToSlash(gone) + "\";\nawait spawn(DEJA, [\"hook-context\"]);\n"
	if err := os.WriteFile(plugin, []byte(dead), 0o644); err != nil {
		t.Fatal(err)
	}
	got := autoRecallRows(t)["opencode"]
	if got.State != "wired" {
		t.Errorf("a plugin that calls deja reads %q, want wired", got.State)
	}
	if !got.BinaryMissing {
		t.Errorf("the row says nothing about a binary that is not there: %+v", got)
	}
	if got.Path != plugin {
		t.Errorf("row path = %q, want %q", got.Path, plugin)
	}

	// And one calling a binary that is there is not reported dead. A real file
	// named the way deja names itself, because the check only looks at paths
	// that end in its own name — the test binary does not.
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	live := "const DEJA = \"" + filepath.ToSlash(bin) + "\";\nawait spawn(DEJA, [\"hook-context\"]);\n"
	if err := os.WriteFile(plugin, []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := autoRecallRows(t)["opencode"]; got.BinaryMissing {
		t.Errorf("a plugin calling a binary that exists was reported dead: %+v", got)
	}
}

// The two rows this section did not have. doctor's text report has named the
// Claude Code and codex hook wirings since they existed, and the JSON a script
// reads carried neither, because both predate the autoWirings table. So a
// machine watching its own install saw nothing at all about the harness most
// people run — including the state an upgrade leaves, where the entries are
// wired, name a deja that is gone, and every hook exits 127 (#3502, #3510).
func TestDoctorJSONCarriesTheClaudeAndCodexHookRows(t *testing.T) {
	hermeticEnv(t)

	// Nothing installed: both named, and named as missing rather than left out
	// of the report, which reads the same as a harness deja cannot wire.
	rows := autoRecallRows(t)
	for _, name := range []string{"claude-code", "codex-hook"} {
		row, ok := rows[name]
		if !ok {
			t.Fatalf("doctor --json has no %s row", name)
		}
		if row.State != "missing" {
			t.Errorf("%s with nothing installed reads %q, want missing", name, row.State)
		}
		if row.Path == "" {
			t.Errorf("%s row names no file: %+v", name, row)
		}
	}

	all := make([]string, 0, len(claudeHookWiring))
	for _, h := range claudeHookWiring {
		all = append(all, h.Event)
	}

	// Everything the installer writes, naming a binary that is there.
	writeClaudeSettings(t, all...)
	got := autoRecallRows(t)["claude-code"]
	if got.State != "wired" {
		t.Errorf("a complete claude wiring reads %q, want wired", got.State)
	}
	if got.BinaryMissing {
		t.Errorf("a wiring naming the running binary was reported dead: %+v", got)
	}

	// The same wiring after the binary moved away.
	gone := filepath.Join(os.Getenv("HOME"), "gone", "deja")
	writeClaudeSettingsNaming(t, gone, all...)
	got = autoRecallRows(t)["claude-code"]
	if got.State != "wired" {
		t.Errorf("a wiring whose binary is gone reads %q, want wired", got.State)
	}
	if !got.BinaryMissing {
		t.Errorf("the row says nothing about a binary that is not there: %+v", got)
	}

	// And an older install that has some of the events: the JSON says which
	// state it is in, not just that a file is present.
	writeClaudeSettings(t, all[:1]...)
	if got := autoRecallRows(t)["claude-code"].State; got != "out of date" {
		t.Errorf("a one-of-%d wiring reads %q, want out of date", len(all), got)
	}
}

// collectDoctorKeys gathers every json field name doctorReport marshals,
// through structs, slices, pointers and maps. Map keys are dynamic data
// (harness names, activation names), so only the value type is walked.
func collectDoctorKeys(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name != "" {
			into[name] = true
		}
		collectDoctorKeys(f.Type, into)
	}
}

// docs/json-output.md is the published contract for `deja doctor --json`. This
// pins every key doctorReport can marshal to the doctor section, and every key
// of the section's examples to something doctor can marshal. It used to compare
// against a list kept in this file, which called seven keys documented that the
// section never named.
func TestDoctorJSONKeysMatchTheDocumentedContract(t *testing.T) {
	section := docSection(t, jsonOutputDoc(t), "## `deja doctor --json`")
	emitted := map[string]bool{}
	collectDoctorKeys(reflect.TypeFor[doctorReport](), emitted)
	for _, k := range sortedSet(emitted) {
		if !namesKey(section, k) {
			t.Errorf("doctor --json emits %q, and its section in docs/json-output.md never names it", k)
		}
	}
	for _, m := range jsonBlockRE.FindAllStringSubmatch(section, -1) {
		var v any
		if json.Unmarshal([]byte(m[1]), &v) != nil {
			continue
		}
		shown := map[string]bool{}
		// Keyed by harness, path and activation name in the examples.
		collectJSONKeys(v, []string{"ingest_health", "ingest_files", "activations", "commands"}, nil, shown)
		for _, k := range sortedSet(shown) {
			if !emitted[k] {
				t.Errorf("docs/json-output.md shows %q under doctor --json, which doctor no longer emits", k)
			}
		}
	}
}

// The human doctor names a damaged index (#735) and the JSON called the same
// store "ok", so a script watching it for index health was told the store was
// fine while it could not answer a query (#2292).
func TestDoctorJSONReportsADamagedIndex(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"gateway_timeout"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	state := func() string {
		t.Helper()
		out, err := captureRun(t, "doctor", "--json", "--offline")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Index struct {
				State string `json:"state"`
			} `json:"index"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("doctor --json is not JSON: %v", err)
		}
		return report.Index.State
	}

	// The premise: a healthy index reports ok.
	if got := state(); got != "ok" {
		t.Fatalf("a fresh index reports %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The premise for the second half: deja itself considers this damaged.
	if !index.Damaged(dir) {
		t.Fatal("the store is not damaged, so this measures nothing")
	}
	if got := state(); got == "ok" {
		t.Error("the JSON calls a damaged index ok")
	} else if !strings.Contains(got, "damaged") {
		t.Errorf("the JSON reports %q, which does not say the index is damaged", got)
	}
}

// The text screen has carried a `format` row since #877 and the JSON carried
// nothing, so a script watching index health read "ok" on a store whose recall
// was off — the same miss #2292 closed for damage (#3600).
func TestDoctorJSONSaysWhenTheStoreIsNotWhatThisBuildWrites(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "fmt", []string{
		`{"type":"user","sessionId":"fmt","timestamp":"2026-08-20T10:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the migration ran twice"}}`,
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// A store this build wrote says nothing about its format.
	got := inspectDoctorIndex(dir, nil)
	if got.Format != "" {
		t.Errorf("a current store reported a format: %q", got.Format)
	}
	if got.State != "ok" {
		t.Errorf("a current store is not ok: %q", got.State)
	}

	saved := indexReadState
	t.Cleanup(func() { indexReadState = saved })
	for _, c := range []struct {
		state        index.ReadState
		wantFormat   string
		wantState    string
		healthSignal string
	}{
		{index.ReadStateWithheld, "withheld", "rereading", "recall is off"},
		{index.ReadStateUnreadable, "unreadable", "rereading", "recall is off"},
		{index.ReadStateOlderRules, "older-rules", "ok", "still answering"},
		{index.ReadStateNewer, "newer", "ok", "still answering"},
	} {
		indexReadState = func(string) index.ReadState { return c.state }
		got := inspectDoctorIndex(dir, nil)
		if got.Format != c.wantFormat {
			t.Errorf("%s: format = %q, want %q", c.healthSignal, got.Format, c.wantFormat)
		}
		if got.State != c.wantState {
			t.Errorf("%s: state = %q, want %q", c.healthSignal, got.State, c.wantState)
		}
	}

	// Every value the field can take has to be named in the contract, or a
	// consumer branching on it is reading prose that does not cover it.
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"unreadable", "withheld", "older-rules", "newer", "rereading"} {
		if !strings.Contains(string(doc), "`"+value+"`") {
			t.Errorf("docs/json-output.md does not name the %q state", value)
		}
	}
}

// The human report names two tools deja needs and says what each one is for.
// The JSON carried one of them, so a machine checking this install could see
// that sqlite3 was missing and not that git was (#2411).
func TestDoctorJSONReportsBothTools(t *testing.T) {
	tmp := hermeticEnv(t)
	_ = tmp
	out, _ := captureBoth(t, "doctor", "--json")

	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json emitted something that is not JSON: %v\n%s", err, out)
	}
	for _, tool := range []string{"sqlite3", "git"} {
		component, ok := report[tool].(map[string]any)
		if !ok {
			t.Errorf("the report says nothing about %s: %v", tool, report[tool])
			continue
		}
		state, _ := component["state"].(string)
		switch state {
		case "ok", "missing":
		default:
			t.Errorf("%s.state = %q, want ok or missing", tool, state)
		}
	}
}

// The text report has said an MCP entry points at a deja binary that is gone
// since #2216; the JSON row for the same entry read plain "wired", so a script
// could tell a dead hook (binary_missing, #3502) from a live one but not a dead
// server (#4177).
func TestDoctorJSONMarksAnMCPEntryWhoseBinaryIsGone(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(bin string) {
		t.Helper()
		body := "[mcp_servers.deja]\ncommand = \"" + filepath.ToSlash(bin) + "\"\nargs = [\"mcp\"]\n"
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	row := func() doctorMCPStatus {
		t.Helper()
		out, err := captureRun(t, "doctor", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			MCP []doctorMCPStatus `json:"mcp"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("doctor --json: %v: %s", err, out)
		}
		for _, r := range report.MCP {
			if r.Name == "codex" {
				return r
			}
		}
		t.Fatalf("no codex row:\n%s", out)
		return doctorMCPStatus{}
	}

	write(filepath.Join(home, "nowhere", "deja"))
	if got := row(); got.State != "wired" || !got.BinaryMissing {
		t.Errorf("an entry naming a binary that is gone reads %+v, want wired and binary_missing", got)
	}

	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(bin)
	if got := row(); got.State != "wired" || got.BinaryMissing {
		t.Errorf("an entry naming a binary that is there reads %+v, want wired and not binary_missing", got)
	}
}

// The text row says which machine an ssh host turns out to be (#2415), and
// `sessions_from_there` is counted by that same name. The JSON carried the
// host alone, so a consumer could not join a peer to the work that came from
// it (#2423).
func TestDoctorJSONPeerCarriesTheMachineName(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	body := `{"peers":[{"host":"dev@192.0.2.7","machine":"quicksilver","last_pull":"2026-08-28T10:00:00Z"},` +
		`{"host":"mini","last_pull":"2026-08-28T09:00:00Z"}]}`
	if err := os.WriteFile(peersFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, _ := captureBoth(t, "doctor", "--json")
	var report struct {
		Sync struct {
			Peers []map[string]any `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	if len(report.Sync.Peers) != 2 {
		t.Fatalf("want two peer rows, got %d", len(report.Sync.Peers))
	}
	for _, p := range report.Sync.Peers {
		switch p["host"] {
		case "dev@192.0.2.7":
			if p["machine"] != "quicksilver" {
				t.Errorf("the row does not say which machine that host is: %v", p)
			}
		case "mini":
			// A machine that has never said what it calls itself adds nothing.
			if _, ok := p["machine"]; ok {
				t.Errorf("the row invented a name: %v", p)
			}
		}
	}
}

// docs/json-output.md is the contract for anyone parsing this. It names the
// three keys that may be absent — embed, ingest_health, deep — so every other
// documented field has to be there. stale_stores carried omitempty, so the
// zero the example shows was the one value never written, and a script reading
// it raised on every machine with a fresh index (#1710).
func TestDoctorJSONCarriesEveryDocumentedField(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"schema_version", "stores", "index", "mcp", "sqlite3", "version", "policy"} {
		if _, ok := report[key]; !ok {
			t.Errorf("%q is missing from the report", key)
		}
	}
	idx, _ := report["index"].(map[string]any)
	if _, ok := idx["stale_stores"]; !ok {
		t.Errorf("index.stale_stores is documented and absent: %v", idx)
	}
}

// The policy path in the docs has to be the file deja actually reads.
func TestDocsNameThePolicyFileDejaWrites(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "json-output.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "policy.toml") {
		t.Error("docs/json-output.md names policy.toml; deja reads and writes policy.json")
	}
}

// opencode takes comments in opencode.json, and the dead-binary check read
// the file as strict JSON: on a commented config the entry's command — a list
// in opencode's shape — was never found, so a server pointing at a deleted
// binary read `wired` (#4197).
func TestDoctorFindsTheDejaCommandInACommentedConfig(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "moved", "deja")
	p := filepath.Join(dir, "opencode.json")
	body := `{
  "$schema": "https://opencode.ai/config.json",
  // the reader's own note
  "model": "shim/luna",
  "mcp": {
    "deja": {"type":"local","command":[` + jsonString(gone) + `,"mcp"]},
  },
}
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandMissing(p); got != gone {
		t.Fatalf("dejaCommandMissing = %q, want %q", got, gone)
	}
	if !doctorJSONWiredIn(doctorOpencodeServers)(p) {
		t.Fatal("a commented config with the entry read as not wired")
	}
}

// PowerShell 5.1 writes a byte-order mark; install reads past it (#3696), so
// doctor has to as well or the same dead entry reads `wired`.
func TestDoctorReadsPastAByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "moved", "deja")
	p := filepath.Join(dir, "opencode.json")
	body := "{\n  // note\n  \"mcp\": {\"deja\": {\"type\":\"local\",\"command\":[" + jsonString(gone) + ",\"mcp\"]}}\n}\n"
	if err := os.WriteFile(p, append(append([]byte(nil), utf8BOM...), body...), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandMissing(p); got != gone {
		t.Fatalf("dejaCommandMissing = %q, want %q", got, gone)
	}
}

// Amp keeps its servers under the flat "amp.mcpServers" key; once a commented
// file parsed, the command under it was no longer found by the line scan.
func TestDoctorFindsTheDejaCommandUnderAmpsKey(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "moved", "deja")
	p := filepath.Join(dir, "settings.json")
	body := "{\n  // note\n  \"amp.mcpServers\": {\"memory\": {\"command\": " + jsonString(gone) + "}}\n}\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandMissing(p); got != gone {
		t.Fatalf("dejaCommandMissing = %q, want %q", got, gone)
	}
}

// Kimi keeps state.json beside each session's agents/main/wire.jsonl and the
// reader opens it for the title and the working directory; doctor counted one
// per session as a file it could not read (#3309).
func TestDoctorDoesNotCallKimisStateUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "kimi-code")
	dir := filepath.Join(root, "sessions", "wd_proj_abc", "session_1")
	if err := os.MkdirAll(filepath.Join(dir, "agents", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "main", "wire.jsonl"),
		[]byte(`{"role":"user","content":"why does the retry loop drop the last attempt","origin":{"kind":"user"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"title":"retry loop","workDir":"/w/api"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", root)
	t.Setenv("DEJA_KIMI_ROOT", root)
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(line, "kimi") && strings.Contains(line, "not recognised") {
			t.Errorf("the session's own state file is counted as a transcript deja failed to read: %q", line)
		}
	}
}

// The table is headed "Hooks" and every row read "wired", including two that
// are not hooks. aider's file is a context file that only `deja aider`
// refreshes — bare aider reads whatever was written last. roo's is guidance:
// the agent is asked to call recall, and hands itself nothing if it does not.
//
// Someone reading a row that says wired concludes memory arrives on its own.
// For those two it does not, and the whole point of this command is to be the
// thing a person trusts when memory is not working.
func TestTheHooksTableSaysWhichRowsAreNotHooks(t *testing.T) {
	var out bytes.Buffer
	doctorAutoRecall(&out)
	got := out.String()

	for _, want := range []struct{ name, says string }{
		{"aider", "context file"},
		{"roo", "guidance"},
	} {
		line := ""
		for l := range strings.SplitSeq(got, "\n") {
			if strings.Contains(l, want.name+" ") {
				line = l
			}
		}
		if line == "" {
			t.Errorf("%s has no row at all", want.name)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("%s reads as a hook like any other:\n  %s", want.name, line)
		}
	}

	// And a real hook carries no such note, or the distinction says nothing.
	for l := range strings.SplitSeq(got, "\n") {
		if strings.Contains(l, "opencode ") &&
			(strings.Contains(l, "context file") || strings.Contains(l, "guidance")) {
			t.Errorf("a real hook was annotated as something else:\n  %s", l)
		}
	}
}

// The entry names the launcher now, and someone can delete it — it sits in a
// config directory people tidy. The check that reports a hook running a binary
// that is gone read the file's name, so it stopped covering the file every
// hook runs on the day the launcher landed (#3422).
func TestDoctorNamesALauncherThatIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	hermeticEnv(t)
	launcher := dejaLauncherPath()

	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": launcher + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	if got := out.String(); !strings.Contains(got, "runs "+reportPath(launcher)+", which is not there") {
		t.Errorf("doctor said nothing about the missing launcher:\n%s", got)
	}

	// And once it is back, nothing extra.
	if _, err := writeDejaLauncher("/usr/local/bin/deja"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("doctor reported a launcher that is there:\n%s", got)
	}
}

// With the launcher gone, a hook row said so twice: once as the binary the hook
// runs and once as the launcher, the same file both times (#4245).
func TestDoctorNamesAMissingLauncherOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	for _, target := range []string{"hermes-auto", "claude-auto"} {
		t.Run(target, func(t *testing.T) {
			hermeticEnv(t)
			bin := filepath.Join(os.Getenv("HOME"), "bin", "deja")
			if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(target, bin, false); err != nil {
				t.Fatal(err)
			}
			launcher := dejaLauncherPath()
			if err := os.Remove(launcher); err != nil {
				t.Fatal(err)
			}
			out, err := captureRun(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			notes := 0
			for line := range strings.SplitSeq(out, "\n") {
				if strings.Contains(line, filepath.Base(launcher)+", which is not there") {
					notes++
				}
			}
			// Hermes's MCP entry names the launcher too since #5269, and its
			// own row says so once.
			want := 1
			if target == "hermes-auto" {
				want = 2
			}
			if notes != want {
				t.Errorf("the missing launcher is named %d times, want %d:\n%s", notes, want, out)
			}
		})
	}
}

// The check beside this one asks whether the file a hook names is on disk.
// Since the launcher landed that file is always there, so the question moved:
// what can be gone is everything the launcher resolves to. A machine in that
// state has hooks that start, find nothing and exit 0 — silence that reads as
// having no history (#3422).
func TestDoctorSaysWhenTheLauncherFindsNoDeja(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	// Nothing on the PATH and no well-known copy, so the launcher has only
	// what the record names.
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))
	t.Setenv("DEJA_BIN", "")
	saved := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = saved })
	launcherWellKnown = nil

	gone := filepath.Join(tmp, "old", "deja")
	writeWiringFixture(t, wiringState{Version: "dev", Targets: []string{"claude-code"}, Exe: gone, Home: homeDir()})
	if _, err := writeDejaLauncher(gone); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": dejaLauncherPath() + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	if got := out.String(); !strings.Contains(got, "finds no deja to run") {
		t.Errorf("doctor said nothing about a launcher that resolves to nothing:\n%s", got)
	}

	// Put a binary where the record says, and the row goes quiet again.
	if err := os.MkdirAll(filepath.Dir(gone), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gone, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "finds no deja to run") {
		t.Errorf("doctor complained about a launcher that resolves:\n%s", got)
	}
}

func TestDoctorReportsABinaryThatFailsToStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	savedWK := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = savedWK })
	launcherWellKnown = nil
	t.Setenv("DEJA_BIN", "")
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))

	home := os.Getenv("HOME")
	broken := filepath.Join(home, ".local", "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	launcher := dejaLauncherPath()
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte(dejaLauncherScript(broken)), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(tmp, "settings.json")
	if err := os.WriteFile(cfg, []byte(launcher), 0o644); err != nil {
		t.Fatal(err)
	}

	healthy := filepath.Join(home, "go", "bin", "deja")
	origRunErr := dejaBinaryRunErr
	t.Cleanup(func() { dejaBinaryRunErr = origRunErr })
	dejaBinaryRunErr = func(p string) string {
		if p == broken {
			return "signal: killed"
		}
		return ""
	}

	if got := doctorLauncherNote(cfg, "claude"); !strings.Contains(got, "fails to start") {
		t.Errorf("all-broken note = %q, want substring %q", got, "fails to start")
	}

	// With a healthy fallback and the resilient launcher, doctor stays quiet.
	if err := os.MkdirAll(filepath.Dir(healthy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(healthy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := doctorLauncherNote(cfg, "claude"); got != "" {
		t.Errorf("resilient launcher with healthy fallback note = %q, want empty", got)
	}
	if resolved, ok := launcherResolves(); !ok || resolved != healthy {
		t.Errorf("launcherResolves() = (%q, %v), want (%q, true)", resolved, ok, healthy)
	}

	// Legacy exec launcher picks broken first candidate even when fallback exists.
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec \"$c\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := doctorLauncherNote(cfg, "claude"); !strings.Contains(got, "fails to start") {
		t.Errorf("legacy launcher + broken primary note = %q, want substring %q", got, "fails to start")
	}
}

func TestDejaBinaryRunErrNeverRunsAStranger(t *testing.T) {
	tmp := t.TempDir()
	stranger := filepath.Join(tmp, "deja")
	sentinel := filepath.Join(tmp, "ran")
	if err := os.WriteFile(stranger, []byte("#!/bin/sh\ntouch "+shellQuote(sentinel)+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := probeDejaBinaryRunErr(stranger); got != "" {
		t.Fatalf("probeDejaBinaryRunErr(stranger) = %q, want empty (strangers are never executed)", got)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("probeDejaBinaryRunErr executed a non-deja script")
	}
}

// Every target that writes an MCP server has a row in the report. Nine did
// not: deepseek's entry on the author's machine pointed at a throwaway build
// for weeks with every other row repaired, because no row asked (#3661).
func TestEveryMCPTargetHasADoctorRow(t *testing.T) {
	hermeticEnv(t)
	rows := map[string]bool{}
	for _, c := range doctorMCPConfigs() {
		rows[c.name] = true
		if c.path == "" {
			t.Errorf("%s: row has no path — a report that names no file cannot be checked", c.name)
		}
	}
	// claude-code is the row's name for the claude target, and vscode and
	// copilot are rows for harnesses whose target is spelled differently.
	rows["claude"] = rows["claude-code"]
	for _, target := range installTargetNames() {
		if strings.HasSuffix(target, "-auto") {
			continue
		}
		switch target {
		case "statusline", "sync-timer", "aider":
			// No MCP client: aider's target writes a context file, the other
			// two are not harnesses.
			continue
		}
		if !writesMCPServer(t, target) {
			continue
		}
		if !rows[target] {
			t.Errorf("`deja install %s` writes an MCP server and doctor has no row for it", target)
		}
	}
}

// writesMCPServer runs the target against a temp HOME and reports whether it
// wrote a file naming deja's server. Read off the install rather than a list
// beside it: a list would only ever agree with itself.
func writesMCPServer(t *testing.T, target string) bool {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installTarget(target, filepath.Join(home, "bin", "deja"), false); err != nil {
		return false
	}
	found := false
	_ = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		b, err := os.ReadFile(path) //nolint:gosec // the test walks its own temp tree
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		text := string(b)
		if strings.Contains(text, `"deja"`) && strings.Contains(text, "mcp") ||
			strings.Contains(text, "serverName: deja") {
			found = true
		}
		return nil
	})
	return found
}

// dsh has no server key at all — the name is a field in a patch-list row — so
// the reader that names the wired binary stopped one line short of it and a
// stale build there was reported as healthy.
func TestTheCommandIsReadableInADSHPatchLayer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cordis.patch.yml")
	layer := "- insert:\n" +
		"    - id: mcp-deja\n" +
		"      name: '@deepseek-ai/dsh-mcp-client'\n" +
		"      config:\n" +
		"        serverName: deja\n" +
		"        transport: stdio\n" +
		"        command: \"/scratch/deja-probe\"\n" +
		"        args: ['mcp']\n"
	if err := os.WriteFile(path, []byte(layer), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != "/scratch/deja-probe" {
		t.Errorf("dejaCommandIn = %q, want the command beside serverName", got)
	}
	if !doctorDSHWired(path) {
		t.Error("a layer holding deja's entry did not read as wired")
	}
	// Someone else's entry keeps its own command out of it.
	other := filepath.Join(dir, "other.yml")
	if err := os.WriteFile(other, []byte("- insert:\n    - id: mcp-x\n      config:\n        serverName: x\n        command: \"/scratch/x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(other); got != "" {
		t.Errorf("another server's command was read as deja's: %q", got)
	}
	if doctorDSHWired(other) {
		t.Error("a layer without deja's entry read as wired")
	}
}

// goose keeps a `slash_commands` list at the bottom of the same config it keeps
// its MCP extensions in, and one of those commands is called `deja`. The
// whole-file scan answered with that name — a bare word, so neither binary
// check had anything to look at — and the extension three lines from the top,
// pointing at a build in a scratch directory, was never read (#3662).
func TestASlashCommandDoesNotMaskTheWiredBinary(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-shapes")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	body := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    name: deja\n" +
		"    cmd: \"" + stray + "\"\n    args:\n      - \"mcp\"\n    timeout: 60\n" +
		"  developer:\n    enabled: true\n    type: builtin\n" +
		"slash_commands:\n  - command: \"deja\"\n    prompt: \"search past sessions\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != stray {
		t.Errorf("dejaCommandIn = %q, want the extension's binary %q", got, stray)
	}
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })
	if note := otherBinaryNote(path, "goose"); !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name the build in the scratch directory", note)
	}
}

// OpenClaw and ZCode keep their servers one level deeper, under `mcp.servers`.
// That map was read as if it were a single entry, so nothing here could name
// the binary either of them runs and both rows reported `wired` about builds in
// a scratch directory (#3663).
func TestTheCommandIsReadableUnderNestedMCPServers(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-prog2")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "openclaw.json")
	body, err := json.Marshal(map[string]any{
		"mcp": map[string]any{"servers": map[string]any{
			"deja": map[string]any{"command": stray, "args": []string{"mcp"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != stray {
		t.Errorf("dejaCommandIn = %q, want the nested entry's command %q", got, stray)
	}
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })
	if note := otherBinaryNote(path, "openclaw"); !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name the build in the scratch directory", note)
	}
	// And a binary that is gone is the other half of the same read.
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandMissing(path); got != stray {
		t.Errorf("dejaCommandMissing = %q, want %q", got, stray)
	}
}

// A TOML table keeps every key at the header's own indent, so the scan that
// looked for the command *under* the anchor stopped at the first sibling that
// was not one — `type = "stdio"`, the line codex installs before #5590 wrote
// right after the header.
// A codex config whose command is not the first key read as naming no binary at
// all, and the row said `wired` about a build in a scratch directory (#3668).
func TestTheCommandIsReadableAnywhereInATOMLTable(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-prog")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	body := "model = \"gpt-5\"\n\n[mcp_servers.deja]\ntype = \"stdio\"\ncommand = " +
		strconv.Quote(stray) + "\nargs = [\"mcp\"]\nstartup_timeout_sec = 30\n\n" +
		"[mcp_servers.other]\ncommand = \"/usr/bin/other\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != stray {
		t.Errorf("dejaCommandIn = %q, want deja's own command %q", got, stray)
	}
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })
	if note := otherBinaryNote(path, "codex"); !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name the build in the scratch directory", note)
	}
	// And the table that ends before a command of its own is not answered for
	// by the table after it.
	empty := filepath.Join(dir, "empty.toml")
	emptyBody := "[mcp_servers.deja]\ntype = \"stdio\"\n\n[mcp_servers.other]\ncommand = \"/usr/bin/other\"\n"
	if err := os.WriteFile(empty, []byte(emptyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(empty); got != "" {
		t.Errorf("another table's command was read as deja's: %q", got)
	}
}

// The guidance column said "unsupported" about files deja had written: four
// harnesses' manuals come from their own install target rather than from the
// generic guidance step.
func TestGuidanceStatusSeesAHarnessOwnSkillFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, h := range []string{"kilocode", "gjc", "commandcode", "kiro"} {
		if got := guidanceStatus(h); got != "missing" {
			t.Errorf("%s: nothing installed, status = %q, want missing", h, got)
		}
	}
	if _, err := installTarget("kilocode", filepath.Join(home, "bin", "deja"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := installSkillFile(kilocodeSkillPath(), false); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("kilocode"); got != "written" {
		t.Errorf("with the skill on disk, status = %q, want written", got)
	}
}

// Zed's server key is the id its extension owns — `deja-context-server`, not
// `deja` — and its settings carry comments, so the file is read as text. With a
// build under another name in that entry, neither the name test nor the `deja`
// key matched and the row said `wired` about a server pointing into a scratch
// directory (#3683).
func TestTheCommandIsReadableUnderDejasOtherKeys(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-arm")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	// JSONC, the shape Zed writes: a comment at the top is what keeps this out
	// of the JSON branch.
	jsonc := filepath.Join(dir, "settings.json")
	body := "// Zed settings\n{\n  \"context_servers\": {\n    \"deja-context-server\": {\n" +
		"      \"args\": [\n        \"mcp\"\n      ],\n      \"command\": " +
		strconv.Quote(stray) + "\n    }\n  }\n}\n"
	if err := os.WriteFile(jsonc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(jsonc); got != stray {
		t.Errorf("jsonc: dejaCommandIn = %q, want %q", got, stray)
	}

	// And the same key in a file that does parse as JSON.
	plain := filepath.Join(dir, "plain.json")
	b, err := json.Marshal(map[string]any{"context_servers": map[string]any{
		"deja-context-server": map[string]any{"command": stray, "args": []string{"mcp"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(plain, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(plain); got != stray {
		t.Errorf("json: dejaCommandIn = %q, want %q", got, stray)
	}

	// Somebody else's server keeps its own command out of it, whichever shape.
	other := filepath.Join(dir, "other.json")
	ob, err := json.Marshal(map[string]any{"context_servers": map[string]any{
		"memory": map[string]any{"command": "/usr/local/bin/memory"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, ob, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(other); got != "" {
		t.Errorf("another server's command was read as deja's: %q", got)
	}
}

// Reported from a real setup: the server registered by hand as "deja-vu" for
// three releases, answering calls daily, and doctor called it not wired. The
// first thing someone runs when memory goes quiet must not send them looking
// in the wrong place.
func TestDoctorFindsTheServerUnderAnyName(t *testing.T) {
	dir := t.TempDir()
	probe := doctorJSONWired("mcpServers")
	cases := map[string]string{
		"named deja-vu":    `{"mcpServers":{"deja-vu":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`,
		"named memory":     `{"mcpServers":{"memory":{"command":"deja","args":["mcp"]}}}`,
		"windows cmd shim": `{"mcpServers":{"deja-vu":{"command":"cmd","args":["/c","deja","mcp"]}}}`,
		"windows exe":      `{"mcpServers":{"dj":{"command":"C:\\tools\\deja.exe","args":["mcp"]}}}`,
		"nested transport": `{"mcpServers":{"whatever":{"transport":{"type":"stdio","command":"/opt/deja","args":["mcp"]}}}}`,
		"canonical name":   `{"mcpServers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`,
	}
	for name, body := range cases {
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if !probe(p) {
			t.Fatalf("%s: reported not wired", name)
		}
	}
	// A config with someone else's server must still read as not wired, or
	// the check tells everyone they are fine.
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"mcpServers":{"memory":{"command":"npx","args":["-y","@modelcontextprotocol/server-memory"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if probe(other) {
		t.Fatal("an unrelated MCP server was reported as deja")
	}
}

func TestDoctorTOMLFindsTheServerUnderAnyName(t *testing.T) {
	dir := t.TempDir()
	renamed := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(renamed, []byte("[mcp_servers.deja-vu]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorTOMLWired(renamed) {
		t.Fatal("renamed toml server reported not wired")
	}
	foreign := filepath.Join(dir, "foreign.toml")
	if err := os.WriteFile(foreign, []byte("[mcp_servers.memory]\ncommand = \"npx\"\nargs = [\"-y\",\"server-memory\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if doctorTOMLWired(foreign) {
		t.Fatal("an unrelated toml server was reported as deja")
	}
}

// The row says when a store holds a transcript the index has never read.
// Audited on a real store: ten of them, five written eight weeks earlier, and
// nothing anywhere said so — a session count of zero reads as "nothing written
// yet" rather than "five files never opened" (#3747).
func TestDoctorSaysWhenATranscriptWasNeverRead(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	write := func(name, sid, text string) {
		t.Helper()
		p := filepath.Join(claude, "-p-app", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-09-01T10:00:00Z","cwd":"/p/app","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("one.jsonl", "aaa", "the pool ran dry under load")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, dir)
		for l := range strings.SplitSeq(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "claude ") {
				return l
			}
		}
		t.Fatal("no claude row in the report")
		return ""
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q, want no such note while everything is read", got)
	}

	// A second transcript arrives and nothing has indexed it.
	write("two.jsonl", "bbb", "the cache eviction was wrong")
	got := row()
	if !strings.Contains(got, "1 transcript never read") {
		t.Fatalf("row = %q, want it to name the transcript never read", got)
	}
	// And what to do about it, since the reader cannot know that a search or a
	// recall is what adopts it.
	if !strings.Contains(got, "deja index") {
		t.Fatalf("row = %q, want the command that reads it", got)
	}

	// After a pass it goes quiet: a prompt, not a permanent complaint.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q after an index pass, want the note gone", got)
	}
}

// The OpenClaw row printed a count and nothing else, so a transcript deja
// could not read was invisible there while every other file-listing harness
// named it (#3317). The store keeps files beside the transcripts that the
// reader skips on purpose — the per-session trajectory-path.json, the
// sessions.json list, checkpoint snapshots and the archive of a reset whose
// live file is still there — and none of those is a failure either.
func TestDoctorNamesAnUnreadOpenClawTranscriptButNotItsSidecars(t *testing.T) {
	tmp := hermeticEnv(t)
	agents := filepath.Join(tmp, "openclaw", "agents")
	sessions := filepath.Join(agents, "main", "sessions")
	if err := os.MkdirAll(filepath.Join(sessions, "2026-09"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", agents)

	sid := "4a1b2c3d-0000-4000-8000-000000000001"
	line := `{"type":"message","role":"user","content":"why does the retry loop drop the last attempt","timestamp":"2026-09-01T10:00:00.000Z"}` + "\n"
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(sid+".jsonl", line)
	write(sid+".trajectory-path.json", `{"path":"/w/api"}`)
	write("sessions.json", `[{"id":"`+sid+`"}]`)
	write(sid+".checkpoint.4a1b2c3d-0000-4000-8000-000000000002.jsonl", line)
	write(sid+".jsonl.reset.1756713600", line)
	// A transcript one directory deeper than the reader looks: the file deja
	// holds nothing from, and the only thing the row has to say.
	if err := os.WriteFile(filepath.Join(sessions, "2026-09", "moved.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	var row string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "openclaw") && strings.Contains(l, "file") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("no openclaw file row in the report at all")
	}
	if !strings.Contains(row, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the one transcript deja did not read", row)
	}
	if strings.Contains(row, "2 not recognised") || strings.Contains(row, "3 not recognised") ||
		strings.Contains(row, "4 not recognised") || strings.Contains(row, "5 not recognised") {
		t.Errorf("row = %q, the files the reader skips on purpose are counted as failures", row)
	}
}

// openclaw-auto writes a hook pack that fires only in gateway mode and a plugin
// that carries the digest and per-prompt recall everywhere else. The row read
// the hook pack alone, so with the plugin gone a local session got no recall
// and doctor still said wired (#4580).
func TestDoctorOpenClawRowSeesTheMissingPlugin(t *testing.T) {
	hermeticEnv(t)
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	row := func() (string, string) {
		var out bytes.Buffer
		doctorAutoRecall(&out)
		for l := range strings.SplitSeq(out.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "openclaw ") {
				state, _ := autoWiringState(openclawAutoRow(t))
				return l, state
			}
		}
		t.Fatalf("no openclaw row:\n%s", out.String())
		return "", ""
	}
	// The control: both halves there.
	if line, state := row(); state != "wired" || !strings.Contains(line, "wired") {
		t.Fatalf("a full install does not read wired, so this measures nothing: %q (%s)", line, state)
	}
	if err := os.RemoveAll(filepath.Join(sources.OpenClawStateDir(), "extensions", openclawPluginID)); err != nil {
		t.Fatal(err)
	}
	line, state := row()
	if state == "wired" || strings.Contains(line, " wired ") {
		t.Errorf("the plugin is gone and the row still says wired: %q (json %s)", line, state)
	}
	if !strings.Contains(line, "openclaw-auto") {
		t.Errorf("the row does not say how to put the plugin back: %q", line)
	}
}

func openclawAutoRow(t *testing.T) autoWiring {
	t.Helper()
	for _, a := range autoWirings() {
		if a.name == "openclaw" {
			return a
		}
	}
	t.Fatal("no openclaw row in autoWirings")
	return autoWiring{}
}

// A wiring that names a deja which exists and is neither this one nor the one
// on PATH is a landmine: it works until that file goes, and the row said
// `wired` either way (#3656). And the check has to stay quiet about the
// ordinary case, or every row of a dev build's report carries a complaint.
func TestOtherBinaryNoteNamesOnlyAStrangePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	// Everything a test writes is in a temp directory, so the judgement is
	// driven here rather than inherited: one path counts as disposable, the
	// rest do not.
	stray := filepath.Join(dir, "scratch", "deja")
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })

	// The deja a user would recognise: the one on PATH.
	onPath := filepath.Join(dir, "deja")
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wiring := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(wiring, []byte(`{"command":"`+onPath+` hook-context"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if note := otherBinaryNote(wiring, "codex-auto"); note != "" {
		t.Errorf("the deja on PATH was reported as a stranger: %s", note)
	}

	// A build left in a scratch directory is the case worth naming.
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiring, []byte(`{"command":"`+stray+` hook-context"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	note := otherBinaryNote(wiring, "codex-auto")
	if note == "" {
		t.Fatalf("a wiring pointing at %s was not reported", stray)
	}
	if !strings.Contains(note, stray) {
		t.Errorf("the note does not name the path: %s", note)
	}
	if !strings.Contains(note, "deja install codex-auto") {
		t.Errorf("the note does not say how to fix it: %s", note)
	}
}

// Opening a harness and closing it without typing leaves a file of setup
// records and no conversation. Reporting that as a parse failure sends people
// looking for a bug in deja — it happened on my own machine after taking a
// screenshot of a TUI.
func TestParsedZeroIgnoresSessionsWithNoConversation(t *testing.T) {
	dir := t.TempDir()
	setupOnly := filepath.Join(dir, "wire.jsonl")
	body := `{"type":"metadata","protocol_version":"1.4"}
{"type":"config.update","profileName":"agent","systemPrompt":"You are a coding agent"}
{"type":"tools.set_active_tools","tools":["read","write"]}
`
	if err := os.WriteFile(setupOnly, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if fileHasConversation(setupOnly) {
		t.Fatal("a file of setup records was read as a conversation")
	}

	// A file that does hold turns must still be flagged when it parses to
	// nothing: that is the case the warning exists for.
	withTurns := filepath.Join(dir, "real.jsonl")
	if err := os.WriteFile(withTurns, []byte(body+`{"type":"user","message":{"role":"user","content":"hello"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileHasConversation(withTurns) {
		t.Fatal("a file with a user turn was read as empty")
	}

	// aider's markdown store marks turns with #### rather than JSON roles.
	md := filepath.Join(dir, "history.md")
	if err := os.WriteFile(md, []byte("# aider chat started at 2026-01-01\n\n#### fix the parser\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileHasConversation(md) {
		t.Fatal("aider's turn marker was not recognised")
	}
	// An unreadable file is a real problem and must not be silenced.
	if !fileHasConversation(filepath.Join(dir, "absent.jsonl")) {
		t.Fatal("a missing file was treated as merely empty")
	}
}

// A store deja cannot read is the loudest thing doctor can learn, and the
// parse error used to be discarded: a harness that changed its schema showed
// up as a healthy store while its recall was empty — indistinguishable from
// having no history with that agent.
func TestDoctorReportsAnUnreadableStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.db")
	if err := os.WriteFile(path, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	failing := doctorStoreCheck{
		// A file-backed harness: for the five deja reads through the sqlite3
		// CLI a parse failure on a machine without it means the tool, not the
		// format (#792), and the subject here is the format case.
		name:  "cline",
		paths: []string{dir},
		files: []string{path},
		parse: func(string) ([]model.Session, error) { return nil, errUnreadableStore },
	}
	got, _ := inspectDoctorStore(failing)
	if got.State != "unreadable" {
		t.Fatalf("state = %q, want unreadable", got.State)
	}

	// A store that reads fine stays quiet, or the warning becomes wallpaper.
	working := failing
	working.parse = func(string) ([]model.Session, error) {
		return []model.Session{{ID: "s1", Messages: []model.Message{{Role: "user", Text: "hi"}}}}, nil
	}
	if got, _ := inspectDoctorStore(working); got.State != "ok" {
		t.Fatalf("a readable store reported %q", got.State)
	}
}

var errUnreadableStore = storeError("query failed, the store schema may have changed")

type storeError string

func (e storeError) Error() string { return string(e) }

// The count beside a machine is looked up by name, and the two names come from
// different places: the peers row is the ssh alias someone typed, while the
// stamp on an imported session is what the sending machine calls itself. macOS
// hands back a capitalised hostname, so `ssh laptop` against a host named
// `Laptop` reported none of the sessions it had sent (#1876).
func TestTheSessionCountFindsTheMachineInEitherCase(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"laptop","last_pull":"2026-08-20T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, 3)
	for i := range 3 {
		rec := map[string]any{
			"harness": "claude", "session_id": "remote" + string(rune('a'+i)), "project": "proj",
			"role": "user", "text": "retry loop", "time": "2026-08-20T10:00:0" + string(rune('0'+i)) + "Z",
			"origin": "Laptop",
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if n, err := index.Import(dir, batch); err != nil || n != 3 {
		t.Fatalf("import: %d records, %v", n, err)
	}

	var text strings.Builder
	doctorPeers(&text, dir, time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	if !strings.Contains(text.String(), "3 sessions from there") {
		t.Errorf("the row does not count the sessions that machine sent:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host     string `json:"host"`
				Sessions int    `json:"sessions_from_there"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 || report.Sync.Peers[0].Sessions != 3 {
		t.Errorf("--json counts %#v, want three sessions from laptop", report.Sync.Peers)
	}
}

// A peer has two names: the ssh host it was added under, and the name the
// machine calls itself. Recall lines and `deja last` print the second; doctor
// printed only the first, so the row about "quicksilver" was headed
// "dev@192.0.2.7" and nothing on any surface joined them (#2415).
func TestThePeerRowNamesTheMachineToo(t *testing.T) {
	now := time.Now()
	p := peers.Peer{
		Host:     "dev@192.0.2.7",
		Machine:  "quicksilver",
		LastPull: now.Add(-2 * time.Hour),
	}
	line := peerLine(p, 3, now)
	if !strings.Contains(line, "quicksilver") {
		t.Errorf("the row about quicksilver's work does not name quicksilver: %q", line)
	}

	// A machine that calls itself what it was added as says it once.
	same := peers.Peer{Host: "mini", Machine: "mini", LastPull: now.Add(-time.Hour)}
	if got := peerLine(same, 1, now); strings.Count(got, "mini") != 0 {
		t.Errorf("the row repeats a name the header already carries: %q", got)
	}

	// The user part of an ssh target is not part of the name.
	prefixed := peers.Peer{Host: "dev@mini", Machine: "mini", LastPull: now.Add(-time.Hour)}
	if got := peerLine(prefixed, 1, now); strings.Contains(got, "calls itself") {
		t.Errorf("the row says a name twice: %q", got)
	}

	// Nothing to add when the machine has never said what it is called.
	unknown := peers.Peer{Host: "build-box", LastPull: now.Add(-time.Hour)}
	if got := peerLine(unknown, 0, now); strings.Contains(got, "(") {
		t.Errorf("the row invented a name: %q", got)
	}
}

// A stamp in the future made the age negative, and everything under a minute
// reads as "just now" — so a peer seventy years out looked like the healthiest
// machine on the screen whose whole purpose is to show a sync that stopped
// (#1855). deja says this about sessions already (index.StampedAhead, #1753).
func TestAPeerStampedAheadIsNotReportedAsJustSynced(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	ahead := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"peers":[{"host":"laptop","last_push":"` + ahead + `"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if strings.Contains(text.String(), "just now") {
		t.Errorf("a stamp in the future reads as a sync that just happened:\n%s", text.String())
	}
	if !strings.Contains(text.String(), "clock") {
		t.Errorf("the line does not say why the date cannot be trusted:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host    string `json:"host"`
				Ahead   bool   `json:"stamped_ahead"`
				LastPsh string `json:"last_push"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 {
		t.Fatalf("want one peer, got %#v", report.Sync.Peers)
	}
	if !report.Sync.Peers[0].Ahead {
		t.Errorf("--json does not say the stamp is ahead of this machine's clock: %#v", report.Sync.Peers[0])
	}
	// The stamp itself is still reported as written — the reader may need it.
	if report.Sync.Peers[0].LastPsh == "" {
		t.Errorf("the stamp was dropped rather than flagged: %#v", report.Sync.Peers[0])
	}
}

// The control: an ordinary peer says neither, on either surface.
func TestAnOrdinaryPeerIsNotFlaggedAsAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	when := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop","last_push":"`+when+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if strings.Contains(text.String(), "clock") {
		t.Errorf("an ordinary peer was flagged:\n%s", text.String())
	}
	if !strings.Contains(text.String(), "hours ago") {
		t.Errorf("an ordinary peer lost its age:\n%s", text.String())
	}
	if got := collectDoctorSync(t.TempDir()); len(got.Peers) != 1 || got.Peers[0].Ahead {
		t.Errorf("an ordinary peer is flagged in --json: %#v", got.Peers)
	}
}

// The slack: a peers file written by a machine a moment ahead, or an NTP step
// landing between the write and the read, must not put "one of the two clocks
// is wrong" against a healthy peer. A real skew still does (#1855).
func TestTheClockSentenceHasSlackForJitter(t *testing.T) {
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		delta time.Duration
		want  bool
	}{
		{"a second behind", -time.Second, false},
		{"exactly now", 0, false},
		{"a millisecond ahead", time.Millisecond, false},
		{"thirty seconds ahead", 30 * time.Second, false},
		{"a minute ahead", time.Minute, false},
		{"two minutes ahead", 2 * time.Minute, true},
		{"two days ahead", 48 * time.Hour, true},
	} {
		if got := peerStampedAhead(now.Add(tc.delta), now); got != tc.want {
			t.Errorf("%s: flagged=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// A peers file that cannot be parsed used to read exactly like a machine with
// no peers, on both surfaces, while `deja sync` walked nothing at all. The
// trust policy has said `unreadable` with the reason since #1027; sync is the
// section that exists because a stopped sync does not announce itself (#1840).
func TestAnUnreadablePeersFileIsReportedAsSuch(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"truncated", `{"peers":[{"host":"laptop","last_push":"2026-08-24T10:00:00Z"}`},
		{"peers is not a list", `{"peers":"not a list"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			path := filepath.Join(tmp, "peers.json")
			t.Setenv("DEJA_PEERS_FILE", path)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}

			var text strings.Builder
			doctorPeers(&text, t.TempDir(), time.Now())
			if strings.Contains(text.String(), "no other machines yet") {
				t.Errorf("a broken peers file reads as an empty one:\n%s", text.String())
			}
			if !strings.Contains(text.String(), path) {
				t.Errorf("the report does not say which file to look at:\n%s", text.String())
			}

			var out strings.Builder
			if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
				t.Fatal(err)
			}
			var report struct {
				Sync struct {
					State string `json:"state"`
					Error string `json:"error"`
				} `json:"sync"`
			}
			if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
				t.Fatal(err)
			}
			if report.Sync.State != "unreadable" {
				t.Errorf("sync.state = %q, want unreadable", report.Sync.State)
			}
			if report.Sync.Error == "" {
				t.Errorf("sync.error is empty, so a script knows something is wrong and not what")
			}
		})
	}
}

// The control: a file deja can read reports ok, and a machine with no file at
// all is not an error — it is a machine that has never synced.
func TestAReadablePeersFileReportsOK(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop","last_push":"2026-08-24T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := collectDoctorSync(t.TempDir()); got.State != "ok" || got.Error != "" {
		t.Errorf("a readable file reports %#v", got)
	}

	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "absent.json"))
	if got := collectDoctorSync(t.TempDir()); got.State != "ok" || got.Error != "" {
		t.Errorf("a machine that never synced reports %#v", got)
	}
	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if !strings.Contains(text.String(), "no other machines yet") {
		t.Errorf("a machine with no peers lost its own line:\n%s", text.String())
	}
}

// The sentence a broken file used to get from `deja sync` told the reader to
// name their first machine — which is what they had already done (#1840).
func TestBareSyncSaysThePeersFileIsBrokenRatherThanEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop"`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runSyncAll(index.DefaultDir(), false)
	if err == nil {
		t.Fatal("a broken peers file did not stop the sync")
	}
	if strings.Contains(err.Error(), "no machines to sync with yet") {
		t.Errorf("a broken file reads as a machine that never synced: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error does not say which file to look at: %v", err)
	}

	// The control: with no file at all, the invitation is still the right
	// sentence.
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "absent.json"))
	// It is a state rather than an error, so it exits 0.
	said := captureStderr(t, func() { err = runSyncAll(index.DefaultDir(), false) })
	if err != nil || !strings.Contains(said, "no machines to sync with yet") {
		t.Errorf("a machine that never synced lost its invitation: %v %q", err, said)
	}
}

// Every odd shape of the file, and what deja calls it. An empty file is not
// "nothing configured": deja never writes one, so it means a write that did not
// finish, and the reader should look at it.
func TestThePeersFileShapesAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"empty file", "", "unreadable"},
		{"whitespace only", "  \n", "unreadable"},
		{"no peers key", "{}", "ok"},
		{"null peers", `{"peers":null}`, "ok"},
		{"a peer with no host", `{"peers":[{"last_push":"2026-08-24T10:00:00Z"}]}`, "ok"},
		{"top-level array", "[]", "unreadable"},
		{"top-level string", `"x"`, "unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			path := filepath.Join(tmp, "peers.json")
			t.Setenv("DEJA_PEERS_FILE", path)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got := collectDoctorSync(t.TempDir())
			if got.State != tc.want {
				t.Errorf("state = %q, want %q (error %q)", got.State, tc.want, got.Error)
			}
			if got.State == "unreadable" && got.Error == "" {
				t.Errorf("unreadable with no reason")
			}
			if got.Peers == nil {
				t.Errorf("peers is null rather than an empty list, which a consumer has to special-case")
			}
		})
	}
}

// The two failure modes the shape table cannot write as a file body: no
// permission to read, and a directory where the file should be.
func TestAFileDejaCannotOpenIsReportedToo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything, so the denied case cannot be built")
	}
	t.Run("permission denied", func(t *testing.T) {
		tmp := hermeticEnv(t)
		path := filepath.Join(tmp, "peers.json")
		t.Setenv("DEJA_PEERS_FILE", path)
		if err := os.WriteFile(path, []byte(`{"peers":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o000); err != nil {
			t.Skipf("cannot drop permissions here: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
		// Chmod reports success on Windows and leaves the file readable — it
		// only toggles the read-only attribute — so the case this test is about
		// never existed there and the assertion failed instead of skipping
		// (#2081). Ask the filesystem rather than the call.
		if f, err := os.Open(path); err == nil {
			_ = f.Close()
			t.Skip("this platform reads the file anyway, so there is no denial to report")
		}
		got := collectDoctorSync(t.TempDir())
		if got.State != "unreadable" || !strings.Contains(got.Error, "permission denied") {
			t.Errorf("a file deja may not read reports %#v", got)
		}
	})
	t.Run("a directory in its place", func(t *testing.T) {
		tmp := hermeticEnv(t)
		path := filepath.Join(tmp, "peers.json")
		t.Setenv("DEJA_PEERS_FILE", path)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		got := collectDoctorSync(t.TempDir())
		if got.State != "unreadable" || got.Error == "" {
			t.Errorf("a directory where the file should be reports %#v", got)
		}
	})
}

// Snapshot is one read: the list and the reason describe the same state of the
// file, which two calls could not promise while a sync rewrites it.
func TestSnapshotAnswersBothFromOneRead(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":"laptop"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	list, why := peers.Snapshot()
	if why != "" || len(list) != 1 {
		t.Fatalf("a readable file gave %d peers and %q", len(list), why)
	}
	if err := os.WriteFile(path, []byte(`{"peers":[{"host":`), 0o600); err != nil {
		t.Fatal(err)
	}
	list, why = peers.Snapshot()
	if why == "" {
		t.Error("a broken file gave no reason")
	}
	if len(list) != 0 {
		t.Errorf("a broken file still produced %d peers", len(list))
	}
}

// writeMixedPeers points deja at a peers file holding the names that break a
// column padded by runes: an ordinary one, a long one, a CJK one, and one
// bounded to 80 columns since #1808.
func writeMixedPeers(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peers.json")
	body := `{"peers":[
	{"host":"laptop","last_push":"2026-08-24T10:00:00Z","last_pull":"2026-08-24T10:00:00Z"},
	{"host":"build-server-in-the-other-office","last_push":"2026-08-23T10:00:00Z"},
	{"host":"数据分片服务器","last_push":"2026-08-22T10:00:00Z"},
	{"host":"` + strings.Repeat("w", 300) + `","last_push":"2026-08-21T10:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_PEERS_FILE", path)
}

// The Sync section is a column, and what it holds is a name someone typed. It
// was padded with %-12s: a 32-character name got no padding at all, a CJK name
// was padded as if each character were one column wide, and an 80-column name
// pushed the state off the screen (#1821).
func TestTheSyncSectionLinesUpWhateverThePeerIsCalled(t *testing.T) {
	hermeticEnv(t)
	writeMixedPeers(t)
	var b strings.Builder
	doctorPeers(&b, t.TempDir(), time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))

	var starts []int
	for line := range strings.SplitSeq(strings.TrimRight(b.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "Sync") {
			continue
		}
		before, _, ok := strings.Cut(line, "last exchange")
		if !ok {
			continue
		}
		starts = append(starts, termwidth.Columns(before))
	}
	if len(starts) != 4 {
		t.Fatalf("want a state line per peer, got %d:\n%s", len(starts), b.String())
	}
	for _, at := range starts[1:] {
		if at != starts[0] {
			t.Errorf("the states start at different columns %v:\n%s", starts, b.String())
			break
		}
	}
	// The control: every name is still on the screen, so the column was not
	// bought by throwing the names away.
	for _, want := range []string{"laptop", "build-server-in-the-other-office", "数据分片服务器"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("%q is missing from the section:\n%s", want, b.String())
		}
	}
}

// pi before 0.99 has no MCP of its own: ~/.pi/agent/mcp.json is read by the
// pi-mcp-adapter package and nothing else. Without it in pi's packages the
// server deja declares there never starts, and the row said wired (#4583).
func TestDoctorPiMCPRowNeedsTheAdapter(t *testing.T) {
	hermeticEnv(t)
	fakePiOnPath(t, "@mariozechner/pi-coding-agent", "0.73.1")
	if _, err := installPiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	row := func() (string, doctorMCPStatus) {
		var out bytes.Buffer
		doctorMCP(&out)
		var line string
		for l := range strings.SplitSeq(out.String(), "\n") {
			if strings.HasPrefix(l, "  pi ") {
				line = l
			}
		}
		for _, r := range collectDoctorMCP() {
			if r.Name == "pi" {
				return out.String(), r
			}
		}
		t.Fatalf("no pi row:\n%s", out.String())
		return line, doctorMCPStatus{}
	}
	text, js := row()
	if js.State == "wired" || strings.Contains(text, "  pi           wired") {
		t.Errorf("pi has no adapter to read mcp.json and the row says wired:\n%s\njson: %+v", text, js)
	}
	if !strings.Contains(text, "pi-mcp-adapter") || !strings.Contains(js.Note, "pi-mcp-adapter") {
		t.Errorf("the row does not name the package pi needs:\n%s\njson: %+v", text, js)
	}

	// The control: with the adapter in pi's packages the same file is wired.
	settings := filepath.Join(sources.PiConfigDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"packages":["npm:pi-mcp-adapter"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, js := row(); js.State != "wired" || !strings.Contains(text, "  pi           wired") {
		t.Errorf("with the adapter installed the row is not wired:\n%s\njson: %+v", text, js)
	}
}

// And install says so, rather than writing a file nothing will read and
// reporting it as done.
func TestInstallPiSaysTheAdapterIsNeeded(t *testing.T) {
	hermeticEnv(t)
	fakePiOnPath(t, "@mariozechner/pi-coding-agent", "0.73.1")
	r, err := installPiAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(r.Note, "pi-mcp-adapter") {
		t.Errorf("install wrote mcp.json without saying pi needs pi-mcp-adapter to read it: %+v", r)
	}
}

// pi also loads an extension from a path in settings.json "extensions" and
// from whatever sits in its extensions/ directory, so an adapter cloned or
// pointed at that way is installed too.
func TestPiAdapterFoundOutsidePackages(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(dir string) error
	}{
		{"settings extensions", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"extensions":["/src/pi-mcp-adapter"]}`), 0o644)
		}},
		{"extensions dir", func(dir string) error {
			return os.MkdirAll(filepath.Join(dir, "extensions", "pi-mcp-adapter"), 0o755)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			dir := sources.PiConfigDir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if piMCPAdapterInstalled() {
				t.Fatal("adapter found before it was put there")
			}
			if err := tc.setup(dir); err != nil {
				t.Fatal(err)
			}
			if !piMCPAdapterInstalled() {
				t.Error("pi loads pi-mcp-adapter this way and doctor says it is missing")
			}
		})
	}
}

// doctor is the screen someone opens to find out what is allowed, and with no
// policy file it decided on the file's absence: "every origin activates
// everywhere", while DEJA_AUTORECALL_LOCAL_ONLY held the auto path to local
// sessions only (#939).
func TestDoctorPolicySeesTheEnvironmentOverride(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "no-such-policy.json"))

	var out bytes.Buffer
	doctorPolicy(&out, filepath.Join(tmp, "index.db"))
	if !strings.Contains(out.String(), "every origin activates everywhere") {
		t.Errorf("an unrestricted machine stopped saying so:\n%s", out.String())
	}

	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	out.Reset()
	doctorPolicy(&out, filepath.Join(tmp, "index.db"))
	got := out.String()
	if strings.Contains(got, "every origin activates everywhere") {
		t.Errorf("doctor called a restricted machine unrestricted:\n%s", got)
	}
	if !strings.Contains(got, "auto") || !strings.Contains(got, "local-only") {
		t.Errorf("doctor does not name what the environment restricts:\n%s", got)
	}
	if !strings.Contains(got, "DEJA_AUTORECALL_LOCAL_ONLY") {
		t.Errorf("doctor does not say where the rule comes from:\n%s", got)
	}
	// The other two paths are untouched by that variable, and saying otherwise
	// would be its own lie.
	if !strings.Contains(got, "local+imported") {
		t.Errorf("doctor restricted paths the variable does not touch:\n%s", got)
	}
}

// doctor asks one question of a store — does it parse into sessions — and for
// opencode it was answering by reading the whole database: 6.5 seconds of an
// 8 second report on a 2.8 GB store.
func TestOpencodeProbeReadsOnlyTheNewestSession(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	sql := `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, time_created INTEGER, time_updated INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT, time_created INTEGER);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT);
INSERT INTO session VALUES ('old','/w',1000,1000);
INSERT INTO session VALUES ('new','/w',2000,2000);
INSERT INTO message VALUES ('m1','old','{"role":"user","time":{"created":1000}}',1000);
INSERT INTO message VALUES ('m2','new','{"role":"user","time":{"created":2000}}',2000);
INSERT INTO part VALUES ('p1','m1','{"type":"text","text":"OLDSESSION"}');
INSERT INTO part VALUES ('p2','m2','{"type":"text","text":"NEWSESSION"}');
`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	ss, err := doctorProbeOpencode(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "new" {
		t.Fatalf("probe returned %d sessions, want just the newest: %+v", len(ss), ss)
	}
	// The point of the check survives: a store that parses reports sessions.
	if len(ss[0].Messages) == 0 {
		t.Fatal("probe returned a session with no messages, doctor would call the store broken")
	}
}
