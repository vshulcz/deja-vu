package index

import (
	"bytes"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A rollout that repeats a message verbatim — same role, same timestamp, same
// text — is deduped by a full build and was not by the append path, so the same
// file held 3 records or 2 depending on how it entered the index. An archive
// checked against a rebuilt copy of itself then compared unequal (#3934).
func TestAppendDedupesARepeatedMessageLikeARebuild(t *testing.T) {
	const dup = `{"timestamp":"2026-06-01T05:00:00.000Z","type":"session_meta","payload":{"id":"dup-1","cwd":"/w/x"}}
{"timestamp":"2026-06-01T05:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same words"}]}}
{"timestamp":"2026-06-01T05:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same words"}]}}
{"timestamp":"2026-06-01T05:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
`
	const other = `{"timestamp":"2026-06-01T04:00:00.000Z","type":"session_meta","payload":{"id":"other-1","cwd":"/w/x"}}
{"timestamp":"2026-06-01T04:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"other session"}]}}
`
	count := func(t *testing.T, incremental bool) int {
		t.Helper()
		tmp := t.TempDir()
		root := filepath.Join(tmp, "codex", "sessions", "x")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		setHome(t, filepath.Join(tmp, "home"))
		t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
		t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
		t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
		t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
		t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
		t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
		dupPath := filepath.Join(root, "rollout-2026-06-01T05-00-00-dup-1.jsonl")
		dir := filepath.Join(tmp, "index.db")
		if incremental {
			// The index exists first and the rollout arrives afterwards, which
			// is the path a machine actually takes.
			if err := os.WriteFile(filepath.Join(root, "rollout-2026-06-01T04-00-00-other-1.jsonl"), []byte(other), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(dir, "codex", false, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(dupPath, []byte(dup), 0o644); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Second)
		_ = os.Chtimes(dupPath, now, now)
		if err := Ensure(dir, "codex", false, nil); err != nil {
			t.Fatal(err)
		}
		recs, err := ReadRecords(dir)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range recs {
			if r.Record.Text == "same words" {
				n++
			}
		}
		return n
	}
	fresh := count(t, false)
	appended := count(t, true)
	if fresh != 1 {
		t.Fatalf("a full build kept %d copies of the repeated message, want 1", fresh)
	}
	if appended != fresh {
		t.Errorf("append kept %d copies, a rebuild %d — the same file indexes differently", appended, fresh)
	}
}

// A pass that lands between zstd writing x.jsonl.zst and removing x.jsonl
// sees both forms. Once the .jsonl goes, the session must hold its messages
// once, as a rebuild of what is left does (#4252).
func TestACodexRolloutSeenInBothFormsIsHeldOnceAfterTheJSONLGoes(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not installed")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
	write(t, p, codexRolloutHead)
	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "-k", p).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v %s", err, out)
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	count := func(idx string) int {
		recs, err := ReadRecords(idx)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range recs {
			if r.Record.Key == "codex:cx-1" && strings.Contains(r.Record.Text, "go test") {
				n++
			}
		}
		return n
	}
	fresh := filepath.Join(tmp, "fresh")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := count(idx), count(fresh); got != want {
		t.Errorf("the command is held %d times after the update, %d after a rebuild", got, want)
	}
}

// Codex compresses a finished rollout in place, x.jsonl to x.jsonl.zst. When
// nothing else in the pass shrank, the vanished .jsonl was kept as a cleanup
// and the pass took the append path, which has no rename rule: the .zst's
// records went in beside the old ones and every message showed twice, under a
// path that no longer exists (#4252).
func TestACodexRolloutCompressedInPlaceIsAMove(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not installed")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
	write(t, p, codexRolloutHead)
	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "--rm", p).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v %s", err, out)
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if row := m.Sessions["codex:cx-1"]; row.Path != p+".zst" || row.Shared {
		t.Errorf("row on %s shared=%v, want it moved to the .zst", row.Path, row.Shared)
	}
	if _, ok := m.Files[p]; ok {
		t.Errorf("the old path %s is still held", p)
	}
	recs, err := ReadRecords(idx)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, r := range recs {
		if r.Record.Key == "codex:cx-1" && strings.Contains(r.Record.Text, "go test") {
			cmds = append(cmds, r.Record.SourcePath)
		}
	}
	if len(cmds) != 1 {
		t.Errorf("the command is held %d times, from %q; want once", len(cmds), cmds)
	}
}

// Codex writes an interactive session twice: the rollout, which holds the
// whole conversation and the directory it ran in, and a line of history.jsonl
// with the same session id and only the prompt. history.jsonl sorts before
// sessions/, so it owned the row: every TUI session was filed under the
// project "history", counted as a clash, and refused by resume as an exec
// entry (#4180). One conversation in two stores is the goose case.
func TestACodexRolloutOwnsTheSessionItsHistoryLineShares(t *testing.T) {
	hist := filepath.FromSlash("/home/u/.codex/history.jsonl")
	roll := filepath.FromSlash("/home/u/.codex/sessions/2026/10/01/rollout-2026-10-01T12-31-51-01a0.jsonl")

	held := SessionMeta{Harness: "codex", ID: "01a0", Path: hist, Project: "history"}
	owns, collided := attributeSession(held, model.Session{Harness: "codex", ID: "01a0", Path: roll, Project: "work"})
	if collided {
		t.Error("the rollout was reported as clashing with its own history line")
	}
	if !owns {
		t.Error("the history line kept the row; the session is filed under \"history\"")
	}

	heldRoll := SessionMeta{Harness: "codex", ID: "01a0", Path: roll, Project: "work"}
	owns, collided = attributeSession(heldRoll, model.Session{Harness: "codex", ID: "01a0", Path: hist, Project: "history"})
	if collided || owns {
		t.Errorf("the history line arriving second: owns=%v collided=%v, want neither", owns, collided)
	}

	// Two rollouts under one id are still two transcripts.
	other := SessionMeta{Harness: "codex", ID: "01a0", Path: filepath.Join(filepath.Dir(roll), "rollout-other-01a0.jsonl")}
	if _, collided = attributeSession(other, model.Session{Harness: "codex", ID: "01a0", Path: roll}); !collided {
		t.Error("two rollouts under one id stopped being reported")
	}
}

func codexHistoryLine(id string, at time.Time, text string) string {
	return hjson(map[string]any{"session_id": id, "ts": at.Unix(), "text": text})
}

// A pass that reads Codex's history.jsonl beside a new rollout used to read
// the log whole and replace every session known from it alone: 9 changed
// files counted as 257 sessions replaced (#5403). Read from where the last
// pass stopped, only the session with a new line is replaced, and the index
// still matches a rebuild.
func TestCodexHistoryReplacesOnlyTheSessionsWithNewLines(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	hist := filepath.Join(tmp, "codex", "history.jsonl")
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	var body strings.Builder
	for i := range 30 {
		id := fmt.Sprintf("019a0000-0000-7000-8000-%012d", i)
		body.WriteString(codexHistoryLine(id, base.Add(time.Duration(i)*time.Minute), fmt.Sprintf("why does the ledger %d retry", i)))
		body.WriteString(codexHistoryLine(id, base.Add(time.Duration(i)*time.Minute+time.Second), "and the outbox too"))
	}
	parityWrite(t, hist, body.String(), false)
	rollout := func(id string, day int) string {
		return filepath.Join(tmp, "codex", "sessions", "2026", "06", fmt.Sprintf("%02d", day), fmt.Sprintf("rollout-2026-06-%02dT10-00-00-%s.jsonl", day, id))
	}
	at := base.Add(48 * time.Hour)
	parityWrite(t, rollout("019a1111-0000-7000-8000-000000000001", 3),
		codexLine(at, "session_meta", map[string]any{"id": "019a1111-0000-7000-8000-000000000001", "cwd": "/w/app"})+
			codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "why does the pool deadlock"}}}), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)

	// One more prompt for one history-only session, and the rollout of
	// another arriving: a takeover, which only the replacement path does.
	parityWrite(t, hist, codexHistoryLine("019a0000-0000-7000-8000-000000000007", base.Add(time.Hour), "the kafka webhook again"), true)
	at = base.Add(72 * time.Hour)
	parityWrite(t, rollout("019a0000-0000-7000-8000-000000000005", 4),
		codexLine(at, "session_meta", map[string]any{"id": "019a0000-0000-7000-8000-000000000005", "cwd": "/w/svc"})+
			codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "why does the schema migration hang"}}}), false)
	var out bytes.Buffer
	if err := Ensure(inc, "", false, &out); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(\d+) sessions? replaced`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no replacement pass ran, so this measures nothing:\n%s", out.String())
	}
	if m[1] != "2" {
		t.Errorf("sessions replaced = %s, want 2 (the one with a new prompt and the one its rollout took over):\n%s", m[1], out.String())
	}
	if !strings.Contains(out.String(), "2 changed transcripts") {
		t.Errorf("want the history and the new rollout read:\n%s", out.String())
	}
	sameAsRebuild(t, inc)

	// Codex trims history.jsonl from the front once it passes max_bytes: the
	// prefix no longer matches, and the log is read whole again.
	trimmed := strings.SplitAfterN(body.String(), "\n", 5)[4] +
		codexHistoryLine("019a0000-0000-7000-8000-000000000007", base.Add(time.Hour), "the kafka webhook again") +
		codexHistoryLine("019a0000-0000-7000-8000-000000000009", base.Add(2*time.Hour), "and the tokenizer")
	parityWrite(t, hist, trimmed, false)
	parityPass(t, inc, false)
	sameAsRebuild(t, inc)
}

// The history parse skips a session whose rollout is on disk, so a session
// can leave the parse or come back to it with no new line: a rollout that
// holds no message yet arriving for a logged session, or one deleted. Each
// pass still matches a rebuild.
func TestCodexHistoryTailFollowsRolloutsThatComeAndGo(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	hist := filepath.Join(tmp, "codex", "history.jsonl")
	a, b := "019a0000-0000-7000-8000-00000000000a", "019a0000-0000-7000-8000-00000000000b"
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	roll := filepath.Join(tmp, "codex", "sessions", "2026", "06", "03", "rollout-2026-06-03T10-00-00-"+a+".jsonl")
	meta := codexLine(base.Add(48*time.Hour), "session_meta", map[string]any{"id": a, "cwd": "/w/app"})

	t.Run("a rollout with no message yet arrives", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		parityWrite(t, hist, codexHistoryLine(b, base.Add(2*time.Minute), "the outbox again"), true)
		parityWrite(t, roll, meta, false)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
		parityWrite(t, roll, codexLine(base.Add(49*time.Hour), "response_item", map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "why does the pool deadlock"}}}), true)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})

	// The same with the log left alone in the pass that moves the session.
	t.Run("a rollout with no message yet arrives beside an untouched log", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		parityWrite(t, roll, meta, false)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})
	t.Run("an empty rollout goes beside an untouched log", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		parityWrite(t, roll, meta, false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		if err := os.Remove(roll); err != nil {
			t.Fatal(err)
		}
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})
	t.Run("an empty rollout goes", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		parityWrite(t, roll, meta, false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		if err := os.Remove(roll); err != nil {
			t.Fatal(err)
		}
		parityWrite(t, hist, codexHistoryLine(b, base.Add(2*time.Minute), "the outbox again"), true)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})
}
