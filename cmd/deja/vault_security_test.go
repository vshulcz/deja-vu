package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func withSecurityVaultEnv(t *testing.T) (tmp, dir string) {
	t.Helper()
	tmp = hermeticEnv(t)
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
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

	// init --encrypted must seal the derived index, not leave it plaintext
	// until a later `deja lock`.
	if !vault.IsLocked(dir) {
		t.Fatal("init --encrypted left the store unsealed (IsLocked false)")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("init --encrypted left plaintext indexDir %s on disk", dir)
	}
	if _, statErr := os.Stat(vault.ArchivePath(dir)); statErr != nil {
		t.Fatalf("init --encrypted did not write a sealed archive: %v", statErr)
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
	listRaw, marshalErr := json.Marshal(scoped[4])
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
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

func TestVaultLiveSidecarsMigrationAndLockedPreservation(t *testing.T) {
	_, dir := withSecurityVaultEnv(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	preDir := filepath.Join(deferredRoot(dir), "sesskey")
	if err := os.MkdirAll(preDir, 0o700); err != nil {
		t.Fatal(err)
	}
	preFile := filepath.Join(preDir, "0001-prompt")
	if err := os.WriteFile(preFile, []byte("canary_preinit_deferred"), 0o600); err != nil {
		t.Fatal(err)
	}

	const pass = "sidecar-vault-pass-42"
	if _, err := vault.Init(dir, pass, 30*time.Minute); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	t.Setenv(vault.EnvVaultPassphrase, pass)
	if err := vault.Unlock(dir, pass); err != nil {
		t.Fatalf("vault.Unlock after Init: %v", err)
	}

	// Pre-Init plaintext deferred file must have been sealed by the cmd_sidecars migrator.
	preRaw, err := os.ReadFile(preFile)
	if err != nil {
		t.Fatalf("read migrated pre-init deferred file: %v", err)
	}
	if strings.Contains(string(preRaw), "canary_preinit_deferred") || !vault.IsSealedBytes(preRaw) {
		t.Fatalf("migrated deferred file not sealed: %q", preRaw)
	}
	if got := takeDeferred(dir, "sesskey"); got != "canary_preinit_deferred" {
		t.Fatalf("takeDeferred(sesskey) = %q, want canary_preinit_deferred", got)
	}

	// Verify live AEAD encryption + unlocked round-trips for all cmd/deja sidecars.
	app := tuiApp{dir: dir, query: []rune("canary_tui_query"), rows: []tuiRow{{}}}
	app.remember()
	tuiRaw, err := os.ReadFile(tuiHistoryPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tuiRaw), "canary_tui_query") || !vault.IsSealedBytes(tuiRaw) {
		t.Fatalf(".tui-history not sealed: %q", tuiRaw)
	}
	if hist := loadTUIHistory(dir); len(hist) != 1 || hist[0] != "canary_tui_query" {
		t.Fatalf("loadTUIHistory = %#v, want [canary_tui_query]", hist)
	}

	writeLiveSessions(dir, map[string]time.Time{"canary-live-sess": time.Now().UTC()})
	liveRaw, err := os.ReadFile(liveSessionsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(liveRaw), "canary-live-sess") || !vault.IsSealedBytes(liveRaw) {
		t.Fatalf(".live not sealed: %q", liveRaw)
	}
	if live, lErr := readLiveSessionsErr(dir); lErr != nil || live["canary-live-sess"].IsZero() {
		t.Fatalf("readLiveSessionsErr = (%v, %v), want canary-live-sess", live, lErr)
	}

	stampEnvironmentServed(dir)
	envRaw, err := os.ReadFile(environmentMarker(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsSealedBytes(envRaw) {
		t.Fatalf(".envblock not sealed: %q", envRaw)
	}
	if !environmentServedRecently(dir) {
		t.Fatal("environmentServedRecently = false right after stampEnvironmentServed")
	}

	if !dejaVuLineDue(dir, "canary-dj-sess") {
		t.Fatal("first dejaVuLineDue = false, want true")
	}
	djRaw, err := os.ReadFile(dir + ".dejavu")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(djRaw), "canary-dj-sess") || !vault.IsSealedBytes(djRaw) {
		t.Fatalf(".dejavu not sealed: %q", djRaw)
	}
	if dejaVuLineDue(dir, "canary-dj-sess") {
		t.Fatal("second dejaVuLineDue within window = true, want false")
	}

	forkPlain := "canary-fork-sid canary-fork-parent " + strconv.FormatInt(time.Now().Unix(), 10)
	forkSealed, err := vault.SealSidecarLine(dir, vault.DomainSidecar, []byte(forkPlain))
	if err != nil {
		t.Fatal(err)
	}
	if wErr := os.WriteFile(forkMarksPath(dir), []byte(forkSealed+"\n"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	forkRaw, err := os.ReadFile(forkMarksPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(forkRaw), "canary-fork-sid") || strings.Contains(string(forkRaw), "canary-fork-parent") {
		t.Fatalf(".forks leaked plaintext: %q", forkRaw)
	}
	if got := claudeForkMark(dir, "canary-fork-sid"); got != "canary-fork-parent" {
		t.Fatalf("claudeForkMark = %q, want canary-fork-parent", got)
	}
	pruneForkMarks(forkMarksPath(dir))
	if got := claudeForkMark(dir, "canary-fork-sid"); got != "canary-fork-parent" {
		t.Fatalf("claudeForkMark after prune = %q, want canary-fork-parent", got)
	}

	deferText(dir, "sesskey2", "hook-prompt", "canary_live_deferred")
	liveDefEntries, err := os.ReadDir(filepath.Join(deferredRoot(dir), "sesskey2"))
	if err != nil || len(liveDefEntries) != 1 {
		t.Fatalf("expected 1 deferred entry for sesskey2, got %v (err=%v)", liveDefEntries, err)
	}
	liveDefRaw, err := os.ReadFile(filepath.Join(deferredRoot(dir), "sesskey2", liveDefEntries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(liveDefRaw), "canary_live_deferred") || !vault.IsSealedBytes(liveDefRaw) {
		t.Fatalf("deferred/* not sealed: %q", liveDefRaw)
	}

	// Simulate missing DEK while sidecar files sit sealed on disk: every reader/writer
	// must fail closed without destroying or truncating sealed state.
	t.Setenv(vault.EnvVaultPassphrase, "")
	vault.ClearCache()

	if hasDeferred(dir) {
		t.Fatal("hasDeferred returned true with missing DEK")
	}
	if hasDeferredFor(dir, "sesskey2") {
		t.Fatal("hasDeferredFor returned true with missing DEK")
	}
	if got := takeDeferred(dir, "sesskey2"); got != "" {
		t.Fatalf("takeDeferred returned %q with missing DEK", got)
	}
	sweepDeferred(dir)
	writeLiveSessions(dir, nil)
	dropHookCaches(dir)
	pruneForkMarks(forkMarksPath(dir))
	if dejaVuLineDue(dir, "canary-dj-new") {
		t.Fatal("dejaVuLineDue returned true with missing DEK")
	}
	if hist := loadTUIHistory(dir); len(hist) != 0 {
		t.Fatalf("loadTUIHistory returned %#v with missing DEK", hist)
	}
	if got := claudeForkMark(dir, "canary-fork-sid"); got != "" {
		t.Fatalf("claudeForkMark returned %q with missing DEK", got)
	}

	// Confirm sealed sidecar bytes were preserved on disk across locked calls.
	if _, err := os.Stat(filepath.Join(deferredRoot(dir), "sesskey2", liveDefEntries[0].Name())); err != nil {
		t.Fatalf("deferred file was removed while locked: %v", err)
	}
	if afterLive, err := os.ReadFile(liveSessionsPath(dir)); err != nil || !bytes.Equal(afterLive, liveRaw) {
		t.Fatalf(".live changed under writeLiveSessions(dir, nil) while DEK missing: err=%v", err)
	}
	if afterDJ, err := os.ReadFile(dir + ".dejavu"); err != nil || !bytes.Equal(afterDJ, djRaw) {
		t.Fatalf(".dejavu changed under dejaVuLineDue while DEK missing: err=%v", err)
	}

	// Restore DEK and confirm full recovery of preserved sidecars, plus lock -> unlock archive round-trip.
	t.Setenv(vault.EnvVaultPassphrase, pass)
	if err := vault.Unlock(dir, pass); err != nil {
		t.Fatalf("vault.Unlock recovery: %v", err)
	}
	if !hasDeferred(dir) || !hasDeferredFor(dir, "sesskey2") {
		t.Fatal("deferred state missing after unlock")
	}
	if got := takeDeferred(dir, "sesskey2"); got != "canary_live_deferred" {
		t.Fatalf("recovered takeDeferred(sesskey2) = %q, want canary_live_deferred", got)
	}
	if live, err := readLiveSessionsErr(dir); err != nil || live["canary-live-sess"].IsZero() {
		t.Fatalf("recovered readLiveSessionsErr = (%v, %v)", live, err)
	}
	if hist := loadTUIHistory(dir); len(hist) != 1 || hist[0] != "canary_tui_query" {
		t.Fatalf("recovered loadTUIHistory = %#v", hist)
	}
	if got := claudeForkMark(dir, "canary-fork-sid"); got != "canary-fork-parent" {
		t.Fatalf("recovered claudeForkMark = %q", got)
	}
	if !environmentServedRecently(dir) {
		t.Fatal("recovered environmentServedRecently = false")
	}
	if dejaVuLineDue(dir, "canary-dj-sess") {
		t.Fatal("preserved .dejavu window failed to rate-limit canary-dj-sess")
	}
	if !dejaVuLineDue(dir, "canary-dj-new") {
		t.Fatal("unlocked dejaVuLineDue for fresh session failed")
	}

	if err := vault.Lock(dir, pass); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("indexDir still exists after vault.Lock: %v", statErr)
	}
	if err := vault.Unlock(dir, pass); err != nil {
		t.Fatalf("vault.Unlock after full Lock: %v", err)
	}
	if live, err := readLiveSessionsErr(dir); err != nil || live["canary-live-sess"].IsZero() {
		t.Fatalf("readLiveSessionsErr after Lock+Unlock = (%v, %v)", live, err)
	}
}

type memTestKeyring struct {
	mu   sync.Mutex
	keys map[string][]byte
}

func (m *memTestKeyring) Name() string { return "keyring" }

func (m *memTestKeyring) Set(keyID string, dek []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keys == nil {
		m.keys = make(map[string][]byte)
	}
	cp := append([]byte(nil), dek...)
	m.keys[keyID] = cp
	return nil
}

func (m *memTestKeyring) Get(keyID string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dek, ok := m.keys[keyID]
	if !ok {
		return nil, errors.New("keyring entry not found")
	}
	return append([]byte(nil), dek...), nil
}

func (m *memTestKeyring) Delete(keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, keyID)
	return nil
}

func (m *memTestKeyring) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.keys)
}

func captureDoctorArgs(t *testing.T, args []string, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := runDoctor(&buf, args, nil, dir); err != nil {
		t.Fatalf("runDoctor(%v): %v", args, err)
	}
	return buf.String()
}

func runHookPromptJSON(t *testing.T, payload string) (string, error) {
	t.Helper()
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		_ = r.Close()
	}()
	return captureStdoutRun(t, []string{"hook-prompt"})
}

func TestVaultLauncherEnvKeyringHooksAndDoctor(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := index.DefaultDir()
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	kr := &memTestKeyring{}
	restore := vault.SetKeyringForTest(kr)
	defer restore()
	t.Setenv("DEJA_VAULT_BACKEND", "keyring")

	// 1. launcherEnv must forward DEJA_VAULT_BACKEND and DEJA_VAULT_FILE, never DEJA_VAULT_PASSPHRASE.
	vaultFile := filepath.Join(tmp, "custom-vault.json")
	pairs := launcherEnv([]string{
		"HOME=" + tmp,
		"DEJA_VAULT_BACKEND=keyring",
		"DEJA_VAULT_FILE=" + vaultFile,
		"DEJA_VAULT_PASSPHRASE=never-leak-this",
		"DEJA_INDEX_DIR=" + dir,
	})
	envMap := map[string]string{}
	for _, kv := range pairs {
		envMap[kv[0]] = kv[1]
	}
	if envMap["DEJA_VAULT_BACKEND"] != "keyring" {
		t.Fatalf("launcherEnv missing DEJA_VAULT_BACKEND: %+v", envMap)
	}
	if envMap["DEJA_VAULT_FILE"] != vaultFile {
		t.Fatalf("launcherEnv missing DEJA_VAULT_FILE: %+v", envMap)
	}
	if _, leaked := envMap["DEJA_VAULT_PASSPHRASE"]; leaked {
		t.Fatalf("launcherEnv leaked DEJA_VAULT_PASSPHRASE: %+v", envMap)
	}

	// 2. Doctor telemetry before vault is initialized.
	plainText := captureDoctorArgs(t, []string{"--offline"}, dir)
	if !strings.Contains(plainText, "plaintext on disk") || !strings.Contains(plainText, "Vault\n") {
		t.Fatalf("doctor plaintext output missing expected lines:\n%s", plainText)
	}
	plainJSON := captureDoctorArgs(t, []string{"--offline", "--json"}, dir)
	var docPlain doctorReport
	if err := json.Unmarshal([]byte(plainJSON), &docPlain); err != nil {
		t.Fatalf("unmarshal plain doctor JSON: %v\n%s", err, plainJSON)
	}
	if docPlain.Vault.Enabled || docPlain.Vault.Locked || !docPlain.Vault.KeyringAvailable {
		t.Fatalf("unexpected pre-init doctor vault JSON: %+v", docPlain.Vault)
	}

	// 3. Seed & index a session, initialize encrypted keyring vault, and unlock into keyring.
	t.Setenv("DEJA_VAULT_BACKEND", "keyring")
	seedVaultSecuritySessions(t, filepath.Join(tmp, "claude"))
	if err := run([]string{"index"}); err != nil {
		t.Fatalf("index: %v", err)
	}
	const pass = "keyring-vault-pass-99" //nolint:gosec // G101: synthetic test passphrase
	if _, err := vault.Init(dir, pass, 30*time.Minute); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	if err := vault.Unlock(dir, pass); err != nil {
		t.Fatalf("vault.Unlock: %v", err)
	}
	if kr.count() == 0 {
		t.Fatal("vault.Unlock did not store DEK in keyring backend")
	}

	// 4. Simulate stripped hook environment (no passphrase env, cleared memory DEK cache):
	// keyring backend must unlock hook-prompt and doctor must report unlocked vault.
	t.Setenv(vault.EnvVaultPassphrase, "")
	vault.ClearCache()

	unlockedText := captureDoctorArgs(t, []string{"--offline"}, dir)
	if strings.Contains(unlockedText, "plaintext on disk") || !strings.Contains(unlockedText, "vault encryption enabled") {
		t.Fatalf("doctor unlocked output kept plaintext warning:\n%s", unlockedText)
	}
	if !strings.Contains(unlockedText, "enabled          true (unlocked)") || !strings.Contains(unlockedText, "key_backend      keyring") {
		t.Fatalf("doctor unlocked Vault section unexpected:\n%s", unlockedText)
	}
	unlockedJSON := captureDoctorArgs(t, []string{"--offline", "--json"}, dir)
	var docUnlocked doctorReport
	if err := json.Unmarshal([]byte(unlockedJSON), &docUnlocked); err != nil {
		t.Fatalf("unmarshal unlocked doctor JSON: %v\n%s", err, unlockedJSON)
	}
	if !docUnlocked.Vault.Enabled || docUnlocked.Vault.Locked || !docUnlocked.Vault.EncryptedStore || docUnlocked.Vault.KeyBackend != "keyring" {
		t.Fatalf("unexpected unlocked doctor vault JSON: %+v", docUnlocked.Vault)
	}

	hkPayload := `{"session_id":"hk-kr-1","cwd":"/tmp/alpha","prompt":"canary_alpha_9911 deployment"}`
	hkOut, hkErr := runHookPromptJSON(t, hkPayload)
	if hkErr != nil {
		t.Fatalf("hook-prompt under unlocked keyring returned err: %v", hkErr)
	}
	if !strings.Contains(hkOut, "canary_alpha_9911") {
		t.Fatalf("hook-prompt under stripped-env unlocked keyring did not recall canary_alpha_9911:\n%s", hkOut)
	}

	// 5. Lock the vault (deletes keyring entry and seals archive): doctor reports locked, hook fails closed.
	if err := vault.Lock(dir, pass); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	vault.ClearCache()

	lockedText := captureDoctorArgs(t, []string{"--offline"}, dir)
	if !strings.Contains(lockedText, "enabled          true (locked)") {
		t.Fatalf("doctor locked Vault section unexpected:\n%s", lockedText)
	}
	lockedJSON := captureDoctorArgs(t, []string{"--offline", "--json"}, dir)
	var docLocked doctorReport
	if err := json.Unmarshal([]byte(lockedJSON), &docLocked); err != nil {
		t.Fatalf("unmarshal locked doctor JSON: %v\n%s", err, lockedJSON)
	}
	if !docLocked.Vault.Enabled || !docLocked.Vault.Locked || !docLocked.Vault.EncryptedStore {
		t.Fatalf("unexpected locked doctor vault JSON: %+v", docLocked.Vault)
	}

	hkLockedOut, _ := runHookPromptJSON(t, hkPayload)
	if strings.Contains(hkLockedOut, "canary_alpha_9911") {
		t.Fatalf("hook-prompt leaked canary_alpha_9911 while vault locked:\n%s", hkLockedOut)
	}
}

func TestVaultCmdSidecarsAndDoctorBranches(t *testing.T) {
	tmp, dir := withSecurityVaultEnv(t)
	phrase := "test-fixture-alpha"
	t.Setenv(vault.EnvVaultPassphrase, phrase)
	if _, iErr := vault.Init(dir, phrase, 0); iErr != nil {
		t.Fatalf("vault.Init: %v", iErr)
	}
	dek, kErr := vault.CheckUnlocked(dir)
	if kErr != nil {
		t.Fatalf("vault.CheckUnlocked: %v", kErr)
	}
	defer clear(dek)

	// Empty and missing directories are no-ops.
	if mErr := migrateCmdSidecars("", dek); mErr != nil {
		t.Fatalf("migrateCmdSidecars empty = %v", mErr)
	}
	if mErr := migrateCmdSidecars(filepath.Join(tmp, "absent-idx"), dek); mErr != nil {
		t.Fatalf("migrateCmdSidecars absent = %v", mErr)
	}

	// Regular file at .deferred path is ignored safely.
	fileDeferredIdx := filepath.Join(tmp, "file-def-idx")
	defRegPath := deferredRoot(fileDeferredIdx)
	if mkErr := os.MkdirAll(filepath.Dir(defRegPath), 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	if wErr := os.WriteFile(defRegPath, []byte("not-a-dir"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateCmdSidecars(fileDeferredIdx, dek); mErr != nil {
		t.Fatalf("migrateCmdSidecars file deferred = %v", mErr)
	}

	// Nested .deferred files get sealed; hidden and already-sealed files remain untouched.
	defSub := filepath.Join(deferredRoot(dir), "sub")
	if mkErr := os.MkdirAll(defSub, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	if mkErr := os.MkdirAll(filepath.Join(deferredRoot(dir), ".hidden"), 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	plainNote := filepath.Join(defSub, "note.txt")
	if wErr := os.WriteFile(plainNote, []byte("secret-deferred-note"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	sealedPre, sErr := vault.SealBytes(dek, vault.DomainSidecar, []byte("pre-sealed"))
	if sErr != nil {
		t.Fatal(sErr)
	}
	prePath := filepath.Join(deferredRoot(dir), "already.bin")
	if wErr := os.WriteFile(prePath, sealedPre, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(filepath.Join(deferredRoot(dir), ".hidden", "skip.txt"), []byte("hidden"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateCmdSidecars(dir, dek); mErr != nil {
		t.Fatalf("migrateCmdSidecars nested: %v", mErr)
	}
	gotNote, rErr := os.ReadFile(plainNote)
	if rErr != nil || !vault.IsSealedBytes(gotNote) {
		t.Fatalf("migrateCmdSidecars did not seal plain note: %q (%v)", gotNote, rErr)
	}

	// recordDejaVuLine unlocked skips malformed timestamps; locked returns false.
	now := time.Now()
	djPath := filepath.Join(tmp, "custom.dejavu")
	seedLine := "bad-ts notanumber\nold-sess 1\n"
	if wErr := vault.WriteSealedFile(dir, djPath, vault.DomainSidecar, []byte(seedLine), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if !recordDejaVuLine(dir, djPath, "s1", now) {
		t.Fatal("recordDejaVuLine unlocked = false, want true")
	}

	// pruneForkMarks + claudeForkMark undecryptable line handling when enabled vs disabled.
	forksPath := forkMarksPath(dir)
	validLine, slErr := vault.SealSidecarLine(dir, vault.DomainSidecar, []byte("sid-keep claude 9999999999"))
	if slErr != nil {
		t.Fatal(slErr)
	}
	badEncLine := vault.NoteLinePrefix + "invalid-cipher-data"
	mixedForks := badEncLine + "\n" + validLine + "\n"
	if wErr := os.WriteFile(forksPath, []byte(mixedForks), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	pruneForkMarks(forksPath)
	afterPrune, rErr := os.ReadFile(forksPath)
	if rErr != nil || !strings.Contains(string(afterPrune), badEncLine) {
		t.Fatalf("pruneForkMarks enabled dropped undecryptable line: %q (%v)", afterPrune, rErr)
	}
	if gotFork := claudeForkMark(dir, "sid-keep"); gotFork != "claude" {
		t.Fatalf("claudeForkMark with corrupt prefix = %q, want claude", gotFork)
	}

	// Disabled vault skips undecryptable lines in pruneForkMarks; sealed .dejavu fails closed in hook_dejavu_line.
	if wErr := os.WriteFile(dir+".dejavu", sealedPre, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "disabled-vault.json"))
	vault.ClearCache()
	pruneForkMarks(forksPath)
	if dejaVuLineDue(dir, "s2") {
		t.Fatal("dejaVuLineDue succeeded on sealed .dejavu while vault disabled")
	}
	sealedOffPath := filepath.Join(tmp, "sealed-when-off.dejavu")
	if wErr := os.WriteFile(sealedOffPath, sealedPre, 0o600); wErr == nil {
		if recordDejaVuLine(dir, sealedOffPath, "s3", now) {
			t.Fatal("recordDejaVuLine succeeded on sealed file while vault disabled")
		}
	}

	// Locked vault fails closed in recordDejaVuLine and pruneForkMarks.
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv(vault.EnvVaultPassphrase, "")
	vault.ClearCache()
	if lErr := vault.Lock(dir, phrase); lErr != nil {
		t.Fatal(lErr)
	}
	if recordDejaVuLine(dir, djPath, "s4", now) {
		t.Fatal("recordDejaVuLine succeeded while locked")
	}
	pruneForkMarks(forksPath)

	// doctorVault renders key_backend fallback and error line when vault.json is corrupt.
	corruptVaultPath := filepath.Join(tmp, "corrupt-vault.json")
	if wErr := os.WriteFile(corruptVaultPath, []byte("{corrupt"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	t.Setenv("DEJA_VAULT_FILE", corruptVaultPath)
	vault.ClearCache()
	var docBuf bytes.Buffer
	doctorVault(&docBuf, dir)
	if !strings.Contains(docBuf.String(), "keyring          available=") || !strings.Contains(docBuf.String(), "error            ") {
		t.Fatalf("doctorVault corrupt config output missing expected fields:\n%s", docBuf.String())
	}
}
