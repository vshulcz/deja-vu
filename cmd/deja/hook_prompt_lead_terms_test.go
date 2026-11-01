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
