package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `kiro-cli mcp add` writes a server as command, args, env. Install then
// uninstall put args first in every other server, because only the top-level
// order was given back; the file never came back to its bytes (#4306). Cursor
// and zcode write their configs the same way.
func TestInstallUninstallGivesTheConfigBackByteForByte(t *testing.T) {
	const kiroAdd = "{\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"/bin/echo\",\n      \"args\": [\n        \"hi\"\n      ],\n      \"env\": {}\n    }\n  }\n}\n"
	for _, tc := range []struct {
		target, rel, body string
	}{
		{"kiro", ".kiro/settings/mcp.json", kiroAdd},
		{"cursor", ".cursor/mcp.json", kiroAdd},
		{"zcode", ".zcode/cli/setting.json", "{\n  \"theme\": \"dark\",\n  \"mcp\": {\n    \"servers\": {\n      \"other\": {\n        \"type\": \"stdio\",\n        \"command\": \"/bin/echo\",\n        \"args\": [\n          \"hi\"\n        ]\n      }\n    }\n  },\n  \"editor\": \"vim\"\n}\n"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(tc.target, "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			installed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(installed), `"deja"`) {
				t.Fatalf("install wrote no entry:\n%s", installed)
			}
			if strings.Index(string(installed), `"command": "/bin/echo"`) > strings.Index(string(installed), `"args"`) {
				t.Errorf("install re-sorted the other server's keys:\n%s", installed)
			}
			if _, err := installTarget(tc.target, "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.body {
				t.Errorf("uninstall did not give the file back:\n--- before\n%s--- after\n%s", tc.body, got)
			}
		})
	}
}

// The order is the reader's at every depth, and a key deja adds goes after
// theirs.
func TestMarshalConfigLikeKeepsNestedOrder(t *testing.T) {
	old := []byte("{\n  \"b\": {\n    \"z\": 1,\n    \"a\": [\n      {\n        \"y\": 1,\n        \"x\": 2\n      }\n    ]\n  },\n  \"a\": 1\n}")
	root := map[string]any{
		"a": 1.0,
		"b": map[string]any{
			"z": 1.0,
			"a": []any{map[string]any{"y": 1.0, "x": 2.0}, map[string]any{"x": 3.0, "y": 4.0}},
			"m": "new",
		},
	}
	got, err := marshalConfigLike(old, root)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"b\": {\n    \"z\": 1,\n    \"a\": [\n      {\n        \"y\": 1,\n        \"x\": 2\n      },\n      {\n        \"y\": 4,\n        \"x\": 3\n      }\n    ],\n    \"m\": \"new\"\n  },\n  \"a\": 1\n}"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// Entries of one array need not list their keys alike: a hand-written hook can
// put command before type beside one that does not. Each entry deja did not
// touch comes back in its own order, wherever deja's own entry went in.
func TestMarshalConfigLikeKeepsEachArrayEntrysOrder(t *testing.T) {
	old := []byte("{\n  \"hooks\": [\n    {\n      \"type\": \"command\",\n      \"command\": \"a\"\n    },\n    {\n      \"command\": \"b\",\n      \"type\": \"command\"\n    }\n  ]\n}")
	var root map[string]any
	if err := json.Unmarshal(old, &root); err != nil {
		t.Fatal(err)
	}
	got, err := marshalConfigLike(old, root)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Errorf("round trip changed the file:\n%s", got)
	}

	hooks := root["hooks"].([]any)
	root["hooks"] = append([]any{map[string]any{"type": "command", "command": "deja hook"}}, hooks...)
	got, err = marshalConfigLike(old, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "{\n      \"command\": \"b\",\n      \"type\": \"command\"\n    }") {
		t.Errorf("an entry added in front moved the order of the ones after it:\n%s", got)
	}
}

// An entry deja edits inside, a matcher group it adds its hook to, keeps the
// reader's order for the group and for the hooks already in it.
func TestMarshalConfigLikeKeepsAnEditedEntrysOrder(t *testing.T) {
	old := []byte(`{"PreToolUse":[{"matcher":"A","hooks":[{"type":"command","command":"x"}]},{"hooks":[{"command":"y","type":"command"}],"matcher":"B"}]}`)
	var root map[string]any
	if err := json.Unmarshal(old, &root); err != nil {
		t.Fatal(err)
	}
	group := root["PreToolUse"].([]any)[1].(map[string]any)
	group["hooks"] = append(group["hooks"].([]any), map[string]any{"type": "command", "command": "deja hook"})
	got, err := marshalConfigLike(old, root)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"PreToolUse":[{"matcher":"A","hooks":[{"type":"command","command":"x"}]},{"hooks":[{"command":"y","type":"command"},{"command":"deja hook","type":"command"}],"matcher":"B"}]}`
	var compact bytes.Buffer
	if err := json.Compact(&compact, got); err != nil {
		t.Fatal(err)
	}
	if compact.String() != want {
		t.Errorf("got\n%s\nwant\n%s", compact.String(), want)
	}
}

// A config read and written back unchanged comes back byte for byte, whatever
// order the reader kept their keys in — including two array items with the
// same value in different orders, and keys whose names look like paths.
func TestTheReadersNestedKeyOrderSurvivesARewrite(t *testing.T) {
	for name, old := range map[string]string{
		"same value, two orders": `{
  "a": [
    {
      "x": 1,
      "y": 2
    }
  ],
  "b": [
    {
      "y": 2,
      "x": 1
    }
  ]
}`,
		"repeats in one array": `{
  "hooks": [
    {
      "type": "command",
      "command": "a"
    },
    {
      "command": "a",
      "type": "command"
    },
    {
      "type": "command",
      "command": "a"
    }
  ]
}`,
		"dotted keys": `{
  "a.b": {
    "y": 1,
    "x": 2
  },
  "a": {
    "b": {
      "x": 1,
      "y": 2
    }
  },
  "a/\"b\"": {
    "z": 1,
    "w": 2
  }
}`,
	} {
		var root map[string]any
		if err := json.Unmarshal([]byte(old), &root); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := marshalConfigLike([]byte(old), root)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != old {
			t.Errorf("%s: came back changed:\n%s\n--- want\n%s", name, got, old)
		}
	}
}
