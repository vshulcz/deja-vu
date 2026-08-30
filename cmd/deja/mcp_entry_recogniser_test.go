package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// install and doctor each grew their own answer to "does this entry run deja",
// and they disagreed in both directions: a nested transport object and an
// /opt/Deja were deja to doctor and invisible to install, and opencode's list
// form was the reverse. One command then called a file wired while the other
// wrote a second server into it (#2713).
func TestBothRecognisersReadAnEntryTheSameWay(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		deja  bool
	}{
		{"the plain shape", `{"command":"/usr/local/bin/deja","args":["mcp"]}`, true},
		{"a nested transport", `{"transport":{"command":"/usr/local/bin/deja","args":["mcp"]}}`, true},
		{"a list command", `{"command":["/old/bin/deja","mcp"]}`, true},
		{"a windows path", `{"command":"C:\\bin\\deja.exe","args":["mcp"]}`, true},
		{"a capitalised install", `{"command":"/opt/Deja"}`, true},
		{"the binary behind a wrapper", `{"command":"cmd","args":["/c","/bin/deja","mcp"]}`, true},
		{"somebody else's server", `{"command":"/usr/local/bin/mine","args":["serve"]}`, false},
		{"a name that only starts the same", `{"command":"npx","args":["-y","deja-vu-mcp"]}`, false},
		{"nothing at all", `{}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var entry map[string]any
			if err := json.Unmarshal([]byte(c.entry), &entry); err != nil {
				t.Fatal(err)
			}
			install := entryRunsDeja(entry)
			doctor := mcpEntryRunsDeja(entry)
			if install != doctor {
				t.Errorf("install says %v and doctor says %v about the same entry", install, doctor)
			}
			if install != c.deja {
				t.Errorf("read as deja=%v, want %v", install, c.deja)
			}
		})
	}
}

// And the answer to "which binary" follows the same shapes: a check that says
// "wired" but cannot name the command has nothing to report when that command
// is gone (#2713).
func TestTheCommandBehindAnEntryIsFoundInEveryShape(t *testing.T) {
	for _, c := range []struct{ name, entry, want string }{
		{"the plain shape", `{"command":"/usr/local/bin/deja","args":["mcp"]}`, "/usr/local/bin/deja"},
		{"a nested transport", `{"transport":{"command":"/usr/local/bin/deja"}}`, "/usr/local/bin/deja"},
		{"a list command", `{"command":["/old/bin/deja","mcp"]}`, "/old/bin/deja"},
		{"the binary behind a wrapper", `{"command":"cmd","args":["/c","/bin/deja","mcp"]}`, "/bin/deja"},
		{"a quoted windows path", `{"command":"\"C:\\bin\\deja.exe\"","args":["mcp"]}`, `"C:\bin\deja.exe"`},
		{"somebody else's server", `{"command":"/usr/local/bin/mine"}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var entry map[string]any
			if err := json.Unmarshal([]byte(c.entry), &entry); err != nil {
				t.Fatal(err)
			}
			if got := mcpEntryDejaCommand(entry); got != c.want {
				t.Errorf("named %q, want %q", got, c.want)
			}
			if got := mcpEntryDejaCommand(entry); got != "" && !entryRunsDeja(entry) {
				t.Errorf("an entry read as not deja still named %q", got)
			}
			// And the other direction, which is the one that hides a dead
			// binary: an entry read as deja whose command nothing can name
			// leaves the missing-binary check with nothing to report.
			if entryRunsDeja(entry) && mcpEntryDejaCommand(entry) == "" {
				t.Error("an entry read as deja named no command at all")
			}
		})
	}
}

// The wider recogniser reaches the merge as well, and the merge writes the
// launch at the top level: an entry in the nested shape kept its transport
// beside the command deja had just written, so a client that prefers the
// nested one went on running the old binary (#2716).
func TestMergingOntoANestedEntryLeavesOneLaunchShape(t *testing.T) {
	var prev map[string]any
	if err := json.Unmarshal([]byte(`{"type":"local","transport":{"command":["/old/bin/deja","mcp"]}}`), &prev); err != nil {
		t.Fatal(err)
	}
	out, _ := mergeDejaEntry(prev, map[string]any{
		"type":    "local",
		"command": []any{"/usr/local/bin/deja", "mcp"},
	})
	if _, ok := out["transport"]; ok {
		b, _ := json.Marshal(out)
		t.Errorf("the entry carries two ways to launch deja:\n%s", b)
	}
	if got := mcpEntryDejaCommand(out); got != "/usr/local/bin/deja" {
		t.Errorf("the entry names %q, want the binary that was just installed", got)
	}
}

// And the note about somebody else's entry reads the nested shape too, or the
// silence #2712 closed comes back for the harnesses that write it.
func TestTheSecondEntryNoteReadsTheNestedShape(t *testing.T) {
	var servers map[string]any
	seed := `{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]},` +
		`"deja-vu":{"transport":{"command":"/old/bin/deja","args":["mcp"]}}}`
	if err := json.Unmarshal([]byte(seed), &servers); err != nil {
		t.Fatal(err)
	}
	if note := otherDejaEntriesNote(servers, "deja"); !strings.Contains(note, "deja-vu") {
		t.Errorf("a second entry in the nested shape went unmentioned: %q", note)
	}
}
