package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dsh-deja and openclaw-deja ask for the JSON reply and give the model additionalContext,
// where it used to give it what --plain printed; the systemMessage beside it
// goes to the person. That swap is only safe while the two forms carry the
// same text for the model, so each form runs on a fresh copy of the same
// store — the hooks keep once-only state, and a second call on one store
// answers differently by design.
func TestJSONReplyCarriesWhatPlainPrints(t *testing.T) {
	seed := func(t *testing.T) string {
		t.Helper()
		withStatsStores(t)
		root := os.Getenv("DEJA_CLAUDE_ROOT")
		when := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "beta", "drain.jsonl"), "drainpass", []string{
			`{"type":"user","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"user","content":"how should the zorbit queue drain on deploy"}}`,
			`{"type":"assistant","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"we settled on draining the zorbit queue with --grace 40 before each deploy, because 10 dropped jobs"}]}}`,
		})
		for i, topic := range []string{"css grid layout for the settings page", "upgrade the postgres driver", "flaky login test on ci", "rename the billing module"} {
			id := "bg" + string(rune('a'+i))
			writeClaudeFixture(t, filepath.Join(root, "beta", id+".jsonl"), id, []string{
				`{"type":"user","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"user","content":"` + topic + `"}}`,
				`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"done: ` + topic + `"}]}}`,
			})
		}
		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(t.TempDir(), "tmp", "beta")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		return cwd
	}
	decode := func(t *testing.T, out string) (context, notes string) {
		t.Helper()
		var resp sessionStartHookResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("not the hook envelope: %v\n%s", err, out)
		}
		return resp.HookSpecificOutput.AdditionalContext, resp.SystemMessage
	}

	prompt := func(t *testing.T, plain bool) string {
		t.Helper()
		cwd := seed(t)
		payload, jerr := json.Marshal(map[string]any{"prompt": "how do we drain the zorbit queue", "cwd": cwd, "session_id": "parity-1"})
		if jerr != nil {
			t.Fatal(jerr)
		}
		var out bytes.Buffer
		if err := runHookPromptMode(index.DefaultDir(), bytes.NewReader(payload), &out, plain); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	var plainPrompt, jsonPrompt string
	t.Run("prompt-plain", func(t *testing.T) { plainPrompt = prompt(t, true) })
	t.Run("prompt-json", func(t *testing.T) { jsonPrompt = prompt(t, false) })
	if !strings.Contains(plainPrompt, "zorbit") {
		t.Fatalf("the stand never spoke, so there is nothing to compare:\n%s", plainPrompt)
	}
	ctx, notes := decode(t, jsonPrompt)
	if strings.TrimSpace(plainPrompt) != strings.TrimSpace(ctx) {
		t.Errorf("the model would read something else:\n--plain:\n%s\n--json:\n%s", plainPrompt, ctx)
	}
	if !strings.Contains(notes, "deja-vu — ") {
		t.Errorf("the JSON reply carries no line for the person: %q", notes)
	}

	start := func(t *testing.T, plain bool) string {
		t.Helper()
		cwd := seed(t)
		payload, jerr := json.Marshal(map[string]any{"session_id": "parity-2", "cwd": cwd, "source": "startup", "deja_once": true})
		if jerr != nil {
			t.Fatal(jerr)
		}
		withHookStdin(t, string(payload))
		return captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), plain) })
	}
	var plainStart, jsonStart string
	t.Run("start-plain", func(t *testing.T) { plainStart = start(t, true) })
	t.Run("start-json", func(t *testing.T) { jsonStart = start(t, false) })
	if !strings.Contains(plainStart, "zorbit") {
		t.Fatalf("the digest never spoke, so there is nothing to compare:\n%s", plainStart)
	}
	ctx, notes = decode(t, jsonStart)
	if strings.TrimSpace(plainStart) != strings.TrimSpace(ctx) {
		t.Errorf("the model would read another digest:\n--plain:\n%s\n--json:\n%s", plainStart, ctx)
	}
	if notes == "" {
		t.Error("the session start carries no line for the person")
	}
}

// The other ways the two hooks answer: a compaction's recovery packet, and the
// nudge alone (which the asked-for handoff also goes through). Each has its
// own plain and JSON branch.
func TestJSONReplyCarriesWhatPlainPrintsForThePacketAndTheNudge(t *testing.T) {
	packet := func(t *testing.T, shape hookToolShape) string {
		t.Helper()
		dir, workspace, _ := prepareCompaction(t)
		var out bytes.Buffer
		if delivered, err := emitCompactionRecovery(dir, "compaction-fixture", workspace, "UserPromptSubmit", shape, &out); err != nil || !delivered {
			t.Fatalf("no packet: delivered=%v err=%v", delivered, err)
		}
		return out.String()
	}
	var plain, js string
	t.Run("packet-plain", func(t *testing.T) { plain = packet(t, hookToolPlain) })
	t.Run("packet-json", func(t *testing.T) { js = packet(t, hookToolClaude) })
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(js)), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, js)
	}
	if strings.TrimSpace(plain) == "" || strings.TrimSpace(plain) != strings.TrimSpace(resp.HookSpecificOutput.AdditionalContext) {
		t.Errorf("the packet differs between forms:\n--plain:\n%s\n--json:\n%s", plain, resp.HookSpecificOutput.AdditionalContext)
	}

	var a, b bytes.Buffer
	_ = emitNudgeOnly(&a, true, "last time this failed, it was the lockfile")
	_ = emitNudgeOnly(&b, false, "last time this failed, it was the lockfile")
	resp = sessionStartHookResponse{}
	if err := json.Unmarshal(b.Bytes(), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, b.String())
	}
	if strings.TrimSpace(a.String()) != strings.TrimSpace(resp.HookSpecificOutput.AdditionalContext) {
		t.Errorf("the nudge differs between forms:\n%s\n%s", a.String(), resp.HookSpecificOutput.AdditionalContext)
	}
}

// DEJA_AUTORECALL_LOCAL_ONLY=1 keeps synced sessions out of the startup
// digest while leaving them searchable.
func TestAutoRecallLocalOnly(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-isoproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"loc1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"local isoproj work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "loc1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.EnsureForSearch(dir, search.Options{Query: "isoproj"}, false, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(batch, "deja-sync-x-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	if jerr := enc.Encode(index.SyncRecord{Harness: "claude", SessionID: "rem1", Project: "tmp/isoproj", Role: "user", Text: "remote isoproj work", Time: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)}); jerr != nil {
		t.Fatal(jerr)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := index.Import(dir, batch); err != nil || n != 1 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/isoproj")

	digest := hookDigest(index.DefaultDir())
	if !strings.Contains(digest, "imported:") {
		t.Fatalf("default digest should include imported session, got: %q", digest)
	}
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	digest = hookDigest(index.DefaultDir())
	if strings.Contains(digest, "imported:") {
		t.Fatalf("local-only digest leaked imported session: %q", digest)
	}
	if !strings.Contains(digest, "isoproj") {
		t.Fatalf("local-only digest lost local session: %q", digest)
	}
}

func TestPolicyFileBlocksImportedInSearchHits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"mcp":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hits := []search.Hit{
		{Session: model.Session{ID: "a", Project: "deja-vu"}},
		{Session: model.Session{ID: "b", Project: "imported:mini/deja-vu"}},
	}
	got := policyFilterHits(policy.ActivationMCP, hits)
	if len(got) != 1 || got[0].Session.ID != "a" {
		t.Fatalf("mcp filter wrong: %#v", got)
	}
	// The search path has no rule in this file, so nothing is dropped.
	hits2 := []search.Hit{
		{Session: model.Session{ID: "a", Project: "deja-vu"}},
		{Session: model.Session{ID: "b", Project: "imported:mini/deja-vu"}},
	}
	if got := policyFilterHits(policy.ActivationSearch, hits2); len(got) != 2 {
		t.Fatalf("search filter must pass all: %#v", got)
	}
}

func TestLogLastNamesPolicy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	usage.RecordDigestPolicy(dir, usage.KindHook, "digest body", 2, 100, "local-only")
	var out bytes.Buffer
	if err := runLogTo(&out, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "policy: local-only") {
		t.Fatalf("log --last must name the policy:\n%s", out.String())
	}
}

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

func TestSafeScanRootAndSkippedScanDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if got := safeScanRoot(home); got != "" {
			t.Fatalf("safeScanRoot(home) = %q, want empty", got)
		}
		if got := safeScanRoot(filepath.Join(home, "Library", "Caches")); got != "" {
			t.Fatalf("safeScanRoot(home/Library/Caches) = %q, want empty", got)
		}
	}
	tmp := t.TempDir()
	if got := safeScanRoot(tmp); got != tmp {
		t.Fatalf("safeScanRoot(t.TempDir()) = %q, want %q", got, tmp)
	}
	for _, skip := range []string{"Library", "node_modules", ".git", "venv", "target"} {
		if !isSkippedScanDir(skip) {
			t.Fatalf("isSkippedScanDir(%q) = false, want true", skip)
		}
	}
	if isSkippedScanDir("cmd") {
		t.Fatal("isSkippedScanDir(\"cmd\") = true, want false")
	}
}

func TestConversationalRussianPromptTermsFiltered(t *testing.T) {
	terms := extractHistoricalQueryTerms("давай дальше изучать вопрос низкоуровнево и улучшать инструмент", nil)
	for _, bad := range []string{"давай", "дальше", "вопрос"} {
		for _, got := range terms {
			if got == bad {
				t.Fatalf("extractHistoricalQueryTerms kept conversational stop word %q in %v", bad, terms)
			}
		}
	}
}

func TestMalformedStandingRuleFiltered(t *testing.T) {
	for _, bad := range []string{
		"Do not suggest ,: ",
		"Never use ,",
		"Always ;",
	} {
		if !isMalformedStandingRuleText(bad) {
			t.Fatalf("isMalformedStandingRuleText(%q) = false, want true", bad)
		}
	}
	if isMalformedStandingRuleText("Never push to origin without explicit user approval.") {
		t.Fatal("isMalformedStandingRuleText rejected valid rule")
	}
}

func TestFileHookPrecisionHelpers(t *testing.T) {
	if !hasGenericBasename("cmd/app/main.go") {
		t.Fatal("hasGenericBasename(main.go) = false, want true")
	}
	if hasGenericBasename("cmd/deja/hook_tool_file.go") {
		t.Fatal("hasGenericBasename(hook_tool_file.go) = true, want false")
	}
	if mentionsFile("cmd/app/main.go", "edited main.go to fix bug") {
		t.Fatal("mentionsFile matched generic basename main.go without directory")
	}
	if !mentionsFile("cmd/app/main.go", "edited cmd/app/main.go to fix bug") {
		t.Fatal("mentionsFile failed to match qualified cmd/app/main.go")
	}
	clipped := trimClippedDecision("check that the handler validates tokens in this specific request", 42)
	if strings.HasSuffix(clipped, " in…") || strings.HasSuffix(clipped, " in this…") {
		t.Fatalf("trimClippedDecision left dangling connector: %q", clipped)
	}
}

func TestEditTargetMismatchLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sample.go")
	src := "package main\n\nfunc greet(name string) string {\n\treturn \"hello \" + name\n}\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	oldStr := "func greet(name string) string {\n\treturn \"hi \" + name\n}"
	got := editTargetMismatchLine(dir, "sample.go", oldStr, "func greet() {}")
	if !strings.Contains(got, "edit target not verbatim on disk; closest L3-L5:") {
		t.Fatalf("editTargetMismatchLine = %q, want closest L3-L5 hint", got)
	}
	if strings.Contains(got, "\n") || len(got) > 240 {
		t.Fatalf("editTargetMismatchLine must be single-line <= 240B, got %q", got)
	}
}

func TestFailingTestSourceLinePythonTraceback(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pyFile := filepath.Join(srcDir, "worker.py")
	pySrc := "def run():\n    x = 1\n    raise ValueError('bad input')\n"
	if err := os.WriteFile(pyFile, []byte(pySrc), 0o600); err != nil {
		t.Fatal(err)
	}
	tb := "Traceback (most recent call last):\n" +
		"  File \"/usr/lib/python3.11/site-packages/runner.py\", line 10, in exec\n" +
		"    fn()\n" +
		"  File \"" + pyFile + "\", line 3, in run\n" +
		"    raise ValueError('bad input')\n" +
		"ValueError: bad input\n"
	got := failingTestSourceLine(dir, tb, policy.Policy{})
	if !strings.HasPrefix(got, "failing test line — pkg/worker.py:3:") || !strings.Contains(got, "raise ValueError") {
		t.Fatalf("failingTestSourceLine(python traceback) = %q, want prefix failing test line — pkg/worker.py:3:", got)
	}
	winTB := "Traceback (most recent call last):\n" +
		"  File \"" + pyFile + "\", line 3, in run\n" +
		"    raise ValueError('bad input')\n" +
		"  File \"C:\\repo\\.venv\\Lib\\site-packages\\runner.py\", line 10, in exec\n" +
		"    fn()\n" +
		"  File \"C:\\Python311\\Lib\\python3.11\\unittest.py\", line 20, in run\n" +
		"    fn()\n" +
		"ValueError: bad input\n"
	targets := extractFailingTestTargets(winTB)
	if len(targets) != 1 || targets[0].filePart != pyFile {
		t.Fatalf("extractFailingTestTargets(windows venv) = %#v, want single target %q", targets, pyFile)
	}
	gotWin := failingTestSourceLine(dir, winTB, policy.Policy{})
	if !strings.HasPrefix(gotWin, "failing test line — pkg/worker.py:3:") || !strings.Contains(gotWin, "raise ValueError") {
		t.Fatalf("failingTestSourceLine(windows venv) = %q, want prefix failing test line — pkg/worker.py:3:", gotWin)
	}
}

func TestFileMetaMatchesRepoSubpath(t *testing.T) {
	formsMeta := index.SessionMeta{Touched: []string{"webapp/forms/models.py"}}
	authMeta := index.SessionMeta{Touched: []string{"webapp/auth/models.py"}}
	bareMeta := index.SessionMeta{Touched: []string{"models.py"}}

	if !fileMetaMatchesRepoSubpath(formsMeta, "/Users/alex/repo", "/Users/alex/repo/webapp/forms/models.py") {
		t.Fatal("expected exact subpath match to be kept")
	}
	if fileMetaMatchesRepoSubpath(authMeta, "/Users/alex/repo", "/Users/alex/repo/webapp/forms/models.py") {
		t.Fatal("expected sibling module auth/models.py to be filtered out for webapp/forms/models.py")
	}
	if !fileMetaMatchesRepoSubpath(bareMeta, "/Users/alex/repo", "/Users/alex/repo/webapp/forms/models.py") {
		t.Fatal("expected bare basename session without directory info to remain compatible")
	}
}

