package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func seedHookLockAndSourceHistory(t *testing.T, proj string, includeEdits bool) string {
	t.Helper()
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(claudeRoot, filepath.Base(proj))
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-48 * time.Hour).UTC()
	for i, sid := range []string{"hist-pg-1", "hist-pg-2"} {
		at := base.Add(time.Duration(i) * time.Hour)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
				sid, proj, at.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":"We settled on pgbouncer transaction mode with prepared statements disabled for connection pooling."}}`,
				sid, proj, at.Add(time.Minute).Format(time.RFC3339)),
		}
		writeClaudeFixture(t, filepath.Join(store, sid+".jsonl"), sid, rows)
	}
	if includeEdits {
		for k := range 6 {
			sid := fmt.Sprintf("hist-edit-%d", k)
			at := base.Add(time.Duration(k) * time.Hour)
			rows := []string{
				fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"work on ledger concurrency"}}`,
					sid, proj, at.Format(time.RFC3339)),
				fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"text","text":"We settled on ledger.go validating nonces before taking the lock."},{"type":"tool_use","name":"Edit","input":{"file_path":%q,"old_string":"x","new_string":"y"}}]}}`,
					sid, proj, at.Add(time.Minute).Format(time.RFC3339), filepath.Join(proj, "ledger.go")),
				fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":"ledger.go validates nonces before lock (%d)"}}`,
					sid, proj, at.Add(2*time.Minute).Format(time.RFC3339), k),
			}
			writeClaudeFixture(t, filepath.Join(store, sid+".jsonl"), sid, rows)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHookSeenLockDoesNotSerializeUnrelatedSessions(t *testing.T) {
	hermeticEnv(t)
	tmp := t.TempDir()
	projA := filepath.Join(tmp, "proj-a")
	projB := filepath.Join(tmp, "proj-b")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	widgetPath := filepath.Join(projA, "widget.go")
	if err := os.WriteFile(widgetPath, []byte("package demo\n\nfunc HandleRetryToken() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := seedHookLockAndSourceHistory(t, projB, true)
	t.Cleanup(func() {
		repoSymbolScanHook = nil
		repoSymbolMapCache.Delete(projA)
		repoSymbolMapCache.Delete(projB)
		orientTreeCache.Delete(projA)
		orientTreeCache.Delete(projB)
	})

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlockA := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unlockA)

	var enteredOnce sync.Once
	repoSymbolScanHook = func(p string) {
		if filepath.Clean(p) == filepath.Clean(widgetPath) {
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
	}

	var outA bytes.Buffer
	errChA := make(chan error, 1)
	doneA := false
	payloadA := fmt.Sprintf(`{"prompt":"check HandleRetryToken behavior","session_id":"ses-slow-a","cwd":%q}`, projA)
	go func() {
		errChA <- runHookPromptMode(dir, strings.NewReader(payloadA), &outA, true)
	}()
	t.Cleanup(func() {
		unlockA()
		if !doneA {
			select {
			case err := <-errChA:
				_ = err
			case <-time.After(5 * time.Second):
			}
		}
	})

	select {
	case <-entered:
	case err := <-errChA:
		t.Fatalf("session A finished before reaching repoSymbolScanHook (err=%v, out=%q)", err, outA.String())
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for session A to enter repoSymbolScanHook")
	}

	type hookRes struct {
		out string
		err error
	}
	resChB := make(chan hookRes, 1)
	go func() {
		var promptB bytes.Buffer
		payloadB := fmt.Sprintf(`{"prompt":"what did we settle about pgbouncer prepared statements","session_id":"ses-fast-b","cwd":%q}`, projB)
		if err := runHookPromptMode(dir, strings.NewReader(payloadB), &promptB, true); err != nil {
			resChB <- hookRes{err: err}
			return
		}
		var toolB bytes.Buffer
		editPayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q},"cwd":%q,"session_id":"ses-fast-b"}`,
			filepath.Join(projB, "ledger.go"), projB)
		if err := runHookToolMode(dir, strings.NewReader(editPayload), &toolB, hookToolPlain); err != nil {
			resChB <- hookRes{err: err}
			return
		}
		resChB <- hookRes{out: promptB.String() + "\n" + toolB.String()}
	}()

	select {
	case res := <-resChB:
		if res.err != nil {
			t.Fatalf("session B failed while session A paused: %v", res.err)
		}
		if !strings.Contains(res.out, "transaction mode") || !strings.Contains(res.out, "ledger.go") {
			t.Fatalf("session B missed expected recall output while session A paused:\n%s", res.out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session B serialized behind session A's source scan")
	}

	unlockA()
	select {
	case err := <-errChA:
		doneA = true
		if err != nil {
			t.Fatalf("session A failed after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for session A after release")
	}
}

func TestConcurrentSameSessionHooksSuppressDuplicates(t *testing.T) {
	hermeticEnv(t)
	proj := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := seedHookLockAndSourceHistory(t, proj, true)

	const workers = 6
	promptPayload := fmt.Sprintf(`{"prompt":"what did we settle about pgbouncer prepared statements","session_id":"ses-same-prompt","cwd":%q}`, proj)
	promptOuts := make([]string, workers)
	promptErrs := make([]error, workers)
	startPrompt := make(chan struct{})
	var readyPrompt, wgPrompt sync.WaitGroup
	readyPrompt.Add(workers)
	wgPrompt.Add(workers)
	for i := range workers {
		go func(idx int) {
			defer wgPrompt.Done()
			var buf bytes.Buffer
			readyPrompt.Done()
			<-startPrompt
			promptErrs[idx] = runHookPromptMode(dir, strings.NewReader(promptPayload), &buf, true)
			promptOuts[idx] = buf.String()
		}(i)
	}
	readyPrompt.Wait()
	close(startPrompt)
	wgPrompt.Wait()

	nonEmptyPrompt := 0
	for i := range workers {
		if promptErrs[i] != nil {
			t.Fatalf("prompt worker %d err: %v", i, promptErrs[i])
		}
		if strings.TrimSpace(promptOuts[i]) != "" {
			nonEmptyPrompt++
			if !strings.Contains(promptOuts[i], "transaction mode") {
				t.Fatalf("prompt worker %d unexpected output:\n%s", i, promptOuts[i])
			}
		}
	}
	if nonEmptyPrompt != 1 {
		t.Fatalf("expected exactly 1 non-empty prompt hook output under concurrency, got %d", nonEmptyPrompt)
	}

	toolPayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q},"cwd":%q,"session_id":"ses-same-tool"}`,
		filepath.Join(proj, "ledger.go"), proj)
	toolOuts := make([]string, workers)
	toolErrs := make([]error, workers)
	startTool := make(chan struct{})
	var readyTool, wgTool sync.WaitGroup
	readyTool.Add(workers)
	wgTool.Add(workers)
	for i := range workers {
		go func(idx int) {
			defer wgTool.Done()
			var buf bytes.Buffer
			readyTool.Done()
			<-startTool
			toolErrs[idx] = runHookToolMode(dir, strings.NewReader(toolPayload), &buf, hookToolPlain)
			toolOuts[idx] = buf.String()
		}(i)
	}
	readyTool.Wait()
	close(startTool)
	wgTool.Wait()

	nonEmptyTool := 0
	for i := range workers {
		if toolErrs[i] != nil {
			t.Fatalf("tool worker %d err: %v", i, toolErrs[i])
		}
		if strings.TrimSpace(toolOuts[i]) != "" {
			nonEmptyTool++
			if !strings.Contains(toolOuts[i], "ledger.go") {
				t.Fatalf("tool worker %d unexpected output:\n%s", i, toolOuts[i])
			}
		}
	}
	if nonEmptyTool != 1 {
		t.Fatalf("expected exactly 1 non-empty tool hook output under concurrency, got %d", nonEmptyTool)
	}
}

