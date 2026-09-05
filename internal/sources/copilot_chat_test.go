package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copilotChatRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)
	return root
}

func writeCopilotChatSession(t *testing.T, root, hash, id, workspace, body, ext string) string {
	t.Helper()
	dir := filepath.Join(root, "workspaceStorage", hash, "chatSessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if workspace != "" {
		wp := filepath.Join(root, "workspaceStorage", hash, "workspace.json")
		if err := os.WriteFile(wp, []byte(workspace), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, id+ext)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeCopilotChatJSONL(t *testing.T, dir string, lines []string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func copilotChatReq(id, user, assistant string, ts, rts int64) string {
	return `{"requestId":"` + id + `","timestamp":` + itoa64(ts) + `,"message":{"text":"` + user + `"},"response":[{"value":"` + assistant + `"}],"responseTimestamp":` + itoa64(rts) + `}`
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	u := uint64(n)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}

func TestParseCopilotChatJSONLReplay(t *testing.T) {
	root := t.TempDir()
	ts, rts := int64(1763727104742), int64(1763727400000)
	req := copilotChatReq("r1", "hello", "world", ts, rts)
	initial := `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"customTitle":"t1","requests":[]}}`

	tests := []struct {
		name string
		body string
		want int
		id   string
		user string
	}{
		{
			name: "kind0-then-push",
			body: initial + "\n" + `{"kind":2,"k":["requests"],"v":[` + req + `]}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "kind1-set-title",
			body: `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` + req + `]}}` + "\n" +
				`{"kind":1,"k":["customTitle"],"v":"from set"}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "kind3-delete-unused-field",
			body: `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` + req + `],"pendingRequests":true}}` + "\n" +
				`{"kind":3,"k":["pendingRequests"]}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "later-0-resets",
			body: `{"kind":0,"v":{"version":3,"sessionId":"old","creationDate":1763727100000,"requests":[` + req + `]}}` + "\n" +
				`{"kind":0,"v":{"version":3,"sessionId":"new","creationDate":1763727100000,"requests":[` + copilotChatReq("r2", "after", "reset", ts, rts) + `]}}` + "\n",
			want: 1, id: "new", user: "after",
		},
		{
			name: "kind1-before-0",
			body: `{"kind":1,"k":["customTitle"],"v":"nope"}` + "\n" + initial + "\n",
			want: 0,
		},
		{
			name: "malformed-line-drops",
			body: initial + "\n" + `{not json` + "\n" + `{"kind":2,"k":["requests"],"v":[` + req + `]}` + "\n",
			want: 0,
		},
		{
			name: "unknown-kind-drops",
			body: initial + "\n" + `{"kind":9,"v":1}` + "\n",
			want: 0,
		},
		{
			name: "empty-file",
			body: "",
			want: 0,
		},
		{
			name: "whitespace-only",
			body: "\n\n  \n",
			want: 0,
		},
		{
			name: "push-i-pads",
			body: initial + "\n" + `{"kind":2,"k":["requests"],"v":[` + req + `],"i":2}` + "\n",
			want: 1, id: "s1", user: "hello",
		},
		{
			name: "push-v-two-items",
			body: initial + "\n" + `{"kind":2,"k":["requests"],"v":[` +
				copilotChatReq("a", "one", "A", ts, rts) + `,` +
				copilotChatReq("b", "two", "B", ts+1, rts+1) + `]}` + "\n",
			want: 1, id: "s1", user: "one",
		},
		{
			name: "truncate-push",
			body: `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` +
				copilotChatReq("a", "keep", "A", ts, rts) + `,` +
				copilotChatReq("b", "drop", "B", ts, rts) + `]}}` + "\n" +
				`{"kind":2,"k":["requests"],"v":[` + copilotChatReq("c", "new", "C", ts, rts) + `],"i":1}` + "\n",
			want: 1, id: "s1", user: "keep",
		},
		{
			name: "missing-parent-drops",
			body: initial + "\n" + `{"kind":1,"k":["requests",0,"message"],"v":{"text":"x"}}` + "\n",
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(root, tc.name+".jsonl")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			ss, err := ParseCopilotChatFile(p)
			if err != nil {
				t.Fatalf("err %v", err)
			}
			if len(ss) != tc.want {
				t.Fatalf("sessions = %d, want %d (%#v)", len(ss), tc.want, ss)
			}
			if tc.want == 0 {
				return
			}
			if ss[0].ID != tc.id {
				t.Fatalf("id = %q, want %q", ss[0].ID, tc.id)
			}
			if ss[0].Harness != "copilot-chat" {
				t.Fatalf("harness = %q", ss[0].Harness)
			}
			if ss[0].Messages[0].Text != tc.user {
				t.Fatalf("user = %q, want %q", ss[0].Messages[0].Text, tc.user)
			}
			if got := ss[0].Messages[0].Time.UnixMilli(); got != ts {
				t.Fatalf("first user time = %d, want %d", got, ts)
			}
			if ss[0].Started.IsZero() || ss[0].Updated.IsZero() {
				t.Fatalf("times zero: %v %v", ss[0].Started, ss[0].Updated)
			}
		})
	}

	t.Run("push-v-two-items-count", func(t *testing.T) {
		p := filepath.Join(root, "push-v-two-items.jsonl")
		ss, err := ParseCopilotChatFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		var users int
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users++
			}
		}
		if users != 2 {
			t.Fatalf("users = %d, want 2", users)
		}
	})
	t.Run("truncate-keeps-two", func(t *testing.T) {
		p := filepath.Join(root, "truncate-push.jsonl")
		ss, _ := ParseCopilotChatFile(p)
		var users []string
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users = append(users, m.Text)
			}
		}
		if strings.Join(users, ",") != "keep,new" {
			t.Fatalf("users = %v", users)
		}
	})
	t.Run("kind1-set-title-applied", func(t *testing.T) {
		p := filepath.Join(root, "kind1-set-title.jsonl")
		ss, _ := ParseCopilotChatFile(p)
		if ss[0].Title != "from set" {
			t.Fatalf("title = %q", ss[0].Title)
		}
	})
}

func TestParseCopilotChatFlatJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{"version":3,"sessionId":"flat","creationDate":1763727100000,"customTitle":"flat title","requests":[` +
		copilotChatReq("r1", "ask", "ans", 1763727104742, 1763727400000) + `]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse = %#v, %v", ss, err)
	}
	if ss[0].ID != "flat" || ss[0].Title != "flat title" {
		t.Fatalf("meta %#v", ss[0])
	}
	if len(ss[0].Messages) != 2 || ss[0].Messages[1].Text != "ans" {
		t.Fatalf("messages %#v", ss[0].Messages)
	}
}

func TestParseCopilotChatStringResponseAndV2Title(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "str.json")
	body := `{"version":3,"sessionId":"s","creationDate":1763727100000,"requests":[{` +
		`"timestamp":1763727104742,"message":"q","response":"plain reply","responseTimestamp":1763727400000}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 || ss[0].Messages[1].Text != "plain reply" {
		t.Fatalf("string response %#v, %v", ss, err)
	}

	p2 := filepath.Join(dir, "v2.json")
	v2 := `{"version":2,"computedTitle":"old title` + "\\n" + `second line","sessionId":"v2","creationDate":1763727100000,"customTitle":"ignored","requests":[{` +
		`"timestamp":1763727104742,"message":"q","response":[{"value":"a"}],"responseTimestamp":1763727400000}]}`
	if err := os.WriteFile(p2, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err = ParseCopilotChatFile(p2)
	if err != nil || len(ss) != 1 || ss[0].Title != "old title" {
		t.Fatalf("v2 title = %#v, %v", ss, err)
	}

	p3 := filepath.Join(dir, "nover.json")
	nover := `{"customTitle":"should-not-stick","sessionId":"n","creationDate":1763727100000,"requests":[{` +
		`"timestamp":1763727104742,"message":"q","response":[{"value":"a"}],"responseTimestamp":1763727400000}]}`
	if err := os.WriteFile(p3, []byte(nover), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err = ParseCopilotChatFile(p3)
	if err != nil || len(ss) != 1 || ss[0].Title != "" {
		t.Fatalf("missing version title = %q, %v", ss[0].Title, err)
	}
}

func TestParseCopilotChatMissingFile(t *testing.T) {
	if _, err := ParseCopilotChatFile(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestCopilotChatAccountLabelNeverIndexed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"kind":0,"v":{"version":3,"sessionId":"s","creationDate":1763727100000,` +
		`"inputState":{"selectedModel":{"metadata":{"auth":{"accountLabel":"someone"}}}},` +
		`"requests":[` + copilotChatReq("r", "the retry loop", "ok", 1763727104742, 1763727400000) + `]}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "someone") {
			t.Fatalf("accountLabel leaked into %s: %q", m.Role, m.Text)
		}
	}
}

func TestCopilotChatSkipsThinkingAndIndexesTools(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{
		"version":3,"sessionId":"s","creationDate":1763727100000,
		"requests":[{
			"timestamp":1763727104742,
			"message":{"text":"fix retry"},
			"response":[
				{"kind":"thinking","value":"secret-reason"},
				{"kind":"progressMessage","content":{"value":"working"}},
				{"value":"visible answer"},
				{"kind":"toolInvocationSerialized","toolId":"copilot_readFile",
					"resultDetails":[{"path":"/w/retry.go"}],
					"toolSpecificData":{"kind":"terminal","commandLine":{"original":"go test ./...","userEdited":"go test"}}},
				{"kind":"inlineReference","inlineReference":{"path":"/w/other.go"}}
			],
			"responseTimestamp":1763727400000
		}]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	var assistant, files, cmds []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case "assistant":
			assistant = append(assistant, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		case RoleCommand:
			cmds = append(cmds, m.Text)
		}
	}
	if len(assistant) != 1 || assistant[0] != "visible answer" {
		t.Fatalf("assistant = %v", assistant)
	}
	joined := strings.Join(assistant, "\n")
	if strings.Contains(joined, "secret-reason") || strings.Contains(joined, "working") {
		t.Fatalf("chrome leaked: %q", joined)
	}
	if strings.Join(files, ",") != "/w/retry.go,/w/other.go" {
		t.Fatalf("files = %v", files)
	}
	if strings.Join(cmds, ",") != "$ go test" {
		t.Fatalf("cmds = %v", cmds)
	}
}

func TestCopilotChatProjectFromWorkspaceJSON(t *testing.T) {
	root := copilotChatRoot(t)
	posix := writeCopilotChatSession(t, root, "hash1", "s1",
		`{"folder":"file:///tmp/registry-demo"}`,
		`{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[`+
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000)+`]}}`+"\n",
		".jsonl")
	ss, err := ParseCopilotChatFile(posix)
	if err != nil || len(ss) != 1 || ss[0].Project != "registry-demo" {
		t.Fatalf("posix project = %#v, %v", ss, err)
	}

	win := writeCopilotChatSession(t, root, "hash2", "s2",
		`{"folder":"file:///c%3A/Users/x/proj/demo"}`,
		`{"kind":0,"v":{"version":3,"sessionId":"s2","creationDate":1763727100000,"requests":[`+
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000)+`]}}`+"\n",
		".jsonl")
	ss, err = ParseCopilotChatFile(win)
	if err != nil || len(ss) != 1 || ss[0].Project != "demo" {
		t.Fatalf("windows c%%3A project = %#v, %v", ss, err)
	}

	multi := writeCopilotChatSession(t, root, "hash3", "s3",
		`{"workspace":"file:///tmp/multi.code-workspace"}`,
		`{"kind":0,"v":{"version":3,"sessionId":"s3","creationDate":1763727100000,"requests":[`+
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000)+`]}}`+"\n",
		".jsonl")
	ss, err = ParseCopilotChatFile(multi)
	if err != nil || len(ss) != 1 || ss[0].Project != "multi.code-workspace" {
		t.Fatalf("workspace uri project = %#v, %v", ss, err)
	}
}

