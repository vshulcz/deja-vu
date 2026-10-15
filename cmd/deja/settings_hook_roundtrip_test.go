package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A hook array the reader wrote on one line goes back on one line when deja
// adds its entry to it (#2704), but it was compacted from the marshalled form,
// so the reader's own entry inside it came back with its keys sorted — and an
// uninstall then had nothing to restore it from (#4167).
func TestInstallAndUninstallGiveBackTheReadersOwnHookEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	mine := `[{"matcher": "Bash", "hooks": [{"type": "command", "command": "~/bin/guard.sh"}]}]`
	orig := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {
    "PreToolUse": ` + mine + `
  }
}
`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := "/opt/deja/deja-hook hook-tool"
	if _, err := installSettingsHookCmd(path, "PreToolUse", "Bash|Edit", 30, cmd, false); err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(path)
	if want := `"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "~/bin/guard.sh"}]}, `; !strings.Contains(string(installed), want) {
		t.Errorf("the install rewrote the reader's own entry; want it kept as\n%s\ngot:\n%s", want, installed)
	}
	if !strings.Contains(string(installed), cmd) {
		t.Fatalf("the install did not add its entry:\n%s", installed)
	}
	if _, err := installSettingsHookCmd(path, "PreToolUse", "Bash|Edit", 30, cmd, true); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	if string(back) != orig {
		t.Errorf("install then uninstall did not give the file back\nwant:\n%s\ngot:\n%s", orig, back)
	}
}

// An entry the reader wrote on one line inside an array laid out over many
// lines is matched by its value, not its position: deja's entry added ahead of
// it moves it along.
func TestAnInlineArrayEntryThatMovedKeepsItsText(t *testing.T) {
	old := []byte(`{
  "list": [
    {"b": 1, "a": 2}
  ]
}
`)
	next := []byte(`{
  "list": [
    {
      "deja": true
    },
    {
      "a": 2,
      "b": 1
    }
  ]
}
`)
	got := string(keepInlineBlocks(old, next))
	if !strings.Contains(got, `{"b": 1, "a": 2}`) {
		t.Errorf("the reader's entry lost its text when it moved:\n%s", got)
	}
}

// The block deja changes keeps the reader's key order and spacing too, not
// only the entries inside it: a hooks object on one line, and a minified file.
func TestInstallAndUninstallKeepTheShapeOfTheBlockDejaChanges(t *testing.T) {
	cmd := "/opt/deja/deja-hook hook-tool"
	for name, orig := range map[string]string{
		"one-line hooks": `{
  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "a"}]}], "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "b"}]}]}
}
`,
		"minified": `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"b"}]}]}}
`,
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, uninstall := range []bool{false, true} {
			if _, err := installSettingsHookCmd(path, "PreToolUse", "Bash|Edit", 30, cmd, uninstall); err != nil {
				t.Fatal(err)
			}
		}
		if back, _ := os.ReadFile(path); string(back) != orig {
			t.Errorf("%s: install then uninstall did not give the file back\nwant:\n%s\ngot:\n%s", name, orig, back)
		}
	}
}

// Two entries equal in value but written differently each come back once, in
// their own places, when deja's entry goes in ahead of them and out again.
func TestEqualEntriesWrittenDifferentlyStayApart(t *testing.T) {
	old := []byte(`{
  "list": [
    {"a": 1},
    {"b": 1, "c": 2},
    {"c": 2, "b": 1}
  ]
}
`)
	next := []byte(`{
  "list": [
    {
      "a": 1
    },
    {
      "deja": true
    },
    {
      "b": 1,
      "c": 2
    },
    {
      "b": 1,
      "c": 2
    }
  ]
}
`)
	got := string(keepInlineBlocks(old, next))
	if !strings.Contains(got, "{\"b\": 1, \"c\": 2},\n    {\"c\": 2, \"b\": 1}") {
		t.Errorf("equal entries were swapped or merged:\n%s", got)
	}
}

// A long array with deja's entry added ahead of the reader's stays linear: the
// lookup by value decoded every entry again for every entry it moved. A fixed
// 2s ceiling tripped on a loaded runner under -race (#5025), so the time is
// compared against an array a quarter the size: linear is about 4x, the
// quadratic lookup was 16x.
func TestALongArrayWithAMovedEntryStaysFast(t *testing.T) {
	build := func(count int) (string, string) {
		var o, n strings.Builder
		o.WriteString("{\n  \"list\": [\n")
		n.WriteString("{\n  \"list\": [\n    {\n      \"deja\": true\n    },\n")
		for i := 0; i < count; i++ {
			sep := ","
			if i == count-1 {
				sep = ""
			}
			fmt.Fprintf(&o, "    {\"name\": \"e%d\", \"id\": %d}%s\n", i, i, sep)
			fmt.Fprintf(&n, "    {\n      \"id\": %d,\n      \"name\": \"e%d\"\n    }%s\n", i, i, sep)
		}
		o.WriteString("  ]\n}\n")
		n.WriteString("  ]\n}\n")
		return o.String(), n.String()
	}
	// The fastest of three runs, so one stall on a busy runner does not count.
	fastest := func(o, n string) (time.Duration, string) {
		var best time.Duration
		var got string
		for range 3 {
			start := time.Now()
			got = string(keepInlineBlocks([]byte(o), []byte(n)))
			if took := time.Since(start); best == 0 || took < best {
				best = took
			}
		}
		return best, got
	}
	const count = 3000
	small, _ := fastest(build(count / 4))
	large, got := fastest(build(count))
	if large > 10*small && large > 50*time.Millisecond {
		t.Errorf("%d entries took %v against %v for %d: grows faster than linear", count, large, small, count/4)
	}
	if !strings.Contains(got, `{"name": "e2999", "id": 2999}`) {
		t.Errorf("the moved entries lost their text")
	}
}

// A one-line block under a key of an array entry — the inner hooks list of the
// reader's Stop hook — is not an array entry itself, and goes back as written.
func TestAOneLineBlockInsideAnArrayEntryIsKept(t *testing.T) {
	orig := `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "~/bin/notify.sh"}]
      }
    ]
  }
}
`
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := "/opt/deja/deja-hook hook-tool"
	for _, uninstall := range []bool{false, true} {
		if _, err := installSettingsHookCmd(path, "PreToolUse", "Bash|Edit", 30, cmd, uninstall); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"hooks": [{"type": "command", "command": "~/bin/notify.sh"}]`) {
			t.Errorf("uninstall=%v expanded the reader's one-line list:\n%s", uninstall, b)
		}
	}
	if back, _ := os.ReadFile(path); string(back) != orig {
		t.Errorf("install then uninstall did not give the file back\nwant:\n%s\ngot:\n%s", orig, back)
	}
}
