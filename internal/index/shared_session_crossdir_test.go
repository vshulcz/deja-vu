package index

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Random passes over copies of one session spread over several project
// directories, with deletes, renames and rewrites, each compared to a rebuild:
// the records, the row, the command tables and the session facts. A deleted
// copy keeps every turn it held. CROSS_DIR_PARITY_SEEDS=from:to sweeps more
// seeds. The fixes are compared too once a pass mines them as a rebuild does
// (#5504): add "fixes" to the keys below.
func TestRandomPassesOverCopiesInSeveralProjectsMatchRebuild(t *testing.T) {
	from, to := 0, 3
	if testing.Short() {
		to = 2
	}
	if v := os.Getenv("CROSS_DIR_PARITY_SEEDS"); v != "" {
		a, b, _ := strings.Cut(v, ":")
		from, _ = strconv.Atoi(a)
		to, _ = strconv.Atoi(b)
	}
	for seed := from; seed < to; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) { crossDirRun(t, int64(seed)) })
	}
}

func crossDirRun(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	tmp := hermeticIndexEnv(t)
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	dir := filepath.Join(tmp, "idx")
	ids := []string{"s-a", "s-b"}
	words := []string{"alpha", "bravo", "cache", "delta", "queue", "retry", "webhook", "lockfile"}
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	// A Claude transcript is named for its session, so two projects share a
	// session by holding a file of the same name (#699).
	files := []string{"-tmp-app/s-a.jsonl", "-tmp-app/s-b.jsonl", "-tmp-infra/s-a.jsonl", "-tmp-infra/s-b.jsonl", "-tmp-ops/s-a.jsonl"}
	ofFile := make([]string, len(files))
	for i, f := range files {
		ofFile[i] = strings.TrimSuffix(filepath.Base(f), ".jsonl")
	}
	last := make([]int, len(files))
	// One turn: a prompt, a command and its output, the same in every copy
	// that holds turn n, so copies share it.
	turn := func(i int) string {
		n := min(last[i]+rng.Intn(3), 7)
		last[i] = n
		id, at := ofFile[i], base.Add(time.Duration(n)*time.Minute)
		cmd := "make zz" + words[n]
		out := "ok"
		if n%2 == 0 {
			out = "make: *** [zz" + words[n] + "] Error 2"
		}
		line := func(off time.Duration, role, content string) string {
			return fmt.Sprintf(`{"type":%q,"sessionId":%q,"timestamp":%q,"cwd":"/tmp/%s","message":{"role":%q,"content":%s}}`,
				role, id, at.Add(off).Format(time.RFC3339), words[n], role, content) + "\n"
		}
		tid := fmt.Sprintf("t%d", n)
		return line(0, "user", strconv.Quote(words[n]+" "+words[(n+3)%len(words)])) +
			line(time.Second, "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"`+cmd+`"}}]`) +
			line(2*time.Second, "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"`+out+`"}]`)
	}
	path := func(i int) string { return filepath.Join(claude, filepath.FromSlash(files[i])) }
	rewrite := func(i int) {
		last[i] = rng.Intn(3)
		var s strings.Builder
		for range 1 + rng.Intn(3) {
			s.WriteString(turn(i))
		}
		write(t, path(i), s.String())
	}
	view := func(dir, id string) string {
		return sortedLines(sessionView(t, dir, "claude", id) + rowView(t, dir, "claude", id))
	}
	tables := func(dir string) map[string]string {
		return map[string]string{
			"commands": fmt.Sprint(ReadCommands(dir)),
			"fails":    fmt.Sprint(ReadCommandFails(dir)),
			"facts":    fmt.Sprint(ReadSessionFacts(dir)),
			"fixes":    fmt.Sprint(ReadFixes(dir)),
		}
	}
	// The prompts each deleted file held, which its session keeps: by path,
	// until a file is written there again.
	gone, seen := map[string][]string{}, map[string][]string{}
	snapshot := func() {
		clear(seen)
		for i, f := range files {
			if b, err := os.ReadFile(path(i)); err == nil {
				seen[f] = crossDirPrompts(t, string(b))
			}
		}
	}
	rewrite(0)
	rewrite(2)
	indexPass(t, dir)
	snapshot()
	for pass := range 8 {
		for range 1 + rng.Intn(3) {
			i := rng.Intn(len(files))
			p := path(i)
			_, err := os.Stat(p)
			op := rng.Intn(6)
			t.Logf("pass %d: %s op %d (exists %v)", pass, files[i], op, err == nil)
			switch {
			case err != nil:
				delete(gone, files[i])
				rewrite(i)
			case op <= 1 || op == 3:
				appendTo(t, p, turn(i))
			case op == 2:
				rewrite(i)
				bumpMTime(t, p)
			case op == 4:
				// To another project, under its name, the way a client moves
				// a transcript when its project moves.
				j := rng.Intn(len(files))
				if _, err := os.Stat(path(j)); err == nil || ofFile[j] != ofFile[i] {
					break
				}
				t.Logf("pass %d: %s renamed to %s", pass, files[i], files[j])
				delete(gone, files[j])
				if err := os.MkdirAll(filepath.Dir(path(j)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(p, path(j)); err != nil {
					t.Fatal(err)
				}
				last[j] = last[i]
			default:
				if prompts := seen[files[i]]; len(prompts) > 0 {
					gone[files[i]] = prompts
				}
				_ = os.Remove(p)
			}
		}
		// A new file holding every prompt of a deleted one under its id is
		// that file moved, and the index follows it.
		for f, held := range gone {
			for k, g := range files {
				b, err := os.ReadFile(path(k))
				if _, before := seen[g]; err != nil || before {
					continue
				}
				now := crossDirPrompts(t, string(b))
				if crossDirID(held) == crossDirID(now) && crossDirHolds(now, held) {
					delete(gone, f)
				}
			}
		}
		indexPass(t, dir)
		fresh := filepath.Join(t.TempDir(), "rebuilt")
		if err := os.CopyFS(fresh, os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(fresh, "", true, nil); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if got, want := view(dir, id), view(fresh, id); got != want {
				t.Errorf("claude:%s after incremental passes:\n%s\nafter a rebuild:\n%s", id, got, want)
			}
		}
		got, want := tables(dir), tables(fresh)
		for _, k := range []string{"commands", "fails", "facts"} {
			if got[k] != want[k] {
				t.Errorf("%s after incremental passes:\n%s\nafter a rebuild:\n%s", k, got[k], want[k])
			}
		}
		snapshot()
		for f, held := range gone {
			id := crossDirID(held)
			v := sessionView(t, dir, "claude", id)
			for _, h := range held {
				if _, turn, _ := strings.Cut(h, " "); !strings.Contains(v, turn+"\n") {
					t.Errorf("claude:%s lost %q of deleted %s", id, turn, f)
				}
			}
		}
		if t.Failed() {
			for i := range files {
				b, _ := os.ReadFile(path(i))
				t.Logf("%s:\n%s", files[i], b)
			}
			t.Fatalf("diverged after pass %d", pass)
		}
	}
}

// crossDirPrompts is each prompt line of a transcript as "id turn", the turn
// as sessionView prints it.
func crossDirPrompts(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		var w struct {
			SessionID string
			Timestamp string
			Message   struct{ Content json.RawMessage }
		}
		if err := json.Unmarshal([]byte(l), &w); err != nil {
			t.Fatal(err)
		}
		var text string
		if json.Unmarshal(w.Message.Content, &text) == nil {
			out = append(out, w.SessionID+" user|"+w.Timestamp+"|"+text)
		}
	}
	return out
}

// crossDirID is the session a file is: its last line's.
func crossDirID(prompts []string) string {
	if len(prompts) == 0 {
		return ""
	}
	id, _, _ := strings.Cut(prompts[len(prompts)-1], " ")
	return id
}

func crossDirHolds(have, want []string) bool {
	in := map[string]bool{}
	for _, h := range have {
		_, turn, _ := strings.Cut(h, " ")
		in[turn] = true
	}
	for _, w := range want {
		if _, turn, _ := strings.Cut(w, " "); !in[turn] {
			return false
		}
	}
	return true
}