func TestCopilotChatEmptyWindowWorkingDirectory(t *testing.T) {
	root := copilotChatRoot(t)
	dir := filepath.Join(root, "globalStorage", "emptyWindowChatSessions")
	p := writeCopilotChatJSONL(t, filepath.Join(dir, "ew.jsonl"), []string{
		`{"kind":0,"v":{"version":3,"sessionId":"ew","creationDate":1763727100000,` +
			`"workingDirectory":"file:///tmp/other-proj","requests":[` +
			copilotChatReq("r", "q", "a", 1763727104742, 1763727400000) + `]}}`,
	})
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 || ss[0].Project != "other-proj" {
		t.Fatalf("empty-window project = %#v, %v", ss, err)
	}
}

func TestCopilotChatSessionFilesDedupeAndDiscovery(t *testing.T) {
	root := copilotChatRoot(t)
	body := `{"kind":0,"v":{"version":3,"sessionId":"s1","creationDate":1763727100000,"requests":[` +
		copilotChatReq("r", "q", "a", 1763727104742, 1763727400000) + `]}}` + "\n"
	jsonl := writeCopilotChatSession(t, root, "h", "s1", `{"folder":"file:///tmp/p"}`, body, ".jsonl")
	flat := strings.TrimSuffix(jsonl, ".jsonl") + ".json"
	if err := os.WriteFile(flat, []byte(`{"version":3,"sessionId":"s1","requests":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	xfer := filepath.Join(root, "globalStorage", "transferredChatSessions")
	if err := os.MkdirAll(xfer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xfer, "x.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ew := filepath.Join(root, "globalStorage", "emptyWindowChatSessions")
	if err := os.MkdirAll(ew, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ew, "ew.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	files := CopilotChatSessionFiles()
	var bases []string
	for _, f := range files {
		bases = append(bases, filepath.Base(f))
		if strings.Contains(f, "transferredChatSessions") {
			t.Fatalf("transferred listed: %s", f)
		}
		if strings.HasSuffix(f, ".json") && !strings.HasSuffix(f, ".jsonl") {
			t.Fatalf("json sibling listed: %s", f)
		}
	}
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	got := strings.Join(bases, ",")
	if !strings.Contains(got, "s1.jsonl") || !strings.Contains(got, "ew.jsonl") {
		t.Fatalf("bases = %v", bases)
	}
	if KindForPath(jsonl) != "copilot-chat" {
		t.Fatalf("KindForPath = %q", KindForPath(jsonl))
	}
	if KindForPath(filepath.Join(xfer, "x.jsonl")) != "" {
		t.Fatal("transferred should not match")
	}
	ss := LoadCopilotChat()
	if len(ss) != 2 {
		t.Fatalf("LoadCopilotChat = %d", len(ss))
	}
}

func TestCopilotChatRootsEnvOverride(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", missing)
	got := CopilotChatRoots()
	if len(got) != 1 || got[0] != missing {
		t.Fatalf("roots = %v", got)
	}
}

func TestParseCopilotChatFilenameStemFallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "stem-id.json")
	body := `{"version":3,"creationDate":1763727100000,"requests":[` +
		copilotChatReq("r", "q", "a", 1763727104742, 1763727400000) + `]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 || ss[0].ID != "stem-id" {
		t.Fatalf("id fallback %#v, %v", ss, err)
	}
}

func TestParseCopilotChatEmptyRequestsDropped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(p, []byte(`{"version":3,"sessionId":"e","requests":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 0 {
		t.Fatalf("empty session %#v, %v", ss, err)
	}
}

// A push whose index is past the end pads with holes first, the way
// `arr.length = i` does in VS Code's reader, so the pushed request lands at
// the index the writer meant and not two slots early.
func TestCopilotChatPushPastTheEndPadsLikeVSCode(t *testing.T) {
	body := `{"kind":0,"v":{"version":3,"sessionId":"s1","requests":[]}}` + "\n" +
		`{"kind":2,"k":["requests"],"v":[{"requestId":"r"}],"i":2}` + "\n"
	state, ok := copilotChatReplay("pad.jsonl", []byte(body))
	if !ok {
		t.Fatal("replay failed")
	}
	reqs, _ := state["requests"].([]any)
	if len(reqs) != 3 || reqs[0] != nil || reqs[1] != nil {
		t.Fatalf("requests after padded push = %#v, want two holes then the request", reqs)
	}
	if m, _ := reqs[2].(map[string]any); m["requestId"] != "r" {
		t.Fatalf("pushed request not at index 2: %#v", reqs[2])
	}
}
