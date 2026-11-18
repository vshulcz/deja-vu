package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// A block whose every quoted line shares one word with the question is about
// something else; it is not sent. One that quotes a line holding two is.
func TestPromptDigestNeedsAQuoteHoldingTheQuestion(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().Add(-48 * time.Hour)
	sess := func(id string, texts ...string) model.Session {
		s := model.Session{ID: id, Harness: "claude", Project: "beta", Started: at, Updated: at}
		for k, text := range texts {
			role := "user"
			if k%2 == 1 {
				role = "assistant"
			}
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: at.Add(time.Duration(k) * time.Second)})
		}
		return s
	}
	terms := []string{"gizmoflux", "warmup", "staging"}
	apart := sess("apart", "the gizmoflux dashboard is slow today", "Noted, I will look at the dashboard layout.",
		"move the staging banner to the left", "Moved the banner on staging.")
	if body, ok := promptDigestBody(dir, "asker", []model.Session{apart}, true, terms, nil, "gizmoflux warmup on staging?", "", ""); ok {
		t.Errorf("a block whose lines each hold one word of the question was sent:\n%s", body)
	}
	together := sess("together", "why is the gizmoflux warmup so slow on staging", "The gizmoflux warmup waited on the cold cache; staging now preloads it.")
	body, ok := promptDigestBody(dir, "asker", []model.Session{together}, true, terms, nil, "gizmoflux warmup on staging?", "", "")
	if !ok || !strings.Contains(body, "preloads") {
		t.Errorf("the session that said the words together was not sent (ok=%v):\n%s", ok, body)
	}
}

