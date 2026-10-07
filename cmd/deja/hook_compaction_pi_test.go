package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// piSessionFile writes a session the way pi 0.73 and omp 17.4 write one,
// copied from a stand run: header, the failing command and its result, and the
// `compaction` entry session_compact fires after. The turns before that entry
// stay in the file, which is what the capture reads.
func piSessionFile(t *testing.T, root, workspace, id string, omp bool) string {
	t.Helper()
	cwd, _ := json.Marshal(workspace)
	attribution := ""
	if omp {
		attribution = `"attribution":"user",`
	}
	lines := []string{
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:15:35.201Z","cwd":` + string(cwd) + `}`,
		`{"type":"model_change","id":"5d90ad3f","parentId":null,"timestamp":"2026-10-07T10:15:35.252Z","provider":"mock","modelId":"mock-model"}`,
		`{"type":"message","id":"fb64858b","parentId":"5d90ad3f","timestamp":"2026-10-07T10:15:35.671Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}],` + attribution + `"timestamp":1791368135631}}`,
		`{"type":"message","id":"f91894f3","parentId":"fb64858b","timestamp":"2026-10-07T10:15:35.726Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"go test ./parser/..."}}],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"toolUse","timestamp":1791368135700}}`,
		`{"type":"message","id":"d5d9ae2b","parentId":"f91894f3","timestamp":"2026-10-07T10:15:35.773Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n\n\nCommand exited with code 1"}],"isError":true,"timestamp":1791368135770}}`,
		`{"type":"message","id":"a48b8c07","parentId":"d5d9ae2b","timestamp":"2026-10-07T10:15:35.777Z","message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"stop","timestamp":1791368135775}}`,
		`{"type":"compaction","id":"e8d9e4dc","parentId":"a48b8c07","timestamp":"2026-10-07T10:15:35.843Z","summary":"Fixing the parser test.","firstKeptEntryId":"a48b8c07","tokensBefore":0,"details":{"readFiles":[],"modifiedFiles":[]},"fromHook":false}`,
	}
	if omp {
		lines = append([]string{`{"type":"title","v":1,"title":"","updatedAt":"2026-10-07T10:15:35.201Z","pad":"    "}`}, lines...)
	}
	path := filepath.Join(root, "--"+strings.Trim(encodeLikeClaude(workspace), "-")+"--", "2026-10-07T10-15-35-201Z_"+id+".jsonl")
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// pi's session_compact fires once the compaction entry is in the file; the
// extension sends the session file it names, and the next prompt carries the
// packet. omp sends the same, and its own name, from the same event.
func TestPiFamilyCompactionIsReadFromTheSessionFile(t *testing.T) {
	for _, host := range []string{"pi", "omp"} {
		t.Run(host, func(t *testing.T) {
			hermeticEnv(t)
			hostsNoWarmup(t)
			workspace := compactionGitRepo(t)
			dir := index.DefaultDir()
			const id = "01a115dc-8221-70f9-956a-50742150659d"
			root, payload := sources.PiRoot(), map[string]any{"session_id": id, "cwd": workspace}
			if host == "omp" {
				root, payload["harness"] = sources.OmpRoot(), "omp"
			}
			payload["transcript_path"] = piSessionFile(t, root, workspace, id, host == "omp")
			wantCompactionPacket(t, precompactThen(t, dir, workspace, payload, ""))
			if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != host {
				t.Errorf("the capture is filed under %q, want %s", state.Harness, host)
			}
		})
	}
}

// The file is only the place to look: a session file whose header names
// another session is not this one.
func TestPiCompactionRefusesAnotherSessionsFile(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	path := piSessionFile(t, sources.PiRoot(), workspace, "01a115dc-0000-7000-8000-000000000000", false)
	b, _ := json.Marshal(map[string]any{"session_id": "01a115dc-8221-70f9-956a-50742150659d", "transcript_path": path, "cwd": workspace})
	withHookStdin(t, string(b))
	runHookPrecompactFor(dir, "")
	if _, found, _ := index.Compaction(dir, "01a115dc-8221-70f9-956a-50742150659d", workspace); found {
		t.Fatal("captured a session file that belongs to another session")
	}
}

// The extensions read the session file off the session manager on the
// compaction event itself, which pi and omp both hand ctx.
func TestPiFamilyExtensionsSendTheSessionFile(t *testing.T) {
	for name, src := range map[string]string{
		"pi":    piExtensionTS("/bin/deja"),
		"omp":   ompExtensionJS("/bin/deja"),
		"prime": primeExtensionTS("/bin/deja"),
	} {
		for _, want := range []string{"m.getSessionFile ? m.getSessionFile() : m.sessionFile", "transcript_path: sessionFile"} {
			if !strings.Contains(src, want) {
				t.Errorf("%s extension lacks %q", name, want)
			}
		}
		if !strings.Contains(src, `pi.on("session_compact", async (_event`) || !strings.Contains(src, "remember(ctx);\n      run([\"hook-precompact\"]") {
			t.Errorf("%s extension does not read the session on session_compact", name)
		}
	}
}
