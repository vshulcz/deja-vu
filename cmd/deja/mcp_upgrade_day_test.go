package main

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// upgradeDayStore builds an index and then rewrites its manifest version, which
// is what a release with a format change leaves behind: the file is there, and
// this build cannot read it.
func upgradeDayStore(t *testing.T) string {
	t.Helper()
	storeWith(t, true)
	dir := index.DefaultDir()
	path := filepath.Join(dir, "manifest.gob")
	var core map[string]any
	// The manifest is a gob of a private struct, so the version is bent by
	// writing a decodable stand-in: any manifest this build cannot decode is
	// the same state to every caller under test.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("no manifest to age")
	}
	_ = core
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(struct{ Version int }{Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if !index.HasManifest(dir) {
		t.Fatal("the fixture removed the manifest instead of ageing it")
	}
	if index.IsCurrentVersion(dir) {
		t.Fatal("the fixture did not age the manifest")
	}
	if index.FormatDirection(dir) != -1 {
		t.Fatalf("the fixture produced some other broken state (%d), not an older format", index.FormatDirection(dir))
	}
	return dir
}

// The first agent call after an upgrade rebuilt the whole index inside the tool
// call and said nothing (#1309). The sentence for this state existed; its
// condition asked whether the manifest file was present, which on upgrade day
// it is.
func TestAgentToolsSayWhenTheIndexIsBeingRebuiltForThisVersion(t *testing.T) {
	dir := upgradeDayStore(t)
	line := buildingNowForAgent(dir)
	if line == "" {
		t.Fatal("an index this build cannot read was reported as ready to answer")
	}
	if !strings.Contains(line, "ask again") {
		t.Errorf("the line does not tell the agent what to do: %q", line)
	}
}

// And the tools that reach the index say it rather than rebuilding inline.
// resources/list, fix, how and blame all called the blocking index.Ensure
// (#1306); fix and how are described to the model as things to call before
// diagnosing an error or inventing a command, so they sit on the latency path
// by design.
func TestReadOnlyToolsDoNotRebuildInsideTheCall(t *testing.T) {
	dir := upgradeDayStore(t)
	for _, tc := range []struct{ name, args string }{
		{"fix", `{"error":"undefined: parseThing"}`},
		{"how", `{"what":"run the tests"}`},
		{"blame", `{"path":"cmd/deja/main.go"}`},
	} {
		out, err := callTool(t, dir, tc.name, tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !strings.Contains(out, "ask again") {
			t.Errorf("%s answered without saying the index is being rebuilt:\n%s", tc.name, out)
		}
	}
	// resources/list answers in the shape the protocol defines: an empty list,
	// not an error carrying an internal manifest path, and not a rebuild.
	res, code, msg := mcpResourcesList(dir)
	if msg != "" || code != 0 {
		t.Fatalf("resources/list returned an error rather than an empty browse: %d %s", code, msg)
	}
	body, _ := res.(map[string]any)
	list, ok := body["resources"].([]map[string]any)
	if !ok || len(list) != 0 {
		t.Errorf("resources/list served a browse from an index this build cannot read: %#v", res)
	}
	if len(body) != 1 {
		t.Errorf("resources/list result carries fields the protocol does not define: %#v", body)
	}
}

// A machine with no index at all is a first run, and building it there is the
// only way an install without hooks ever gets one. That must not change.
func TestAFirstRunStillBuildsFromTheAgentCall(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if index.HasManifest(dir) {
		t.Fatal("the fixture is not a fresh machine")
	}
	if line := buildingNowForAgent(dir); line != "" {
		t.Errorf("a fresh machine was told to ask again, and nothing would have built the index: %q", line)
	}
}

// callTool drives one MCP tool through the server the way a client does.
func callTool(t *testing.T, dir, name, args string) (string, error) {
	t.Helper()
	in := `{"jsonrpc":"2.0","id":"x","method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}` + "\n"
	var out bytes.Buffer
	if err := serveMCP(dir, strings.NewReader(in), &out); err != nil {
		return "", err
	}
	return out.String(), nil
}

// remember has to keep working: it writes the note either way, and says the
// note is not findable yet rather than rebuilding the index to make it so.
func TestRememberSavesOnUpgradeDayWithoutRebuilding(t *testing.T) {
	dir := upgradeDayStore(t)
	out, err := callTool(t, dir, "remember", `{"text":"the staging retry queue needs a longer backoff"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Saved") {
		t.Errorf("remember did not confirm the note:\n%s", out)
	}
	if !strings.Contains(out, "ask again") {
		t.Errorf("remember said nothing about the note not being findable yet:\n%s", out)
	}
}

// And a read-only index directory never repairs itself, so the agent must not
// be told to come back.
func TestNoAskAgainWhenTheIndexCannotBeRebuilt(t *testing.T) {
	dir := upgradeDayStore(t)
	// The parent, not the index directory: a rebuild writes index.db.tmp
	// beside it and renames, so that is the directory whose permissions decide
	// whether it can happen at all.
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Skip("cannot make the index directory read-only here")
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	line := buildingNowForAgent(dir)
	if strings.Contains(line, "ask again") {
		t.Errorf("an agent was told to ask again about a rebuild that cannot happen: %q", line)
	}
	if !strings.Contains(line, "deja index") {
		t.Errorf("the line does not name the command that says what to change: %q", line)
	}
}