// A long session holds every word of any question somewhere, each in another
// turn about another thing; that is not a match. The session that said the
// words together is.
func TestPromptHookWantsTheLeadWordsTogether(t *testing.T) {
	withStatsStores(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC()
	line := func(id, role, text string, k int) string {
		return jsonLine(t, map[string]any{"type": role, "sessionId": id,
			"timestamp": ts.Add(time.Duration(k) * time.Second).Format(time.RFC3339),
			"message":   map[string]any{"role": role, "content": text}})
	}
	// A marathon: each word of the question once, far apart.
	long := make([]string, 0, 340)
	for k := range 340 {
		text := fmt.Sprintf("шаг %d: правлю вёрстку карточки", k)
		switch k {
		case 40:
			text = "сегодня смотрим расписание электричек"
		case 200:
			text = "потом разберём загрузку"
		case 250:
			text = "и фотографии на главной"
		case 300:
			text = "и ещё про сжатие архивов"
		}
		role := "user"
		if k%2 == 1 {
			role = "assistant"
		}
		long = append(long, line("marathon", role, text, k))
	}
	writeClaudeFixture(t, filepath.Join(root, "beta", "marathon.jsonl"), "marathon", long)
	writeClaudeFixture(t, filepath.Join(root, "beta", "focused.jsonl"), "focused", []string{
		line("focused", "user", "почему падает загрузка фотографий после сжатия?", 0),
		line("focused", "assistant", "Загрузка фотографий падала: сжатие отдавало пустой файл, поправил порядок шагов.", 1),
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	ask := func(prompt string) string {
		var out bytes.Buffer
		in, err := json.Marshal(map[string]string{"prompt": prompt, "session_id": "asking-" + prompt[:6]})
		if err != nil {
			t.Fatal(err)
		}
		if err := runHookPromptMode(index.DefaultDir(), bytes.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	got := ask("снова сломалась загрузка фотографий, глянь сжатие")
	if strings.Contains(got, "marathon") || strings.Contains(got, "электрич") {
		t.Errorf("the marathon that holds the words apart was recalled:\n%s", got)
	}
	if !strings.Contains(got, "пустой файл") {
		t.Errorf("the session that said them together was not recalled:\n%s", got)
	}
	// Words of the question met only in the marathon, never together.
	if got := ask("а электрички и архивы как там?"); strings.Contains(got, "marathon") {
		t.Errorf("words met only far apart were taken as a match:\n%s", got)
	}
}

// padStoreWithOtherWork writes sessions about something else entirely into
// another project, about 150 counted messages, so a word said once or twice
// is rare by the fixed floor (search.IdentifyingTerms) and not only by the
// ceiling a small store sets. For tests about rarity on a store of real size.
func padStoreWithOtherWork(t *testing.T, claudeRoot string) {
	t.Helper()
	at := time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)
	for s := range 6 {
		id := fmt.Sprintf("rota%d", s)
		lines := make([]string, 0, 26)
		for k := range 26 {
			role := "user"
			if k%2 == 1 {
				role = "assistant"
			}
			lines = append(lines, jsonLine(t, map[string]any{"type": role, "sessionId": id,
				"timestamp": at.Add(time.Duration(s*100+k) * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": role, "content": fmt.Sprintf("kitchen rota week %d: soup on monday, bread on friday", k)}}))
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-gamma", id+".jsonl"), id, lines)
	}
}

// askOneWordInSmallStore indexes the given sessions, each a user line and an
// agent line, and asks the prompt hook the one word "zarvoxd".
func askOneWordInSmallStore(t *testing.T, sessions map[string][2]string) string {
	t.Helper()
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC()
	for id, said := range sessions {
		lines := make([]string, 0, 2)
		for k, role := range []string{"user", "assistant"} {
			lines = append(lines, jsonLine(t, map[string]any{"type": role, "sessionId": id,
				"timestamp": at.Add(time.Duration(k) * time.Second).Format(time.RFC3339),
				"message":   map[string]any{"role": role, "content": said[k]}}))
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-delta", id+".jsonl"), id, lines)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "delta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(`{"prompt":"zarvoxd"}`), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A new user's store is a session or two, which can never give a word the
// fixed rareness floor. A word only one session says is as rare as that store
// allows, and a one-word question about it still gets the memory.
func TestASmallStoreStillAnswersAOneWordQuestion(t *testing.T) {
	answer := [2]string{"the zarvoxd cache keeps going stale", "zarvoxd now refreshes its cache every minute"}
	other := [2]string{"move the footer links to the left", "moved them and kept the spacing"}
	for name, store := range map[string]map[string][2]string{
		"one session":  {"zv": answer},
		"two sessions": {"zv": answer, "footer": other},
	} {
		t.Run(name, func(t *testing.T) {
			got := askOneWordInSmallStore(t, store)
			if !strings.Contains(got, "refreshes its cache") {
				t.Errorf("a one-word question on a small store got no memory:\n%s", got)
			}
			// And the ranking gives that word at least the ceiling the floor
			// is taken from, whatever the message counts.
			_, _, _, _, idf, err := index.ProjectRelevantNamingCut(index.DefaultDir(), nil, []string{"zarvoxd"}, 5, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if top := index.TopIDF(index.DefaultDir()); idf["zarvoxd"] < top {
				t.Errorf("idf of a one-session word = %v, under TopIDF = %v", idf["zarvoxd"], top)
			}
		})
	}
}

// The ceiling is not a pass for every word: one that every session says names
// none of them.
func TestAWordEverySessionSaysNamesNothing(t *testing.T) {
	got := askOneWordInSmallStore(t, map[string][2]string{
		"zv1": {"the zarvoxd cache keeps going stale", "zarvoxd now refreshes its cache every minute"},
		"zv2": {"zarvoxd logs are too noisy", "turned zarvoxd logging down to warnings"},
	})
	if strings.Contains(got, "zarvoxd") {
		t.Errorf("a word every session says was taken as naming one of them:\n%s", got)
	}
}

func TestPromptHookUnscopedCwdRequiresNamedSubject(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC()
	lines := []string{
		jsonLine(t, map[string]any{"type": "user", "sessionId": "other1",
			"timestamp": at.Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": "почему падает загрузка фотографий в zarvoxd после сжатия?"}}),
		jsonLine(t, map[string]any{"type": "assistant", "sessionId": "other1",
			"timestamp": at.Add(time.Second).Format(time.RFC3339),
			"message":   map[string]any{"role": "assistant", "content": "Загрузка фотографий падала: сжатие отдавало пустой файл в zarvoxd."}}),
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-otherproj", "other1.jsonl"), "other1", lines)
	padStoreWithOtherWork(t, claudeRoot)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	var out bytes.Buffer
	in := jsonLine(t, map[string]string{"prompt": "снова сломалась загрузка фотографий, глянь сжатие", "cwd": home, "session_id": "ask-1"})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "пустой файл") {
		t.Fatalf("unscoped cwd injected unrelated project on generic words:\n%s", out.String())
	}

	out.Reset()
	inNamed := jsonLine(t, map[string]string{"prompt": "что там было с zarvoxd и сжатие?", "cwd": home, "session_id": "ask-2"})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(inNamed), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "zarvoxd") {
		t.Fatalf("unscoped cwd with named subject failed to recall:\n%s", out.String())
	}
}

func TestPromptHookAllLatinMultiClauseAndDistinctDateOverflow(t *testing.T) {
	t.Run("all-latin multi-clause recall", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		padStoreWithOtherWork(t, claudeRoot)

		cwd := filepath.Join(t.TempDir(), "tmp", "alpha")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		writeSession := func(id, userText, assistantText string, offsetDays int) {
			at := now.AddDate(0, 0, offsetDays)
			lines := []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": userText},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": id, "cwd": cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": assistantText},
				}),
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", id+".jsonl"), id, lines)
		}

		writeSession("major-1",
			"I switched my academic major to astrophysics after advisor meeting",
			"Recorded your academic major switch to astrophysics.", -6)
		writeSession("planetarium-1",
			"I visited the downtown planetarium with astronomy club",
			"Logged your downtown planetarium visit.", -4)

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "what academic major did I switch to after advisor meeting or downtown planetarium visit?",
			"cwd":        cwd,
			"session_id": "ask-latin-1",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "astrophysics") {
			t.Fatalf("all-latin multi-clause query missed astrophysics session:\n%s", out.String())
		}
	})

	t.Run("distinct-date third slot and overflow hint", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		padStoreWithOtherWork(t, claudeRoot)

		cwd := filepath.Join(t.TempDir(), "tmp", "alpha")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		days := []int{-40, -25, -10, -2}
		markers := []string{"milestone_q1", "milestone_q2", "milestone_q3", "milestone_q4"}
		details := []string{
			"covering storage compaction and snapshot recovery.",
			"focusing on federation routing and quota telemetry.",
			"delivering webhook replay and audit exports.",
			"completing canary rollback and chaos drills.",
		}
		for idx, offset := range days {
			id := fmt.Sprintf("mile-%d", idx+1)
			at := now.AddDate(0, 0, offset)
			lines := []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": "what is our roadmap milestone target for this quarter?"},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": id, "cwd": cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": "The roadmap milestone target is " + markers[idx] + " " + details[idx]},
				}),
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", id+".jsonl"), id, lines)
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "what is our roadmap milestone target?",
			"cwd":        cwd,
			"session_id": "ask-dates-1",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		found := 0
		for _, m := range markers {
			if strings.Contains(got, m) {
				found++
			}
		}
		if found < 3 {
			t.Fatalf("expected 3 distinct-date milestone sessions in digest, got %d:\n%s", found, got)
		}
		if !strings.Contains(got, "+1 more matching sessions — run deja recall to check all") {
			t.Fatalf("expected overflow hint line for 4th matching session, got:\n%s", got)
		}
	})

	t.Run("verbose first session shares budget across distinct dates", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		padStoreWithOtherWork(t, claudeRoot)

		cwd := filepath.Join(t.TempDir(), "tmp", "beta")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		longBody := "Currently two backup keys remain active after rotating BK_FIRST_VERBOSE. " +
			"We audited every node in the ring and verified BK_FIRST_VERBOSE checksums match. " +
			strings.Repeat("The backup key verification audit completed with clean ledger status for all active shards. ", 9)
		specs := []struct {
			id     string
			offset int
			body   string
		}{
			{"bk-1", -30, longBody},
			{"bk-2", -15, "Currently three backup keys remain active after enrolling BK_SECOND_UPDATE."},
			{"bk-3", -2, "Currently four backup keys remain active after provisioning BK_THIRD_LATEST."},
		}
		for _, sp := range specs {
			at := now.AddDate(0, 0, sp.offset)
			lines := []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": "how many backup keys are active now?"},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.body},
				}),
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-beta", sp.id+".jsonl"), sp.id, lines)
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "how many backup keys are active now?",
			"cwd":        cwd,
			"session_id": "ask-bk-1",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		for _, want := range []string{"BK_FIRST_VERBOSE", "BK_SECOND_UPDATE", "BK_THIRD_LATEST"} {
			if !strings.Contains(got, want) {
				t.Fatalf("expected %s to share digest budget without starvation:\n%s", want, got)
			}
		}
	})
}

func encodeClaudeTestProjectDir(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	if len(s) > 200 {
		return s[:200] + "-test"
	}
	return s
}

func seedMediumFillerSessions(t *testing.T, claudeRoot string, count int) {
	t.Helper()
	now := time.Now().UTC()
	for i := range count {
		id := fmt.Sprintf("med-pad-%02d", i)
		at := now.AddDate(0, 0, -(40 + i))
		cwd := fmt.Sprintf("/tmp/medpad-%02d", i)
		proj := encodeClaudeTestProjectDir(cwd)
		lines := []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": fmt.Sprintf("inspect routine shard %d latency and disk metrics", i)},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": cwd,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Checked routine shard %d status; latency within normal bounds.", i)},
			}),
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, proj, id+".jsonl"), id, lines)
	}
}

func TestPromptHookMediumStoreMultiSessionSubjectUpdate(t *testing.T) {
	t.Run("generic modifier does not block multi-session subject", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 32)
		now := time.Now().UTC()

		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"sc-1", -20, "/tmp/life-1", "Currently I own three cargo scooters parked in the shed.", "Noted your three cargo scooters."},
			{"sc-2", -5, "/tmp/life-2", "I added a folding touring scooter, so with my road scooter I have four scooters total.", "Updated count: four scooters."},
			{"sc-noise", -2, "/tmp/life-3", "I own my own notes and own schedule across own projects every own day.", "Tracked your own schedule notes."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "How many scooters do I currently own?",
			"cwd":        home,
			"session_id": "ask-scooters",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "three cargo") || !strings.Contains(got, "four scooters") {
			t.Fatalf("expected both scooter sessions in digest, got:\n%s", got)
		}
		if strings.Contains(got, "schedule notes") {
			t.Fatalf("expected repetitive 'own' noise session excluded, got:\n%s", got)
		}
	})

	t.Run("subject update matched via speech inflection", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 26)
		now := time.Now().UTC()

		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"mir-1", -18, "/tmp/life-1", "My friend Miriam decided to move so she could live in Denver for her studio job.", "Saved Miriam's Denver move."},
			{"mir-2", -3, "/tmp/life-2", "My friend Miriam actually just moved back to the highlands again.", "Got it, Miriam is back in the highlands."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "Where did Miriam move to live?",
			"cwd":        home,
			"session_id": "ask-miriam",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "Denver") || !strings.Contains(got, "highlands") {
			t.Fatalf("expected both Miriam relocation sessions in digest, got:\n%s", got)
		}
	})
}

