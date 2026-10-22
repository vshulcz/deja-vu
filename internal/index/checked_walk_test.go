package index

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// A new session lands between `deja index`'s freshness check and its pass:
// its prompt in the Claude prompt log, its transcript beside it. The pass
// must not read the log against a walk that has no transcript, and once the
// transcript moves to another project the index must match a rebuild, which
// drops the log's copy of the prompt (#5503).
func TestANewSessionBetweenCheckAndPassMatchesARebuild(t *testing.T) {
	for _, how := range []string{"EnsureChecked", "Ensure", "walk-then-updateIndex"} {
		t.Run(how, func(t *testing.T) {
			h := newTwoWayEnv(t)
			r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
			r.openStores()
			s1 := &historySession{project: "svc", sid: "s1", day: 3}
			r.promptLogged(s1, "start: pool ledger")
			h.put("svc", "s1", hPrompt("svc", "s1", 3, 0, "start: pool ledger")+r.turns(s1))
			pass := func() {
				t.Helper()
				if err := Ensure(r.index, "", false, nil); err != nil {
					t.Fatal(err)
				}
			}
			pass()
			// Something else changes, so the check is stale.
			h.add("svc", "s1", r.turns(s1))
			fresh, _, c := CheckUpToDate(r.index, "")
			if fresh || c == nil {
				t.Fatal("the index is up to date after a transcript grew")
			}
			s10 := &historySession{project: "app", sid: "s10", day: 4}
			r.promptLogged(s10, "start: parser outbox index")
			h.put("app", "s10", hPrompt("app", "s10", 4, 0, "start: parser outbox index")+r.turns(s10))
			var err error
			switch how {
			case "EnsureChecked":
				err = EnsureChecked(c, false, io.Discard)
			case "walk-then-updateIndex":
				// The check's walk, read under the lock as it stands: the
				// transcript is missing from it, as one that lands between any
				// walk and its parse is.
				unlock, lerr := lockDir(r.index)
				if lerr != nil {
					t.Fatal(lerr)
				}
				err = updateIndex(r.index, "", "", c.files, false, io.Discard)
				unlock()
			default:
				err = Ensure(r.index, "", false, io.Discard)
			}
			if err != nil {
				t.Fatal(err)
			}
			// The session moves to another project.
			if err := os.MkdirAll(filepath.Dir(h.path("infra", "s10")), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(h.path("app", "s10"), h.path("infra", "s10")); err != nil {
				t.Fatal(err)
			}
			s10.project = "infra"
			pass()
			r.compare(5, 1)
			h.add("infra", "s10", r.turns(s10))
			pass()
			r.compare(5, 2)
		})
	}
}

// A prompt-log line and its session's transcript read in one pass, the line
// naming the project the transcript has since left: the client's log parser
// looks for the transcript under the old project and keeps the line, and a
// rebuild drops it because the transcript holds the session. The append path
// read the log first and kept it; the replacing path, which another file's
// deletion sends the pass down, kept it whatever the order (#5503).
func TestAPromptLogLineIsDroppedForATranscriptReadInTheSamePass(t *testing.T) {
	for _, replacing := range []bool{false, true} {
		name := "append"
		if replacing {
			name = "replacing"
		}
		t.Run(name, func(t *testing.T) {
			h := newTwoWayEnv(t)
			r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
			r.openStores()
			s1 := &historySession{project: "svc", sid: "s1", day: 3}
			r.promptLogged(s1, "start: pool ledger")
			h.put("svc", "s1", hPrompt("svc", "s1", 3, 0, "start: pool ledger")+r.turns(s1))
			s2 := &historySession{project: "svc", sid: "s2", day: 3}
			h.put("svc", "s2", hPrompt("svc", "s2", 3, 0, "why does the kafka outbox fail")+r.turns(s2))
			if err := Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			s10 := &historySession{project: "app", sid: "s10", day: 4}
			r.promptLogged(s10, "start: parser outbox index")
			s10.project = "infra"
			h.put("infra", "s10", hPrompt("app", "s10", 4, 0, "start: parser outbox index")+r.turns(s10))
			if replacing {
				if err := os.Remove(h.path("svc", "s2")); err != nil {
					t.Fatal(err)
				}
			}
			if err := Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			r.compare(5, 1)
		})
	}
}

// A prompt already in the log when the check walks, and its transcript
// landing after it: the log parser skips a session whose transcript is on
// disk, so a pass over the check's walk read neither, and once the client
// deleted the transcript nothing was left to read. A rebuild keeps the
// session from the log (#5046); the pass now walks again (#5503).
func TestATranscriptLandingAfterTheCheckIsNotLost(t *testing.T) {
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	s1 := &historySession{project: "svc", sid: "s1", day: 3}
	r.promptLogged(s1, "start: pool ledger")
	h.put("svc", "s1", hPrompt("svc", "s1", 3, 0, "start: pool ledger")+r.turns(s1))
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s18 := &historySession{project: "svc", sid: "s18", day: 4}
	r.promptLogged(s18, "fix it")
	fresh, _, c := CheckUpToDate(r.index, "")
	if fresh || c == nil {
		t.Fatal("the index is up to date after the prompt log grew")
	}
	h.put("svc", "s18", hPrompt("svc", "s18", 4, 0, "fix it")+r.turns(s18))
	w := storeWalks.Load()
	if err := EnsureChecked(c, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if storeWalks.Load() == w {
		t.Error("the pass took the check's walk though a transcript arrived after it")
	}
	if err := os.Remove(h.path("svc", "s18")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	r.compare(5, 1)
}

// The same for a Codex rollout filed under a month the store has no
// directory for yet: Codex files them by year, month and day, so the
// directory that changes is two above the ones the walk listed.
func TestARolloutInANewMonthAfterTheCheckIsNotLost(t *testing.T) {
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(5)), index: filepath.Join(h.tmp, "a.db")} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	rollout := func(c *codexRollout, month int) string {
		p := filepath.Join(r.more.codexRoot, "sessions", "2026", fmt.Sprintf("%02d", month), fmt.Sprintf("%02d", c.day),
			fmt.Sprintf("rollout-2026-%02d-%02dT10-00-00-%s.jsonl", month, c.day, c.id))
		writeHistoryFile(r, p, codexLine(c.at(), "session_meta", map[string]any{"id": c.id, "cwd": "/w/app"})+r.codexTurns(c), false)
		return p
	}
	rollout(&codexRollout{id: "019a0000-0000-7000-8000-000000000001", day: 3}, 6)
	rollout(&codexRollout{id: "019a0000-0000-7000-8000-000000000002", day: 4}, 6)
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	b := &codexRollout{id: "019a0000-0000-7000-8000-000000000003", day: 5}
	hist := filepath.Join(r.more.codexRoot, "history.jsonl")
	writeHistoryFile(r, hist, hjson(map[string]any{"session_id": b.id, "ts": b.at().Unix(), "text": "why does the kafka outbox fail"}), false)
	fresh, _, c := CheckUpToDate(r.index, "")
	if fresh || c == nil {
		t.Fatal("the index is up to date after the prompt log grew")
	}
	p := rollout(b, 7)
	w := storeWalks.Load()
	if err := EnsureChecked(c, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if storeWalks.Load() == w {
		t.Error("the pass took the check's walk though a rollout arrived after it")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(r.index, "", false, nil); err != nil {
		t.Fatal(err)
	}
	r.compare(5, 1)
}