func TestSubpathScopingPreventsSiblingModuleDecisionAndCountLeak(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	repoDir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	formsFile := filepath.Join(repoDir, "webapp", "forms", "models.py")
	authFile := filepath.Join(repoDir, "webapp", "auth", "models.py")
	billingFile := filepath.Join(repoDir, "webapp", "billing", "models.py")
	for _, f := range []string{formsFile, authFile, billingFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(formsFile, []byte("class ModelFormField:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authFile, []byte("class UserAuthAccount:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(billingFile, []byte("class BillingInvoice:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSess := func(id, file, dec string, day int) {
		ts := time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		uJSON, err := json.Marshal(file)
		if err != nil {
			t.Fatal(err)
		}
		dJSON, err := json.Marshal(dec)
		if err != nil {
			t.Fatal(err)
		}
		cwdJSON, err := json.Marshal(repoDir)
		if err != nil {
			t.Fatal(err)
		}
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":` + string(cwdJSON) + `,"timestamp":"` + ts + `","message":{"role":"user","content":"update module"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":` + string(cwdJSON) + `,"timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":` + string(uJSON) + `,"old_string":"a","new_string":"b"}},{"type":"text","text":` + string(dJSON) + `}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSess("s-forms-1", formsFile, "Decision: ModelFormField validation in models.py must preserve initial values.", 1)
	writeSess("s-forms-2", formsFile, "Updated ModelFormField error propagation in models.py.", 2)
	writeSess("s-auth-1", authFile, "Decision: UserAuthAccount in models.py hashes tokens on save.", 3)
	writeSess("s-auth-2", authFile, "Verified UserAuthAccount migration in models.py.", 4)
	writeSess("s-billing-1", billingFile, "Decision: BillingInvoice in models.py rounds totals per line item.", 5)
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	self := func(index.SessionMeta, string) bool { return false }
	got := fileHookLineOutsideWithEdit(indexDir, repoDir, formsFile, "", self)
	if !strings.Contains(got, "2 sessions") || !strings.Contains(got, "ModelFormField") {
		t.Fatalf("expected 2-session subpath-scoped decision with ModelFormField, got %q", got)
	}
	for _, leaked := range []string{"UserAuthAccount", "BillingInvoice", "5 sessions"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("unexpected sibling-module data %q leaked into forms/models.py: %q", leaked, got)
		}
	}
	if billingGot := fileHookLineOutsideWithEdit(indexDir, repoDir, billingFile, "", self); billingGot != "" {
		t.Fatalf("expected single-session billing/models.py to stay below 2-session bar, got %q", billingGot)
	}
}

func TestTurn0PromptTaskLocalizationAndRecallBrief(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"settlement_relay.go":      "package payments\n\nfunc DispatchSettlementBatch(batchID string) error { return nil }\n",
		"settlement_relay_test.go": "package payments\n\nimport \"testing\"\n\nfunc TestDispatchSettlementBatch(t *testing.T) { _ = DispatchSettlementBatch(\"b1\") }\n",
		"backoff_jitter.go":        "package payments\n\nfunc ComputeExponentialJitter(attempt int) int { return attempt }\n",
		"ledger_reconcile.go":      "package payments\n\nfunc ReconcileLedgerSnapshot(epoch int64) error { return nil }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Zero-history natural module phrase ("settlement relay") resolves target + sibling test recipe.
	line, tok := promptTaskTargetsLine(idxDir, proj, "tighten settlement relay batch handling", nil, nil)
	if !strings.Contains(line, "likely target: settlement_relay.go") {
		t.Fatalf("expected settlement_relay.go in localization line, got %q", line)
	}
	if !strings.Contains(line, "TestDispatchSettlementBatch") {
		t.Fatalf("expected sibling test recipe in localization line, got %q", line)
	}
	if len(tok) == 0 {
		t.Fatalf("expected non-empty dedup token")
	}

	// 2. Compound identifier phrase ("exponential jitter") resolves ComputeExponentialJitter file.
	compLine, _ := promptTaskTargetsLine(idxDir, proj, "adjust exponential jitter cap on retries", nil, nil)
	if !strings.Contains(compLine, "likely target: backoff_jitter.go") {
		t.Fatalf("expected backoff_jitter.go for compound declaration phrase, got %q", compLine)
	}

	// 3. liveRecallBrief surfaces natural-phrase localization for MCP/CLI recall callers.
	brief := liveRecallBrief(idxDir, proj, "settlement relay")
	if !strings.Contains(brief, "settlement_relay.go") || !strings.Contains(brief, "TestDispatchSettlementBatch") {
		t.Fatalf("expected liveRecallBrief to localize settlement relay + test recipe, got %q", brief)
	}

	// 4. Negative-control guard: conversational filler, generic git/test commands,
	// non-ASCII acknowledgements, and off-topic questions must produce zero targets.
	negatives := []string{
		"thanks, looks good",
		"yes please proceed",
		"run the tests and check git status",
		"commit the changes",
		"продолжай",
		"давай дальше",
		"what is the capital of france",
		"fix bug in main function",
	}
	for _, neg := range negatives {
		if got, _ := promptTaskTargetsLine(idxDir, proj, neg, nil, nil); got != "" {
			t.Errorf("false-positive localization on negative prompt %q: got %q", neg, got)
		}
	}

	// 5. liveRecallBrief rejects speculative targets whose stem/path is absent from the query.
	if liveLikelyTargetMatchesQuery("likely target: payment_worker.go — test target: go test ./...", "retry budget backoff") {
		t.Fatalf("expected liveLikelyTargetMatchesQuery to reject unrelated target payment_worker.go")
	}
	if !liveLikelyTargetMatchesQuery("likely target: settlement_relay.go — test target: go test ./...", "settlement relay") {
		t.Fatalf("expected liveLikelyTargetMatchesQuery to accept stem-matched target settlement_relay.go")
	}
}

func TestSessionActionMapAndWeakPointerSurfaceEditedFiles(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relayFile := filepath.Join(proj, "relay.go")
	if err := os.WriteFile(relayFile, []byte("package payments\n\nfunc DispatchSettlementBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relayTest := filepath.Join(proj, "relay_test.go")
	if err := os.WriteFile(relayTest, []byte("package payments\n\nimport \"testing\"\n\nfunc TestDispatchSettlementBatch(t *testing.T) { _ = DispatchSettlementBatch() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two independent sessions edited relay.go and verified go test, without naming relay.go in prose.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"prevent duplicate settlement batches under worker contention"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test -run TestDispatchSettlementBatch ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"ok  \texample.com/payments\t0.012s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:03:00Z","message":{"role":"assistant","content":"We serialize worker claims with an advisory lease before dispatching."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"tighten lease timeout on settlement batch dispatch"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"go test -run TestDispatchSettlementBatch ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","content":"ok  \texample.com/payments\t0.011s"}]}}`, proj),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	metas, err := index.AllMeta(idxDir)
	if err != nil || len(metas) != 2 {
		t.Fatalf("expected 2 indexed sessions, got %d (%v)", len(metas), err)
	}
	ss := []model.Session{{
		Harness: metas[0].Harness,
		ID:      metas[0].ID,
		Project: metas[0].Project,
		Title:   metas[0].Title,
	}}

	actLine := sessionActionMapLineForPrompt(idxDir, proj, "", ss, nil)
	if !strings.Contains(actLine, "files edited there: relay.go") {
		t.Fatalf("expected relay.go in sessionActionMapLine, got %q", actLine)
	}
	if !strings.Contains(actLine, "go test -run TestDispatchSettlementBatch") {
		t.Fatalf("expected verified test command in sessionActionMapLine, got %q", actLine)
	}

	ptr := weakRecallPointerIn(idxDir, proj, ss, []string{"settlement", "lease"}, nil)
	if !strings.Contains(ptr, "files edited there: relay.go") {
		t.Fatalf("expected weakRecallPointerIn to carry concrete edited files, got %q", ptr)
	}
}

func TestTurn0MultilingualFixPairBridgeAndSelfProjectNoiseGuard(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	storeFile := filepath.Join(proj, "store_codec.go")
	if err := os.WriteFile(storeFile, []byte("package payments\n\nfunc DecodeFrameHeader(buf []byte) error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loopFile := filepath.Join(proj, "worker_lease.go")
	if err := os.WriteFile(loopFile, []byte("package payments\n\nfunc RenewWorkerLease() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sweepFiles := make([]string, 0, 10)
	for i := range 10 {
		p := filepath.Join(proj, fmt.Sprintf("extra_%02d.go", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("package payments\n\nfunc ExtraHandler%02d() {}\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		sweepFiles = append(sweepFiles, p)
	}

	// s1: confirmed self-corrected FixPair on store_codec.go ("makeslice: len out of range").
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"investigate corrupt frame decode crash"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:30Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e0","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, storeFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b0","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b0","is_error":true,"content":"panic: runtime error: makeslice: len out of range\nFAIL\texample.com/payments\t0.02s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:30Z","message":{"role":"assistant","content":"The panic happened because untrusted frame length overflowed makeslice bounds."}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:45Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, storeFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"ok  \texample.com/payments\t0.01s"}]}}`, proj),
	})

	// s2 & s3: Cyrillic sessions editing worker_lease.go with inflected domain words.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"воркер зацикливается при потере аренды очереди"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, loopFile),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s3.jsonl"), "s3", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:00:00Z","message":{"role":"user","content":"убрать повторное зацикливание аренды очереди"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e3","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, loopFile),
	})

	// s4: broad 10-file marathon sweep mentioning the project's own name ("payments").
	s4Lines := make([]string, 0, len(sweepFiles)+2)
	s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"user","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:00:00Z","message":{"role":"user","content":"wide cleanup across payments handlers"}}`, proj))
	for i, sf := range sweepFiles {
		s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:01:%02dZ","message":{"role":"assistant","content":[{"type":"tool_use","id":"es%d","name":"Edit","input":{"file_path":%q,"old_string":"{}","new_string":"{}"}}]}}`, proj, i, i, sf))
	}
	s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:05:00Z","message":{"role":"assistant","content":"Completed wide cleanup across payments."}}`, proj))
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s4.jsonl"), "s4", s4Lines)

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// 1. FixPair memory bridges "makeslice panic" in natural prose -> store_codec.go without pasted stderr.
	fxLoc, _ := promptTaskTargetsLine(idxDir, proj, "harden makeslice panic bounds in payments", nil, nil)
	if !strings.Contains(fxLoc, "store_codec.go") {
		t.Fatalf("expected FixPair bridge to localize store_codec.go, got %q", fxLoc)
	}

	// 2. Pure Cyrillic prompt with inflected stem ("зацикливания" vs "зацикливается") localizes worker_lease.go.
	cyrLoc, _ := promptTaskTargetsLine(idxDir, proj, "проверить защиту от зацикливания аренды очереди", nil, nil)
	if !strings.Contains(cyrLoc, "worker_lease.go") {
		t.Fatalf("expected Cyrillic stem bridge to localize worker_lease.go, got %q", cyrLoc)
	}

	// 3. Broad >8-file sweep session s4 is suppressed in sessionActionMapLine (zero sweep noise).
	sweepSess := []model.Session{{Harness: "claude", ID: "s4", Project: "work/payments", Title: "wide cleanup"}}
	if got := sessionActionMapLineForPrompt(idxDir, proj, "check payments status", sweepSess, nil); got != "" {
		t.Fatalf("expected >8-file sweep session to stay silent in sessionActionMapLineForPrompt, got %q", got)
	}

	// 4. Self-project name ("payments") inside cwd must NOT bypass the 2-term quote gate in promptDigestBodyIn.
	sweepFull := []model.Session{{
		Harness: "claude",
		ID:      "s4",
		Project: "work/payments",
		Title:   "wide cleanup",
		Messages: []model.Message{
			{Role: "user", Text: "wide cleanup across payments handlers"},
			{Role: "assistant", Text: "Completed wide cleanup across payments."},
		},
	}}
	idf := map[string]float64{"payments": 1.2, "unseen_xyz_token": 4.8}
	if body, ok := promptDigestBodyIn(idxDir, proj, "live", sweepFull, true, []string{"payments", "unseen_xyz_token"}, idf, "check payments unseen_xyz_token", "", "", nil, nil, nil, nil); ok {
		t.Fatalf("expected self-project name not to relax 2-term quote gate inside cwd, got:\n%s", body)
	}

	// 5. Ranked touched consensus recovers worker_lease.go when >=2 strongly matching sessions edited it.
	ranked := []model.Session{
		{Harness: "claude", ID: "s2", Project: "work/payments"},
		{Harness: "claude", ID: "s3", Project: "work/payments"},
		{Harness: "claude", ID: "s4", Project: "work/payments"},
		{Harness: "claude", ID: "skip1", Project: "work/payments"},
	}
	rcLine, rcTok := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{2, 2, 2, 2}, []int{1, 1, 1, 1}, nil, map[string]bool{"skip1": true})
	if !strings.Contains(rcLine, "files edited in related sessions: worker_lease.go") || rcTok == "" {
		t.Fatalf("expected rankedTouchedConsensusLine to recover worker_lease.go, got line=%q tok=%q", rcLine, rcTok)
	}
	if seenLine, _ := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{2, 2}, []int{1, 1}, map[string]bool{rcTok: true}, nil); seenLine != "" {
		t.Fatalf("expected seen token to suppress duplicate consensus line, got %q", seenLine)
	}
}

func TestTurn0StructuredPromptDiagnosticGuardrailAndDedup(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "archiver")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/archiver\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(proj, "manifest_verifier.go")
	quorumPath := filepath.Join(proj, "quorum_fencing.go")
	readmePath := filepath.Join(proj, "CHANGELOG.md")
	files := map[string]string{
		manifestPath: "package archiver\n\nimport \"errors\"\n\nfunc VerifyManifestDigest(raw []byte) error {\n\tif len(raw) == 0 {\n\t\treturn errors.New(\"snapshot manifest checksum mismatch\")\n\t}\n\treturn nil\n}\n",
		filepath.Join(proj, "manifest_verifier_test.go"): "package archiver\n\nimport \"testing\"\n\nfunc TestVerifyManifestDigest(t *testing.T) { _ = VerifyManifestDigest([]byte(\"ok\")) }\n",
		quorumPath: "package archiver\n\ntype Change struct{ Epoch int }\n\nfunc AcquireQuorumEpoch(epoch int) error { return nil }\n",
		readmePath: "# Changelog\n\nInitial release.\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)

	// Past sessions on manifest_verifier.go: two sessions touching it, including a
	// verified FixPair breakage so Turn 0 has a concrete guardrail to attach.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "archiver", "m1.jsonl"), "m1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"verify manifest digest handling"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"em1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, manifestPath),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"bm1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"bm1","is_error":true,"content":"--- FAIL: TestVerifyManifestDigest (0.01s)\n    manifest_verifier_test.go:6: empty trailer header\nFAIL\tarchiver\t0.02s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"em2","name":"Edit","input":{"file_path":%q,"old_string":"len(raw) == 0","new_string":"len(raw) < 4"}}]}}`, proj, manifestPath),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:04:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"bm2","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:04:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"bm2","content":"ok  \texample.com/archiver\t0.01s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:04:30Z","message":{"role":"assistant","content":"Fixed TestVerifyManifestDigest in manifest_verifier.go because empty trailer headers were not rejected before slicing."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "archiver", "m2.jsonl"), "m2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"tighten manifest verifier bounds"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"em3","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, manifestPath),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"bm3","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"bm3","content":"ok  \texample.com/archiver\t0.01s"}]}}`, proj),
	})

	// Past sessions bridging external domain vocabulary ("splitbrain", "heartbeat")
	// onto rare repo stem "quorum" / "fencing" via cooccur.gob.
	for i := 1; i <= 3; i++ {
		sid := fmt.Sprintf("q%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "archiver", sid+".jsonl"), sid, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT11:00:00Z","message":{"role":"user","content":"investigate splitbrain stalledleasedetector during quorum fencing"}}`, sid, proj, i+2),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT11:01:00Z","message":{"role":"assistant","content":"Updated quorum fencing AcquireQuorumEpoch to fence splitbrain stalledleasedetector."}}`, sid, proj, i+2),
		})
	}

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// 1. Multi-paragraph structured spec with fork boilerplate, doc mention (CHANGELOG.md),
	// sentence-initial Titlecase word ("Change"), quoted diagnostic error string, and
	// closing git/PR workflow instructions localizes manifest_verifier.go + guardrail!
	structuredPrompt := "Fork started — processing in background\n<fork-boilerplate>\nWorker fork instructions.\n</fork-boilerplate>\n\nYour directive: Change in example.com/archiver (also note CHANGELOG.md).\nUsers see \"snapshot manifest checksum mismatch\" when restoring empty archives.\n\nWork in your own worktree from origin/main (`git fetch origin`).\nDo NOT push or open a PR.\nReport: branch + commit SHA, root cause, and `go test ./...` output."

	turn0 := promptDirectContextIn(idxDir, proj, "live-sess-1", structuredPrompt, false, nil)
	if !strings.Contains(turn0, "likely target: manifest_verifier.go") {
		t.Fatalf("expected structured prompt + diagnostic literal to localize manifest_verifier.go, got:\n%s", turn0)
	}
	if strings.Contains(turn0, "defined in quorum_fencing.go") {
		t.Fatalf("expected sentence-initial Titlecase 'Change' not to trigger symbol brief hijack, got:\n%s", turn0)
	}
	if !strings.Contains(turn0, "past breakage here:") && !strings.Contains(turn0, "prior decision:") {
		t.Fatalf("expected Turn-0 localization to attach verified guardrail for manifest_verifier.go, got:\n%s", turn0)
	}

	// 2. Temporal skip: skipping m1 hides m1's FixPair breakage from Turn-0 guardrail.
	skipped := promptDirectContextIn(idxDir, proj, "", structuredPrompt, true, map[string]bool{"m1": true})
	if strings.Contains(skipped, "TestVerifyManifestDigest") && strings.Contains(skipped, "past breakage here:") {
		t.Fatalf("expected skip[m1] to suppress m1 FixPair guardrail (zero future leakage), got:\n%s", skipped)
	}

	// 3. Cross-surface .seen deduplication: after live-sess-1 already saw the
	// guardrail at Turn 0, PreToolUse on manifest_verifier.go must not re-inject the same guardrail.
	toolPayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q},"cwd":%q,"session_id":"live-sess-1"}`, manifestPath, proj)
	var toolOut bytes.Buffer
	if err := runHookTool(idxDir, strings.NewReader(toolPayload), &toolOut); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(toolOut.String(), "past breakage here:") {
		t.Fatalf("expected .seen deduplication to suppress duplicate PreToolUse guardrail in same session, got:\n%s", toolOut.String())
	}

	// 4. Co-occurrence bridge: domain words ("splitbrain", "stalledleasedetector") absent from
	// source files bridge via cooccur.gob onto quorum_fencing.go, while vague prompts stay silent.
	if len(index.CooccurNeighbors(idxDir, "splitbrain")) > 0 {
		coLoc, _ := promptTaskTargetsLine(idxDir, proj, "prevent splitbrain stalledleasedetector regression", nil, nil)
		if !strings.Contains(coLoc, "quorum_fencing.go") {
			t.Fatalf("expected cooccur.gob bridge to localize quorum_fencing.go, got %q", coLoc)
		}
	}
	if negLoc, _ := promptTaskTargetsLine(idxDir, proj, "please clean up comments and formatting", nil, nil); negLoc != "" {
		t.Fatalf("expected vague negative prompt to stay silent, got %q", negLoc)
	}

	// 5. Direct helper coverage for reversal clipping, breakage ranking, and workflow meta spans.
	if !isWorkflowMetaSpan("git fetch origin main") || isWorkflowMetaSpan("AcquireQuorumEpoch") {
		t.Errorf("unexpected isWorkflowMetaSpan behavior")
	}
	if !usableReversalLine("Откатил кэширование сокета, потому что оно ломало переподключение") || usableReversalLine("Verified: with the hunk reverted, test fails") {
		t.Errorf("unexpected usableReversalLine behavior")
	}
	longRev := "Confirmed: First we inspected the pool stats in detail across several worker Goroutines; abandoned per-request mutex locking in LeaseStore because it deadlocked under retry storms."
	if clipped := clipReversalDecision(longRev, 120); !strings.Contains(clipped, "abandoned") {
		t.Errorf("expected clipReversalDecision to preserve reversal tail, got %q", clipped)
	}
	if !mentionsFileStrictForReversal("internal/lease_workers.go", "reverted worker retry loop") || mentionsFileStrictForReversal("internal/search/query.go", "search package cleanup") {
		t.Errorf("unexpected mentionsFileStrictForReversal behavior")
	}
	if !mentionsDistinctiveSymbolForReversal([]string{"AcquireQuorumEpoch"}, "reverted AcquireQuorumEpoch lock") || mentionsDistinctiveSymbolForReversal([]string{"short"}, "reverted short") {
		t.Errorf("unexpected mentionsDistinctiveSymbolForReversal behavior")
	}
	if !repoEntryMentionsFile(proj, filepath.Join(proj, "quorum_fencing.go"), "always check quorum_fencing invariants") || repoEntryMentionsFile(proj, filepath.Join(proj, "io.go"), "unrelated note") {
		t.Errorf("unexpected repoEntryMentionsFile behavior")
	}
	if !isGenericDirPart("internal") || isGenericDirPart("quorum") {
		t.Errorf("unexpected isGenericDirPart behavior")
	}
	fp := index.FixPair{Symbol: "LeaseStore.RenewQuorum", Why: "RenewQuorum skipped epoch check", Candidate: false}
	if breakageContextRank(fp, []string{"RenewQuorum"}) < 7 || !whyMentionsSymbol(fp.Why, fp.Symbol) {
		t.Errorf("unexpected breakageContextRank / whyMentionsSymbol")
	}
	if !decisionDuplicatesBreakageWhy("Root cause: RenewQuorum skipped epoch check during failover", "TestRenew — RenewQuorum skipped epoch check during failover") {
		t.Errorf("expected decisionDuplicatesBreakageWhy = true")
	}
	if extractBrokenTestName("--- FAIL: TestVerifyManifestDigest (0.01s)") != "TestVerifyManifestDigest" || !isSpecificBreakageError("panic: nil pointer dereference") || isSpecificBreakageError("- bullet item") {
		t.Errorf("unexpected breakage error classification")
	}
}

func TestTurn0StemStopwordMemoryAndSingleSessionConsensus(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "payservice")
	files := map[string]string{
		filepath.Join(proj, ".git", "HEAD"):          "ref: refs/heads/main\n",
		filepath.Join(proj, "batch_after_hook.go"):   "package payservice\n\nfunc RunSettlementWindow() {}\n",
		filepath.Join(proj, "mcp_router.go"):         "package payservice\n\nfunc DispatchMcpFrame() {}\n",
		filepath.Join(proj, "localize_cache.go"):     "package payservice\n\nfunc LocalizeTargetCache() {}\n",
		filepath.Join(proj, "quorum_fence.go"):       "package payservice\n\nfunc FenceEpoch() {}\n",
		filepath.Join(proj, "worker_retry_one.go"):   "package payservice\n\nfunc RetryWorkerOne() {}\n",
		filepath.Join(proj, "worker_retry_two.go"):   "package payservice\n\nfunc RetryWorkerTwo() {}\n",
		filepath.Join(proj, "worker_retry_three.go"): "package payservice\n\nfunc RetryWorkerThree() {}\n",
		filepath.Join(proj, "worker_retry_four.go"):  "package payservice\n\nfunc RetryWorkerFour() {}\n",
		filepath.Join(proj, ".deja", "memory.jsonl"): `{"id":"aa11bb22cc","kind":"gotcha","text":"Settlement rollback fails during reconciliation window","files":["batch_after_hook.go"],"date":"2026-03-05"}` + "\n",
	}
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Generic stopword stem tokens ("after hook") must not pass isTechnicalStemStopWord or spuriously match batch_after_hook.go.
	if !isTechnicalStemStopWord("after") || !isTechnicalStemStopWord("hook") {
		t.Fatalf("expected after/hook to be filtered as technical stem stopwords")
	}
	if stopOut, _ := promptTaskTargetsLine(idxDir, proj, "check behavior after hook runs", nil, nil); strings.Contains(stopOut, "batch_after_hook.go") {
		t.Errorf("generic stopwords unexpectedly localized batch_after_hook.go: %q", stopOut)
	}

	// 2. Cold repo without an index still resolves 3-letter package/stem tokens ("mcp"), ASCII inflections ("localizing"), and .deja/memory.jsonl.
	if mcpOut, _ := promptTaskTargetsLine(idxDir, proj, "fix broken mcp dispatch router", nil, nil); !strings.Contains(mcpOut, "mcp_router.go") {
		t.Errorf("expected 3-letter token to localize mcp_router.go, got %q", mcpOut)
	}
	if inflectOut, _ := promptTaskTargetsLine(idxDir, proj, "fix localizing target cache bug", nil, nil); !strings.Contains(inflectOut, "localize_cache.go") {
		t.Errorf("expected ASCII inflection to localize localize_cache.go, got %q", inflectOut)
	}
	memBoost := repoMemoryTargetBoost(proj, "settlement rollback failure during reconciliation window", selfProjectWordSet(proj))
	if memBoost["batch_after_hook.go"] <= 0 {
		t.Errorf("expected .deja/memory.jsonl boost for batch_after_hook.go, got %v", memBoost)
	}

	// 3. Single strong session fallback in rankedTouchedConsensusLine works when contributingSessions == 1,
	// while weak or cross-project single sessions stay silent.
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fenceFile := filepath.Join(proj, "quorum_fence.go")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payservice", "strong1.jsonl"), "strong1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"strong1","cwd":%q,"timestamp":"2026-03-06T10:00:00Z","message":{"role":"user","content":"fix quorum epoch fencing drift during snapshot handoff"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"strong1","cwd":%q,"timestamp":"2026-03-06T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"func FenceEpoch() {}","new_string":"func FenceEpoch() {}"}}]}}`, proj, fenceFile),
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	var projName string
	for _, m := range metas {
		if m.ID == "strong1" {
			projName = m.Project
			break
		}
	}
	if projName == "" {
		t.Fatalf("expected strong1 metadata in index, got %+v", metas)
	}

	ranked := []model.Session{{Harness: "claude", ID: "strong1", Project: projName}}
	strongLine, strongTok := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{3}, []int{2}, nil, nil)
	if !strings.Contains(strongLine, "files edited in related session: quorum_fence.go") || strongTok == "" {
		t.Errorf("expected single-strong-session fallback to recover quorum_fence.go, got line=%q tok=%q", strongLine, strongTok)
	}
	if weakLine, _ := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{2}, []int{0}, nil, nil); weakLine != "" {
		t.Errorf("expected weak single session to stay silent, got %q", weakLine)
	}
	crossRanked := []model.Session{{Harness: "claude", ID: "strong1", Project: "other/unrelated"}}
	if crossLine, _ := rankedTouchedConsensusLine(idxDir, proj, crossRanked, []int{3}, []int{2}, nil, nil); crossLine != "" {
		t.Errorf("expected cross-project single session to be filtered out, got %q", crossLine)
	}
}

