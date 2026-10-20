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

// The wider run of TestRandomPassesOverSharedSessionsMatchRebuild: more
// files, more passes, renames, and a rebuild of the rebuild, which has to
// hold what the first rebuild held. SHARED_PARITY_SEEDS=from:to sweeps more seeds.
func TestWideRandomPassesOverSharedSessionsMatchRebuild(t *testing.T) {
	from, to := 0, 8
	if testing.Short() {
		to = 4
	}
	if v := os.Getenv("SHARED_PARITY_SEEDS"); v != "" {
		a, b, _ := strings.Cut(v, ":")
		from, _ = strconv.Atoi(a)
		to, _ = strconv.Atoi(b)
	}
	for seed := from; seed < to; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) { wideSharedRun(t, int64(seed)) })
	}
}

func wideSharedRun(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	dir := filepath.Join(tmp, "idx")
	ids := []string{"s-a", "s-b", "s-c"}
	words := []string{"alpha", "bravo", "cache", "delta", "queue", "retry", "webhook", "lockfile"}
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	files := []string{"f0.jsonl", "f1.jsonl", "f2.jsonl", "f3.jsonl", "f4.jsonl", "f5.jsonl"}
	ofFile := make([]string, len(files))
	last := make([]int, len(files))
	line := func(i int) string {
		n := min(last[i]+rng.Intn(3), 7)
		last[i] = n
		return claudeLine(ofFile[i], base.Add(time.Duration(n)*time.Minute).Format(time.RFC3339),
			words[n]+" "+words[(n+3)%len(words)])
	}
	path := func(i int) string { return filepath.Join(claude, "p", files[i]) }
	rewrite := func(i int) {
		ofFile[i], last[i] = ids[rng.Intn(len(ids))], rng.Intn(3)
		var s strings.Builder
		for range 1 + rng.Intn(3) {
			s.WriteString(line(i))
		}
		write(t, path(i), s.String())
	}
	view := func(dir, id string) string {
		return sortedLines(sessionView(t, dir, "claude", id) + rowView(t, dir, "claude", id) + copiesView(t, dir, "claude", id))
	}
	// What each deleted file held, which its session keeps whatever happens
	// to the other files: by path, until a file is written there again.
	gone, seen := map[string][]string{}, map[string][]string{}
	snapshot := func() {
		clear(seen)
		for i, f := range files {
			if b, err := os.ReadFile(path(i)); err == nil {
				seen[f] = strings.Split(strings.TrimSpace(string(b)), "\n")
			}
		}
	}
	forget := func(i int) { delete(gone, files[i]) }
	rebuildOf := func(src string) string {
		fresh := filepath.Join(t.TempDir(), "rebuilt")
		if err := os.CopyFS(fresh, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(fresh, "", true, nil); err != nil {
			t.Fatal(err)
		}
		return fresh
	}
	for i := range 2 {
		rewrite(i)
	}
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
				forget(i)
				rewrite(i)
			case op == 0 || op == 1:
				appendTo(t, p, line(i))
			case op == 2:
				rewrite(i)
				bumpMTime(t, p)
			case op == 3:
				ofFile[i] = ids[rng.Intn(len(ids))]
				appendTo(t, p, line(i))
			case op == 4:
				// Renamed to a free name, the way a client moves a transcript.
				j := rng.Intn(len(files))
				if _, err := os.Stat(path(j)); err == nil {
					break
				}
				t.Logf("pass %d: %s renamed to %s", pass, files[i], files[j])
				forget(j)
				if err := os.Rename(p, path(j)); err != nil {
					t.Fatal(err)
				}
				ofFile[j], last[j] = ofFile[i], last[i]
			default:
				// What the last pass read of it, if it read it at all.
				if lines := seen[files[i]]; len(lines) > 0 {
					gone[files[i]] = lines
				}
				_ = os.Remove(p)
			}
		}
		// A new file holding every turn of a deleted one under its id is that
		// file moved, whenever it lands, and the index follows it.
		for f := range gone {
			for k, g := range files {
				b, err := os.ReadFile(path(k))
				if _, before := seen[g]; err != nil || before || len(gone[f]) == 0 {
					continue
				}
				now := strings.Split(strings.TrimSpace(string(b)), "\n")
				if lineID(t, now[len(now)-1]) == lineID(t, gone[f][len(gone[f])-1]) && holdsTurns(t, now, gone[f]) {
					delete(gone, f)
				}
			}
		}
		indexPass(t, dir)
		fresh := rebuildOf(dir)
		again := rebuildOf(fresh)
		for _, id := range ids {
			if got, want := view(dir, id), view(fresh, id); got != want {
				t.Errorf("claude:%s after incremental passes:\n%s\nafter a rebuild:\n%s", id, got, want)
			}
			if got, want := view(again, id), view(fresh, id); got != want {
				t.Errorf("claude:%s after a rebuild of the rebuild:\n%s\nafter one rebuild:\n%s", id, got, want)
			}
		}
		snapshot()
		for f, held := range gone {
			// The file's session is its last line's.
			id := lineID(t, held[len(held)-1])
			v := sessionView(t, dir, "claude", id)
			for _, h := range held {
				if turn := lineTurn(t, h); !strings.Contains(v, turn+"\n") {
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

// copiesView is the files a session's row names beside its owner, and which
// of them are kept: a deleted copy that drops out of the row can no longer
// keep its turns.
func copiesView(t *testing.T, dir, harness, id string) string {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := m.Sessions[harness+":"+id]
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, p := range row.Copies {
		fmt.Fprintf(&b, "copy=%s kept=%v\n", filepath.Base(p), m.Files[p].Kept)
	}
	return b.String()
}

type wideLine struct {
	SessionID string
	Timestamp string
	Message   struct{ Content string }
}

func parseWideLine(t *testing.T, l string) wideLine {
	t.Helper()
	var w wideLine
	if err := json.Unmarshal([]byte(l), &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func lineID(t *testing.T, l string) string { return parseWideLine(t, l).SessionID }

// lineTurn is the line as sessionView prints it.
func lineTurn(t *testing.T, l string) string {
	w := parseWideLine(t, l)
	return "user|" + w.Timestamp + "|" + w.Message.Content
}

func holdsTurns(t *testing.T, have, want []string) bool {
	in := map[string]bool{}
	for _, l := range have {
		in[lineTurn(t, l)] = true
	}
	for _, l := range want {
		if !in[lineTurn(t, l)] {
			return false
		}
	}
	return true
}