func TestRecallExplicitProjectSkipsUnrelatedRepoScan(t *testing.T) {
	hermeticEnv(t)
	tmp := t.TempDir()
	unrelatedRepo := filepath.Join(tmp, "unrelated-repo")
	targetRepo := filepath.Join(tmp, "target-proj")
	if err := os.MkdirAll(unrelatedRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelatedRepo, "worker.go"), []byte("package demo\n\nfunc OrderSettlementWorker() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	targetStore := filepath.Join(claudeRoot, "target-proj")
	unrelatedStore := filepath.Join(claudeRoot, "unrelated-repo")
	base := time.Now().Add(-24 * time.Hour).UTC()
	writeClaudeFixture(t, filepath.Join(targetStore, "ses-target-1.jsonl"), "ses-target-1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"ses-target-1","cwd":%q,"timestamp":%q,"message":{"role":"user","content":"debug OrderSettlementWorker retry backoff"}}`,
			targetRepo, base.Format(time.RFC3339)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"ses-target-1","cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":"OrderSettlementWorker uses idempotent settlement keys for retries."}}`,
			targetRepo, base.Add(time.Minute).Format(time.RFC3339)),
	})
	for i, sid := range []string{"20261018_012943_14524c", "ses-unrel-2"} {
		at := base.Add(time.Duration(i+2) * time.Hour)
		writeClaudeFixture(t, filepath.Join(unrelatedStore, sid+".jsonl"), sid, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"inspect OrderSettlementWorker in unrelated repo"}}`,
				sid, unrelatedRepo, at.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":"Reviewed OrderSettlementWorker unrelated note %d."}}`,
				sid, unrelatedRepo, at.Add(time.Minute).Format(time.RFC3339), i),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var scans atomic.Int64
	repoSymbolScanHook = func(string) { scans.Add(1) }
	t.Cleanup(func() {
		repoSymbolScanHook = nil
		repoSymbolMapCache.Delete(unrelatedRepo)
		orientTreeCache.Delete(unrelatedRepo)
	})
	t.Chdir(unrelatedRepo)

	checkSkipped := func(name string, fn func() (string, error)) {
		t.Helper()
		repoSymbolMapCache.Delete(unrelatedRepo)
		scans.Store(0)
		out, err := fn()
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		if got := scans.Load(); got != 0 {
			t.Fatalf("%s triggered %d repo scans, want 0", name, got)
		}
		if strings.Contains(out, "live repo brief:") {
			t.Fatalf("%s leaked live repo brief:\n%s", name, out)
		}
	}

	checkSkipped("CLI --project target-proj", func() (string, error) {
		var buf bytes.Buffer
		err := runRecall(dir, []string{"--project", "target-proj", "OrderSettlementWorker"}, &buf)
		return buf.String(), err
	})
	checkSkipped("MCP project target-proj", func() (string, error) {
		return callMCPTool(dir, "recall", json.RawMessage(`{"query":"OrderSettlementWorker","project":"target-proj"}`))
	})
	checkSkipped("CLI --offset 1", func() (string, error) {
		var buf bytes.Buffer
		err := runRecall(dir, []string{"--offset", "1", "OrderSettlementWorker"}, &buf)
		return buf.String(), err
	})
	checkSkipped("MCP offset 1", func() (string, error) {
		return callMCPTool(dir, "recall", json.RawMessage(`{"query":"OrderSettlementWorker","offset":1}`))
	})
	checkSkipped("CLI session-id query", func() (string, error) {
		var buf bytes.Buffer
		err := runRecall(dir, []string{"20261018_012943_14524c"}, &buf)
		return buf.String(), err
	})
	checkSkipped("MCP session-id query", func() (string, error) {
		return callMCPTool(dir, "recall", json.RawMessage(`{"query":"20261018_012943_14524c"}`))
	})

	checkIncluded := func(name string, fn func() (string, error)) {
		t.Helper()
		repoSymbolMapCache.Delete(unrelatedRepo)
		scans.Store(0)
		out, err := fn()
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		if got := scans.Load(); got == 0 {
			t.Fatalf("%s triggered 0 repo scans, want >0", name)
		}
		if !strings.Contains(out, "live repo brief:") {
			t.Fatalf("%s missing live repo brief:\n%s", name, out)
		}
	}

	checkIncluded("CLI empty project", func() (string, error) {
		var buf bytes.Buffer
		err := runRecall(dir, []string{"OrderSettlementWorker"}, &buf)
		return buf.String(), err
	})
	checkIncluded("CLI matching project", func() (string, error) {
		var buf bytes.Buffer
		err := runRecall(dir, []string{"--project", "unrelated-repo", "OrderSettlementWorker"}, &buf)
		return buf.String(), err
	})
	checkIncluded("MCP matching project", func() (string, error) {
		return callMCPTool(dir, "recall", json.RawMessage(`{"query":"OrderSettlementWorker","project":"unrelated-repo"}`))
	})
}

