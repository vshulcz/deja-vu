package main

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// #2641 gave the JSON writers the reader's indent and key order back. What
// they still did was expand every inline object and array, so an install that
// added one server rewrote the reader's own entries around it (#2704).
func TestAConfigKeepsItsInlineBlocks(t *testing.T) {
	old := []byte(`{
  "numberOfStartups": 42,
  "tipsHistory": {"new-user-warmup": 3, "shift-enter": 9},
  "mcpServers": {
    "mine": {"command": "/usr/local/bin/mine", "args": ["serve", "--fast"]}
  },
  "projects": {
    "/work/api": {"allowedTools": ["Bash(git:*)", "Read"], "hasTrustDialogAccepted": true}
  }
}
`)
	var root map[string]any
	if err := json.Unmarshal(old, &root); err != nil {
		t.Fatal(err)
	}
	// What an install does: one server added, nothing else touched.
	servers, _ := root["mcpServers"].(map[string]any)
	servers["deja"] = map[string]any{"command": "/opt/deja", "args": []any{"mcp"}, "type": "stdio"}

	next, err := marshalConfigLike(old, root)
	if err != nil {
		t.Fatal(err)
	}
	got := string(next)

	for _, line := range []string{
		`  "tipsHistory": {"new-user-warmup": 3, "shift-enter": 9},`,
		`    "mine": {"command": "/usr/local/bin/mine", "args": ["serve", "--fast"]}`,
		`    "/work/api": {"allowedTools": ["Bash(git:*)", "Read"], "hasTrustDialogAccepted": true}`,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("a block the install never touched was rewritten; expected\n%s\ngot:\n%s", line, got)
		}
	}
	// The block deja added has no shape of its own to keep, so it takes the
	// file's.
	if !strings.Contains(got, "\"deja\": {\n") {
		t.Errorf("deja's own entry was inlined into a file that writes blocks:\n%s", got)
	}
	// And it is still the same config.
	var back map[string]any
	if err := json.Unmarshal(next, &back); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, got)
	}
	if _, ok := back["mcpServers"].(map[string]any)["deja"]; !ok {
		t.Errorf("the server that was added is gone:\n%s", got)
	}
	if n, _ := back["numberOfStartups"].(float64); n != 42 {
		t.Errorf("a value changed: %v", back["numberOfStartups"])
	}
}

// A file that is one long line stays one long line, and a file deja creates
// has no shape to keep.
func TestInliningLeavesAFileWithNoShapeAlone(t *testing.T) {
	for _, c := range []struct{ name, old string }{
		{"a config on a single line", `{"mcpServers":{"mine":{"command":"/usr/local/bin/mine"}}}`},
		{"a config deja is creating", ""},
	} {
		var root map[string]any
		if c.old != "" {
			if err := json.Unmarshal([]byte(c.old), &root); err != nil {
				t.Fatal(err)
			}
		} else {
			root = map[string]any{"mcpServers": map[string]any{}}
		}
		root["mcpServers"].(map[string]any)["deja"] = map[string]any{"command": "/opt/deja"}
		next, err := marshalConfigLike([]byte(c.old), root)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var back map[string]any
		if err := json.Unmarshal(next, &back); err != nil {
			t.Errorf("%s: the file no longer parses: %v\n%s", c.name, err, next)
		}
		if c.old != "" && strings.Contains(strings.TrimRight(string(next), "\n"), "\n") {
			t.Errorf("%s came back in blocks:\n%s", c.name, next)
		}
	}
}

