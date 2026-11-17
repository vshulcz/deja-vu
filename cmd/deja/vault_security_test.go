package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func withSecurityVaultEnv(t *testing.T) (tmp, dir string) {
	t.Helper()
	tmp = hermeticEnv(t)
	dir = index.DefaultDir()
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })
	return tmp, dir
}

func seedVaultSecuritySessions(t *testing.T, claudeRoot string) {
	t.Helper()
	sessions := []struct {
		dir  string
		file string
		id   string
		text string
	}{
		{
			dir:  filepath.Join(claudeRoot, "-tmp-alpha"),
			file: "sess-alpha-1.jsonl",
			id:   "sess-alpha-1",
			text: "alpha deployment secret canary_alpha_9911",
		},
		{
			dir:  filepath.Join(claudeRoot, "-tmp-beta"),
			file: "sess-beta-1.jsonl",
			id:   "sess-beta-1",
			text: "beta internal secret canary_beta_8822",
		},
	}
	for _, s := range sessions {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","sessionId":"` + s.id + `","timestamp":"2026-11-17T01:00:00Z","message":{"role":"user","content":"` + s.text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(s.dir, s.file), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func sha256HexToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func mcpToolIsError(reply map[string]any) bool {
	if _, hasErr := reply["error"]; hasErr {
		return true
	}
	res, _ := reply["result"].(map[string]any)
	isErr, _ := res["isError"].(bool)
	return isErr
}

func vaultReplyText(reply map[string]any) string {
	if errObj, ok := reply["error"].(map[string]any); ok {
		msg, _ := errObj["message"].(string)
		return msg
	}
	res, _ := reply["result"].(map[string]any)
	items, _ := res["content"].([]any)
	if len(items) == 0 {
		return ""
	}
	first, _ := items[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func repliesByID(replies []map[string]any) map[int]map[string]any {
	byID := make(map[int]map[string]any, len(replies))
	for _, r := range replies {
		if id, ok := r["id"].(float64); ok {
			byID[int(id)] = r
		}
	}
	return byID
}

func captureStdoutRun(t *testing.T, args []string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	runErr := run(args)
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out, runErr
}

func TestVaultSecurityEndToEnd(t *testing.T) {
	tmp, dir := withSecurityVaultEnv(t)
	seedVaultSecuritySessions(t, filepath.Join(tmp, "claude"))

	if err := run([]string{"index"}); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := run([]string{"remember", "--project", "tmp/alpha", "personal canary_note_7733"}); err != nil {
		t.Fatalf("remember: %v", err)
	}

	// Verify vault CLI validation branches before initializing.
	for _, bad := range [][]string{
		{"init"},
		{"init", "--encrypted", "--badflag"},
		{"init", "--encrypted", "--auto-lock"},
		{"init", "--encrypted", "--auto-lock", "notaduration"},
		{"init", "--encrypted", "extra"},
		{"lock", "--badflag"},
		{"lock", "extra"},
		{"unlock", "--badflag"},
		{"unlock", "extra"},
	} {
		if err := run(bad); err == nil {
			t.Errorf("run(%v) unexpectedly succeeded", bad)
		}
	}

	// Initialize encrypted storage reading passphrase via stdin.
	oldStdin := os.Stdin
	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = wIn.WriteString("vault-passphrase-42\n")
	_ = wIn.Close()
	os.Stdin = rIn
	_, initErr := captureStdoutRun(t, []string{"init", "--encrypted", "--passphrase-stdin", "--auto-lock", "30m"})
	os.Stdin = oldStdin
	_ = rIn.Close()
	if initErr != nil {
		t.Fatalf("init --encrypted --passphrase-stdin: %v", initErr)
	}

	notesBytes, err := os.ReadFile(vault.NotesPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(notesBytes), "canary_note_7733") || !strings.HasPrefix(string(notesBytes), vault.NoteLinePrefix) {
		t.Fatalf("notes.jsonl not sealed on disk after init: %s", notesBytes)
	}

	// Lock the vault and confirm zero derived plaintext remains anywhere in cache/notes.
	t.Setenv(vault.EnvVaultPassphrase, "vault-passphrase-42")
	if _, err := captureStdoutRun(t, []string{"lock"}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	t.Setenv(vault.EnvVaultPassphrase, "")

	for _, p := range []string{vault.NotesPath(), vault.ArchivePath(dir), usage.SnapshotPath(dir)} {
		b, _ := os.ReadFile(p)
		for _, canary := range []string{"canary_alpha_9911", "canary_beta_8822", "canary_note_7733"} {
			if strings.Contains(string(b), canary) {
				t.Fatalf("plaintext canary %s found on disk at %s while locked", canary, p)
			}
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plaintext indexDir %s still exists while locked", dir)
	}

	// All CLI/MCP/hook read+write surfaces must fail closed while locked.
	if err := run([]string{"search", "canary_alpha_9911"}); err == nil {
		t.Fatal("CLI search succeeded while vault locked")
	}
	if err := run(nil); err == nil {
		t.Fatal("bare deja succeeded while vault locked")
	}
	if err := run([]string{"remember", "--project", "tmp/alpha", "blocked_while_locked"}); err == nil {
		t.Fatal("CLI remember succeeded while vault locked")
	}
	if err := run([]string{"repo", "add", "--kind", "gotcha", "blocked_repo"}); err == nil {
		t.Fatal("CLI repo add succeeded while vault locked")
	}

	lockedReplies := serveFrames(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remember","arguments":{"text":"blocked","project":"tmp/alpha"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"deja://session/claude:sess-alpha-1"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"prompts/get","params":{"name":"deja","arguments":{"query":"canary_alpha_9911"}}}`,
	)
	setMCPClient("")
	lockedByID := repliesByID(lockedReplies)
	for id := 2; id <= 6; id++ {
		if !mcpToolIsError(lockedByID[id]) {
			t.Errorf("MCP call id=%d (%v) succeeded while vault locked", id, lockedByID[id])
		}
	}
	csRes := contextSearchCall(dir, json.RawMessage(`{"query":"canary_alpha_9911"}`))
	if isErr, _ := csRes["isError"].(bool); !isErr {
		t.Fatalf("context_search succeeded while vault locked: %v", csRes)
	}

	hookOut, hookErr := captureStdoutRun(t, []string{"hook-session-end", "--json"})
	if hookErr != nil || strings.TrimSpace(hookOut) != "{}" {
		t.Fatalf("hook-session-end --json while locked = (%q, %v)", hookOut, hookErr)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("locked hook created index dir %s", dir)
	}

	// `deja log --json` must remain readable while locked and never record raw queries/canaries.
	var logBuf bytes.Buffer
	if err := runLogTo(&logBuf, dir, []string{"--json", "50"}); err != nil {
		t.Fatalf("log --json while locked: %v", err)
	}
	usageRaw, _ := os.ReadFile(usage.Path(dir))
	if !strings.Contains(string(usageRaw), `"kind":"access"`) || !strings.Contains(string(usageRaw), `"decision":"locked"`) {
		t.Fatalf("expected locked access events in usage log: %s", usageRaw)
	}
	if strings.Contains(string(usageRaw), "canary_") || strings.Contains(string(usageRaw), "blocked_while_locked") {
		t.Fatalf("secret query/text leaked into .usage.jsonl: %s", usageRaw)
	}
	var tableBuf bytes.Buffer
	if err := runLogTo(&tableBuf, dir, []string{"10"}); err != nil {
		t.Fatalf("log table while locked: %v", err)
	}
	if !strings.Contains(tableBuf.String(), "access") || !strings.Contains(tableBuf.String(), "locked") {
		t.Fatalf("log table missing access row: %s", tableBuf.String())
	}

	// Wrong passphrase fails; correct passphrase restores recall & notes.
	t.Setenv(vault.EnvVaultPassphrase, "wrong-passphrase")
	if err := run([]string{"unlock"}); err == nil {
		t.Fatal("unlock succeeded with wrong passphrase")
	}
	t.Setenv(vault.EnvVaultPassphrase, "vault-passphrase-42")
	if _, err := captureStdoutRun(t, []string{"unlock"}); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := run([]string{"index"}); err != nil {
		t.Fatalf("re-index after unlock: %v", err)
	}
	unlockedReplies := serveFrames(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_note_7733"}}}`,
	)
	setMCPClient("")
	unlockedByID := repliesByID(unlockedReplies)
	if len(unlockedByID) != 2 || mcpToolIsError(unlockedByID[2]) || !strings.Contains(vaultReplyText(unlockedByID[2]), "canary_note_7733") {
		t.Fatalf("unlocked MCP recall failed: %v", unlockedReplies)
	}
}

