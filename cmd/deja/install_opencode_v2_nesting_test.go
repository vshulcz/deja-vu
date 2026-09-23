package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// OpenCode 2.x keeps its MCP servers under `mcp.servers`. The installer must
// update that object in place so the harness keeps reading the entry and fields
// such as `environment` survive the merge.
func TestOpencodeJSONCUpdatesV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  // my MCP servers\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": {\n" +
		"      \"deja\": {\n" +
		"        \"type\": \"local\",\n" +
		"        \"command\": [\"/usr/local/bin/deja\", \"mcp\"],\n" +
		"        \"environment\": { \"DEJA_HOME\": \"/mnt/nas/deja\" }\n" +
		"      },\n" +
		"      \"theirs\": { \"command\": [\"other\"] }\n" +
		"    }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatalf("nested config was refused: %v", err)
	}
	got := jsoncValue(t, out)
	mcp, ok := got["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp block is missing: %s", out)
	}
	servers, ok := mcp["servers"].(map[string]any)
	if !ok {
		t.Fatalf("servers block is missing: %s", out)
	}
	entry, ok := servers["deja"].(map[string]any)
	if !ok {
		t.Fatalf("deja is not under mcp.servers: %s", out)
	}
	if got, want := entry["command"], []any{"/bin/deja", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	environment, ok := entry["environment"].(map[string]any)
	if !ok || environment["DEJA_HOME"] != "/mnt/nas/deja" {
		t.Errorf("existing environment was not preserved: %#v", entry["environment"])
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was moved to the top-level mcp block:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("the unrelated server was lost:\n%s", out)
	}
	if !strings.Contains(string(out), "// my MCP servers") {
		t.Errorf("the surrounding comment was lost:\n%s", out)
	}
}

// A fresh OpenCode 2.x config has no deja entry yet. It should be added to the
// existing nested object rather than creating an invisible sibling at mcp.
func TestOpencodeJSONCAddsToV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": {\n" +
		"      // keep this server\n" +
		"      \"theirs\": {\"command\": [\"other\"]}\n" +
		"    }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatalf("nested config was refused: %v", err)
	}
	got := jsoncValue(t, out)
	mcp := got["mcp"].(map[string]any)
	servers := mcp["servers"].(map[string]any)
	if _, ok := servers["deja"]; !ok {
		t.Fatalf("deja was not added under mcp.servers:\n%s", out)
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was added beside mcp.servers:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok || !strings.Contains(string(out), "// keep this server") {
		t.Errorf("the existing server or comment was lost:\n%s", out)
	}
}

// The same shape is valid when the nested object was written on one line. The
// generic JSONC writer replaces only the entry value and leaves the rest of the
// line intact.
func TestOpencodeJSONCUpdatesInlineV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": { \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]}, \"theirs\": {\"command\":[\"other\"]} }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatalf("inline nested config was refused: %v", err)
	}
	got := jsoncValue(t, out)
	mcp := got["mcp"].(map[string]any)
	servers := mcp["servers"].(map[string]any)
	entry := servers["deja"].(map[string]any)
	if got, want := entry["command"], []any{"/bin/deja", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was moved to the top-level mcp block:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("the unrelated server was lost:\n%s", out)
	}
}

func TestOpencodeJSONUpdatesV2NestedServers(t *testing.T) {
	seed := []byte(`{"mcp":{"servers":{"deja":{"type":"local","command":["/old/deja","mcp"],"enabled":false},"theirs":{"command":["other"]}}}}`)
	out, _, err := updateOpencodeJSON(seed, "opencode.json", "/bin/deja", false)
	if err != nil {
		t.Fatalf("nested JSON config was refused: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output no longer parses: %v\n%s", err, out)
	}
	mcp := root["mcp"].(map[string]any)
	servers := mcp["servers"].(map[string]any)
	entry := servers["deja"].(map[string]any)
	if got, want := entry["command"], []any{"/bin/deja", "mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %#v, want %#v", got, want)
	}
	if got := entry["enabled"]; got != false {
		t.Errorf("enabled = %#v, want false", got)
	}
	if _, ok := mcp["deja"]; ok {
		t.Errorf("deja was moved to the top-level mcp block:\n%s", out)
	}
}

func TestOpencodeJSONCUninstallsFromV2NestedServers(t *testing.T) {
	seed := "{\n" +
		"  \"mcp\": {\n" +
		"    \"servers\": {\n" +
		"      // keep this server\n" +
		"      \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]},\n" +
		"      \"theirs\": {\"command\": [\"other\"]}\n" +
		"    }\n" +
		"  }\n" +
		"}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", true)
	if err != nil {
		t.Fatalf("nested config uninstall failed: %v", err)
	}
	got := jsoncValue(t, out)
	mcp := got["mcp"].(map[string]any)
	servers := mcp["servers"].(map[string]any)
	if _, ok := servers["deja"]; ok {
		t.Fatalf("deja was not removed from mcp.servers:\n%s", out)
	}
	if _, ok := servers["theirs"]; !ok || !strings.Contains(string(out), "// keep this server") {
		t.Fatalf("the unrelated server or comment was lost:\n%s", out)
	}
}
