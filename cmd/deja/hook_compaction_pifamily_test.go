package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// pifamilySessionFile writes a session the way Senpi 2026.10.10-5, gjc 0.18.7
// and prime-agent 0.9.8 wrote one on a stand: pi's header and entries, each
// host's own extras around them, and the `compaction` entry session_compact
// fires after. The turns before that entry stay in the file.
func pifamilySessionFile(t *testing.T, host, workspace, id string) string {
	t.Helper()
	cwd, _ := json.Marshal(workspace)
	header := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:18:19.235Z","cwd":` + string(cwd) + `}`
	call := `{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"go test ./parser/..."}}`
	result := `{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n\n\nCommand exited with code 1"}],"details":{},"isError":true,"timestamp":1791368300097}`
	var extra []string
	var path string
	switch host {
	case "senpi":
		extra = []string{
			`{"type":"custom","customType":"pi-rules.scan","data":{"cwd":` + string(cwd) + `,"reason":"startup"},"id":"f92033d0","parentId":"015e1f06","timestamp":"2026-10-07T10:18:19.462Z"}`,
			`{"type":"custom_message","customType":"environment-context","content":"<environment_context>\n  <cwd>` + workspace + `</cwd>\n</environment_context>","display":false,"id":"58d9a505","parentId":"f92033d0","timestamp":"2026-10-07T10:18:20.086Z"}`,
		}
		path = filepath.Join(sources.SenpiRoot(), "--"+strings.Trim(encodeLikeClaude(workspace), "-")+"--", "2026-10-07T10-18-19-235Z_"+id+".jsonl")
	case "gjc":
		path = filepath.Join(sources.GjcRoot(), "v2-43csdzcwfkots3utpzkeudsc75bj7d4uqpj4csv5ax2hlkqsrrwq", "2026-10-07T10-31-25-797Z_"+id+".jsonl")
	case "prime":
		// prime's header records the depth and the git state; its one tool
		// is ipython, and a cell's shell result comes back as a BashResult.
		header = `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:24:22.227Z","cwd":` + string(cwd) + `,"rlmDepth":0,"git":{"commit":"cc5a71a13a6672cc3b84a085c912b969a09389c5","branch":"main"}}`
		call = `{"type":"toolCall","id":"call_1","name":"ipython","arguments":{"code":"await bash(\"go test ./parser/...\")"}}`
		result = `{"role":"toolResult","toolCallId":"call_1","toolName":"ipython","content":[{"type":"text","text":"BashResult(exit_code=1, output='--- FAIL: TestParseSeed\\nwant 3, got 4\\nFAIL\\n', duration=0.25)\n"}],"isError":false,"timestamp":1791368300097}`
		path = filepath.Join(sources.PrimeRoot(), id+".jsonl")
	}
	lines := append([]string{
		header,
		`{"type":"model_change","id":"94cf0d5a","parentId":null,"timestamp":"2026-10-07T10:18:19.429Z","provider":"mock","modelId":"mock-model"}`,
		`{"type":"thinking_level_change","id":"015e1f06","parentId":"94cf0d5a","timestamp":"2026-10-07T10:18:19.429Z","thinkingLevel":"off"}`,
	}, extra...)
	lines = append(lines,
		`{"type":"message","id":"9f5237af","parentId":"58d9a505","timestamp":"2026-10-07T10:18:20.086Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}],"timestamp":1791368299936}}`,
		`{"type":"message","id":"ce5c9415","parentId":"9f5237af","timestamp":"2026-10-07T10:18:20.096Z","message":{"role":"assistant","content":[`+call+`],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"toolUse","timestamp":1791368300090}}`,
		`{"type":"message","id":"3d39c20c","parentId":"ce5c9415","timestamp":"2026-10-07T10:18:20.097Z","message":`+result+`}`,
		`{"type":"message","id":"ed73bd4e","parentId":"3d39c20c","timestamp":"2026-10-07T10:18:20.103Z","message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"stop","timestamp":1791368300100}}`,
		`{"type":"compaction","id":"9ecc269c","parentId":"ed73bd4e","timestamp":"2026-10-07T10:18:20.112Z","summary":"Fixing the parser test.","firstKeptEntryId":"ed73bd4e","tokensBefore":182,"details":{"readFiles":[],"modifiedFiles":[]}}`,
	)
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// Senpi and gjc run pi's own extension, which sends the session file
// session_compact names; prime-agent's sends it with its name. The next prompt
// carries the packet, filed under the host whose store holds the file.
func TestPiDescendantsCompactionIsReadFromTheSessionFile(t *testing.T) {
	for _, host := range []string{"senpi", "gjc", "prime"} {
		t.Run(host, func(t *testing.T) {
			hermeticEnv(t)
			hostsNoWarmup(t)
			t.Setenv("SENPI_CODING_AGENT_DIR", "")
			t.Setenv("GJC_CODING_AGENT_DIR", "")
			t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "")
			t.Setenv("PRIME_AGENT_SESSION_DIR", "")
			t.Setenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR", "")
			workspace := compactionGitRepo(t)
			dir := index.DefaultDir()
			const id = "01a115df-02e3-778b-85fd-a6c895d75e88"
			payload := map[string]any{"session_id": id, "cwd": workspace, "transcript_path": pifamilySessionFile(t, host, workspace, id)}
			if host == "prime" {
				payload["harness"] = "prime"
			}
			out := precompactThen(t, dir, workspace, payload, "")
			if host == "prime" {
				// prime runs commands inside an ipython cell, which the reader
				// does not split into commands; the turn and the conclusion
				// still come back.
				for _, want := range []string{"Compaction context", "fix the parser test", "want 3, got 4"} {
					if !strings.Contains(out, want) {
						t.Errorf("packet lacks %q: %s", want, out)
					}
				}
			} else {
				wantCompactionPacket(t, out)
			}
			if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != host {
				t.Errorf("the capture is filed under %q, want %s", state.Harness, host)
			}
		})
	}
}

// The extension a pi descendant runs is pi's, so what it sends on compaction
// is what pi's sends: the session file, not only the id.
func TestPiDescendantExtensionsSendTheSessionFile(t *testing.T) {
	for name, src := range map[string]string{"pi-shaped": piExtensionTS("/bin/deja"), "prime": primeExtensionTS("/bin/deja")} {
		if !strings.Contains(src, `transcript_path: sessionFile`) || !strings.Contains(src, `m.getSessionFile ? m.getSessionFile()`) {
			t.Errorf("%s extension does not send the session file on compaction", name)
		}
	}
}