func TestTurn0FourCharMorphBodyAndCrossScriptLocalize(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "gateway")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":             "module example.com/gateway\n\ngo 1.24\n",
		"subnet_pool.go":     "package gateway\n\n// Allocates cidr ranges and normalizes ingress headers.\nfunc AssignRouteSlot(slot int) int { return slot }\n",
		"localize_filter.go": "package gateway\n\nfunc FilterLocalizedSnippets() {}\n",
		"alpha_worker.go":    "package gateway\n\nfunc RunAlphaWorker() {}\n",
		"beta_worker.go":     "package gateway\n\nfunc RunBetaWorker() {}\n",
		"gamma_worker.go":    "package gateway\n\nfunc RunGammaWorker() {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// (a) 4-char standalone body token ("cidr") + morphological root ("normalizing" -> "normaliz" matching "normalizes").
	bodyLine, _ := promptTaskTargetsLine(idxDir, proj, "fix normalizing cidr allocations", nil, nil)
	if !strings.Contains(bodyLine, "subnet_pool.go") {
		t.Fatalf("expected subnet_pool.go from 4-char + morph body match, got %q", bodyLine)
	}

	// (b) Russian prompt localizes English repo file via CrossScriptTerms without English tokens.
	ruLine, _ := promptTaskTargetsLine(idxDir, proj, "поправь локализацию и фильтрацию сниппетов", nil, nil)
	if !strings.Contains(ruLine, "localize_filter.go") {
		t.Fatalf("expected localize_filter.go from Russian CrossScriptTerms prompt, got %q", ruLine)
	}
}

