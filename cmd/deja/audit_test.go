package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestAuditReportsClaudeRetentionAndUnwiredNextStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("DEJA_XCODE_CLAUDE_ROOT", filepath.Join(home, ".none-xcode-claude"))
	t.Setenv("DEJA_CC_MIRROR_ROOT", filepath.Join(home, ".none-cc-mirror"))
	t.Setenv("DEJA_STORES", "claude")

	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-app")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	prevNow := auditNow
	auditNow = func() time.Time { return now }
	defer func() { auditNow = prevNow }()

	writeTranscript := func(name string, ageDays int) {
		p := filepath.Join(projDir, name)
		line := `{"type":"user","message":{"role":"user","content":"fix jwt rotation"},"timestamp":"2026-10-01T10:00:00Z"}` + "\n"
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-time.Duration(ageDays) * 24 * time.Hour)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	writeTranscript("fresh.jsonl", 5)
	writeTranscript("soon.jsonl", 25)
	writeTranscript("expired.jsonl", 34)

	// Aged auto-memory notes belong to Claude's store but are not deleted by cleanupPeriodDays.
	memDir := filepath.Join(projDir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memPath := filepath.Join(memDir, "project_old.md")
	if err := os.WriteFile(memPath, []byte("# Old note\nMust not count toward transcript expiry.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := now.Add(-45 * 24 * time.Hour)
	if err := os.Chtimes(memPath, oldTS, oldTS); err != nil {
		t.Fatal(err)
	}

	claudeMemDir := filepath.Join(home, ".claude-mem")
	if err := os.MkdirAll(claudeMemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeMemDir, "claude-mem.db"), []byte("sqlite-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	idxDir := filepath.Join(home, ".cache", "deja")
	var out bytes.Buffer
	if err := runAudit(idxDir, nil, &out); err != nil {
		t.Fatalf("runAudit: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"2 of 3 transcripts at deletion risk (1 past 30d, 1 within 7d; default 30d)",
		"claude-mem.db detected",
		"wiring:   not wired",
		"next:     npx @vshulcz/deja-vu install --auto",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("audit output missing %q\ngot:\n%s", want, text)
		}
	}

	// Verify --json output and custom cleanupPeriodDays=0 (disabled).
	settings := []byte(`{"cleanupPeriodDays": 0}`)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runAudit(idxDir, []string{"--json"}, &out); err != nil {
		t.Fatalf("runAudit --json: %v", err)
	}
	var rep auditReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal audit json: %v\n%s", err, out.String())
	}
	if rep.ClaudeRetention.CleanupPeriodDays != 0 || !rep.ClaudeRetention.Explicit {
		t.Errorf("cleanup_period_days = %+v, want explicit 0", rep.ClaudeRetention)
	}
	if rep.ClaudeRetention.ExpiringIn7Days != 0 || rep.ClaudeRetention.PastPeriod != 0 {
		t.Errorf("expected 0 expiring when cleanupPeriodDays=0, got %+v", rep.ClaudeRetention)
	}
	if !rep.ClaudeMemDBPresent {
		t.Errorf("expected claude_mem_db_present = true")
	}
	if rep.NextCommand != "npx @vshulcz/deja-vu install --auto" {
		t.Errorf("next_command = %q", rep.NextCommand)
	}
}

func TestAuditRejectsUnknownFlagsAndArgs(t *testing.T) {
	var out bytes.Buffer
	if err := runAudit(t.TempDir(), []string{"--bogus"}, &out); err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if err := runAudit(t.TempDir(), []string{"extra"}, &out); err == nil {
		t.Fatal("expected error for positional argument")
	}
}

func isolateAuditEnv(t *testing.T, stores string) (root, home, xdgCache, idxDir string) {
	t.Helper()
	root = t.TempDir()
	home = filepath.Join(root, "home")
	xdgCache = filepath.Join(root, "xdg-cache")
	idxDir = filepath.Join(root, "missing-index")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
	t.Setenv("XDG_CACHE_HOME", xdgCache)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(home, ".codex"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("DEJA_XCODE_CLAUDE_ROOT", filepath.Join(home, ".none-xcode-claude"))
	t.Setenv("DEJA_CC_MIRROR_ROOT", filepath.Join(home, ".none-cc-mirror"))
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(root, "notes.jsonl"))
	t.Setenv("DEJA_STORES", stores)
	t.Setenv("NO_COLOR", "1")
	return root, home, xdgCache, idxDir
}

func TestAuditNamedSecretAllowMatchesIndex(t *testing.T) {
	if len(auditNamedSecretAllow) == 0 {
		t.Fatal("auditNamedSecretAllow must not be empty")
	}
	for kind, ok := range auditNamedSecretAllow {
		if !ok || !index.SecretKindNamed(kind) {
			t.Errorf("auditNamedSecretAllow[%q] = %v, SecretKindNamed = %v", kind, ok, index.SecretKindNamed(kind))
		}
	}
	for _, nonNamed := range []string{"entropy", "credential", "quoted-secret"} {
		if index.SecretKindNamed(nonNamed) {
			t.Errorf("SecretKindNamed(%q) unexpectedly true", nonNamed)
		}
	}
	for _, skip := range []string{"/tmp/claude-mem.db", "/tmp/a.db", "/tmp/a.sqlite", "/tmp/a.db-wal", "/tmp/a.bin", "/tmp/img.png"} {
		if !auditShouldSkipPath(skip) {
			t.Errorf("auditShouldSkipPath(%q) = false, want true", skip)
		}
	}
	for _, scan := range []string{"/tmp/session.jsonl", "/tmp/memory.md"} {
		if auditShouldSkipPath(scan) {
			t.Errorf("auditShouldSkipPath(%q) = true, want false", scan)
		}
	}
}

func TestAuditSecretsStreamsRawTranscriptsWithoutPrintingValues(t *testing.T) {
	_, home, _, idxDir := isolateAuditEnv(t, "claude,codex")
	t.Setenv("DEJA_NO_REDACT", "1")

	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	prevNow := auditNow
	auditNow = func() time.Time { return now }
	defer func() { auditNow = prevNow }()

	antKey := "sk-ant-" + "api03-1234567890abcdefghij1234567890abcdefghij"
	ghTok := "ghp_" + "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890"
	awsSecret := "wJalrXUtnFEMI" + "/K7MDENG/bPxRfiCY" + "AUDITKEY123"
	awsAccess := "AKIA" + "IOSFODNN7EXAMPLE99"
	bearerVal := "tok_live_" + "9876543210abcdef9876543210"
	pemBody := "MIIEowIBAAKCAQEAnAuditPemExampleBody0123456789"
	skippedBinaryTok := "npm_" + "BinarySkipped0000000000000000000000000"
	skippedDBTok := "npm_" + "ClaudeMemDbSkipped0000000000000000000"

	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-app")
	memDir := filepath.Join(projDir, "memory")
	codexDir := filepath.Join(home, ".codex", "sessions", "2026", "11", "10")
	claudeMemDir := filepath.Join(home, ".claude-mem")
	for _, d := range []string{projDir, memDir, codexDir, claudeMemDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldLeakPath := filepath.Join(projDir, "old_leak.jsonl")
	oldLeakBody := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"export key=` + antKey + `"},"timestamp":"2026-10-17T10:00:00Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"` + ghTok + `"},"timestamp":"2026-10-17T10:01:00Z"}`,
		`{`,
		`  "aws_secret_access_key":`,
		`  "` + awsSecret + `"`,
		`}`,
	}, "\n") + "\n"
	if err := os.WriteFile(oldLeakPath, []byte(oldLeakBody), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := now.Add(-26 * 24 * time.Hour)
	if err := os.Chtimes(oldLeakPath, oldTS, oldTS); err != nil {
		t.Fatal(err)
	}

	pemPath := filepath.Join(memDir, "key_dump.md")
	pemText := strings.Join([]string{
		"# Recovery key note",
		"-----BEGIN " + "RSA PRIVATE KEY-----",
		pemBody,
		"-----END " + "RSA PRIVATE KEY-----",
	}, "\n") + "\n"
	if err := os.WriteFile(pemPath, []byte(pemText), 0o644); err != nil {
		t.Fatal(err)
	}

	binPath := filepath.Join(projDir, "binary_blob.jsonl")
	binPayload := append([]byte{0x00, 0x01, 0x02, 0x03}, []byte("\ntoken="+skippedBinaryTok+"\n")...)
	if err := os.WriteFile(binPath, binPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	freshTS := now.Add(-2 * 24 * time.Hour)
	if err := os.Chtimes(binPath, freshTS, freshTS); err != nil {
		t.Fatal(err)
	}

	codexPath := filepath.Join(codexDir, "rollout-2026-11-10T10-00-00-c1.jsonl")
	codexBody := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-11-10T10:00:00Z","payload":{"session_id":"c1","cwd":"/Users/alex/app"}}`,
		`{"type":"message","timestamp":"2026-11-10T10:01:00Z","payload":{"role":"user","content":"Authorization: Bearer ` + bearerVal + ` and ` + awsAccess + `"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(codexPath, []byte(codexBody), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(claudeMemDir, "claude-mem.db"), []byte("sqlite "+skippedDBTok), 0o644); err != nil {
		t.Fatal(err)
	}

	var textOut bytes.Buffer
	if err := runAudit(idxDir, []string{"--secrets"}, &textOut); err != nil {
		t.Fatalf("runAudit --secrets: %v", err)
	}
	txt := textOut.String()
	for _, want := range []string{"secrets:", "anthropic-key", "github-token", "private-key", "bearer-token", "aws-access-key", "aws-secret"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text output missing %q:\n%s", want, txt)
		}
	}

	var jsonOut bytes.Buffer
	if err := runAudit(idxDir, []string{"--secrets", "--json"}, &jsonOut); err != nil {
		t.Fatalf("runAudit --secrets --json: %v", err)
	}
	for _, rawSecret := range []string{antKey, ghTok, awsSecret, awsAccess, bearerVal, pemBody, skippedBinaryTok, skippedDBTok} {
		if strings.Contains(txt, rawSecret) {
			t.Fatalf("text output leaked raw credential %q", rawSecret)
		}
		if strings.Contains(jsonOut.String(), rawSecret) {
			t.Fatalf("json output leaked raw credential %q", rawSecret)
		}
	}

	var rep auditReport
	if err := json.Unmarshal(jsonOut.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal audit json: %v\n%s", err, jsonOut.String())
	}
	if !rep.SecretsScanned {
		t.Fatal("expected secrets_scanned = true")
	}
	if !rep.ClaudeMemDBPresent {
		t.Fatal("expected claude_mem_db_present = true")
	}
	if rep.SecretFiles != 3 {
		t.Errorf("secret_files = %d, want 3", rep.SecretFiles)
	}
	if rep.SecretMatches != 6 {
		t.Errorf("secret_matches = %d, want 6 (classes=%v)", rep.SecretMatches, rep.CredentialClasses)
	}
	if rep.CredentialClasses["npm-token"] != 0 {
		t.Errorf("unexpected npm-token match from skipped binary/db files: %v", rep.CredentialClasses)
	}

	rows := map[string]auditStoreRow{}
	for _, r := range rep.Stores {
		rows[r.Name] = r
	}
	cl := rows["claude"]
	if cl.ExpiringFiles != 1 {
		t.Errorf("claude.expiring_files = %d, want 1", cl.ExpiringFiles)
	}
	if cl.SecretFiles != 2 || cl.SecretMatches != 4 {
		t.Errorf("claude secret counts = (%d files, %d matches), want (2, 4): %+v", cl.SecretFiles, cl.SecretMatches, cl)
	}
	cx := rows["codex"]
	if cx.SecretFiles != 1 || cx.SecretMatches != 2 {
		t.Errorf("codex secret counts = (%d files, %d matches), want (1, 2): %+v", cx.SecretFiles, cx.SecretMatches, cx)
	}
}

func TestAuditFailOnSecretsExitCode(t *testing.T) {
	_, home, _, idxDir := isolateAuditEnv(t, "claude")
	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-app")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cleanFile := filepath.Join(projDir, "clean.jsonl")
	if err := os.WriteFile(cleanFile, []byte(`{"type":"user","message":{"role":"user","content":"discuss retry backoff"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var cleanJSON bytes.Buffer
	if err := runAudit(idxDir, []string{"--fail-on-secrets", "--json"}, &cleanJSON); err != nil {
		t.Fatalf("clean runAudit --fail-on-secrets returned err: %v", err)
	}
	var cleanRep auditReport
	if err := json.Unmarshal(cleanJSON.Bytes(), &cleanRep); err != nil {
		t.Fatalf("unmarshal clean report: %v", err)
	}
	if !cleanRep.SecretsScanned || cleanRep.SecretMatches != 0 {
		t.Fatalf("unexpected clean secret report: %+v", cleanRep)
	}

	ghTok := "ghp_" + "FailOnSecrets1234567890abcdefghij"
	leakFile := filepath.Join(projDir, "leak.jsonl")
	if err := os.WriteFile(leakFile, []byte(`{"type":"user","message":{"role":"user","content":"token=`+ghTok+`"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var dirtyJSON bytes.Buffer
	err := runAudit(idxDir, []string{"--fail-on-secrets", "--json"}, &dirtyJSON)
	if !errors.Is(err, errAlreadySaid) {
		t.Fatalf("expected errors.Is(err, errAlreadySaid), got %v", err)
	}
	if strings.Contains(dirtyJSON.String(), ghTok) {
		t.Fatal("json output leaked token value")
	}
	var dirtyRep auditReport
	if uerr := json.Unmarshal(dirtyJSON.Bytes(), &dirtyRep); uerr != nil {
		t.Fatalf("dirty --fail-on-secrets --json did not emit valid JSON: %v\n%s", uerr, dirtyJSON.String())
	}
	if !dirtyRep.SecretsScanned || dirtyRep.SecretMatches != 1 || dirtyRep.CredentialClasses["github-token"] != 1 {
		t.Fatalf("unexpected dirty report: %+v", dirtyRep)
	}

	var dirtyText bytes.Buffer
	err = runAudit(idxDir, []string{"--fail-on-secrets"}, &dirtyText)
	if !errors.Is(err, errAlreadySaid) {
		t.Fatalf("expected text mode errors.Is(err, errAlreadySaid), got %v", err)
	}
	if !strings.Contains(dirtyText.String(), "secrets:") || !strings.Contains(dirtyText.String(), "github-token") {
		t.Fatalf("unexpected dirty text output:\n%s", dirtyText.String())
	}
	if strings.Contains(dirtyText.String(), ghTok) {
		t.Fatal("text output leaked token value")
	}
}

func TestAuditIndexlessRunCreatesZeroFiles(t *testing.T) {
	root, home, xdgCache, idxDir := isolateAuditEnv(t, "claude")
	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-many")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for i := range 55 {
		p := filepath.Join(projDir, "sess-"+string(rune('a'+i/26))+string(rune('a'+i%26))+".jsonl")
		if err := os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"hello"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := runAudit(idxDir, []string{"--secrets"}, &out); err != nil {
		t.Fatalf("runAudit: %v", err)
	}
	if !strings.Contains(out.String(), "55 transcript files") {
		t.Fatalf("expected >=50 transcript files reported, got:\n%s", out.String())
	}

	if notice := startReleaseNotice([]string{"audit"}, true, idxDir, time.Now(), func(string) error {
		t.Fatal("audit must not spawn release notice look")
		return nil
	}); notice != nil {
		t.Fatalf("expected nil releaseNotice for audit, got %+v", notice)
	}

	for _, absent := range []string{
		idxDir,
		idxDir + ".release",
		xdgCache,
		filepath.Join(root, "xdg-config"),
		filepath.Join(root, "notes.jsonl"),
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".config"),
	} {
		if _, err := os.Stat(absent); !os.IsNotExist(err) {
			t.Errorf("expected %s to remain absent after indexless audit (stat err=%v)", absent, err)
		}
	}
}

func TestAuditStreamChunksAndSplitIndex(t *testing.T) {
	withDelim := make([]byte, 20000)
	for i := range withDelim {
		withDelim[i] = 'a'
	}
	delimPos := len(withDelim) - 128
	withDelim[delimPos] = ','
	if got, want := auditChunkSplitIndex(withDelim), delimPos+1; got != want {
		t.Fatalf("auditChunkSplitIndex(withDelim) = %d, want %d", got, want)
	}

	noDelim := make([]byte, 20000)
	for i := range noDelim {
		noDelim[i] = 'z'
	}
	if got := auditChunkSplitIndex(noDelim); got != len(noDelim) {
		t.Fatalf("auditChunkSplitIndex(noDelim) = %d, want %d", got, len(noDelim))
	}

	// Single oversized line containing a delimiter inside the trailing 16 KiB window.
	delimLine := strings.Repeat("a", auditMaxLineChunkBytes-64) + " " + strings.Repeat("b", 256)
	var chunks []string
	if err := auditStreamChunks(strings.NewReader(delimLine+"\ntail\n"), func(chunk string) {
		chunks = append(chunks, chunk)
	}); err != nil {
		t.Fatalf("auditStreamChunks(delimLine): %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("delimLine chunks = %d, want 3", len(chunks))
	}
	if chunks[0]+chunks[1] != delimLine || chunks[2] != "tail" {
		t.Fatalf("unexpected delimLine chunks: lens=%d,%d last=%q", len(chunks[0]), len(chunks[1]), chunks[2])
	}

	// Single oversized line with no delimiters hits the fallback split at len(buf).
	solidLine := strings.Repeat("q", auditMaxLineChunkBytes+64)
	chunks = chunks[:0]
	if err := auditStreamChunks(strings.NewReader(solidLine), func(chunk string) {
		chunks = append(chunks, chunk)
	}); err != nil {
		t.Fatalf("auditStreamChunks(solidLine): %v", err)
	}
	if len(chunks) != 2 || chunks[0]+chunks[1] != solidLine {
		t.Fatalf("solidLine chunks = %d (sum matches=%v), want 2", len(chunks), len(chunks) == 2 && chunks[0]+chunks[1] == solidLine)
	}
}

func TestAuditPrintReportBranchesAndJSONFailOnSecrets(t *testing.T) {
	var emptyOut bytes.Buffer
	printAuditReport(&emptyOut, auditReport{
		SecretsScanned: true,
	})
	for _, want := range []string{
		"stores:   none detected on this machine",
		"0 unredacted credentials found",
		"wiring:   not wired",
	} {
		if !strings.Contains(emptyOut.String(), want) {
			t.Errorf("empty printAuditReport missing %q\ngot:\n%s", want, emptyOut.String())
		}
	}

	var manyOut bytes.Buffer
	printAuditReport(&manyOut, auditReport{
		Stores: []auditStoreRow{
			{Name: "claude", Files: 2, Bytes: 2048, SecretFiles: 1, SecretMatches: 1},
			{Name: "codex", Files: 1, Bytes: 512},
			{Name: "cursor", Files: 1, Bytes: 512},
			{Name: "gemini", Files: 1, Bytes: 512},
			{Name: "opencode", Files: 1, Bytes: 512},
			{Name: "goose", Files: 1, Bytes: 512},
			{Name: "aider", Files: 1, Bytes: 512},
			{Name: "pi", Files: 1, Bytes: 512},
		},
		TotalFiles:        9,
		TotalBytes:        5632,
		SecretsScanned:    true,
		SecretFiles:       1,
		SecretMatches:     1,
		CredentialClasses: map[string]int{"github-token": 1},
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 0,
			Explicit:          true,
			TotalFiles:        2,
			PreservedDeleted:  3,
		},
		IndexedSessions: 4,
		WiredTargets:    []string{"claude"},
	})
	for _, want := range []string{
		"+2 more",
		"cleanupPeriodDays=0 (disabled)",
		"kept:     3 deleted Claude sessions still searchable in deja",
		"1 unredacted match across 1 file (claude (1 file))",
		"index:    4 sessions indexed",
		"1 target wired (claude)",
	} {
		if !strings.Contains(manyOut.String(), want) {
			t.Errorf("many-store printAuditReport missing %q\ngot:\n%s", want, manyOut.String())
		}
	}

	var recoverableOut bytes.Buffer
	printAuditReport(&recoverableOut, auditReport{
		Stores:         []auditStoreRow{{Name: "claude", Files: 1, Bytes: 128, SecretFiles: 1, SecretMatches: 2}},
		TotalFiles:     1,
		TotalBytes:     128,
		SecretsScanned: true,
		SecretFiles:    1,
		SecretMatches:  2,
		CredentialClasses: map[string]int{
			"github-token": 2,
		},
		ClaudeRetention: auditClaudeRetention{
			CleanupPeriodDays: 30,
			TotalFiles:        1,
		},
		IndexedSessions:  3,
		RecoverableFiles: 1,
		WiredTargets:     []string{"claude", "codex"},
	})
	for _, want := range []string{
		"2 unredacted matches across 1 file (claude (1 file))",
		"1 file path recoverable via",
		"2 targets wired (claude, codex)",
	} {
		if !strings.Contains(recoverableOut.String(), want) {
			t.Errorf("recoverable printAuditReport missing %q\ngot:\n%s", want, recoverableOut.String())
		}
	}

	_, home, _, idxDir := isolateAuditEnv(t, "claude")
	t.Setenv("DEJA_NO_REDACT", "1")
	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-json-fail")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tok := "ghp_" + "AuditJsonFail0000000000000000000000000"
	line := `{"type":"user","message":{"role":"user","content":"token ` + tok + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "leak.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var jsonOut bytes.Buffer
	err := runAudit(idxDir, []string{"--json", "--fail-on-secrets"}, &jsonOut)
	if !errors.Is(err, errAlreadySaid) {
		t.Fatalf("runAudit(--json --fail-on-secrets) err = %v, want errAlreadySaid", err)
	}
	if strings.Contains(jsonOut.String(), tok) {
		t.Fatalf("JSON output leaked raw secret value")
	}
	var rep auditReport
	if uerr := json.Unmarshal(jsonOut.Bytes(), &rep); uerr != nil {
		t.Fatalf("unmarshal jsonOut: %v", uerr)
	}
	if rep.SecretMatches == 0 {
		t.Fatalf("expected SecretMatches > 0, got %+v", rep)
	}
}