func TestMissingSymbolDoesNotRebuildRepoSymbolMap(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "alpha.go"), []byte("package demo\n\nfunc KnownSymbolHandler() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	coldRepo := t.TempDir()
	if err := os.WriteFile(filepath.Join(coldRepo, "beta.go"), []byte("package demo\n\nfunc ColdSymbolCheck() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var scans atomic.Int64
	repoSymbolScanHook = func(string) { scans.Add(1) }
	t.Cleanup(func() {
		repoSymbolScanHook = nil
		repoSymbolMapCache.Delete(repo)
		repoSymbolMapCache.Delete(coldRepo)
	})

	file, line, _ := findSymbolDeclarationInRepo(repo, "KnownSymbolHandler")
	if file != "alpha.go" || line != 3 {
		t.Fatalf("warm lookup = (%q, %d), want (alpha.go, 3)", file, line)
	}
	if got := scans.Load(); got != 1 {
		t.Fatalf("warm build scans = %d, want 1", got)
	}

	scans.Store(0)
	time.Sleep(10 * time.Millisecond)

	for range 3 {
		mf, ml, _ := findSymbolDeclarationInRepo(repo, "MissingPhantomHandler")
		if mf != "" || ml != 0 {
			t.Fatalf("missing lookup = (%q, %d), want empty", mf, ml)
		}
		brief, _ := promptSymbolBriefLine(repo, "explain MissingPhantomHandler please", nil)
		if brief != "" {
			t.Fatalf("missing promptSymbolBriefLine = %q, want empty", brief)
		}
	}
	if got := scans.Load(); got != 0 {
		t.Fatalf("missing symbol lookups triggered %d repo rebuild scans, want 0", got)
	}

	scans.Store(0)
	if brief, _ := promptSymbolBriefLine(coldRepo, "ok thanks", nil); brief != "" {
		t.Fatalf("non-symbol prompt brief = %q, want empty", brief)
	}
	if got := scans.Load(); got != 0 {
		t.Fatalf("non-symbol prompt triggered %d cold scans, want 0", got)
	}
}

func TestSourceHintsOptOutAndLauncherEnv(t *testing.T) {
	if !launcherEnvSettings["DEJA_NO_SOURCE_HINTS"] {
		t.Fatal("DEJA_NO_SOURCE_HINTS missing from launcherEnvSettings")
	}

	hermeticEnv(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "auth")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(repo, "token.go")
	if err := os.WriteFile(tokenFile, []byte("package auth\n\nfunc VerifyTokenSignature() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := sources.AddRepoEntries(repo, []sources.RepoEntry{{
		ID:    "jwt-guard",
		Kind:  "gotcha",
		Text:  "Never cache raw JWT strings",
		Files: []string{"token.go"},
		From:  "deja 2026-11-08",
	}})
	if err != nil {
		t.Fatal(err)
	}

	dir := seedHookLockAndSourceHistory(t, repo, false)
	var scans atomic.Int64
	repoSymbolScanHook = func(string) { scans.Add(1) }
	t.Cleanup(func() {
		repoSymbolScanHook = nil
		repoSymbolMapCache.Delete(repo)
		orientTreeCache.Delete(repo)
	})
	t.Chdir(repo)

	verifySuppressed := func(mode string) {
		t.Helper()
		repoSymbolMapCache.Delete(repo)
		orientTreeCache.Delete(repo)
		scans.Store(0)

		if brief, _ := promptSymbolBriefLine(repo, "check VerifyTokenSignature", nil); brief != "" {
			t.Fatalf("%s: promptSymbolBriefLine = %q, want empty", mode, brief)
		}
		if f, l, _ := findSymbolDeclarationInRepo(repo, "VerifyTokenSignature"); f != "" || l != 0 {
			t.Fatalf("%s: findSymbolDeclarationInRepo = (%q, %d), want empty", mode, f, l)
		}
		direct := promptDirectContext(dir, repo, "ses-optout-"+mode, "update VerifyTokenSignature in token.go")
		if strings.Contains(direct, "defined in") || strings.Contains(direct, "likely target:") {
			t.Fatalf("%s: promptDirectContext leaked source hint:\n%s", mode, direct)
		}

		var recallOut bytes.Buffer
		if err := runRecall(dir, []string{"VerifyTokenSignature"}, &recallOut); err != nil {
			t.Fatalf("%s: runRecall symbol failed: %v", mode, err)
		}
		if strings.Contains(recallOut.String(), "live repo brief:") {
			t.Fatalf("%s: runRecall leaked live repo brief:\n%s", mode, recallOut.String())
		}

		var promptOut bytes.Buffer
		payload := fmt.Sprintf(`{"prompt":"what did we settle about pgbouncer prepared statements","session_id":"ses-hist-%s","cwd":%q}`, mode, repo)
		if err := runHookPromptMode(dir, strings.NewReader(payload), &promptOut, true); err != nil {
			t.Fatalf("%s: runHookPromptMode failed: %v", mode, err)
		}
		if !strings.Contains(promptOut.String(), "transaction mode") {
			t.Fatalf("%s: session-history hook prompt suppressed:\n%s", mode, promptOut.String())
		}

		inGuarded := toolHookInput{HookEventName: "PreToolUse", ToolName: "Read", CWD: repo}
		inGuarded.ToolInput.FilePath = tokenFile
		guard := fileReadGuardLine(dir, repo, inGuarded)
		if !strings.Contains(guard, "in .deja/memory.jsonl:") || !strings.Contains(guard, "Never cache raw JWT strings") {
			t.Fatalf("%s: .deja/memory.jsonl guardrail missing: %q", mode, guard)
		}

		if got := scans.Load(); got != 0 {
			t.Fatalf("%s: triggered %d repo scans, want 0", mode, got)
		}
	}

	t.Setenv("DEJA_NO_SOURCE_HINTS", "1")
	verifySuppressed("env")

	t.Setenv("DEJA_NO_SOURCE_HINTS", "")
	polPath := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(polPath, []byte(`{"source_hints": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", polPath)
	verifySuppressed("policy")
}

func TestOversizedSourceFileAndByteBudget(t *testing.T) {
	savedMaxFile := maxRepoSymbolFileBytes
	savedMaxBytes := repoSymbolScanMaxBytes
	t.Cleanup(func() {
		maxRepoSymbolFileBytes = savedMaxFile
		repoSymbolScanMaxBytes = savedMaxBytes
		repoSymbolScanHook = nil
	})

	repo := t.TempDir()
	t.Cleanup(func() { repoSymbolMapCache.Delete(repo) })
	smallPath := filepath.Join(repo, "small.go")
	giantPath := filepath.Join(repo, "giant.go")
	if err := os.WriteFile(smallPath, []byte("package demo\n\nfunc SmallIndexedSymbol() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	giantBody := "package demo\n\nfunc GiantSkippedSymbol() {}\n" + strings.Repeat("// padding line for oversized check\n", 9000)
	if int64(len(giantBody)) <= maxRepoSymbolFileBytes {
		t.Fatalf("test fixture giant.go size %d <= maxRepoSymbolFileBytes %d", len(giantBody), maxRepoSymbolFileBytes)
	}
	if err := os.WriteFile(giantPath, []byte(giantBody), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var scanned []string
	repoSymbolScanHook = func(p string) {
		mu.Lock()
		scanned = append(scanned, filepath.Base(p))
		mu.Unlock()
	}

	idx := loadRepoSymbolMap(repo)
	if idx.numFiles != 2 {
		t.Fatalf("idx.numFiles = %d, want 2 (metadata catalog includes small.go and giant.go)", idx.numFiles)
	}
	if len(scanned) != 1 || scanned[0] != "small.go" {
		t.Fatalf("scanned = %v, want only [small.go] (giant.go skipped before opening)", scanned)
	}
	if f, l, _ := findSymbolDeclarationInRepo(repo, "SmallIndexedSymbol"); f != "small.go" || l != 3 {
		t.Fatalf("small lookup = (%q, %d), want (small.go, 3)", f, l)
	}
	if f, l, _ := findSymbolDeclarationInRepo(repo, "GiantSkippedSymbol"); f != "" || l != 0 {
		t.Fatalf("giant lookup = (%q, %d), want empty", f, l)
	}
	for _, name := range scanned {
		if name == "giant.go" {
			t.Fatal("giant.go should be skipped before reading or calling repoSymbolScanHook")
		}
	}

	repo2 := t.TempDir()
	t.Cleanup(func() { repoSymbolMapCache.Delete(repo2) })
	pad := strings.Repeat("// filler text\n", 130)
	for i, fn := range []string{"FuncOneHandler", "FuncTwoHandler", "FuncThreeHandler"} {
		body := fmt.Sprintf("package demo\n\nfunc %s() {}\n%s", fn, pad)
		if err := os.WriteFile(filepath.Join(repo2, fmt.Sprintf("f%d.go", i+1)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var scans2 atomic.Int64
	repoSymbolScanMaxBytes = 3 << 10
	repoSymbolScanHook = func(string) { scans2.Add(1) }

	idx2 := loadRepoSymbolMap(repo2)
	if !idx2.capped {
		t.Fatal("expected idx2.capped == true when byte budget exceeded")
	}
	scansBeforeMissing := scans2.Load()
	if f, l, _ := findSymbolDeclarationInRepo(repo2, "NonExistentSymbol"); f != "" || l != 0 {
		t.Fatalf("capped missing lookup = (%q, %d), want empty", f, l)
	}
	if scans2.Load() != scansBeforeMissing {
		t.Fatalf("missing symbol on capped index triggered extra scans (%d -> %d)", scansBeforeMissing, scans2.Load())
	}
}