func TestSingleStrongSessionScansPastNotesAndRanksWideTouched(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	noiseNames := []string{
		"noise_one.go", "noise_two.go", "noise_three.go", "noise_four.go", "noise_five.go",
		"noise_six.go", "noise_seven.go", "noise_eight.go", "noise_nine.go",
	}
	for _, name := range noiseNames {
		if err := os.WriteFile(filepath.Join(proj, name), []byte("package payments\n\nfunc NoopTask() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	quorumFile := filepath.Join(proj, "quorum_lease.go")
	if err := os.WriteFile(quorumFile, []byte("package payments\n\nfunc RenewQuorumLease() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Three zero-Touched note sessions ahead of one 10-file session where quorum_lease.go was edited last.
	for i, sid := range []string{"note1", "note2", "note3"} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", sid+".jsonl"), sid, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT10:00:00Z","message":{"role":"user","content":"discuss quorum lease architecture notes"}}`, sid, proj, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT10:01:00Z","message":{"role":"assistant","content":"Reviewed design tradeoffs without editing files."}}`, sid, proj, i+1),
		})
	}
	s4Lines := make([]string, 0, 2+len(noiseNames))
	s4Lines = append(s4Lines,
		fmt.Sprintf(`{"type":"user","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:00:00Z","message":{"role":"user","content":"tighten quorum lease fencing across workers"}}`, proj),
	)
	for i, name := range noiseNames {
		p := filepath.Join(proj, name)
		s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:01:%02dZ","message":{"role":"assistant","content":[{"type":"tool_use","id":"n%d","name":"Edit","input":{"file_path":%q,"old_string":"NoopTask()","new_string":"NoopTask()"}}]}}`, proj, i+1, i+1, p))
	}
	s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"q1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, quorumFile))
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s4.jsonl"), "s4", s4Lines)

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]index.SessionMeta{}
	for _, m := range metas {
		byID[m.ID] = m
	}
	toSess := func(id string) model.Session {
		m := byID[id]
		return model.Session{Harness: m.Harness, ID: m.ID, Project: m.Project, Title: m.Title}
	}
	ranked := []model.Session{toSess("note1"), toSess("note2"), toSess("note3"), toSess("s4")}
	line, tok := rankedTouchedConsensusLineForPrompt(idxDir, proj, "quorum lease fencing bug", ranked, []int{4, 4, 4, 3}, []int{0, 0, 0, 0}, nil, nil)
	if !strings.HasPrefix(line, "files edited in related session: quorum_lease.go") {
		t.Fatalf("expected quorum_lease.go ranked first after scanning past note sessions and >8 touched files, got %q (tok=%q)", line, tok)
	}
}

func TestSelectLowDFTouchedTargets(t *testing.T) {
	sweep := []string{"f01.go", "f02.go", "f03.go", "f04.go", "f05.go", "f06.go", "f07.go", "f08.go", "f09.go", "f10.go"}
	sweepDF := make(map[string]int, len(sweep))
	for _, f := range sweep {
		sweepDF[f] = 1
	}
	if got := selectLowDFTouchedTargets(sweep, sweepDF, 4); len(got) != 0 {
		t.Fatalf("expected pure low-DF sweep to return nil, got %v", got)
	}

	mixed := []string{
		"hub1.go", "hub2.go", "hub3.go", "hub4.go", "hub5.go", "hub6.go", "hub7.go",
		"leaf_alpha.go", "leaf_beta.go",
	}
	mixedDF := map[string]int{
		"hub1.go":       18,
		"hub2.go":       15,
		"hub3.go":       12,
		"hub4.go":       9,
		"hub5.go":       8,
		"hub6.go":       7,
		"hub7.go":       6,
		"leaf_alpha.go": 2,
		"leaf_beta.go":  3,
	}
	got := selectLowDFTouchedTargets(mixed, mixedDF, 4)
	want := []string{"leaf_alpha.go", "leaf_beta.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected mixed hub+leaf session to isolate %v, got %v", want, got)
	}
}

