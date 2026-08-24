package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctor is the surface that answers "where did deja look", so a store it reads
// and never names is a store whose absence from recall cannot be diagnosed.
// Both lists are hand-written; the registry is the one that decides what is
// indexed, so it is what they are checked against (#1738, the shape of #999).
func TestDoctorNamesEveryStoreTheIndexerReads(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := tmp + "/index.db"

	var text bytes.Buffer
	if err := runDoctor(&text, nil, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	inStores := false
	for _, line := range strings.Split(text.String(), "\n") {
		if strings.HasPrefix(line, "Harness stores:") {
			inStores = true
			continue
		}
		if inStores {
			if strings.TrimSpace(line) == "" {
				break
			}
			if f := strings.Fields(line); len(f) > 0 {
				rows[f[0]] = true
			}
		}
	}

	var js bytes.Buffer
	if err := runDoctor(&js, []string{"--json"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name string `json:"name"`
		} `json:"stores"`
	}
	if err := json.Unmarshal(js.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	inJSON := map[string]bool{}
	for _, s := range report.Stores {
		inJSON[s.Name] = true
	}

	for _, h := range sources.Registry() {
		if !rows[h.Name] {
			t.Errorf("doctor never names the %s store in its Harness stores block", h.Name)
		}
		if !inJSON[h.Name] {
			t.Errorf("doctor --json never names the %s store", h.Name)
		}
	}
}

// The two forms have to agree about why a store cannot be read: the text row
// names the missing sqlite3 CLI, and the JSON said "unreadable" — the split
// #999 closed for the stores that existed then.
func TestZedSaysWhySqliteIsWhatIsMissing(t *testing.T) {
	tmp := hermeticEnv(t)
	db := sources.ZedDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	store, _ := inspectDoctorStore(doctorCheckNamed(t, "zed"))
	if store.State != "needs-sqlite3" {
		t.Errorf("zed without the sqlite3 CLI reports %q, while the text row names the CLI", store.State)
	}
	_ = tmp
}

func doctorCheckNamed(t *testing.T, name string) doctorStoreCheck {
	t.Helper()
	for _, c := range doctorStoreChecks() {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no doctor store check named %q", name)
	return doctorStoreCheck{}
}

// The text row says a store's sqlite half is unreadable — "sqlite3 CLI missing
// — IDE sessions unavailable" — while the JSON form called the same store ok.
// Two answers about one store is what #999 closed for the states that existed
// then (#1758).
func TestBothFormsAgreeWhenHalfAStoreNeedsSqlite(t *testing.T) {
	tmp := hermeticEnv(t)
	ide := filepath.Join(tmp, "cursor-user", "globalStorage")
	if err := os.MkdirAll(ide, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file that is not a readable database is enough: the point is the CLI,
	// and without it deja cannot tell one from the other.
	if err := os.WriteFile(filepath.Join(ide, "state.vscdb"), bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(tmp, "cursor-cli", "projects", "p", "agent-transcripts")
	if err := os.MkdirAll(cli, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"c1","cwd":"/w","timestamp":"2026-08-08T01:00:00Z","message":{"role":"user","content":"snorklewhip"}}` + "\n"
	if err := os.WriteFile(filepath.Join(cli, "t.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("PATH", "")

	var check doctorStoreCheck
	for _, c := range doctorStoreChecks() {
		if c.name == "cursor" {
			check = c
		}
	}
	store, _ := inspectDoctorStore(check)
	if store.State == "ok" {
		t.Errorf("the JSON form calls a store ok while its text row says the sqlite3 CLI is missing")
	}
	if store.State != "needs-sqlite3" {
		t.Errorf("state = %q, want needs-sqlite3", store.State)
	}
}
