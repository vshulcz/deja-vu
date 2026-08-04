package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One command, two answers about the same store: the text row said `found`
// where the machine form said `unreadable`, `unplugged` existed only in the
// text, and cline and roo were in neither the JSON nor the list that decides
// whether this machine has any history at all (#999).
func TestDoctorsTwoFormsAgreeAboutEveryStore(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")

	names := map[string]bool{}
	for _, check := range doctorStoreChecks() {
		names[check.name] = true
	}
	for _, want := range []string{"cline", "roo"} {
		if !names[want] {
			t.Errorf("%s is missing from the store list the JSON form and the history check both read", want)
		}
	}

	// A store nobody can parse says so in both forms.
	oc := filepath.Join(tmp, "opencode.db")
	if err := os.WriteFile(oc, []byte("not a db"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", oc)

	var text bytes.Buffer
	doctorHarnesses(&text, dir)
	row := harnessRow(t, text.String(), "opencode")
	if strings.Contains(row, "found") {
		t.Errorf("the text row calls an unreadable store found: %q", row)
	}

	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--json"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"stores"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, s := range report.Stores {
		states[s.Name] = s.State
	}
	// Without the sqlite3 CLI — which CI on windows does not have — the same
	// store is `needs-sqlite3`; either way it is not a healthy one, and the
	// point is that both forms say the same word.
	switch states["opencode"] {
	case "unreadable", "needs-sqlite3":
	default:
		t.Errorf("the machine form calls it %q", states["opencode"])
	}
	if !strings.Contains(row, states["opencode"]) {
		t.Errorf("the two forms use different words: %q vs %q", row, states["opencode"])
	}

	// An empty notes file is not a store with something in it, in either form.
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	if err := os.WriteFile(notes, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runDoctor(&out, []string{"--json"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for _, s := range report.Stores {
		if s.Name == "deja" && s.State == "ok" {
			t.Errorf("an empty notes file is reported as a store with content")
		}
	}
}