func TestEvalPromptTurn0ReadOnlyOmitsLeadAndStaysConcise(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relayFile := filepath.Join(proj, "settlement_relay.go")
	if err := os.WriteFile(relayFile, []byte("package payments\n\nfunc DispatchSettlementBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"investigate backoff scheduling jitter"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":"Settled on epoch fencing tokens in settlement_relay.go so workers abort stale dispatches."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"check coordinator standby lease"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":"Confirmed epoch fencing validates settlement_relay dispatches."}}`, proj),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := evalPromptTurn0ReadOnly(idxDir, proj, "settlement relay epoch fencing", []string{"payments"}, nil)
	if out == "" {
		t.Fatalf("expected non-empty Turn-0 read-only recall output")
	}
	if strings.Contains(out, promptHookLead) {
		t.Fatalf("expected evalPromptTurn0ReadOnly to omit promptHookLead preamble, got %q", out)
	}
	if len(out) > 900 {
		t.Fatalf("expected steady-state Turn-0 output <= 900 bytes, got %d: %q", len(out), out)
	}
}

func TestLongSpecMegaFileDampeningAndDirScope(t *testing.T) {
	idxDir := hermeticEnv(t)
	proj := t.TempDir()
	for _, rel := range []string{"internal/reconcile", "cmd/ledger"} {
		if err := os.MkdirAll(filepath.Join(proj, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	reconcileSrc := `package reconcile

// ReconcileBatchOffsets commits verified offset watermarks per partition.
func ReconcileBatchOffsets(
	partition int,
	watermark int64,
) error {
	return commitOffsetWatermark(partition, watermark)
}

func SharedEntry() {}

func commitOffsetWatermark(partition int, watermark int64) error {
	if watermark < 0 {
		return nil
	}
	_ = partition
	return nil
}
`
	if err := os.WriteFile(filepath.Join(proj, "internal", "reconcile", "offset_table.go"), []byte(reconcileSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	var mega strings.Builder
	mega.WriteString("package main\n\nfunc SharedEntry() {}\n\n")
	for i := range 220 {
		fmt.Fprintf(&mega, "func dispatchLedgerRoute%03d(offset, watermark, batch, partition int) int {\n", i)
		mega.WriteString("\t// reconcile dispatch helper in cmd/ledger router\n")
		mega.WriteString("\treturn offset + watermark + batch + partition\n}\n\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "cmd", "ledger", "router_dispatch.go"), []byte(mega.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := strings.Join([]string{
		"Fix partition watermark persistence bug in internal/reconcile.",
		"Lines below describe the failure symptom and constraints:",
		"When offset watermarks advance, batch commits must stay atomic.",
		"Audit partition watermark handling under internal/reconcile.",
		"Check commitOffsetWatermark bounds before advancing.",
		"Constraints: keep cmd/ledger router dispatch behavior unchanged.",
		"Verification: gofmt -w cmd/ledger and run golangci-lint before report.",
		"Deliverables: branch report only, do not push.",
	}, "\n")

	line, toks := promptTaskTargetsLine(idxDir, proj, spec, nil, nil)
	if line == "" || len(toks) == 0 {
		t.Fatalf("expected non-empty localization targets, got line=%q toks=%v", line, toks)
	}
	offIdx := strings.Index(line, "internal/reconcile/offset_table.go")
	if offIdx < 0 {
		t.Fatalf("expected internal/reconcile/offset_table.go in %q", line)
	}
	if routerIdx := strings.Index(line, "cmd/ledger/router_dispatch.go"); routerIdx >= 0 && routerIdx < offIdx {
		t.Fatalf("expected internal/reconcile/offset_table.go before mega-file, got %q", line)
	}

	idx := loadRepoSymbolMap(proj)
	loc, ok := idx.firstDecl["reconcilebatchoffsets"]
	if !ok {
		t.Fatalf("expected ReconcileBatchOffsets in idx.firstDecl")
	}
	if loc.rel != "internal/reconcile/offset_table.go" || loc.line != 4 {
		t.Fatalf("unexpected decl location: %+v", loc)
	}
	if !strings.Contains(loc.sig, "watermark int64") || strings.Contains(loc.sig, "\n") {
		t.Fatalf("expected collapsed multi-line signature with watermark int64, got %q", loc.sig)
	}

	rel, lineNo, sig := findSymbolDeclarationInRepo(proj, "ReconcileBatchOffsets")
	if rel != "internal/reconcile/offset_table.go" || lineNo != 4 || !strings.Contains(sig, "watermark int64") {
		t.Fatalf("findSymbolDeclarationInRepo got (%q, %d, %q)", rel, lineNo, sig)
	}

	symLine, symTok := promptSymbolBriefLine(proj, "audit SharedEntry vs ReconcileBatchOffsets", nil)
	if !strings.HasPrefix(symTok, "prompt:sb:") {
		t.Fatalf("expected prompt:sb token from promptSymbolBriefLine, got %q (%q)", symTok, symLine)
	}
	if !strings.Contains(symLine, "internal/reconcile/offset_table.go:4") || !strings.Contains(symLine, "watermark int64") {
		t.Fatalf("expected df=1 ReconcileBatchOffsets signature in promptSymbolBriefLine output, got %q", symLine)
	}
	if strings.Contains(symLine, "SharedEntry") || strings.Contains(symLine, "router_dispatch.go") {
		t.Fatalf("expected ReconcileBatchOffsets (df=1) to beat SharedEntry (df=2), got %q", symLine)
	}
}

func TestStage2ImplicitLocalizationAndCompactDigestBudget(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "myapp")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/myapp\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"internal/search/fork_copy.go":     "package search\n\nfunc NormalizeForkCopyGroup(id string) string { return id }\n",
		"cmd/app/fork_copies.go":           "package main\n\nfunc ListForkCopies() int { return 0 }\n",
		"extensions/widget/index.js":       "export function registerWidget() { return true }\n",
		"extensions/widget/lib.js":         "export const WIDGET_VERSION = 1\n",
		"cmd/app/install_widget_plugin.go": "package main\n\nfunc InstallWidgetPlugin() error { return nil }\n",
		"cmd/app/release_notice.go":        "package main\n\nimport \"os\"\n\nfunc ReleaseNoticeDisabled() bool { return os.Getenv(\"MYAPP_NO_RELEASE_NOTICE\") == \"1\" }\n",
		"cmd/app/notice_caller.go":         "package main\n\nfunc checkNotice() bool { return ReleaseNoticeDisabled() }\n",
	}
	for i := range 20 {
		files[fmt.Sprintf("internal/misc/mod_%02d.go", i)] = fmt.Sprintf("package misc\n\nfunc Helper%02d() int { return %d }\n", i, i)
	}
	for rel, body := range files {
		full := filepath.Join(proj, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Rules: paragraph preserves technical copy/fork grouping scope and -y/-ies stem plural match.
	forkPrompt := "Audit deduplication in recent sessions.\nRules: keep your change inside the copy/fork grouping logic."
	forkLine, _ := promptTaskTargetsLine(idxDir, proj, forkPrompt, nil, nil)
	if !strings.Contains(forkLine, "internal/search/fork_copy.go") || !strings.Contains(forkLine, "cmd/app/fork_copies.go") {
		t.Fatalf("expected both fork_copy.go and fork_copies.go in %q", forkLine)
	}

	// 2. Explicit small directory scope (`extensions/widget/`) prepends directory files ahead of suffix bigrams.
	dirPrompt := "Update the widget plugin (`extensions/widget/`) startup payload."
	dirLine, _ := promptTaskTargetsLine(idxDir, proj, dirPrompt, nil, nil)
	idxIdx := strings.Index(dirLine, "extensions/widget/index.js")
	plugIdx := strings.Index(dirLine, "cmd/app/install_widget_plugin.go")
	if !strings.Contains(dirLine, "extensions/widget/lib.js") || idxIdx < 0 || plugIdx < 0 || plugIdx < idxIdx {
		t.Fatalf("expected explicit small dir targets before widget plugin file, got %q", dirLine)
	}

	// 3. Unique compound env/config identifier resolves release_notice.go.
	envPrompt := "Fix startup update check when MYAPP_NO_RELEASE_NOTICE=1 is set."
	envLine, _ := promptTaskTargetsLine(idxDir, proj, envPrompt, nil, nil)
	if !strings.Contains(envLine, "cmd/app/release_notice.go") {
		t.Fatalf("expected cmd/app/release_notice.go in %q", envLine)
	}

	// 4. Guidance detection, compact target supplement, and index-backed caller resolution.
	if !nudgeHasLocalizedGuidance("likely target: a.go") ||
		!nudgeHasLocalizedGuidance("defined in a.go:10: func Foo()") ||
		!nudgeHasLocalizedGuidance("touch together: a.go") {
		t.Fatalf("expected nudgeHasLocalizedGuidance to recognize target/symbol/touch prefixes")
	}
	supp := compactTargetSupplementLine(
		"likely target: a.go — run go test ./pkg -run '^TestA$'",
		"touch together: a.go, b.go — run go test ./pkg -run '^TestA$'",
	)
	if supp != "files edited there: b.go" {
		t.Fatalf("compactTargetSupplementLine = %q, want %q", supp, "files edited there: b.go")
	}
	idx := loadRepoSymbolMap(proj)
	callers := crossFileCallersFromIndexLine(proj, &idx, "cmd/app/release_notice.go", "ReleaseNoticeDisabled", "func ReleaseNoticeDisabled() bool")
	if callers != " — callers of ReleaseNoticeDisabled: cmd/app/notice_caller.go:3" {
		t.Fatalf("crossFileCallersFromIndexLine = %q", callers)
	}
}

// The one state nobody ever sets is "rejected" — zero sessions carry it on a
// real store — because it is asked for after the fact. The moment it can be
// captured is the sentence where the user says it failed, so that is where the
// hook asks, whether or not the same prompt earns a recall.
func TestPromptHookAsksToRecordADeadEndWithNoRecallToCarryIt(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	// A prompt with no informative terms: the recall path gives up early, and
	// the nudge still has to reach the agent.
	out := promptHookOut(t, dir, "ok we rolled back that change")
	if out == "" {
		t.Fatal("a reported dead end produced nothing")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("not the hook shape: %v (%q)", err, out)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "deja remember") {
		t.Errorf("the nudge does not say how to record it: %q", resp.HookSpecificOutput.AdditionalContext)
	}

	// Twice in a row is noise: a prompt hook is paid on every message.
	if again := promptHookOut(t, dir, "and we reverted the other one too"); again != "" {
		t.Errorf("the nudge repeated within the gap: %q", again)
	}
}

func TestPromptHookStaysSilentOnAnOrdinaryMessage(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if out := promptHookOut(t, dir, "please add a retry to the webhook sender"); out != "" {
		t.Errorf("an ordinary message was answered: %q", out)
	}
	// A question about reverting is the moment before the decision, not the
	// decision, and must not be treated as a report.
	if out := promptHookOut(t, dir, "should we have reverted that?"); out != "" {
		t.Errorf("a question was read as a report: %q", out)
	}
}

func promptHookOut(t *testing.T, dir, prompt string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          prompt,
		"session_id":      "now",
	})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := runHookPrompt(dir, strings.NewReader(string(payload)), &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// The second session someone ever has is the first time deja can say "you have
// been here", and it said nothing: in a one-session store every idf collapses
// to zero, so the term the session had to have SPOKEN was picked by the prompt's
// word order and came out "seeing" (#2257).
func TestAStoreOfOneSessionStillRecallsIt(t *testing.T) {
	inject := func(t *testing.T, fillers int) string {
		t.Helper()
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		claude := filepath.Join(tmp, "claude")
		if err := os.MkdirAll(filepath.Join(claude, "beta"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(claude, "alpha"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_CLAUDE_ROOT", claude)
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
		t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
		at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":"hookfix","timestamp":%q,`+
			`"message":{"role":"user","content":"gateway_timeout on the reconnect_loop keeps dropping heartbeats"}}`, at)
		if err := os.WriteFile(filepath.Join(claude, "beta", "hookfix.jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for i := range fillers {
			l := fmt.Sprintf(`{"type":"user","sessionId":"filler%d","timestamp":%q,`+
				`"message":{"role":"user","content":"unrelated work about deployments and dashboards"}}`, i, at)
			if err := os.WriteFile(filepath.Join(claude, "alpha", fmt.Sprintf("f%d.jsonl", i)), []byte(l+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		dir := filepath.Join(tmp, "index.db")
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(tmp, "proj", "beta")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		var out bytes.Buffer
		in := strings.NewReader(`{"prompt":"seeing gateway_timeout in the reconnect_loop again"}`)
		if err := runHookPrompt(dir, in, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	// The premise: with a second session in the store this prompt recalls.
	if got := inject(t, 1); !strings.Contains(got, "gateway_timeout") {
		t.Fatalf("two sessions and no recall, so this measures nothing: %q", got)
	}
	if got := inject(t, 0); !strings.Contains(got, "gateway_timeout") {
		t.Errorf("the only session in the store was not recalled: %q", got)
	}
}

// hookPayload builds what a harness writes to a hook's stdin.
//
// Pasting a path into a JSON string with `+` breaks the moment the path holds a
// backslash — every Windows path does — and the payload a test then feeds the
// hook is not JSON at all. Nothing says so: the hook reads no session id and
// the assertion fails several steps later, about a session that was never in a
// payload deja could parse (#2081).
func hookPayload(t *testing.T, fields map[string]string) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The escaping itself, so the helper cannot quietly stop doing it.
func TestAHookPayloadSurvivesAWindowsPath(t *testing.T) {
	payload := hookPayload(t, map[string]string{
		"source": "startup", "session_id": "ses1", "cwd": `C:\Users\me\work\api`,
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("the payload is not JSON: %v: %s", err, payload)
	}
	if got["cwd"] != `C:\Users\me\work\api` {
		t.Errorf("the path did not survive: %q", got["cwd"])
	}
	if got["session_id"] != "ses1" {
		t.Errorf("the session id did not survive: %q", got["session_id"])
	}
	// And the shape a hand-built payload had: a bare backslash run, which is
	// what made it unparseable.
	if !strings.Contains(payload, `\\Users`) {
		t.Errorf("the backslashes are not escaped: %s", payload)
	}
}

// claudeRecord builds one line of a claude transcript. A record is JSON too, so
// a path pasted into one has the same hole a payload does — `filepath.ToSlash`
// covers the backslash a Windows path carries and nothing else (#2096).
func claudeRecord(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// A path can hold a quote on every Unix filesystem, and that is the character
// ToSlash does nothing about.
func TestAFixtureSurvivesAPathHoldingAQuote(t *testing.T) {
	odd := `/tmp/say "hello"/app`
	payload := hookPayload(t, map[string]string{"source": "startup", "session_id": "ses1", "cwd": odd})
	var got map[string]string
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("the payload is not JSON: %v: %s", err, payload)
	}
	if got["cwd"] != odd {
		t.Errorf("the path did not survive: %q", got["cwd"])
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": odd,
		"message": map[string]any{"role": "user", "content": "the pool was exhausted"},
	})
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rec)), &line); err != nil {
		t.Fatalf("the record is not JSON: %v: %s", err, rec)
	}
	if line["cwd"] != odd {
		t.Errorf("the record's path did not survive: %v", line["cwd"])
	}
}

// A hook fed something that is not a payload injects anyway — an agent that
// asked for context gets it — but the receiver goes missing from the log that
// exists to answer "to whom", and nothing told the two cases apart: a host that
// sent nothing and a host whose payload deja could not read left the same row
// (#2161).
func TestAnUnreadablePayloadIsRecordedAsOne(t *testing.T) {
	for _, c := range []struct{ name, payload, want string }{
		{"prose", "not a payload at all", "unreadable"},
		{"truncated", `{"session_id":"ses1"`, "unreadable"},
		{"binary", "\x00\x01\x02", "unreadable"},
		{"a list", "[1, 2]", "unreadable"},
		// These decode. A host that names no session is not a host deja could
		// not read, and the row says so by saying nothing.
		{"empty", "", ""},
		{"whitespace", "   \n", ""},
		{"empty object", "{}", ""},
		{"null", "null", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			withStatsStores(t)
			claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
			old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
			writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "unreadterm", []string{
				`{"type":"user","sessionId":"unreadterm","timestamp":"` + old +
					`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
			})
			if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(t.TempDir(), "tmp", "beta")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(cwd)
			t.Setenv("CLAUDE_PROJECT_DIR", cwd)

			withHookStdin(t, c.payload)
			if out := captureStdout(t, func() { runHookContextPlain(t) }); !strings.Contains(out, "transaction mode") {
				t.Fatalf("the hook injected nothing, so there is no record to check:\n%q", out)
			}

			b, err := os.ReadFile(usage.SnapshotPath(index.DefaultDir()))
			if err != nil {
				t.Fatalf("nothing was written to the injection log: %v", err)
			}
			out, err := captureRun(t, "log", "5")
			if err != nil {
				t.Fatal(err)
			}
			if c.want == "" {
				if strings.Contains(string(b), "unreadable") || strings.Contains(out, "into: unknown") {
					t.Errorf("a payload deja could read was reported as broken:\n%s\n%s", b, out)
				}
				return
			}
			if !strings.Contains(string(b), `"unreadable":true`) {
				t.Errorf("the row does not say the payload could not be read:\n%s", b)
			}
			if !strings.Contains(out, "into: unknown") {
				t.Errorf("the log does not say the receiver is unknown:\n%s", out)
			}
		})
	}
}

// A decode that fails on one field still read the others, so a payload that
// names its session and mistypes something else keeps its receiver — the flag
// is about the payload, not about the id.
func TestAPayloadThatNamesItsSessionKeepsTheReceiver(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "halfterm", []string{
		`{"type":"user","sessionId":"halfterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	// cwd is a number: the decode fails, after reading session_id.
	withHookStdin(t, `{"session_id":"ses_half","cwd":123}`)
	if out := captureStdout(t, func() { runHookContextPlain(t) }); !strings.Contains(out, "transaction mode") {
		t.Fatalf("the hook injected nothing:\n%q", out)
	}
	out, err := captureRun(t, "log", "5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "into: ses_half") {
		t.Errorf("a receiver the payload did name was thrown away:\n%s", out)
	}
	// The header is written by the other writer, and only it would show a
	// receiver dropped there.
	last, err := captureRun(t, "log", "--last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last, "into: ses_half") {
		t.Errorf("the header dropped a receiver the list kept:\n%s", last)
	}
}

// --last carries the same answer as the list: the header is where #2301 put
// the receiver, so it is where its absence has to be explained too.
func TestTheLastHeaderSaysTheReceiverIsUnknown(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "lastterm", []string{
		`{"type":"user","sessionId":"lastterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	withHookStdin(t, "not a payload at all")
	if out := captureStdout(t, func() { runHookContextPlain(t) }); !strings.Contains(out, "transaction mode") {
		t.Fatalf("the hook injected nothing:\n%q", out)
	}
	out, err := captureRun(t, "log", "--last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "into: unknown") {
		t.Errorf("the header does not explain the missing receiver:\n%s", out)
	}
}

// #2458 gave the plan hook the remedy, and only when the census had no command
// of its own — so the better the match, the less deja said. A plan about to run
// the command four sessions ran into a wall with was told the wall and the
// command, while the confirmed pair that got past it was dropped (#2485).
func TestThePlanFindingCarriesTheRemedyBesideTheCensusCommand(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	write := func(sid string, msgs [][2]any, base int) {
		lines := make([]string, 0, len(msgs))
		for i, m := range msgs {
			rec := map[string]any{"type": m[0], "sessionId": sid, "timestamp": at(base - i), "cwd": "/work/app",
				"message": map[string]any{"role": m[0], "content": m[1]}}
			b, jerr := json.Marshal(rec)
			if jerr != nil {
				t.Fatal(jerr)
			}
			lines = append(lines, string(b))
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wall := `psql: error: connection to the orders migration database failed: Connection refused`
	topics := []string{"the sidebar layout", "the invoice pdf renderer", "the webpack config", "the login throttle",
		"the avatar uploader", "the search autocomplete", "the cron scheduler", "the email templates",
		"the feature flags", "the toast component", "the graphql schema", "the retry budget",
		"the docker base image", "the css variables", "the storybook snapshots", "the i18n strings"}
	for i, top := range topics {
		write(fmt.Sprintf("f%d", i), [][2]any{
			{"user", "work on " + top + " today, it has been slow to load and the team wants it done"},
			{"assistant", "looked at " + top + ", changed what was needed and the tests pass"},
		}, 900-10*i)
	}
	for k := range 4 {
		write(fmt.Sprintf("m%d", k), [][2]any{
			{"user", fmt.Sprintf("run the orders migration (%d) against the production database", k)},
			{"assistant", []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "make migrate-orders"}}}},
			{"user", []any{map[string]any{"type": "tool_result", "is_error": true, "content": wall}}},
			{"assistant", "the orders migration needs the database reachable first"},
			{"assistant", []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "docker compose up -d db"}}}},
			{"user", []any{map[string]any{"type": "tool_result", "content": "db started"}}},
		}, 300-30*k)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	plan := "1. run the orders migration against the production database\n2. verify the row counts"
	findings := planFindings(dir, plan, "")
	if len(findings) != 1 {
		t.Fatalf("the fixture is meant to produce one finding, got %d: %v", len(findings), findings)
	}
	got := findings[0]
	if !strings.Contains(got, "make migrate-orders") {
		t.Errorf("the census command the plan matched is missing:\n  %s", got)
	}
	if !strings.Contains(got, "docker compose up -d db") {
		t.Errorf("deja knows what got past this wall and did not say it:\n  %s", got)
	}
}

// The plan hook speaks before an agent walks into a wall, which is the best
// moment deja has. It named the wall and stopped there, while `deja fix`
// answered the same error with the command that got past it — the gap 427
// closed for the environment block, still open on the surface that speaks
// first (#2458).
func TestAPlanFindingNamesTheRemedyDejaKnows(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	wall := "pgbouncer: error: connection refused while starting the migration runner pool"
	at := func(mins int) string {
		return time.Now().Add(-time.Duration(mins) * time.Minute).UTC().Format(time.RFC3339)
	}
	for k := range 4 {
		sid := fmt.Sprintf("pool%d", k)
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"switch the connection pool to pgbouncer for the migrations"}}`, sid, at(400-30*k)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"pgbouncer -d /etc/pgbouncer.ini"}}]}}`, sid, at(399-30*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, sid, at(398-30*k), wall),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew services start postgresql@16"}}]}}`, sid, at(397-30*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"==> Successfully started postgresql@16"}]}}`, sid, at(396-30*k)),
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Enough unrelated history that the plan's words identify something.
	for i := range 40 {
		sid := fmt.Sprintf("noise%d", i)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"unrelated question %d about the css build and postcss"}}`, sid, at(200-2*i), i)
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	plan := "1. Switch the connection pool to pgbouncer for the migration runner.\n2. Update the docs."
	findings := planFindings(dir, plan, "")
	if len(findings) == 0 {
		t.Fatalf("the plan walks into a wall four sessions hit and the hook said nothing")
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "pgbouncer") {
		t.Fatalf("the finding does not name the wall:\n%s", joined)
	}
	if !strings.Contains(joined, "brew services start postgresql@16") {
		t.Errorf("the finding names the wall and not the way past it, which deja knows:\n%s", joined)
	}
}

func TestExtractPlanSteps(t *testing.T) {
	tests := []struct {
		name string
		plan string
		want []string
	}{
		{
			name: "numbered",
			plan: "## Plan\n1. Update gateway_timeout handling\n2) Test reconnect_loop recovery",
			want: []string{"Update gateway_timeout handling", "Test reconnect_loop recovery"},
		},
		{
			name: "bulleted",
			plan: "- Inspect exporter_batch\n* Repair utc_midnight rollover\n+ Add regression coverage",
			want: []string{"Inspect exporter_batch", "Repair utc_midnight rollover", "Add regression coverage"},
		},
		{
			name: "mixed",
			plan: "Plan\n1. Inspect auth_cache\n- [ ] Update token_rotation\n2) Test Windows paths",
			want: []string{"Inspect auth_cache", "Update token_rotation", "Test Windows paths"},
		},
		{
			name: "fallback",
			plan: "## Inspect gateway_timeout\nRepair reconnect_loop",
			want: []string{"Inspect gateway_timeout", "Repair reconnect_loop"},
		},
		{
			name: "empty",
			plan: "\r\n  \n",
			want: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractPlanSteps(test.plan); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("steps = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPlanSearchStepsCapsTermsAndSteps(t *testing.T) {
	plan := strings.Join([]string{
		`1. Inspect C:\work\repo\auth.go gateway_timeout reconnect_loop token_rotation extra_identifier`,
		"2. Update exporter_batch utc_midnight rollover_guard",
		"3. Test websocket_reconnect heartbeat_timeout retry_budget",
		"4. Document deployment_pipeline release_checklist",
	}, "\n")
	got := planSearchSteps(plan)
	if len(got) != planStepLimit {
		t.Fatalf("steps = %d, want %d: %v", len(got), planStepLimit, got)
	}
	for i, terms := range got {
		if len(terms) < 2 || len(terms) > planTermLimit {
			t.Fatalf("step %d terms = %v", i, terms)
		}
	}
	// A Windows path is ordinary text to the tokenizer: backslash splits it,
	// and only the basename survives as a term. Path-aware matching is out of
	// scope here; this pins the actual behavior so nobody reads more into the
	// fixture above.
	joined := strings.Join(got[0], " ")
	if !strings.Contains(joined, "auth.go") || strings.Contains(joined, `\`) {
		t.Fatalf("step 0 terms = %v, want the path reduced to its basename token", got[0])
	}
}

func TestPlanWeakInputStopsBeforeTheIndex(t *testing.T) {
	oldReady := planIndexReady
	oldLookup := planFrictionLookup
	t.Cleanup(func() {
		planIndexReady = oldReady
		planFrictionLookup = oldLookup
	})

	readyCalls := 0
	lookupCalls := 0
	planIndexReady = func(string) bool {
		readyCalls++
		return true
	}
	planFrictionLookup = func(string, [][]string, func(index.SessionMeta) bool, int) []index.PlanCooccurrence {
		lookupCalls++
		return samplePlanMatches("local-project")
	}

	// "retry" alone is a real tech term but neither long enough nor
	// symbol-shaped to count as an identifier, so it still needs a second
	// term it doesn't have.
	for _, plan := range []string{"", "- ok", "1. retry"} {
		if got := planFindings(filepath.Join(t.TempDir(), "index.db"), plan, "session"); len(got) != 0 {
			t.Fatalf("plan %q produced %v", plan, got)
		}
	}
	if readyCalls != 0 || lookupCalls != 0 {
		t.Fatalf("weak plans touched the index: ready=%d lookup=%d", readyCalls, lookupCalls)
	}
}

// TestPlanSearchStepsIdentifierSingleTermAdmitted covers the same rule
// hook-prompt already applies to auto-recall (hasIdentifierTerm,
// cmd/deja/hook_prompt.go:319): a step naming something identifier-shaped is
// specific enough on one word, so it should not need a second term to be
// searched.
func TestPlanSearchStepsIdentifierSingleTermAdmitted(t *testing.T) {
	got := planSearchSteps("1. auth.go")
	if len(got) != 1 {
		t.Fatalf("steps = %v, want one admitted step", got)
	}
	if len(got[0]) != 1 || got[0][0] != "auth.go" {
		t.Fatalf("step terms = %v, want [\"auth.go\"]", got[0])
	}
}

// TestPlanSearchStepsNonIdentifierSingleTermDropped is the negative half of
// the case above: an ordinary single word carries no more signal here than
// it does for auto-recall, so the step is dropped rather than searched.
func TestPlanSearchStepsNonIdentifierSingleTermDropped(t *testing.T) {
	if got := planSearchSteps("1. Retry"); len(got) != 0 {
		t.Fatalf("steps = %v, want none admitted", got)
	}
}

func TestPlanPolicyDenyProducesNoOutput(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"auto":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)
	withPlanLookup(t, samplePlanMatches(`C:\work\private-repo`))

	got := planFindings(
		filepath.Join(t.TempDir(), "index.db"),
		"1. Run timeout around deploy_probe",
		"agent-session",
	)
	if len(got) != 0 {
		t.Fatalf("denied sessions produced findings: %v", got)
	}
}

func TestPlanFindingClaimIsNeutral(t *testing.T) {
	match := samplePlanMatches("local-project")[0]
	match.Wall.Text = "build conflicts with the cache and contradicts the manifest"
	match.Command = "echo already tried and was abandoned"

	got := strings.ToLower(formatPlanFinding(match))
	for _, forbidden := range []string{
		"conflicts with",
		"contradicts",
		"already tried",
		"was abandoned",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("finding contains forbidden claim %q: %q", forbidden, got)
		}
	}
	if !strings.HasPrefix(got, "wall hit in 3 sessions since ") {
		t.Fatalf("finding is not a neutral factual report: %q", got)
	}
	if strings.Contains(got, " should ") || strings.Contains(got, " must ") {
		t.Fatalf("finding commands the reader: %q", got)
	}
}

// TestPlanFindingClaimIsNeutralWallOnly is the wall-only counterpart to
// TestPlanFindingClaimIsNeutral: the forbidden-vocabulary and non-imperative
// checks apply to the wall clause on its own, since v1.5 lets a finding
// stand without a command clause at all.
func TestPlanFindingClaimIsNeutralWallOnly(t *testing.T) {
	match := samplePlanMatches("local-project")[0]
	match.Wall.Text = "build conflicts with the cache and contradicts the manifest"
	match.Command = ""
	match.CommandSessions = nil

	got := strings.ToLower(formatPlanFinding(match))
	for _, forbidden := range []string{
		"conflicts with",
		"contradicts",
		"already tried",
		"was abandoned",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("finding contains forbidden claim %q: %q", forbidden, got)
		}
	}
	if !strings.HasPrefix(got, "wall hit in 3 sessions since ") {
		t.Fatalf("finding is not a neutral factual report: %q", got)
	}
	if strings.Contains(got, "also ran") {
		t.Fatalf("wall-only finding unexpectedly carries a command clause: %q", got)
	}
	if strings.Contains(got, " should ") || strings.Contains(got, " must ") {
		t.Fatalf("finding commands the reader: %q", got)
	}
}

func TestPlanHookBudgetIncludesRecallFrame(t *testing.T) {
	huge := strings.Repeat("oversized_gateway_failure_ﾎｩ ", 400)
	findings := []planFinding{
		{line: "wall hit in 3 sessions: " + huge},
		{line: "wall hit in 4 sessions: " + huge},
	}
	budgeted := budgetPlanFindings(findings, planHookBudget-recallFrameOverhead)
	if len(budgeted) == 0 {
		t.Fatal("oversized finding was dropped rather than truncated")
	}
	lines := make([]string, 0, len(budgeted))
	for _, finding := range budgeted {
		lines = append(lines, finding.line)
	}
	payload := strings.Join(lines, "\n")
	framed := frameRecall(payload)
	if len(payload) > planHookBudget-recallFrameOverhead {
		t.Fatalf("payload = %d bytes, budget = %d", len(payload), planHookBudget-recallFrameOverhead)
	}
	if len(framed) > planHookBudget {
		t.Fatalf("framed payload = %d bytes, budget = %d", len(framed), planHookBudget)
	}
	if !strings.Contains(payload, "…") {
		t.Fatalf("oversized finding was not visibly truncated: %q", payload)
	}
}

func TestPlanHookSeenDedupeSuppressesSecondInvocation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	withPlanLookup(t, samplePlanMatches("local-project"))

	first := planFindings(dir, "1. Run timeout around deploy_probe", "agent-1")
	if len(first) != 1 {
		t.Fatalf("first findings = %v", first)
	}
	second := planFindings(dir, "1. Run timeout around deploy_probe", "agent-1")
	if len(second) != 0 {
		t.Fatalf("second invocation repeated findings: %v", second)
	}
	seen := alreadyInjected(dir, "agent-1")
	for _, id := range []string{"wall-a", "wall-b", "wall-c"} {
		if !seen[id] {
			t.Errorf("%s was not recorded in .hookseen: %v", id, seen)
		}
	}
}

func TestPlanHookEmitsEachWallOnce(t *testing.T) {
	match := samplePlanMatches("local-project")[0]
	withPlanLookup(t, []index.PlanCooccurrence{match, match})

	got := planFindings(
		filepath.Join(t.TempDir(), "index.db"),
		"1. Run timeout around deploy_probe",
		"",
	)
	if len(got) != 1 {
		t.Fatalf("duplicate wall emitted %d times: %v", len(got), got)
	}
}

func TestHookPlanJSONContract(t *testing.T) {
	t.Run("hit with unknown fields and Windows paths", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches(`C:\work\repo`))
		t.Setenv("CLAUDE_PROJECT_DIR", "")

		input := `{
			"hook_event_name":"PreToolUse",
			"tool_name":"ExitPlanMode",
			"tool_input":{
				"plan":"1. Run timeout around deploy_probe",
				"planFilePath":"C:\\work\\repo\\PLAN.md",
				"future_field":true
			},
			"session_id":"",
			"cwd":"C:\\work\\repo",
			"unknown_top_level":{"value":1}
		}`
		var out bytes.Buffer
		if err := runHookPlan(filepath.Join(t.TempDir(), "index.db"), strings.NewReader(input), &out); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatal("matching hook produced no output")
		}
		if strings.Contains(out.String(), "permissionDecision") {
			t.Fatalf("security-sensitive field appeared in output: %s", out.String())
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
			t.Fatalf("bad JSON %q: %v", out.String(), err)
		}
		if len(raw) != 1 {
			t.Fatalf("top-level shape = %v", raw)
		}
		var envelope struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		}
		if err := json.Unmarshal(raw["hookSpecificOutput"], &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.HookEventName != "PreToolUse" {
			t.Fatalf("event = %q", envelope.HookEventName)
		}
		if !strings.HasPrefix(envelope.AdditionalContext, recallFrameHeader) ||
			!strings.HasSuffix(envelope.AdditionalContext, recallFrameFooter) {
			t.Fatalf("context is not framed: %q", envelope.AdditionalContext)
		}
		if len(envelope.AdditionalContext) > planHookBudget {
			t.Fatalf("context = %d bytes, budget = %d", len(envelope.AdditionalContext), planHookBudget)
		}
	})

	t.Run("miss", func(t *testing.T) {
		withPlanLookup(t, nil)
		var out bytes.Buffer
		err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_name":"ExitPlanMode","tool_input":{"plan":"1. Run timeout around deploy_probe"}}`),
			&out,
		)
		if err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("miss produced %q", out.String())
		}
	})

	t.Run("other tool", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches("local-project"))
		var out bytes.Buffer
		err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_name":"Bash","tool_input":{"plan":"1. Run timeout around deploy_probe"}}`),
			&out,
		)
		if err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("other tool produced %q", out.String())
		}
	})

	t.Run("missing tool input", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches("local-project"))
		var out bytes.Buffer
		if err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_name":"ExitPlanMode","future_field":"accepted"}`),
			&out,
		); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("missing tool_input produced %q", out.String())
		}
	})

	t.Run("missing tool name", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches("local-project"))
		var out bytes.Buffer
		if err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_input":{"plan":"1. Run timeout around deploy_probe"}}`),
			&out,
		); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("input without tool_name produced %q", out.String())
		}
	})
}