func TestClientPolicyAuthAndScopesEndToEnd(t *testing.T) {
	tmp, dir := withSecurityVaultEnv(t)
	seedVaultSecuritySessions(t, filepath.Join(tmp, "claude"))
	if err := run([]string{"index"}); err != nil {
		t.Fatalf("index: %v", err)
	}

	polPath := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", polPath)
	polJSON := `{
	  "require_clients": true,
	  "clients": {
	    "claude-code": {"read": true, "write": true, "projects": ["tmp/alpha"]},
	    "readonly": {"read": true, "write": false, "projects": ["*"]},
	    "revoked": {"read": false, "write": false}
	  },
	  "client_tokens": {
	    "claude-code": "` + sha256HexToken("tok-claude-secret") + `",
	    "readonly": "` + sha256HexToken("tok-ro-secret") + `",
	    "revoked": "` + sha256HexToken("tok-revoked") + `"
	  }
	}`
	if err := os.WriteFile(polPath, []byte(polJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Spoofed/missing token MCP client is rejected even if clientInfo.name matches an enrolled client.
	noTok := repliesByID(serveFrames(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
	))
	setMCPClient("")
	if !mcpToolIsError(noTok[2]) || !strings.Contains(vaultReplyText(noTok[2]), "client token required") {
		t.Fatalf("expected missing token denial, got %v", noTok[2])
	}

	// Unenrolled client is rejected.
	unknown := repliesByID(serveFrames(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"rogue-client"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
	))
	setMCPClient("")
	if !mcpToolIsError(unknown[2]) || !strings.Contains(vaultReplyText(unknown[2]), "client not enrolled") {
		t.Fatalf("expected unenrolled client denial, got %v", unknown[2])
	}

	// Wrong token is rejected.
	t.Setenv(envDejaClientToken, "wrong-secret")
	badTok := repliesByID(serveFrames(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
	))
	setMCPClient("")
	if !mcpToolIsError(badTok[2]) || !strings.Contains(vaultReplyText(badTok[2]), "invalid client token") {
		t.Fatalf("expected invalid token denial, got %v", badTok[2])
	}

	// Valid claude-code token enforces tmp/alpha project scope across recall, resources, and remember.
	t.Setenv(envDejaClientToken, "tok-claude-secret")
	scoped := repliesByID(serveFrames(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_beta_8822"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"deja://session/claude:sess-beta-1"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"remember","arguments":{"text":"cross-project write","project":"tmp/beta"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"remember","arguments":{"text":"scoped alpha note","project":"tmp/alpha"}}}`,
	))
	setMCPClient("")
	if mcpToolIsError(scoped[2]) || !strings.Contains(vaultReplyText(scoped[2]), "canary_alpha_9911") {
		t.Fatalf("in-scope alpha recall failed: %v", scoped[2])
	}
	if betaTxt := vaultReplyText(scoped[3]); strings.Contains(betaTxt, "beta internal secret") || !strings.Contains(betaTxt, "not being shown") {
		t.Fatalf("out-of-scope beta content check failed: %v", scoped[3])
	}
	listRaw, _ := json.Marshal(scoped[4])
	if !strings.Contains(string(listRaw), "sess-alpha-1") || strings.Contains(string(listRaw), "sess-beta-1") {
		t.Fatalf("resources/list leaked out-of-scope session: %s", listRaw)
	}
	if !mcpToolIsError(scoped[5]) {
		t.Fatalf("resources/read allowed out-of-scope beta session: %v", scoped[5])
	}
	if !mcpToolIsError(scoped[6]) || !strings.Contains(vaultReplyText(scoped[6]), "project outside client scope") {
		t.Fatalf("cross-project remember was not denied: %v", scoped[6])
	}
	if mcpToolIsError(scoped[7]) {
		t.Fatalf("in-scope remember failed: %v", scoped[7])
	}

	// CLI readonly client cannot write, revoked client cannot read or write.
	t.Setenv(envDejaClient, "readonly")
	t.Setenv(envDejaClientToken, "tok-ro-secret")
	if err := run([]string{"remember", "--project", "tmp/alpha", "readonly write"}); err == nil || !strings.Contains(err.Error(), "client write not granted") {
		t.Fatalf("readonly remember = %v, want client write not granted", err)
	}
	if err := run([]string{"search", "canary_alpha_9911"}); err != nil {
		t.Fatalf("readonly search failed: %v", err)
	}

	t.Setenv(envDejaClient, "revoked")
	t.Setenv(envDejaClientToken, "tok-revoked")
	if err := run([]string{"search", "canary_alpha_9911"}); err == nil {
		t.Fatal("revoked client search succeeded")
	}
	if err := run([]string{"log"}); err == nil {
		t.Fatal("revoked client log succeeded")
	}

	// Verify content-free access audit log recorded allow and deny entries cleanly.
	events := usage.Events(dir, 50)
	var sawAllow, sawDeny bool
	for _, ev := range events {
		if ev.Kind == usage.KindAccess {
			if ev.Decision == "allow" {
				sawAllow = true
			}
			if ev.Decision == "deny" {
				sawDeny = true
			}
		}
	}
	if !sawAllow || !sawDeny {
		t.Fatalf("expected both allow and deny access events, got: %+v", events)
	}
}
