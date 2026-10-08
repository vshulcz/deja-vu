package sources

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func kimiWriteBackSession(home string) model.Session {
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	id := "session_1b2e4e28-4ab8-40cd-b705-b22159519311"
	return model.Session{
		Harness: "kimi", ID: id, Title: "why is the cache cold",
		Path:    filepath.Join(home, "sessions", "wd_demo_ae41f942e4c2", id, "agents", "main", "wire.jsonl"),
		Started: at, Updated: at.Add(time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: "why is the cache cold", Time: at},
			{Role: RoleCommand, Text: "$ redis-cli info", Time: at.Add(time.Second)},
			{Role: "assistant", Text: "the TTL is 0", Time: at.Add(2 * time.Second)},
			{Role: "user", Text: "set it to 60", Time: at.Add(3 * time.Second)},
		},
	}
}

func TestKimiWriteBackReadsBackAndListsTheSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	t.Setenv("DEJA_KIMI_ROOT", "")
	work := filepath.Join(t.TempDir(), "demo")
	if err := os.WriteFile(filepath.Join(home, "workspaces.json"),
		[]byte(`{"version":1,"workspaces":{"wd_demo_ae41f942e4c2":{"root":`+jsonQuote(work)+`,"name":"demo"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := kimiWriteBackSession(home)
	// Kimi's own delete leaves a tombstone after the entry.
	index := `{"sessionId":"` + s.ID + `","sessionDir":"x","workDir":"y"}` + "\n" + `{"sessionId":"` + s.ID + `","deleted":true}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	sidecars, appends, err := WriteBackExtras(s, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(sidecars) != 1 || filepath.Base(sidecars[0].Path) != "state.json" || !strings.Contains(string(sidecars[0].Data), jsonQuote(work)) {
		t.Fatalf("state.json sidecar = %+v", sidecars)
	}
	if len(appends) != 1 || !strings.Contains(string(appends[0].Data), `"workDir":`+jsonQuote(work)) {
		t.Fatalf("index entry after the tombstone = %+v", appends)
	}
	for _, p := range append(sidecars, WriteBackPart{Path: f.Path, Data: f.Data}) {
		if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.Path, p.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := KimiSessionDir(f.Path); got != work {
		t.Errorf("resume directory = %q, want %q", got, work)
	}
	ss, err := ParseKimiFile(f.Path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse back: %v %d", err, len(ss))
	}
	var got []string
	for _, m := range ss[0].Messages {
		got = append(got, m.Role+":"+m.Text)
	}
	want := "user:why is the cache cold|assistant:the TTL is 0|user:set it to 60"
	if strings.Join(got, "|") != want {
		t.Errorf("turns read back = %q, want %q", strings.Join(got, "|"), want)
	}
	if ss[0].ID != s.ID {
		t.Errorf("id = %q", ss[0].ID)
	}

	// A live entry needs nothing appended.
	live := `{"sessionId":"` + s.ID + `","sessionDir":"x","workDir":"y"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, appends, _ := WriteBackExtras(s, f); len(appends) != 0 {
		t.Errorf("appended to an index that already lists the session: %+v", appends)
	}
}

func TestKimiWriteBackRefusesWithoutTheDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	t.Setenv("DEJA_KIMI_ROOT", "")
	_, err := RenderWriteBack(kimiWriteBackSession(home))
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "directory the session ran in") {
		t.Fatalf("err = %v, want a refusal naming the missing directory", err)
	}
}

func jsonQuote(s string) string {
	b, _ := jsonLines([]any{s})
	return strings.TrimSpace(string(b))
}