// The writer edits text, so what it must never do is change what the file
// says. These are the shapes that break a text pass: braces and colons inside
// strings, escaped quotes, a Windows path, an empty block, arrays of objects,
// unicode, a key that repeats a path further down (#2704).
func TestInliningNeverChangesWhatTheConfigSays(t *testing.T) {
	configs := []string{
		`{"mcpServers":{"mine":{"command":"C:\\Program Files\\mine.exe","args":["--flag=\"quoted\""]}}}`,
		`{
  "permissions": {"allow": ["Bash(git:*)", "Read(//net/{a,b})"], "deny": []},
  "mcpServers": {"mine": {"command": "/bin/mine"}},
  "empty": {},
  "list": [],
  "nested": [{"a": {"b": [1, 2, {"c": "}{"}]}}]
}`,
		`{"a":{"b":{"c":{"d":{"e":[{"f":"ключ: значение, {скобка}"}]}}}}}`,
		`{
  "mcpServers": {
    "one": {"command": "/bin/one"},
    "two": {
      "command": "/bin/two"
    }
  },
  "one": {"command": "not the same one"}
}`,
		`{"mcpServers": {"mine": {"env": {"A": "1", "B": "2"}, "args": ["x"]}}}`,
	}
	for i, cfg := range configs {
		var root map[string]any
		if err := json.Unmarshal([]byte(cfg), &root); err != nil {
			t.Fatalf("config %d is not JSON to begin with: %v", i, err)
		}
		want := map[string]any{}
		if err := json.Unmarshal([]byte(cfg), &want); err != nil {
			t.Fatal(err)
		}
		// The one edit deja makes.
		servers, ok := root["mcpServers"].(map[string]any)
		if !ok {
			servers = map[string]any{}
			root["mcpServers"] = servers
		}
		servers["deja"] = map[string]any{"command": "/opt/deja", "args": []any{"mcp"}}
		wantServers, ok := want["mcpServers"].(map[string]any)
		if !ok {
			wantServers = map[string]any{}
			want["mcpServers"] = wantServers
		}
		wantServers["deja"] = map[string]any{"command": "/opt/deja", "args": []any{"mcp"}}

		out, err := marshalConfigLike([]byte(cfg), root)
		if err != nil {
			t.Fatalf("config %d: %v", i, err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("config %d no longer parses: %v\n%s", i, err, out)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config %d changed what it says:\nwant %#v\ngot  %#v\n%s", i, want, got, out)
		}
		// Writing it again changes nothing: an installer runs more than once.
		again, err := marshalConfigLike(out, got)
		if err != nil {
			t.Fatalf("config %d, second pass: %v", i, err)
		}
		if string(again) != string(out) {
			t.Errorf("config %d is not stable across two writes:\nfirst:\n%s\nsecond:\n%s", i, out, again)
		}
	}
}

// Three shapes the first version got wrong, each found by measuring rather
// than reading (#2704).
func TestInliningHandlesTheShapesThatBrokeIt(t *testing.T) {
	// An entry that moved inside an array must not inherit its neighbour's
	// shape: deja's own hook is inline at position 0, the reader's is written
	// in blocks at position 1, and taking deja's out shifts theirs into
	// position 0.
	t.Run("an entry that moved along an array", func(t *testing.T) {
		old := []byte(`{
  "hooks": {
    "sessionStart": [
      {"command": "/opt/deja hook-context"},
      {
        "command": "/usr/bin/mine",
        "timeout": 5
      }
    ]
  }
}
`)
		var root map[string]any
		if err := json.Unmarshal(old, &root); err != nil {
			t.Fatal(err)
		}
		hooks, _ := root["hooks"].(map[string]any)
		list, _ := hooks["sessionStart"].([]any)
		hooks["sessionStart"] = list[1:]

		next, err := marshalConfigLike(old, root)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(next), "\"command\": \"/usr/bin/mine\",\n") {
			t.Errorf("the reader's block was folded onto one line by the position deja left:\n%s", next)
		}
	})

	// A key the marshaller escapes: a path built from raw bytes never matches
	// the one built from the reader's file.
	t.Run("a key the marshaller escapes", func(t *testing.T) {
		old := []byte(`{
  "projects": {
    "/work/R&D": {"allowedTools": ["Read"], "hasTrustDialogAccepted": true}
  },
  "mcpServers": {}
}
`)
		var root map[string]any
		if err := json.Unmarshal(old, &root); err != nil {
			t.Fatal(err)
		}
		root["mcpServers"].(map[string]any)["deja"] = map[string]any{"command": "/opt/deja"}
		next, err := marshalConfigLike(old, root)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(next), `{"allowedTools": ["Read"], "hasTrustDialogAccepted": true}`) {
			t.Errorf("a project whose name is escaped when written was expanded:\n%s", next)
		}
	})

	// And the file this is all about: one inline entry per checkout. Rescanning
	// after every rewrite made a real one take minutes.
	t.Run("a config with a thousand inline blocks", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("{\n  \"projects\": {\n")
		for i := 0; i < 1000; i++ {
			b.WriteString(`    "/work/repo` + strconv.Itoa(i) +
				`": {"allowedTools": ["Bash(git:*)", "Read"], "hasTrustDialogAccepted": true},` + "\n")
		}
		b.WriteString("    \"/work/last\": {\"allowedTools\": [\"Read\"]}\n  },\n  \"mcpServers\": {}\n}\n")
		old := []byte(b.String())
		var root map[string]any
		if err := json.Unmarshal(old, &root); err != nil {
			t.Fatal(err)
		}
		root["mcpServers"].(map[string]any)["deja"] = map[string]any{"command": "/opt/deja"}

		done := make(chan []byte, 1)
		go func() {
			next, err := marshalConfigLike(old, root)
			if err != nil {
				t.Error(err)
			}
			done <- next
		}()
		select {
		case next := <-done:
			if n := strings.Count(string(next), "hasTrustDialogAccepted"); n != 1000 {
				t.Errorf("%d project entries survived, want 1000", n)
			}
			if strings.Contains(string(next), "\n      \"allowedTools\"") {
				t.Errorf("a project entry was expanded:\n%s", next[:400])
			}
		case <-time.After(20 * time.Second):
			t.Fatal("a config with a thousand inline blocks did not come back within 20s")
		}
	})
}
