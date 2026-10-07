package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// DeepSeek Harness appends compaction/start to the session log and shadows the
// summarised turns rather than dropping them, so the log still holds them. The
// plugin sends the session's uuid on compaction/start; deja finds the log by it
// and the next recall carries the packet. Records as dsh 0.1.1-rc.2 wrote them
// on a stand, in the raw (unframed) encoding dsh can be configured to write.
func TestDeepSeekCompactionIsReadFromTheSessionLog(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "03698097-e226-4edb-93a7-b2890b9e469c"
	cwd, _ := json.Marshal(workspace)
	output, _ := json.Marshal("--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n[exit code: 1]")
	lines := []string{
		`{"type":"session","version":0,"id":"session-` + id + `","createdAt":1791368690569,"cwd":` + string(cwd) + `,"delegationDepth":0}`,
		`{"type":"turn/start","seq":4,"time":1791368690578,"data":{"turn":1}}`,
		`{"type":"step/start","seq":6,"time":1791368690756,"data":{"turn":1,"step":1}}`,
		`{"type":"user/message","seq":7,"time":1791368690756,"data":{"content":[{"type":"text","text":"fix the parser test"}],"source":{"kind":"user"},"role":"user","id":"1417e875-4d5f-47af-bf83-7e1483a25dcb"},"surfaceOp":"append"}`,
		`{"type":"user/message","seq":8,"time":1791368690757,"data":{"content":[{"type":"text","text":"Current runtime context. This snapshot supersedes earlier runtime-context snapshots."}],"source":{"kind":"plugin","plugin":"@deepseek-ai/dsh-system-prompt","form":"snapshot"},"role":"user","id":"r1"},"surfaceOp":"append"}`,
		`{"type":"assistant/message","seq":20,"time":1791368690804,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_1","name":"bash","arguments":"{\"command\": \"go test ./parser/...\", \"description\": \"run the parser tests\"}"}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"},"id":"a1"}},"surfaceOp":"append"}`,
		`{"type":"tool/call","seq":21,"time":1791368690805,"data":{"turn":1,"step":1,"callId":"call_1","name":"bash","arguments":"{\"command\": \"go test ./parser/...\", \"description\": \"run the parser tests\"}"}}`,
		`{"type":"tool/result","seq":22,"time":1791368690928,"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_1"},"content":[{"type":"tool-result","toolCallId":"call_1","content":[{"type":"text","text":` + string(output) + `}],"isError":false}],"role":"user","id":"t1"}},"surfaceOp":"append"}`,
		`{"type":"assistant/message","seq":23,"time":1791368690930,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"},"id":"a2"}},"surfaceOp":"append"}`,
		`{"type":"compaction/start","seq":24,"time":1791368690933,"data":{"compactionId":"3b712367-45bd-4f84-847b-fe9a46642758","turn":1}}`,
	}
	compactionWrite(t, filepath.Join(sources.DeepSeekRoot(), "--w-proj--", "session-"+id, "session.jsonl"), strings.Join(lines, "\n")+"\n")
	// What the plugin sends on compaction/start.
	out := precompactThen(t, dir, workspace, map[string]any{"session_id": id, "cwd": workspace, "harness": "deepseek"}, "")
	wantCompactionPacket(t, out)
}