func TestPromptHookWhenClausePairedAndAbsentSubject(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	seedMediumFillerSessions(t, claudeRoot, 30)
	now := time.Now().UTC()

	for _, sp := range []struct {
		id     string
		offset int
		cwd    string
		user   string
		asst   string
	}{
		{"wh-1", -25, "/tmp/life-a", "I attended the ceramic workshop at the pottery studio today.", "Logged WORKSHOP_MAR_10 ceramic studio notes."},
		{"wh-2", -5, "/tmp/life-b", "I glazed a stoneware bowl as a gift for my cousin tonight.", "Logged GIFT_APR_02 stoneware bowl gift."},
	} {
		at := now.AddDate(0, 0, sp.offset)
		proj := encodeClaudeTestProjectDir(sp.cwd)
		writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": sp.user},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": sp.asst},
			}),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	var pairedOut bytes.Buffer
	pairedIn := jsonLine(t, map[string]string{
		"prompt":     "How many days passed between when I attended the ceramic workshop at the local pottery studio and when I glazed a stoneware bowl for my cousin?",
		"cwd":        home,
		"session_id": "ask-when-paired",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(pairedIn), &pairedOut, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pairedOut.String(), "WORKSHOP_MAR_10") || !strings.Contains(pairedOut.String(), "GIFT_APR_02") {
		t.Fatalf("expected both when-clause event sessions in digest, got:\n%s", pairedOut.String())
	}

	var absentOut bytes.Buffer
	absentIn := jsonLine(t, map[string]string{
		"prompt":     "How many days passed when the quasar rotator ran beside the workshop?",
		"cwd":        home,
		"session_id": "ask-when-absent",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(absentIn), &absentOut, true); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(absentOut.String()) != "" {
		t.Fatalf("expected absent-subject when-query to suppress injection, got:\n%s", absentOut.String())
	}
}

func TestPromptHookScopedRareSubjectDistinctDateAndOrQuery(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	projDir := filepath.Join(t.TempDir(), "notebook")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projSlug := encodeClaudeTestProjectDir(projDir)
	now := time.Now().UTC()

	for i := range 112 {
		id := fmt.Sprintf("pad-%03d", i)
		at := now.AddDate(0, 0, -(40 + i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, id+".jsonl"), id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": fmt.Sprintf("inspect routine shard %d latency and disk metrics", i)},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Checked routine shard %d status; latency within normal bounds.", i)},
			}),
		})
	}

	for _, sp := range []struct {
		id     string
		offset int
		user   string
		asst   string
	}{
		{"r-1", -20, "Rachel is planning a relocation to Chicago next month.", "Got it, noted Rachel's upcoming relocation to Chicago."},
		{"r-2", -3, "Rachel actually just moved back to the suburbs this week.", "Updated: Rachel moved back to the suburbs."},
		{"p-1", -15, "I have led the gateway project for two quarters.", "Recorded the gateway project you led."},
		{"p-2", -4, "Currently leading the billing project migration too.", "Recorded the billing project you are currently leading."},
	} {
		at := now.AddDate(0, 0, sp.offset)
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, sp.id+".jsonl"), sp.id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": sp.user},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": sp.asst},
			}),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projDir)

	var relocOut bytes.Buffer
	relocIn := jsonLine(t, map[string]string{
		"prompt":     "Where did Rachel move to after her recent relocation?",
		"cwd":        projDir,
		"session_id": "ask-relocation",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(relocIn), &relocOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Chicago", "suburbs"} {
		if !strings.Contains(relocOut.String(), want) {
			t.Fatalf("expected %q in relocation digest, got:\n%s", want, relocOut.String())
		}
	}

	var projOut bytes.Buffer
	projIn := jsonLine(t, map[string]string{
		"prompt":     "How many projects have I led or am currently leading?",
		"cwd":        projDir,
		"session_id": "ask-projects",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(projIn), &projOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gateway", "billing"} {
		if !strings.Contains(projOut.String(), want) {
			t.Fatalf("expected %q in projects digest, got:\n%s", want, projOut.String())
		}
	}
}

func TestPromptHookMediumStoreGenericModifierAndSingleRareSubject(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	projDir := filepath.Join(t.TempDir(), "notebook")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projSlug := encodeClaudeTestProjectDir(projDir)
	now := time.Now().UTC()

	for i := range 96 {
		id := fmt.Sprintf("pad-%03d", i)
		at := now.AddDate(0, 0, -(40 + i))
		text := fmt.Sprintf("inspect routine shard %d latency and own metrics", i)
		switch {
		case i < 16:
			text = fmt.Sprintf("inspect model variant %d latency and own metrics", i)
		case i < 19:
			text = fmt.Sprintf("recently worked and bought spare cables for shard %d", i)
		case i < 22:
			text = fmt.Sprintf("currently reviewing shard %d ownership metrics", i)
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, id+".jsonl"), id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": text},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Recorded shard %d metrics.", i)},
			}),
		})
	}

	for _, sp := range []struct {
		id     string
		offset int
		user   string
		asst   string
	}{
		{"b-1", -18, "I currently own three bikes in the shed.", "Noted three bikes currently in the shed."},
		{"b-2", -3, "I will have four bikes now after fixing the frame.", "Got it, four bikes total now."},
		{"m-1", -14, "I worked on two model kits over the weekend.", "Logged the two model kits you worked on."},
		{"m-2", -2, "Picked up three additional model kits at the hobby counter.", "Added three additional model kits to your list."},
	} {
		at := now.AddDate(0, 0, sp.offset)
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, sp.id+".jsonl"), sp.id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": sp.user},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": sp.asst},
			}),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projDir)

	var bikeOut bytes.Buffer
	bikeIn := jsonLine(t, map[string]string{
		"prompt":     "How many bikes do I currently own?",
		"cwd":        projDir,
		"session_id": "ask-bikes",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(bikeIn), &bikeOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"three bikes", "four bikes"} {
		if !strings.Contains(bikeOut.String(), want) {
			t.Fatalf("expected %q in bikes digest, got:\n%s", want, bikeOut.String())
		}
	}

	var kitOut bytes.Buffer
	kitIn := jsonLine(t, map[string]string{
		"prompt":     "How many model kits have I worked on or bought?",
		"cwd":        projDir,
		"session_id": "ask-kits",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(kitIn), &kitOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"two model kits", "three additional model kits"} {
		if !strings.Contains(kitOut.String(), want) {
			t.Fatalf("expected %q in model kits digest, got:\n%s", want, kitOut.String())
		}
	}
}

func TestPromptHookBetweenEventsSharesBudgetAndWeakFallback(t *testing.T) {
	t.Run("between event recall across dates", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 30)
		now := time.Now().UTC()

		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"evt-1", -24, "/tmp/events-a", "Visited the hilltop observatory tour last evening.", "Logged OBSERVATORY_TOUR_1 notes."},
			{"evt-2", -4, "/tmp/events-b", "Attended the weekend planetarium talk with friends.", "Logged PLANETARIUM_TALK_2 notes."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "How many days passed between the observatory tour and the planetarium talk?",
			"cwd":        home,
			"session_id": "ask-between-events",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "OBSERVATORY_TOUR_1") || !strings.Contains(got, "PLANETARIUM_TALK_2") {
			t.Fatalf("expected both between-clause sessions in digest, got:\n%s", got)
		}
	})

	t.Run("multi-session digest shares budget across bulky update", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 30)
		now := time.Now().UTC()

		longUser := "My friend Nadia decided to relocate to Seattle after finishing the catalog review, packing every crate carefully and coordinating the freight schedule for weeks before the truck departed."
		longAsst := "Noted that Nadia completed her Seattle relocation after wrapping up the catalog review, packing crates carefully and scheduling the freight truck departure over several busy weeks."
		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"nad-1", -21, "/tmp/life-n1", longUser, longAsst},
			{"nad-2", -3, "/tmp/life-n2", "My friend Nadia actually just moved back to the lakefront cottage this week.", "Updated: Nadia moved back to the lakefront cottage."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "Where did Nadia move to after her recent relocation?",
			"cwd":        home,
			"session_id": "ask-nadia-reloc",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "Seattle") || !strings.Contains(got, "lakefront") {
			t.Fatalf("expected both earlier and updated relocation sessions in digest, got:\n%s", got)
		}
	})

	t.Run("speculative unconfident match falls back to weak pointer", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		projDir := filepath.Join(t.TempDir(), "svc")
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			t.Fatal(err)
		}
		projSlug := encodeClaudeTestProjectDir(projDir)
		now := time.Now().UTC()

		for i := range 90 {
			id := fmt.Sprintf("fill-%02d", i)
			at := now.AddDate(0, 0, -(40 + i))
			user := fmt.Sprintf("Inspect worker pool %d metrics.", i)
			if i < 30 {
				user = fmt.Sprintf("Audit worker pool %d metrics.", i)
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, id+".jsonl"), id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": projDir,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": id, "cwd": projDir,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Worker pool %d metrics healthy.", i)},
				}),
			})
		}

		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, "other-ve.jsonl"), "other-ve", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "other-ve", "cwd": projDir,
				"timestamp": now.AddDate(0, 0, -20).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "Deployed vault_exporter on staging."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "other-ve", "cwd": projDir,
				"timestamp": now.AddDate(0, 0, -20).Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Verified vault_exporter scrape endpoint."},
			}),
		})

		at := now.AddDate(0, 0, -5)
		longSetup := "Audit the standard quarterly operational review agenda step by step across every regional service group, verifying routine checklist signoffs, dashboard owners, incident handoff sheets, capacity planning worksheets, SLA adherence notes, backlog queue depth metrics, pager duty schedules, and weekly escalation rotations before finally inspecting quartz_scheduler."
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, "target-qs.jsonl"), "target-qs", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "target-qs", "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": longSetup},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "target-qs", "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Ack: logged operational review agenda items."},
			}),
		})

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		t.Chdir(projDir)

		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "Did we audit quartz_scheduler or vault_exporter?",
			"cwd":        projDir,
			"session_id": "ask-qs",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "this project has history") && !strings.Contains(got, "deja recall") {
			t.Fatalf("expected weak recall pointer fallback when speculative digest lacks 2-term quote, got:\n%s", got)
		}
	})
}

