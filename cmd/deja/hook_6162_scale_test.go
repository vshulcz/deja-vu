package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func joinedMessagesText(msgs []model.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestHookPromptLargeRolloutFastWithAndWithoutTranscriptPath(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	t.Setenv("CODEX_HOME", root)
	t.Setenv("DEJA_CODEX_ROOT", root)
	// Put a seed rollout under Day 0 and active 24 MiB rollout under newest day.
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	parentID := "019d6162-pare-4000-8000-000000000000"
	activeID := "019d6162-live-4000-8000-000000000001"
	start := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	seq := 0
	for day := range 200 {
		dt := start.AddDate(0, 0, day)
		dayDir := filepath.Join(root, "sessions", dt.Format("2006"), dt.Format("01"), dt.Format("02"))
		if err := os.MkdirAll(dayDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range 5 {
			seq++
			uid := fmt.Sprintf("019d6162-0000-4000-8000-%012x", seq)
			name := fmt.Sprintf("rollout-%s-%02d-%s.jsonl", dt.Format("2006-01-02T15"), i, uid)
			var content string
			if day == 0 && i == 0 {
				uid = parentID
				name = fmt.Sprintf("rollout-%s-%02d-%s.jsonl", dt.Format("2006-01-02T15"), i, uid)
				content = fmt.Sprintf(
					`{"timestamp":"2026-04-01T10:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":%q,"timestamp":"2026-04-01T10:00:00Z"}}`+"\n"+
						`{"timestamp":"2026-04-01T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"verify rollout parser for repo"}]}}`+"\n"+
						`{"timestamp":"2026-04-01T10:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checked"}]}}`+"\n",
					uid, repo,
				)
			} else {
				content = "{}\n"
			}
			if err := os.WriteFile(filepath.Join(dayDir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	newest := start.AddDate(0, 0, 200)
	activeDir := filepath.Join(root, "sessions", newest.Format("2006"), newest.Format("01"), newest.Format("02"))
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(activeDir, "rollout-2026-10-18T18-00-00-"+activeID+".jsonl")
	f, err := os.Create(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	// Line 1: 96 KiB session_meta (> 64 KiB header buffer, < 1 MiB limit), with forked_from_id.
	metaPad := strings.Repeat("h", 96<<10)
	metaLine := fmt.Sprintf(
		`{"timestamp":"2026-10-18T18:00:00Z","type":"session_meta","payload":{"id":%q,"forked_from_id":%q,"cwd":%q,"timestamp":"2026-10-18T18:00:00Z","base_instructions":{"text":%q}}}`+"\n",
		activeID, parentID, repo, metaPad,
	)
	if _, err := f.WriteString(metaLine); err != nil {
		t.Fatal(err)
	}
	padLine := fmt.Sprintf(
		`{"timestamp":"2026-10-18T18:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pad %s"}]}}`+"\n",
		strings.Repeat("p", 4096),
	)
	written := int64(len(metaLine))
	for written < 22<<20 {
		n, err := f.WriteString(padLine)
		if err != nil {
			t.Fatal(err)
		}
		written += int64(n)
	}
	// Old rule placed ~2 MiB before EOF: inside 4 MiB CompactionTailBytes, outside 256 KiB LiveTranscriptTailBytes.
	oldRule := `{"timestamp":"2026-10-18T18:10:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Always keep legacy-alpha-flag disabled."}]}}` + "\n"
	if _, err := f.WriteString(oldRule); err != nil {
		t.Fatal(err)
	}
	for tailWritten := int64(0); tailWritten < 2<<20; {
		n, err := f.WriteString(padLine)
		if err != nil {
			t.Fatal(err)
		}
		tailWritten += int64(n)
	}
	recentUser := `{"timestamp":"2026-10-18T18:59:58Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Always pass --bounded-live-tail when running verification."}]}}` + "\n"
	recentAsst := `{"timestamp":"2026-10-18T18:59:59Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Running verification with --bounded-live-tail."}]}}` + "\n"
	if _, err := f.WriteString(recentUser + recentAsst); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	head, ok := sources.CodexSessionHead(activeID)
	if !ok || head.ID != activeID || head.Parent != parentID || head.Kind != "fork" {
		t.Fatalf("CodexSessionHead(%q) = (%+v, %v), want ID=%q Parent=%q Kind=fork", activeID, head, ok, activeID, parentID)
	}

	msgsWithPath, ok := sources.ReadLiveTranscriptTailMessages("codex", rolloutPath, activeID)
	if !ok {
		t.Fatal("ReadLiveTranscriptTailMessages with transcriptPath returned ok=false")
	}
	txtWithPath := joinedMessagesText(msgsWithPath)
	if !strings.Contains(txtWithPath, "--bounded-live-tail") {
		t.Fatalf("tail messages missing live rule:\n%s", txtWithPath)
	}
	if strings.Contains(txtWithPath, "legacy-alpha-flag") {
		t.Fatalf("tail messages leaked old 2 MiB rule outside 256 KiB live tail:\n%s", txtWithPath)
	}

	msgsByID, ok := sources.ReadLiveTranscriptTailMessages("codex", "", activeID)
	if !ok {
		t.Fatal("ReadLiveTranscriptTailMessages without transcriptPath returned ok=false")
	}
	txtByID := joinedMessagesText(msgsByID)
	if !strings.Contains(txtByID, "--bounded-live-tail") || strings.Contains(txtByID, "legacy-alpha-flag") {
		t.Fatalf("by-ID tail messages mismatch:\n%s", txtByID)
	}

	// Index the newly written 24 MiB rollout so indexedTranscriptPath resolves it
	// without scanning the store on the prompt hook path (#6162, #6163).
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	ws := compactionWorkspace(repo)
	for _, tc := range []struct {
		name           string
		sessionID      string
		transcriptPath string
		wantRule       bool
	}{
		{name: "with_transcript_path", sessionID: activeID, transcriptPath: rolloutPath, wantRule: true},
		{name: "indexed_without_transcript_path", sessionID: activeID, transcriptPath: "", wantRule: true},
		{name: "unknown_session_miss", sessionID: "019d6162-miss-4000-8000-000000000999", transcriptPath: "", wantRule: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := promptHookInput{
				Prompt:         "verify rollout parser for repo",
				SessionID:      tc.sessionID,
				TranscriptPath: tc.transcriptPath,
				CWD:            repo,
				Harness:        "codex",
			}
			liveMsgs := readLiveHookTranscriptMessages(dir, in, repo, ws)
			liveTxt := joinedMessagesText(liveMsgs)
			if tc.wantRule {
				if !strings.Contains(liveTxt, "--bounded-live-tail") {
					t.Fatalf("readLiveHookTranscriptMessages missing --bounded-live-tail:\n%s", liveTxt)
				}
				if strings.Contains(liveTxt, "legacy-alpha-flag") {
					t.Fatalf("readLiveHookTranscriptMessages read 4 MiB/24 MiB instead of 256 KiB tail:\n%s", liveTxt)
				}
			}

			payload, err := json.Marshal(map[string]any{
				"prompt":          "verify rollout parser for repo",
				"session_id":      tc.sessionID,
				"transcript_path": tc.transcriptPath,
				"cwd":             repo,
				"harness":         "codex",
			})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			startRun := time.Now()
			if err := runHookPrompt(dir, bytes.NewReader(payload), &out); err != nil {
				t.Fatalf("runHookPrompt error = %v", err)
			}
			if elapsed := time.Since(startRun); elapsed > 2*time.Second {
				t.Fatalf("runHookPrompt(%s) took %v, expected bounded fast path", tc.name, elapsed)
			}
		})
	}
}

func TestAskerLineageCutForHarnessStrictPerHarnessIsolation(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed Claude store so index.Ensure succeeds and creates dir.
	claudeProj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(claudeProj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeProj, "seed.jsonl"),
		[]byte(`{"type":"user","message":{"role":"user","content":"seed"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"seed-1","cwd":`+fmt.Sprintf("%q", repo)+`}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	sharedID := "shared-session-6162"
	grokParentID := "grok-parent-6162"
	clineParentID := "cline-parent-6162"
	claudeMarkParentID := "claude-mark-parent-6162"

	// 1. Seed decoy Grok fork for sharedID.
	grokDir := filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", grokDir)
	grokSessDir := filepath.Join(grokDir, "sessions", "work", sharedID)
	if err := os.MkdirAll(grokSessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokSessDir, "updates.jsonl"),
		[]byte(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"subtask"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokSessDir, "summary.json"),
		[]byte(fmt.Sprintf(`{"info":{"id":%q,"cwd":%q},"session_kind":"headless","parent_session_id":%q,"forked_at":"2026-10-18T10:05:00Z"}`, sharedID, repo, grokParentID)), 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. Seed decoy Cline fork for sharedID.
	clineRoot := filepath.Join(tmp, "cline-sessions")
	t.Setenv("DEJA_CLINE_ROOT", clineRoot)
	clineSessDir := filepath.Join(clineRoot, sharedID)
	if err := os.MkdirAll(clineSessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clineSessDir, sharedID+".json"),
		[]byte(fmt.Sprintf(`{"session_id":%q,"cwd":%q,"metadata":{"fork":{"forkedFromSessionId":%q}}}`, sharedID, repo, clineParentID)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clineSessDir, sharedID+".messages.json"),
		[]byte(`{"agent":"lead","messages":[{"role":"user","content":"forked","ts":1760781900000}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// 3. Seed decoy Claude fork mark for sharedID.
	forkMarkLine := fmt.Sprintf("%s %s %d\n", sharedID, claudeMarkParentID, time.Now().Unix())
	if err := os.WriteFile(dir+".forks", []byte(forkMarkLine), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Seed Codex forked rollout in isolated CODEX_HOME for codexChildID.
	codexRoot := filepath.Join(tmp, "codex")
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	codexParentID := "019d6162-papa-4000-8000-000000000010"
	codexChildID := "019d6162-kid0-4000-8000-000000000011"
	codexDir := filepath.Join(codexRoot, "sessions", "2026", "10", "18")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexBody := fmt.Sprintf(
		`{"timestamp":"2026-10-18T11:00:00Z","type":"session_meta","payload":{"id":%q,"forked_from_id":%q,"cwd":%q,"timestamp":"2026-10-18T11:00:00Z"}}`+"\n"+
			`{"timestamp":"2026-10-18T11:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"codex fork start"}]}}`+"\n",
		codexChildID, codexParentID, repo,
	)
	if err := os.WriteFile(filepath.Join(codexDir, "rollout-2026-10-18T11-00-00-"+codexChildID+".jsonl"), []byte(codexBody), 0o600); err != nil {
		t.Fatal(err)
	}

	// Unrelated explicit harnesses must never touch Grok/Cline/Claude fork stores.
	for _, h := range []string{"gemini", "cursor", "qwen", "deepseek", "codex", "antigravity", "goose"} {
		if head, ok := askerHeadForHarness(sharedID, "", h); ok {
			t.Errorf("askerHeadForHarness(%q, %q) leaked %+v", sharedID, h, head)
		}
		set, _ := askerLineageCutForHarness(dir, sharedID, "", h)
		for _, forbidden := range []string{grokParentID, clineParentID, claudeMarkParentID} {
			if set[forbidden] {
				t.Errorf("askerLineageCutForHarness(%q) leaked unrelated parent %q: %v", h, forbidden, set)
			}
		}
	}

	// Matching explicit harnesses resolve only their own lineage store.
	if head, ok := askerHeadForHarness(sharedID, "", "claude"); ok {
		t.Errorf("askerHeadForHarness(%q, \"claude\") leaked non-Claude head %+v", sharedID, head)
	}
	claudeSet, _ := askerLineageCutForHarness(dir, sharedID, "", "claude")
	if !claudeSet[claudeMarkParentID] || claudeSet[grokParentID] || claudeSet[clineParentID] {
		t.Fatalf("claude lineage set = %v, want only %q", claudeSet, claudeMarkParentID)
	}

	clineSet, _ := askerLineageCutForHarness(dir, sharedID, "", "cline")
	if !clineSet[clineParentID] || clineSet[grokParentID] || clineSet[claudeMarkParentID] {
		t.Fatalf("cline lineage set = %v, want only %q", clineSet, clineParentID)
	}

	grokSet, _ := askerLineageCutForHarness(dir, sharedID, "", "grok")
	if !grokSet[grokParentID] || grokSet[clineParentID] || grokSet[claudeMarkParentID] {
		t.Fatalf("grok lineage set = %v, want only %q", grokSet, grokParentID)
	}

	// Negative cache entry for "gemini" must not poison subsequent "codex" lookup.
	if head, ok := askerHeadForHarness(codexChildID, "", "gemini"); ok {
		t.Fatalf("askerHeadForHarness(%q, gemini) unexpectedly returned %+v", codexChildID, head)
	}
	codexHead, ok := askerHeadForHarness(codexChildID, "", "codex")
	if !ok || codexHead.Parent != codexParentID {
		t.Fatalf("askerHeadForHarness(%q, codex) = (%+v, %v), want Parent=%q", codexChildID, codexHead, ok, codexParentID)
	}
	codexSet, _ := askerLineageCutForHarness(dir, codexChildID, "", "codex")
	if !codexSet[codexParentID] {
		t.Fatalf("askerLineageCutForHarness(codex) = %v, want %q", codexSet, codexParentID)
	}

	// Hook callers forward Harness: tool hook & coord must isolate per harness.
	var toolIn toolHookInput
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"session_id":%q,"harness":"gemini"}`, sharedID)), &toolIn); err != nil {
		t.Fatal(err)
	}
	skipFn := askerSkip(dir, toolIn)
	if skipFn != nil && skipFn(index.SessionMeta{ID: grokParentID}, "a.go") {
		t.Fatalf("askerSkip with harness=gemini leaked Grok parent %q", grokParentID)
	}
	var cached map[string]bool
	if coordSameLineage(dir, sharedID, "", "", "gemini", grokParentID, coordState{}, &cached) {
		t.Fatalf("coordSameLineage with harness=gemini leaked Grok parent %q", grokParentID)
	}
}