func TestPlanFrictionIndexJoin(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := range 3 {
		id := fmt.Sprintf("plan-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run the deploy probe"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./deploy_probe --timeout 5 --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: timeout: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// The recurring wall appears in three sessions. Additional unrelated
	// sessions make its terms clear the same IDF floor used by auto recall.
	for i := range 27 {
		id := fmt.Sprintf("plan-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. Run timeout around deploy_probe")
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("manifest wall and command records did not join")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(strings.ToLower(got.Wall.Text), "timeout") {
		t.Fatalf("wall = %q", got.Wall.Text)
	}
	if !strings.Contains(got.Command, "deploy_probe") {
		t.Fatalf("command = %q", got.Command)
	}
}

// TestPlanFrictionIndexJoinSingleIdentifierTerm covers the join end to end
// for a plan step that names nothing but a single identifier. The command
// record here shares only that one term with the step (no second word like
// "timeout" to fall back on), so the join succeeds only because
// planCommandForStep's threshold drops to one for an identifier step.
func TestPlanFrictionIndexJoinSingleIdentifierTerm(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := range 3 {
		id := fmt.Sprintf("plan-single-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-single-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-single", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run the deploy probe"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./deploy_probe --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: deploy_probe: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// Same noise volume as TestPlanFrictionIndexJoin: deploy_probe appears in
	// exactly the three wall sessions, and this many unrelated sessions is
	// what clears the IDF floor at that document count.
	for i := range 27 {
		id := fmt.Sprintf("plan-single-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "noise-single", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. deploy_probe")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("single-identifier-term step did not join to a command")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(got.Command, "deploy_probe") {
		t.Fatalf("command = %q", got.Command)
	}
}

// TestPlanFrictionIndexJoinWallOnly is the v1.5 case: a census across real
// walls found a matching command logged alongside the wall in only 1 of 19
// carriers, so requiring one would reject nearly every recurrence. Here the
// wall text itself names the step's term but no carrier's command does —
// the join must still succeed, with Command left empty.
func TestPlanFrictionIndexJoinWallOnly(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := range 3 {
		id := fmt.Sprintf("plan-wallonly-%d", i)
		toolID := fmt.Sprintf("tool-plan-wallonly-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-wallonly", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run the gateway again"}}`,
				id, old,
			),
			// A command is logged, but it shares no term with the plan step —
			// this is the realistic majority shape, not the 1-in-19 case
			// where a command happens to name the wall too.
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"echo done"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: gateway_timeout: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// Same noise volume as the other join tests: clears the IDF floor for
	// gateway_timeout at this document count.
	for i := range 27 {
		id := fmt.Sprintf("plan-wallonly-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "noise-wallonly", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. gateway_timeout")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("wall-only recurrence did not produce a finding")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(strings.ToLower(got.Wall.Text), "gateway_timeout") {
		t.Fatalf("wall = %q", got.Wall.Text)
	}
	if got.Command != "" || len(got.CommandSessions) != 0 {
		t.Fatalf("expected no command strengthening, got command=%q sessions=%v", got.Command, got.CommandSessions)
	}

	line := formatPlanFinding(got)
	if line == "" {
		t.Fatal("wall-only match produced no finding line")
	}
	if strings.Contains(line, "also ran") {
		t.Fatalf("wall-only line unexpectedly carries a command clause: %q", line)
	}
	if !strings.Contains(line, "gateway_timeout") {
		t.Fatalf("finding does not name the wall: %q", line)
	}
}

// TestPlanFrictionIndexJoinIdentifierMidFloorFires is the v1.7 case: a
// two-tier floor, not a blanket exemption. A re-measurement against 2,886
// real sessions found identifier-shaped false-positive anchors (cannot,
// directory, branch, runner) topping out at idf 0.70 — the v1.6 full
// exemption let those through — while true anchors (hermes 1.09,
// hermes-agent 1.21) started at 1.09. Here "pipeline_probe" appears in 7 of
// 30 sessions: idf = ln(31/8) ≈ 1.35, above planIdentifierIDFFloor (1.0) but
// below the full dejaVuIDFFloor (2.0) a non-identifier term would need — the
// join fires only because the term is identifier-shaped and clears the
// lower bar.
func TestPlanFrictionIndexJoinIdentifierMidFloorFires(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := range 3 {
		id := fmt.Sprintf("plan-anchor-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-anchor-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run pipeline_probe again"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./pipeline_probe --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: pipeline_probe: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// 4 more sessions repeat the same word so it clears 7 of 30 total —
	// idf ≈ 1.35, inside the [1.0, 2.0) band only an identifier gets.
	for i := range 4 {
		id := fmt.Sprintf("plan-anchor-common-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-common", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"pipeline_probe discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	// The remaining 23 sessions carry neither term, bringing the corpus to
	// 30 and the document count to the 31 the idf figure above assumes.
	for i := range 23 {
		id := fmt.Sprintf("plan-anchor-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. pipeline_probe")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("identifier term inside the [1.0, 2.0) band did not join")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(strings.ToLower(got.Wall.Text), "pipeline_probe") {
		t.Fatalf("wall = %q", got.Wall.Text)
	}
}

// TestPlanFrictionIndexJoinIdentifierBelowFloorSilent is the negative
// control for the case above: the same identifier-shaped word, spread
// common enough to drop its idf below planIdentifierIDFFloor (1.0), does not
// join even though it is identifier-shaped — v1.7 narrows the exemption to a
// lower bar, it does not remove the bar.
func TestPlanFrictionIndexJoinIdentifierBelowFloorSilent(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := range 3 {
		id := fmt.Sprintf("plan-anchor-lo-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-anchor-lo-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-lo", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run pipeline_probe again"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./pipeline_probe --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: pipeline_probe: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// 17 more sessions repeat the same word so it clears 20 of 30 total —
	// idf = ln(31/21) ≈ 0.39, below planIdentifierIDFFloor (1.0).
	for i := range 17 {
		id := fmt.Sprintf("plan-anchor-lo-common-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-lo-common", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"pipeline_probe discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	for i := range 10 {
		id := fmt.Sprintf("plan-anchor-lo-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-lo-noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. pipeline_probe")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) != 0 {
		t.Fatalf("identifier term below planIdentifierIDFFloor should still be silent, got %v", matches)
	}
}

// TestPlanFrictionIndexJoinNonIdentifierStillNeedsTheFloor is the negative
// control for the case above: two ordinary, non-identifier words made just
// as common in the corpus do not get the same exemption, so the step yields
// no anchor terms and the join stays silent — v1.6 narrows the floor for
// identifier-shaped words specifically, it does not lift it in general.
func TestPlanFrictionIndexJoinNonIdentifierStillNeedsTheFloor(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := range 3 {
		id := fmt.Sprintf("plan-common-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-common-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-common", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"cache retry again"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"echo cache retry done"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: cache retry limit exceeded"}]}}`,
				id, old, toolID,
			),
		})
	}

	// Same 20-of-30 spread as the identifier case above, but with two plain
	// English words instead of an identifier.
	for i := range 17 {
		id := fmt.Sprintf("plan-common-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-common-noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"cache retry discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	for i := range 10 {
		id := fmt.Sprintf("plan-common-other-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-common-other", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. cache retry")
	if len(steps) != 1 || len(steps[0]) != 2 {
		t.Fatalf("steps = %v, want a single two-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) != 0 {
		t.Fatalf("non-identifier common terms should still fail the floor, got %v", matches)
	}
}

func samplePlanMatches(project string) []index.PlanCooccurrence {
	base := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	sessions := []index.SessionMeta{
		{ID: "wall-a", Harness: "claude", Project: project, Updated: base},
		{ID: "wall-b", Harness: "codex", Project: project, Updated: base.Add(24 * time.Hour)},
		{ID: "wall-c", Harness: "claude", Project: project, Updated: base.Add(48 * time.Hour)},
	}
	return []index.PlanCooccurrence{{
		Wall: index.Friction{
			Text:     "/bin/sh: timeout: command not found",
			Sessions: sessions,
			Last:     sessions[2].Updated,
		},
		Command:         `timeout 5 deploy_probe --config C:\work\repo\PLAN.md`,
		CommandSessions: sessions[:2],
	}}
}

func withPlanLookup(t *testing.T, result []index.PlanCooccurrence) {
	t.Helper()
	oldReady := planIndexReady
	oldLookup := planFrictionLookup
	planIndexReady = func(string) bool { return true }
	planFrictionLookup = func(
		_ string,
		_ [][]string,
		keep func(index.SessionMeta) bool,
		limit int,
	) []index.PlanCooccurrence {
		var out []index.PlanCooccurrence
		for _, match := range result {
			var wall []index.SessionMeta
			members := map[string]bool{}
			for _, meta := range match.Wall.Sessions {
				if keep(meta) {
					wall = append(wall, meta)
					members[planMetaKey(meta)] = true
				}
			}
			if len(wall) < index.FrictionMinSessions {
				continue
			}
			var commands []index.SessionMeta
			for _, meta := range match.CommandSessions {
				if keep(meta) && members[planMetaKey(meta)] {
					commands = append(commands, meta)
				}
			}
			if len(commands) == 0 {
				continue
			}
			match.Wall.Sessions = wall
			match.CommandSessions = commands
			out = append(out, match)
			if limit > 0 && len(out) == limit {
				break
			}
		}
		return out
	}
	t.Cleanup(func() {
		planIndexReady = oldReady
		planFrictionLookup = oldLookup
	})
}

// A rule that hides everything left session start completely silent, which is
// what a project with no history looks like. The count survives the empty
// digest so the line can say a rule decided it.
func TestHookSaysWhenThePolicyWithheldEverything(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the ticker window is 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	message := func(t *testing.T) string {
		t.Helper()
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			SystemMessage      string `json:"systemMessage"`
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q (%v)", out, err)
		}
		if strings.Contains(resp.HookSpecificOutput.AdditionalContext, "ticker window") {
			t.Fatalf("a withheld session was injected anyway: %q", resp.HookSpecificOutput.AdditionalContext)
		}
		return resp.SystemMessage
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":false,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	got := message(t)
	if !strings.Contains(got, "withheld 1 session") {
		t.Fatalf("session start does not say the rule withheld everything: %q", got)
	}
	if !strings.Contains(got, "nothing activates") {
		t.Fatalf("the line does not name the rule: %q", got)
	}
	// Repeating it every session start would be wallpaper.
	if again := message(t); strings.Contains(again, "withheld") {
		t.Fatalf("the same line was repeated on the next session start: %q", again)
	}
}

// The receipt named the policy whenever it was not the default, so the line
// read the same whether the rule had withheld memory from this very session or
// merely existed. `search` has reported the count since it had one.
func TestHookReceiptSaysWhenThePolicyWithheldSomething(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local: the ticker window is 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	message := func(t *testing.T) string {
		t.Helper()
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if strings.TrimSpace(out) == "" {
			return ""
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q (%v)", out, err)
		}
		return resp.SystemMessage
	}

	// The rule is set, but there is nothing imported for it to hide.
	got := message(t)
	if !strings.Contains(got, "policy: local-only") {
		t.Fatalf("the receipt does not name the policy: %q", got)
	}
	if strings.Contains(got, "withheld") {
		t.Errorf("a rule that hid nothing claimed it withheld something: %q", got)
	}

	// Now a session from another machine, in the same project, which the rule
	// does hide.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "proj", Role: "user", Text: "PEER: the ticker window should be 5s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(dir + ".receipt")
	for _, p := range mustGlob(t, dir+".hookcache-*") {
		_ = os.Remove(p)
	}

	got = message(t)
	if !strings.Contains(got, "withheld") {
		t.Errorf("the receipt does not say the rule withheld a session: %q", got)
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A compaction throws away what the session was shown; the seen list has to
// forget all of it. The session-start rows (start:<sid>) and the once mark
// (once:<sid>) outlived the compaction, so the digest the model had just lost
// was the one deja refused to send again (#3307). Kimi is where it shows most:
// those two are the only rows it ever writes.
func TestPrecompactForgetsTheStartRowsAndTheOnceMark(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seen := dir + ".hookseen"
	lines := []string{
		"start:sess-1 other-session 2026-09-08T08:13:38Z start:proj",
		"once:sess-1 once-digest",
		"sess-1 block-fingerprint",
		"start:sess-2 other-session 2026-09-08T08:13:39Z start:proj",
		"once:sess-2 once-digest",
		"sess-2 someone-elses-block",
	}
	if err := os.WriteFile(seen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetInjected(dir, "sess-1")
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, gone := range []string{"start:sess-1", "once:sess-1", "sess-1 block-fingerprint"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the compaction:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"start:sess-2", "once:sess-2", "sess-2 someone-elses-block"} {
		if !strings.Contains(got, kept) {
			t.Errorf("another session's row %q was dropped:\n%s", kept, got)
		}
	}
}

// A session id shaped like one of deja's own key prefixes forgets only its own
// rows: the prefixed forms would belong to another session (#3307 review).
func TestPrecompactDoesNotReachAcrossAPrefixedSessionID(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seen := dir + ".hookseen"
	lines := []string{
		"once:xyz once-digest",
		"once:once:xyz block",
		"xyz block-fingerprint",
	}
	if err := os.WriteFile(seen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetInjected(dir, "once:xyz")
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "once:xyz once-digest") {
		t.Errorf("the id's own row survived:\n%s", got)
	}
	for _, kept := range []string{"once:once:xyz block", "xyz block-fingerprint"} {
		if !strings.Contains(got, kept) {
			t.Errorf("a prefixed id reached across into %q:\n%s", kept, got)
		}
	}
}

// Compaction throws away the blocks a session was shown; the list that stops
// them repeating used to outlive them, so the memory the agent had just lost
// was exactly the memory recall refused to send again.
func TestPrecompactForgetsWhatThisSessionWasShown(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "compactterm", []string{
		`{"type":"user","sessionId":"compactterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	payload := `{"prompt":"do we need pgbouncer here","session_id":"agent-1"}`
	var first bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "transaction mode") {
		t.Fatalf("nothing was recalled to begin with:\n%q", first.String())
	}

	// Another session's entry, which compaction here must leave alone.
	f, err := os.OpenFile(index.DefaultDir()+".hookseen", os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("agent-2 someone-elses-block\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	withHookStdin(t, `{"session_id":"agent-1","hook_event_name":"PreCompact"}`)
	runHookPrecompact(index.DefaultDir())

	if got := alreadyInjected(index.DefaultDir(), "agent-1"); len(got) != 0 {
		t.Errorf("compaction left this session's seen list behind: %v", got)
	}
	if got := alreadyInjected(index.DefaultDir(), "agent-2"); !got["someone-elses-block"] {
		t.Error("compaction in one session cleared another session's entries")
	}

	var second bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &second, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "transaction mode") {
		t.Errorf("after compaction the memory was still withheld:\n%q", second.String())
	}
}

// A payload without a session id must not wipe the file: a harness that sends
// no id would otherwise clear every session's dedupe on its first compaction.
func TestPrecompactWithoutASessionIDKeepsTheList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.WriteFile(dir+".hookseen", []byte("agent-1 block-a\nagent-2 block-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetInjected(dir, "")
	for sid, block := range map[string]string{"agent-1": "block-a", "agent-2": "block-b"} {
		if !alreadyInjected(dir, sid)[block] {
			t.Errorf("%s lost %s to a compaction that named no session", sid, block)
		}
	}
}

// A compaction takes the session-start digest off the screen along with
// everything else, and the seen list is what stops deja sending it again. The
// per-prompt rows were forgotten and the two the session-start hook writes —
// `start:<sid>` and the once mark — were not, so the next start refused to
// send back exactly what the compaction had just lost. On Kimi, whose only
// rows are those two, nothing was forgotten at all (#3307).
func TestCompactionForgetsEverySeenKeyOfTheSession(t *testing.T) {
	dir := t.TempDir() + "/index"
	const sid = "session_3f1f"
	rememberInjectedIDs(dir, sid, "block-fingerprint")
	rememberInjectedIDsFor(dir, sessionStartKeyPrefix+sid, "proj", []string{"other-session"})
	rememberSessionDigest(dir, sid)
	// Another session's rows, which this must leave alone.
	rememberInjectedIDsFor(dir, sessionStartKeyPrefix+"session_other", "proj", []string{"kept-session"})
	rememberSessionDigest(dir, "session_other")

	before, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{sid + " block-fingerprint", sessionStartKeyPrefix + sid, onceDigestKey(sid)} {
		if !strings.Contains(string(before), want) {
			t.Fatalf("the fixture never wrote %q, so this measures nothing:\n%s", want, before)
		}
	}

	forgetInjected(dir, sid)

	after, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{sid + " block-fingerprint", sessionStartKeyPrefix + sid + " ", onceDigestKey(sid) + " "} {
		if strings.Contains(string(after), gone) {
			t.Errorf("%q survived the compaction:\n%s", gone, after)
		}
	}
	for _, kept := range []string{sessionStartKeyPrefix + "session_other", onceDigestKey("session_other")} {
		if !strings.Contains(string(after), kept) {
			t.Errorf("another session lost %q:\n%s", kept, after)
		}
	}
}

func withHookStdin(t *testing.T, payload string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
}

func TestHookContextCompactLead(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"compact survivor fact"}}`,
	})
	if err := run([]string{"index"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/p")

	withHookStdin(t, `{"source":"compact","session_id":"x"}`)
	out := captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), true) })
	if !strings.Contains(out, "Context was just compacted") {
		t.Fatalf("compact lead missing: %q", out)
	}
	if !strings.Contains(out, "✓ recalled from claude session") {
		t.Fatalf("provenance marker missing: %q", out)
	}

	withHookStdin(t, `{"source":"startup"}`)
	out = captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), true) })
	if strings.Contains(out, "Context was just compacted") || !strings.Contains(out, "recent history") {
		t.Fatalf("startup lead wrong: %q", out)
	}

	// Malformed stdin must not break the hook.
	withHookStdin(t, `{not json`)
	out = captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), true) })
	if out == "" {
		t.Fatal("hook produced nothing on malformed stdin")
	}
}

func TestRunHookPrecompactParsesAndGuards(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	defer func() { spawnWarmup = oldSpawn }()

	payload, jerr := json.Marshal(map[string]any{"session_id": "abc", "trigger": "auto"})
	if jerr != nil {
		t.Fatal(jerr)
	}
	withHookStdin(t, string(payload))
	runHookPrecompact(index.DefaultDir())
	if spawned != 1 {
		t.Fatalf("spawned=%d, want 1", spawned)
	}
	// Malformed stdin still triggers the guarded warmup path.
	withHookStdin(t, "garbage")
	runHookPrecompact(index.DefaultDir())
	if spawned != 1 {
		t.Fatalf("sentinel must suppress the second spawn, got %d", spawned)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// The payload says which project a hook call is about. deja used to write that
// answer into its own environment and read it back, and the write refused to
// overwrite what was there, so a second payload in the same process was
// answered with the first one's project — framed and injected exactly as a
// right answer would be (#2182; the write itself went in #2185).
//
// One process per invocation is the shape today, which is what made this a
// landmine rather than a fault.
func TestEachHookPayloadIsAnsweredForItsOwnProject(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "alpha", "one.jsonl"), "alpha1", []string{
		`{"type":"user","sessionId":"alpha1","timestamp":"` + old +
			`","message":{"role":"user","content":"the alpha work: pgbouncer runs in transaction mode"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "beta1", []string{
		`{"type":"user","sessionId":"beta1","timestamp":"` + old +
			`","message":{"role":"user","content":"the beta work: the kafka consumer keeps rebalancing"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	alpha := filepath.Join(base, "tmp", "alpha")
	beta := filepath.Join(base, "tmp", "beta")
	for _, d := range []string{alpha, beta} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Neither the working directory nor the environment names a project, so
	// the payload is the only thing that does.
	t.Chdir(base)
	t.Setenv("CLAUDE_PROJECT_DIR", "")

	answer := func(cwd string) string {
		t.Helper()
		withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "s", "cwd": cwd}))
		return captureStdout(t, func() { runHookContextPlain(t) })
	}
	// The premise: asked about alpha first, it answers about alpha.
	first := answer(alpha)
	if !strings.Contains(first, "transaction mode") {
		t.Fatalf("the first call did not carry alpha's memory, so this measures nothing:\n%s", first)
	}
	// The one that mattered: a second session, another project, same process.
	second := answer(beta)
	if strings.Contains(second, "transaction mode") {
		t.Errorf("a session in beta was handed alpha's memory:\n%s", second)
	}
	if !strings.Contains(second, "rebalancing") {
		t.Errorf("a session in beta was not handed beta's memory:\n%s", second)
	}
	// And back, so the answer follows the payload rather than latching onto
	// whichever project came last.
	third := answer(alpha)
	if !strings.Contains(third, "transaction mode") || strings.Contains(third, "rebalancing") {
		t.Errorf("the third call, about alpha again, was not answered about alpha:\n%s", third)
	}
}

// The doors are many and the fault was one line each: read the project out of
// the process environment rather than out of the payload that named it. This
// keeps that line from coming back anywhere in the hook files — the two places
// that may touch the export are the helper that reads the chain and the one
// that sets it, plus antigravity's guard on whether to set it at all.
func TestNoHookDoorReadsTheProjectOutOfTheEnvironment(t *testing.T) {
	files, err := filepath.Glob("hook_*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("found %d files, so this measures nothing", len(files))
	}
	allowed := map[string]bool{
		// hookCWD is the chain itself: payload, then whatever the host
		// exported, then where the process stands. It is the only place that
		// may read it.
		"hook_context.go": true,
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[filepath.Base(f)] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `os.Getenv("CLAUDE_PROJECT_DIR")`) {
			t.Errorf("%s reads the project out of the environment: the payload names it, and the export "+
				"is written once per process and will not change (#2182)", f)
		}
	}
}

// A session start in /work/api recalled a client's acme/api under "sessions
// from this project": the candidate list carries the checkout's bare directory
// name and RecentProjects matched it as a substring (#2333).
func TestSessionStartStaysInsideItsOwnProject(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write := func(dirName, id, cwd, text string, hoursAgo int) {
		dir := filepath.Join(root, dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":%q}}`, id, at, cwd, text)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("-work-api", "mine", "/work/api", "my own retry budget question", 8)
	write("-clients-acme-api", "acme", "/clients/acme/api", "the acme ledger cutover", 2)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := hookContextFor(t, dir, `{"hook_event_name":"SessionStart","cwd":"/work/api","session_id":"agent1"}`)
	if !strings.Contains(out, "my own retry budget question") {
		t.Fatalf("the checkout's own session is missing, so this measures nothing:\n%s", out)
	}
	if strings.Contains(out, "acme") {
		t.Errorf("a session from another project ending in /api was injected as this project's:\n%s", out)
	}
}

// hookContextFor runs the session-start hook over one payload and returns what
// it wrote.
func hookContextFor(t *testing.T, dir, payload string) string {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = inW.WriteString(payload)
		_ = inW.Close()
	}()
	oldIn, oldOut := os.Stdin, os.Stdout
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin, os.Stdout = inR, outW
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := outR.Read(buf)
			b.Write(buf[:n])
			if rerr != nil {
				break
			}
		}
		done <- b.String()
	}()
	hookErr := runHookContext(dir, false)
	os.Stdin, os.Stdout = oldIn, oldOut
	_ = outW.Close()
	out := <-done
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	return out
}

// Nobody asked. A harness delivers its own plumbing as the next user turn — a
// finished background task, a system reminder — and this hook fired on it like
// a question. The terms were the envelope's field names plus a task id that
// exists nowhere else, and what they matched was another notification in
// another session: noise injected as recalled history, under a line telling the
// user that deja fires on noise (#3156).
func TestHookPromptStandsDownOnHarnessArtifacts(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	// A past session holding a notification of its own — what the noisy prompt
	// matched — and a real answer beside it, so silence here is a decision and
	// not an empty store.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "past.jsonl"), "past", []string{
		`{"type":"user","sessionId":"past","timestamp":"` + ts +
			`","message":{"role":"user","content":"<task-notification>\n<task-id>zx91qq4kb</task-id>\n<status>failed</status>\n</task-notification>"}}`,
		`{"type":"assistant","sessionId":"past","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"the zibblex retry cap is four, we settled that"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	for _, artifact := range []string{
		`<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n</task-notification>`,
		`<system-reminder>The task list is empty.</system-reminder>`,
		`Summary: 1. Primary Request and Intent:\n   keep polishing the tool\n2. Key Technical Concepts:\n   - hooks`,
	} {
		var out bytes.Buffer
		in := strings.NewReader(`{"prompt":"` + artifact + `","session_id":"asking"}`)
		if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "deja-recall") || strings.Contains(out.String(), "deja-vu — ") {
			t.Errorf("the hook recalled on a harness artifact:\nprompt: %s\ngot: %q", artifact, out.String())
		}
	}

	// The control: a real question in the same session, against the same
	// store, still gets its block.
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about the zibblex retry cap","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "retry cap is four") {
		t.Fatalf("a real question went unanswered, so the silence above proves nothing:\n%q", out.String())
	}
}

// A turn that is only the host's envelope — a task notification delivered as
// a user prompt — gets nothing: no block, no "you have been here" line. Seen
// live on 2026-09-08 with the envelope's field names as the identifying terms
// (#3156). The same session, asked a real question, still gets its block.
func TestHookPromptStandsDownOnTheHostsOwnTurn(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "notice.jsonl"), "notice", []string{
		`{"type":"user","sessionId":"notice","timestamp":"` + old + `","message":{"role":"user","content":"<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n</task-notification>"}}`,
		`{"type":"assistant","sessionId":"notice","timestamp":"` + old + `","message":{"role":"assistant","content":"That is the earlier background task-id run; it failed on the task-notification path."}}`,
		`{"type":"user","sessionId":"notice","timestamp":"` + old + `","message":{"role":"user","content":"the exporter_batch job drops rows at utc_midnight"}}`,
		`{"type":"assistant","sessionId":"notice","timestamp":"` + old + `","message":{"role":"assistant","content":"utc_midnight rollover: the exporter_batch cursor was compared in local time."}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	ask := func(prompt string) string {
		var out bytes.Buffer
		in := `{"prompt":` + jsonString(prompt) + `,"session_id":"agent-9"}`
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := ask("<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n<summary>Background command failed</summary>\n</task-notification>"); strings.TrimSpace(got) != "" {
		t.Fatalf("the host's own turn got a block:\n%s", got)
	}
	// A person's words with the host's reminder appended still count as the
	// person's words — and the reminder's words are not the query.
	got := ask("exporter_batch dropping rows at utc_midnight again\n<system-reminder>\nTERSE MODE ACTIVE\n</system-reminder>")
	if !strings.Contains(got, "utc_midnight") {
		t.Fatalf("a real question with a reminder appended got nothing:\n%s", got)
	}
	if strings.Contains(got, "TERSE") {
		t.Fatalf("the reminder's words reached the block:\n%s", got)
	}
}

// A `/loop 1m …` sends the same text every minute, and the per-session
// cooldowns are counted per session shown — so an identical prompt walked one
// session further down the ranking on every tick: 25 blocks from 21 different
// sessions in one evening, none of them the loop's subject (#3189).
func TestTheSameQuestionIsAnsweredOnce(t *testing.T) {
	dir := t.TempDir() + "/index"
	terms := []string{"zebraquux", "fetcher", "timeout"}
	key := askKey(terms)

	if askedRecently(dir, "sess-1", key, repeatAskWindow) {
		t.Fatal("nothing has been asked yet")
	}
	rememberInjectedIDsFor(dir, "sess-1", "", []string{key})
	if !askedRecently(dir, "sess-1", key, repeatAskWindow) {
		t.Error("the same question from the same reader is answered again")
	}
	// The order of the terms is the ranking's business, not the reader's.
	if !askedRecently(dir, "sess-1", askKey([]string{"timeout", "zebraquux", "fetcher"}), repeatAskWindow) {
		t.Error("the same terms in another order read as another question")
	}
	// Another question, and another reader, are not this one.
	if askedRecently(dir, "sess-1", askKey([]string{"quokkabloom", "retry"}), repeatAskWindow) {
		t.Error("a different question was taken for the one just answered")
	}
	if askedRecently(dir, "sess-2", key, repeatAskWindow) {
		t.Error("one reader's answer silenced another reader")
	}
	// The window is what ends the silence: the same subject a day later is a
	// question again.
	if askedRecently(dir, "sess-1", key, time.Nanosecond) {
		t.Error("the window is not honoured")
	}
}

// The row has to carry a time, or the window cannot end.
func TestTheAnsweredQuestionIsStamped(t *testing.T) {
	dir := t.TempDir() + "/index"
	rememberInjectedIDsFor(dir, "sess-1", "", []string{askKey([]string{"zebraquux"})})
	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(strings.TrimSpace(string(b)))
	if len(parts) < 3 {
		t.Fatalf("row has no timestamp: %q", b)
	}
	if _, err := time.Parse(time.RFC3339, parts[2]); err != nil {
		t.Errorf("stamp %q does not parse: %v", parts[2], err)
	}
	if !strings.HasPrefix(parts[1], "ask:") {
		t.Errorf("the question key %q could be taken for a session id", parts[1])
	}
	// Wide enough that two questions do not share a key: a narrow one silences
	// a question nobody asked, which is the failure the reader cannot see.
	if got := len(strings.TrimPrefix(parts[1], "ask:")); got != 16 {
		t.Errorf("question key is %d hex digits, want 16", got)
	}
}

// Compaction takes the block off the screen, so the question may be answered
// again — the same rule the block fingerprint follows.
func TestCompactionClearsTheAnsweredQuestion(t *testing.T) {
	dir := t.TempDir() + "/index"
	key := askKey([]string{"zebraquux", "fetcher"})
	rememberInjectedIDsFor(dir, "sess-1", "", []string{key})
	forgetInjected(dir, "sess-1")
	if askedRecently(dir, "sess-1", key, repeatAskWindow) {
		t.Error("the question stayed answered after the answer was compacted away")
	}
}

// A CJK prompt is one field to FieldsFunc (no spaces) and techTerm rejects
// every rune above 127, so prompt.Terms returned nothing at all and
// runHookPrompt bailed at the "fewer than two terms" guard: auto-recall could
// never fire for a Chinese, Japanese or Korean user.
func TestPromptSearchTermsCJK(t *testing.T) {
	cases := []struct {
		prompt  string
		wantAny []string
	}{
		{"韓國簽證點樣申請", []string{"韓國", "簽證"}},
		{"刷新令牌怎么实现", []string{"刷新", "令牌"}},
		{"パスワードの再設定", []string{"パス"}},
		{"полное совпадение по-русски", nil}, // Cyrillic path unchanged
	}
	for _, c := range cases {
		got := prompt.Terms(c.prompt)
		if c.wantAny == nil {
			continue
		}
		if len(got) < 2 {
			t.Errorf("%q: got %v, want at least two terms so the hook can fire", c.prompt, got)
			continue
		}
		for _, want := range c.wantAny {
			var found bool
			for _, g := range got {
				if g == want {
					found = true
				}
			}
			if !found {
				t.Errorf("%q: term %q missing from %v", c.prompt, want, got)
			}
		}
	}
}

// Grammar must not become a term: 什么 / 在哪 style pairs are the CJK
// counterpart of a stop word, and RelevanceTerms already drops them, so the
// hook inherits that filtering for free.
func TestPromptSearchTermsCJKDropsGrammar(t *testing.T) {
	got := prompt.Terms("我为什么不用 postgres 的索引")
	for _, junk := range []string{"什么", "在哪", "怎么"} {
		for _, g := range got {
			if g == junk {
				t.Errorf("grammar bigram %q became a search term: %v", junk, got)
			}
		}
	}
	var hasIndex bool
	for _, g := range got {
		if g == "索引" {
			hasIndex = true
		}
	}
	if !hasIndex {
		t.Errorf("content bigram 索引 missing from %v", got)
	}
}

// An ASCII prompt must behave exactly as before: the CJK branch is skipped.
func TestPromptSearchTermsASCIIUnchanged(t *testing.T) {
	got := prompt.Terms("fix the authentication middleware timeout")
	for _, g := range got {
		if strings.ContainsFunc(g, func(r rune) bool { return r > 0x2E80 }) {
			t.Errorf("ASCII prompt produced a CJK term: %v", got)
		}
	}
	if len(got) == 0 {
		t.Errorf("ASCII prompt lost its terms: %v", got)
	}
}

// Cursor's beforeSubmitPrompt names the conversation conversation_id and sends
// no session_id. Read as no session at all, the cooldown never recorded and
// the same block went out on every prompt of the conversation (#3287).
func TestCursorConversationIDIsTheSession(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "repeatterm", []string{
		`{"type":"user","sessionId":"repeatterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	ask := func(conversation string) string {
		t.Helper()
		var out bytes.Buffer
		in := strings.NewReader(`{"conversation_id":"` + conversation + `","prompt":"do we need pgbouncer here","workspace_roots":["` + jsonEscaped(t, cwd) + `"]}`)
		if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if first := ask("conv-x"); !strings.Contains(first, "transaction mode") {
		t.Fatalf("the first prompt got no memory, so there is nothing to repeat:\n%q", first)
	}
	if again := ask("conv-x"); strings.Contains(again, "transaction mode") {
		t.Errorf("the same conversation was handed the same session again:\n%q", again)
	}
}

// Narrowing a candidate walks every message it holds, and the session being
// written is the longest one on a real store — measured there, doing it before
// the two cheap discards cost 45 ms of the hook's 181. The output is the same
// either way, so only a count can hold the order in place.
func TestHookPromptDoesNotNarrowASessionItWillDiscard(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)

	// Long enough to be narrowed, and it is the session asking.
	lines := make([]string, 0, dejaVuMaxMessages+2)
	for i := range dejaVuMaxMessages + 1 {
		lines = append(lines, fmt.Sprintf(
			`{"type":"assistant","sessionId":"selflong","timestamp":%q,"message":{"role":"assistant","content":"kestrel notes %d"}}`,
			ts, i))
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "selflong.jsonl"), "selflong", lines)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	focusCalls.Store(0)
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel","session_id":"selflong"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "kestrel notes") {
		t.Fatalf("the session being written was recalled to itself:\n%q", out.String())
	}
	if focusCalls.Load() != 0 {
		t.Errorf("narrowed %d session(s) that were then discarded — the cheap checks must come first", focusCalls.Load())
	}
}

// A Grok subagent spawned with the parent's context is kind "subagent_fork".
// Recall left it out of its parent's lineage, but the start digest still
// listed it as a session of its own.
func TestStartDigestLeavesOutEverySpawnedKind(t *testing.T) {
	in := []model.Session{
		{ID: "p", Harness: "grok"},
		{ID: "a", Harness: "grok", Kind: "subagent_fork", Parent: "p"},
		{ID: "b", Harness: "grok", Kind: "subagent", Parent: "p"},
		{ID: "c", Harness: "claude", Kind: "sidechain", Parent: "x"},
		{ID: "f", Harness: "grok", Kind: "fork", Parent: "p"},
	}
	got := withoutSubagentRuns(in)
	if len(got) != 2 || got[0].ID != "p" || got[1].ID != "f" {
		t.Fatalf("kept %+v, want the parent and the fork only", got)
	}
}