func TestPromptHookAdvicePointerAndUpdatePrecision(t *testing.T) {
	writePair := func(t *testing.T, claudeRoot string, ts time.Time, id, cwd, userText, asstText string) {
		t.Helper()
		proj := encodeClaudeTestProjectDir(cwd)
		writeClaudeFixture(t, filepath.Join(claudeRoot, proj, id+".jsonl"), id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
				"timestamp": ts.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": userText},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": cwd,
				"timestamp": ts.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": asstText},
			}),
		})
	}

	t.Run("open-ended advice prompt emits weak pointer while again follow-up emits full digest", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 36)
		now := time.Now().UTC()

		writePair(t, claudeRoot, now.AddDate(0, 0, -4), "cook-pref", "/tmp/home-a",
			"I recently made beef stew in my slow cooker and want vegetarian slow cooker ideas too.",
			"Low and slow works best for stew; preheat the ceramic insert for custard or yogurt.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -6), "lisbon-trip", "/tmp/home-b",
			"I'm planning a trip to Lisbon, which neighborhood do you suggest for quiet cafes?",
			"Try Principe Real in Lisbon for quiet leafy squares and morning espresso spots.")

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var adviceOut bytes.Buffer
		adviceIn := jsonLine(t, map[string]string{
			"prompt":     "I've been struggling with my slow cooker recipes. Any advice on getting better results?",
			"cwd":        home,
			"session_id": "ask-advice-cook",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(adviceIn), &adviceOut, true); err != nil {
			t.Fatal(err)
		}
		adviceGot := adviceOut.String()
		if !strings.Contains(adviceGot, "this project has history") && !strings.Contains(adviceGot, "call deja") {
			t.Fatalf("expected weak recall pointer on open-ended advice prompt, got:\n%s", adviceGot)
		}
		if strings.Contains(adviceGot, "deja:cook-pref") {
			t.Fatalf("expected open-ended advice prompt to avoid full digest block, got:\n%s", adviceGot)
		}

		var againOut bytes.Buffer
		againIn := jsonLine(t, map[string]string{
			"prompt":     "I'm planning my trip to Lisbon again, what neighborhood did you recommend?",
			"cwd":        home,
			"session_id": "ask-again-lisbon",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(againIn), &againOut, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(againOut.String(), "Principe Real") {
			t.Fatalf("expected full digest carrying Principe Real on again follow-up, got:\n%s", againOut.String())
		}
	})

	t.Run("multi-term update rejects 1-word noise and orders newest date first", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 36)
		now := time.Now().UTC()

		writePair(t, claudeRoot, now.AddDate(0, 0, -2), "coupon-1", "/tmp/errands-a",
			"I redeemed a $5 coupon on coffee creamer last Sunday.",
			"Using Cartwheel at Target is a handy way to catch surprise coupons on groceries.")
		projErrandsMulti := encodeClaudeTestProjectDir("/tmp/errands-multi")
		writeClaudeFixture(t, filepath.Join(claudeRoot, projErrandsMulti, "coupon-multi.jsonl"), "coupon-multi", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "I have been using the Cartwheel app from Target to organize coupons."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "A coupon binder also pairs nicely with digital app clips."},
			}),
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Add(2 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "I actually redeemed a $5 coupon on coffee creamer last Sunday."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Add(3 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Finding unexpected discounts in your inbox is always a nice bonus."},
			}),
		})
		writePair(t, claudeRoot, now.AddDate(0, 0, -7), "coffee-table", "/tmp/errands-b",
			"I will clear the clutter off the coffee table before organizing cables.",
			"Starting with the coffee table surface will make sorting remotes much faster.")

		writePair(t, claudeRoot, now.AddDate(0, 0, -10), "reloc-old", "/tmp/reloc-a",
			"Rachel is planning a relocation to the city soon; she moved to Chicago.",
			"Sounds exciting to plan a visit once Rachel settles in Chicago.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -2), "reloc-new", "/tmp/reloc-a",
			"My friend Rachel actually just moved back to the suburbs again.",
			"Updated note: Rachel relocated back to the suburbs this week.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -15), "author-qa", "/tmp/reloc-a",
			"I got to ask the author Rachel Smith a question during the bookstore Q&A.",
			"Glad you got to hear Rachel Smith discuss her novel at the Q&A.")

		projMovies := encodeClaudeTestProjectDir("/tmp/movies-a")
		writeClaudeFixture(t, filepath.Join(claudeRoot, projMovies, "watch-marathon.jsonl"), "watch-marathon", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "I just finished a Star Wars marathon, watched all the main films in a week and a half!"},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Sounds like a fun movie marathon!"},
			}),
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Add(2 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "Yes, I will add those Star Wars films to the simple list, plus Rogue One and Solo, along with several recent releases and documentaries we talked about earlier before adding all the Marvel Cinematic Universe movies too."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Add(3 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "I added both franchises to your list."},
			}),
		})

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var couponOut bytes.Buffer
		couponIn := jsonLine(t, map[string]string{
			"prompt":     "Where did I redeem a $5 coupon on coffee creamer?",
			"cwd":        "/tmp/errands-multi",
			"session_id": "ask-coupon-adj",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(couponIn), &couponOut, true); err != nil {
			t.Fatal(err)
		}
		couponGot := couponOut.String()
		if !strings.Contains(couponGot, "Target") || !strings.Contains(couponGot, "deja:coupon-m") {
			t.Fatalf("expected adjacent user Target context and deja:coupon-m, got:\n%s", couponGot)
		}
		if strings.Contains(couponGot, "coffee table") {
			t.Fatalf("expected 1-word coffee table distractor excluded, got:\n%s", couponGot)
		}

		var relocOut bytes.Buffer
		relocIn := jsonLine(t, map[string]string{
			"prompt":     "Where did Rachel move to after her recent relocation?",
			"cwd":        "/tmp/reloc-a",
			"session_id": "ask-rachel-reloc",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(relocIn), &relocOut, true); err != nil {
			t.Fatal(err)
		}
		relocGot := relocOut.String()
		if strings.Contains(relocGot, "Rachel Smith") {
			t.Fatalf("expected in-scope 1-word author Q&A distractor excluded, got:\n%s", relocGot)
		}
		idxSuburbs := strings.Index(relocGot, "reloc-new")
		idxChicago := strings.Index(relocGot, "reloc-old")
		if idxSuburbs < 0 || idxChicago < 0 || idxSuburbs > idxChicago {
			t.Fatalf("expected newer reloc-new before reloc-old, got:\n%s", relocGot)
		}
		if !strings.Contains(relocGot, "deja:reloc-new") {
			t.Fatalf("expected opener line to cite newer reloc-new session, got:\n%s", relocGot)
		}

		var marathonOut bytes.Buffer
		marathonIn := jsonLine(t, map[string]string{
			"prompt":     "How many weeks did it take me to watch all the Marvel Cinematic Universe movies and the main Star Wars films?",
			"cwd":        "/tmp/movies-a",
			"session_id": "ask-marathon",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(marathonIn), &marathonOut, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(marathonOut.String(), "week and a half") {
			t.Fatalf("expected earlier user quantity turn with 'week and a half', got:\n%s", marathonOut.String())
		}
	})

	t.Run("partial-subject sessions excluded from third slot and overflow", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 36)
		now := time.Now().UTC()

		writePair(t, claudeRoot, now.AddDate(0, 0, -2), "fam-paris", "/tmp/travel-1",
			"We just returned from our recent family trip to Paris.",
			"Logged your recent family trip to Paris.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -5), "fam-hawaii", "/tmp/travel-2",
			"Our recent family trip to Hawaii had great snorkeling spots.",
			"Logged your recent family trip to Hawaii.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -10), "solo-yosemite", "/tmp/travel-3",
			"Testing water filters on my recent solo trip to Yosemite.",
			"Noted gear test from your solo trip to Yosemite.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -12), "solo-moab", "/tmp/travel-4",
			"Packing repair kit for my recent solo trip to Moab.",
			"Noted packing list for your solo trip to Moab.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -14), "solo-banff", "/tmp/travel-5",
			"Checking trail maps for my recent solo trip to Banff.",
			"Noted maps for your solo trip to Banff.")

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var tripOut bytes.Buffer
		tripIn := jsonLine(t, map[string]string{
			"prompt":     "Where did I go on my most recent family trip?",
			"cwd":        home,
			"session_id": "ask-family-trip",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(tripIn), &tripOut, true); err != nil {
			t.Fatal(err)
		}
		tripGot := tripOut.String()
		if !strings.Contains(tripGot, "Paris") || !strings.Contains(tripGot, "Hawaii") {
			t.Fatalf("expected both Paris and Hawaii family trip sessions, got:\n%s", tripGot)
		}
		if strings.Contains(tripGot, "Yosemite") || strings.Contains(tripGot, "more matching sessions") {
			t.Fatalf("expected Yosemite excluded and no overflow hint, got:\n%s", tripGot)
		}
	})
}

func mustJSONBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHookPromptIndexedSessionSurfacesPostEditRootCauseOverEarlyGuess(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	now := time.Now().UTC()

	repoDir := filepath.Join(t.TempDir(), "work", "relsite")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "pkg", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseAbs := filepath.Join(repoDir, "pkg", "models", "base.py")
	if err := os.WriteFile(baseAbs, []byte("class ModelBase:\n    def _prepare_related_fields_for_save(self):\n        pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := encodeClaudeTestProjectDir(repoDir)
	ts := func(mins int) string {
		return now.Add(-24*time.Hour + time.Duration(mins)*time.Minute).Format(time.RFC3339)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, proj, "past-root-cause.jsonl"), "past-root-cause", []string{
		jsonLine(t, map[string]any{
			"type": "user", "entrypoint": "cli", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(0),
			"message": map[string]any{
				"role":    "user",
				"content": "Saving parent object after setting on child loses primary key\n\nWhen assigning a parent object before save, saving the parent later drops the primary key column in ModelBase.",
			},
		}),
		jsonLine(t, map[string]any{
			"type": "assistant", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(1),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Now I understand the issue! In the `__set__` method at line 257, it copies the primary key value at assignment time. Let me make this fix:",
					},
				},
			},
		}),
		jsonLine(t, map[string]any{
			"type": "assistant", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(2),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "e1",
						"name":  "Edit",
						"input": map[string]any{"file_path": baseAbs, "old_string": "pass", "new_string": "self._sync_fk()"},
					},
					map[string]any{
						"type":  "tool_use",
						"id":    "b1",
						"name":  "Bash",
						"input": map[string]any{"command": "pytest tests/test_models.py"},
					},
				},
			},
		}),
		jsonLine(t, map[string]any{
			"type": "user", "entrypoint": "cli", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(3),
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "e1", "content": "ok"},
					map[string]any{"type": "tool_result", "tool_use_id": "b1", "content": "2 passed in 0.12s"},
				},
			},
		}),
		jsonLine(t, map[string]any{
			"type": "assistant", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(4),
			"message": map[string]any{
				"role":    "assistant",
				"content": "## Bug Summary\nSaving parent object after assignment lost the primary key column.\n\n## Root Cause\nIn pkg/models/base.py, `_prepare_related_fields_for_save` must refresh the related primary key attribute from the parent object before saving.",
			},
		}),
	})

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	payload := string(mustJSONBytes(t, map[string]string{
		"session_id": "live-asker",
		"cwd":        repoDir,
		"prompt":     "Saving parent object with non-numeric primary key fails after child assignment\n\nModelBase drops the primary key column on save after related object assignment.",
	}))
	var out bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "_prepare_related_fields_for_save") {
		t.Fatalf("expected post-edit Root Cause quoting _prepare_related_fields_for_save, got:\n%s", got)
	}
	if strings.Contains(got, "line 257") || strings.Contains(got, "Let me make this fix") {
		t.Fatalf("expected early hypothesis suppressed in favor of post-edit Root Cause, got:\n%s", got)
	}
}
