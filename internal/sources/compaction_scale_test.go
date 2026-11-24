package sources

import (
	"encoding/base64"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestCompactionCandidatesByIDScaleBoundedOnMultiYearCodexStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "codex")
	t.Setenv("DEJA_CODEX_ROOT", root)

	// Build a multi-year store: 400 day directories × 10 rollouts = 4,000 files.
	start := time.Date(2025, 10, 19, 10, 0, 0, 0, time.UTC)
	seq := 0
	for day := range 400 {
		dt := start.AddDate(0, 0, day)
		dir := filepath.Join(root, "sessions", dt.Format("2006"), dt.Format("01"), dt.Format("02"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range 10 {
			seq++
			uid := fmt.Sprintf("00000000-0000-4000-8000-%012x", seq)
			name := fmt.Sprintf("rollout-%s-%02d-%s.jsonl", dt.Format("2006-01-02T15"), i, uid)
			if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	newestDay := start.AddDate(0, 0, 399)
	activeID := "019d7777-active-4000-8000-000000000001"
	activeDir := filepath.Join(root, "sessions", newestDay.Format("2006"), newestDay.Format("01"), newestDay.Format("02"))
	activePath := filepath.Join(activeDir, fmt.Sprintf("rollout-%s-99-%s.jsonl", newestDay.Format("2006-01-02T23"), activeID))
	if err := os.WriteFile(activePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gotMissing, missStats := compactionCandidatesByIDObserved("codex", "019d9999-unknown-4000-8000-000000000000")
	if len(gotMissing) != 0 {
		t.Fatalf("unknown session returned %d candidates, want 0", len(gotMissing))
	}
	if missStats.DirEntries > compactionMaxDirEntries {
		t.Fatalf("unknown session visited %d dir entries, want <= %d", missStats.DirEntries, compactionMaxDirEntries)
	}
	if missStats.StatCalls > compactionMaxStatCalls {
		t.Fatalf("unknown session made %d stat calls, want <= %d", missStats.StatCalls, compactionMaxStatCalls)
	}
	if missStats.ProbeBytes != 0 {
		t.Fatalf("unknown session probed %d content bytes across UUID-named files, want 0", missStats.ProbeBytes)
	}

	gotActive, hitStats := compactionCandidatesByIDObserved("codex", activeID)
	if len(gotActive) != 1 || gotActive[0].path != activePath {
		t.Fatalf("active lookup = %+v, want [%s]", gotActive, activePath)
	}
	if hitStats.DirEntries > 128 {
		t.Fatalf("newest-day hit visited %d dir entries, want <= 128", hitStats.DirEntries)
	}
	if hitStats.StatCalls > 4 {
		t.Fatalf("newest-day hit made %d stat calls, want <= 4", hitStats.StatCalls)
	}
	if hitStats.ProbeBytes != 0 {
		t.Fatalf("named hit read %d probe bytes, want 0", hitStats.ProbeBytes)
	}
}

func TestCompactionCandidatesByIDShallowProjectLookupSkipsProjectWalks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)

	// 50 project dirs × 100 files = 5,000 transcripts.
	projects := filepath.Join(root, claudeProjectsDirName())
	var targetPath string
	targetID := "11111111-2222-4333-8444-555555555555"
	for p := range 50 {
		dir := filepath.Join(projects, fmt.Sprintf("proj-%02d", p))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range 100 {
			uid := fmt.Sprintf("20000000-0000-4000-8000-%04x%08x", p, f)
			if err := os.WriteFile(filepath.Join(dir, uid+".jsonl"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if p == 27 {
			targetPath = filepath.Join(dir, targetID+".jsonl")
			if err := os.WriteFile(targetPath, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	got, stats := compactionCandidatesByIDObserved("claude", targetID)
	if len(got) != 1 || got[0].path != targetPath {
		t.Fatalf("claude lookup = %+v, want [%s]", got, targetPath)
	}
	if stats.DirEntries > 64 {
		t.Fatalf("shallow lookup visited %d dir entries, want <= 64 (must not walk project subdirs)", stats.DirEntries)
	}
	if stats.StatCalls > 64 {
		t.Fatalf("shallow lookup made %d stat calls, want <= 64", stats.StatCalls)
	}
	if stats.ProbeBytes != 0 {
		t.Fatalf("shallow lookup read %d probe bytes, want 0", stats.ProbeBytes)
	}

	miss, missStats := compactionCandidatesByIDObserved("claude", "99999999-9999-4999-8999-999999999999")
	if len(miss) != 0 {
		t.Fatalf("missing claude lookup returned %d candidates, want 0", len(miss))
	}
	if missStats.DirEntries > 64 || missStats.StatCalls > 64 || missStats.ProbeBytes != 0 {
		t.Fatalf("missing shallow lookup stats = %+v, want <= 64 dir/stat and 0 bytes", missStats)
	}
}

func TestEveryRegistryHarnessHasBoundedCompactionPlan(t *testing.T) {
	for _, h := range allHarnesses() {
		if !hasCompactionSearchPlan(h.Name) {
			t.Errorf("harness %q is missing a bounded compactionSearchPlan entry", h.Name)
		}
	}
}

func TestInteractiveHooksNeverCallUnboundedHarnessFiles(t *testing.T) {
	// Collect *Files/*Transcripts helper names registered on all harnesses.
	forbiddenIdents := map[string]bool{
		"walkFiles":                true,
		"ClaudeFiles":              true,
		"CodeArtsFiles":            true,
		"DoubaoFiles":              true,
		"CodexFiles":               true,
		"TraeFiles":                true,
		"OpencodeDiffFiles":        true,
		"McodeSessionFiles":        true,
		"AiderFiles":               true,
		"AmpThreadFiles":           true,
		"GeminiChatFiles":          true,
		"CursorTranscripts":        true,
		"AntigravityTranscripts":   true,
		"GrokSessionFiles":         true,
		"HermesSessionFiles":       true,
		"GooseSessionFiles":        true,
		"QwenSessionFiles":         true,
		"QoderSessionFiles":        true,
		"KimiSessionFiles":         true,
		"ClineSessionFiles":        true,
		"CherryStudioSessionFiles": true,
		"SenpiSessionFiles":        true,
		"GjcSessionFiles":          true,
		"KimchiSessionFiles":       true,
		"ClawCodeSessionFiles":     true,
		"CommandCodeSessionFiles":  true,
		"ZCodeSessionFiles":        true,
		"ZCodeTranscriptFiles":     true,
		"KiroSessionFiles":         true,
		"KiroCLIFiles":             true,
		"KiroIDEFiles":             true,
		"KiloSessionFiles":         true,
		"KiloTaskFiles":            true,
		"RooTaskFiles":             true,
		"ContinueSessionFiles":     true,
		"PiSessionFiles":           true,
		"PrimeSessionFiles":        true,
		"OmpSessionFiles":          true,
		"OpenClawStoreFiles":       true,
		"OpenClawSessionFiles":     true,
		"CopilotSessionFiles":      true,
		"CopilotChatSessionFiles":  true,
		"CodeWhaleSessionFiles":    true,
		"CodeBuddySessionFiles":    true,
		"ReasonixSessionFiles":     true,
		"MuseSessionFiles":         true,
		"DeepSeekSessionFiles":     true,
		"DevinFiles":               true,
		"JunieSessionFiles":        true,
		"JetBrainsSessionFiles":    true,
		"WindsurfSessionFiles":     true,
		"MimoSessionFiles":         true,
		"TeleAgentSessionFiles":    true,
		"OmnigentFiles":            true,
		"OrcaFiles":                true,
		"AnteSessionFiles":         true,
	}

	var files []string
	sourceMatches, err := filepath.Glob("compaction*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, sourceMatches...)
	tailMatches, err := filepath.Glob("live_tail*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, tailMatches...)

	cmdDir := filepath.Join("..", "..", "cmd", "deja")
	for _, pat := range []string{"hook_live_rules.go", "hook_prompt*.go", "hook_context.go", "hook_compaction.go", "hook_tool.go", "hook_coord.go", "live_session.go"} {
		m, gErr := filepath.Glob(filepath.Join(cmdDir, pat))
		if gErr != nil {
			t.Fatal(gErr)
		}
		files = append(files, m...)
	}

	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			t.Fatalf("parse %s: %v", path, pErr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if forbiddenIdents[fn.Name] {
					t.Errorf("%s:%d calls unbounded enumeration %s()", pos.Filename, pos.Line, fn.Name)
				}
			case *ast.SelectorExpr:
				if fn.Sel.Name == "Files" {
					t.Errorf("%s:%d calls unbounded harness enumeration .%s()", pos.Filename, pos.Line, fn.Sel.Name)
				}
				if forbiddenIdents[fn.Sel.Name] {
					t.Errorf("%s:%d calls unbounded enumeration %s()", pos.Filename, pos.Line, fn.Sel.Name)
				}
				if x, ok := fn.X.(*ast.Ident); ok && x.Name == "filepath" && (fn.Sel.Name == "WalkDir" || fn.Sel.Name == "Walk") {
					t.Errorf("%s:%d calls unbounded filepath.%s()", pos.Filename, pos.Line, fn.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestMcodeAndMuseSessionPathScaleBounded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	start := time.Date(2025, 10, 19, 10, 0, 0, 0, time.UTC)

	t.Run("mcode", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "mcode", "v2", "sessions")
		t.Setenv("DEJA_MCODE_ROOT", root)
		seq := 0
		for day := range 400 {
			dt := start.AddDate(0, 0, day)
			dayDir := filepath.Join(root, dt.Format("2006"), dt.Format("01"), dt.Format("02"))
			for i := range 5 {
				seq++
				sid := fmt.Sprintf("mvs_old_%04d", seq)
				enc := base64.RawURLEncoding.EncodeToString([]byte(sid))
				sdir := filepath.Join(dayDir, fmt.Sprintf("10-00-0%d-000-session_%s", i, enc))
				if err := os.MkdirAll(sdir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sdir, mcodeTranscriptName), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		newest := start.AddDate(0, 0, 399)
		activeID := "mvs_newest_active"
		enc := base64.RawURLEncoding.EncodeToString([]byte(activeID))
		activeDir := filepath.Join(root, newest.Format("2006"), newest.Format("01"), newest.Format("02"), "23-59-59-000-session_"+enc)
		if err := os.MkdirAll(activeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		activePath := filepath.Join(activeDir, mcodeTranscriptName)
		if err := os.WriteFile(activePath, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		var missStats compactionLookupStats
		if got := mcodeSessionPathBounded("mvs_missing", &missStats); got != "" {
			t.Fatalf("missing mcodeSessionPathBounded = %q, want empty", got)
		}
		if missStats.DirEntries > compactionMaxDirEntries || missStats.StatCalls > compactionMaxStatCalls || missStats.ProbeBytes != 0 {
			t.Fatalf("mcode miss stats = %+v, want dir<=%d stat<=%d probe=0", missStats, compactionMaxDirEntries, compactionMaxStatCalls)
		}

		var hitStats compactionLookupStats
		if got := mcodeSessionPathBounded(activeID, &hitStats); got != activePath {
			t.Fatalf("active mcodeSessionPathBounded = %q, want %q", got, activePath)
		}
		if hitStats.DirEntries > 128 || hitStats.StatCalls > 4 || hitStats.ProbeBytes != 0 {
			t.Fatalf("mcode hit stats = %+v, want dir<=128 stat<=4 probe=0", hitStats)
		}
		if got := McodeSessionPath(activeID); got != activePath {
			t.Fatalf("McodeSessionPath = %q, want %q", got, activePath)
		}
	})

	t.Run("muse", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "muse")
		t.Setenv("DEJA_MUSE_ROOTS", root)
		seq := 0
		for day := range 400 {
			dt := start.AddDate(0, 0, day)
			for range 5 {
				seq++
				uid := fmt.Sprintf("00000000-0000-4000-8000-%012x", seq)
				sdir := filepath.Join(root, dt.Format("2006"), dt.Format("01"), dt.Format("02"), uid)
				if err := os.MkdirAll(sdir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sdir, museLogName), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		newest := start.AddDate(0, 0, 399)
		activeID := "11111111-1111-4111-8111-111111111111"
		activeDir := filepath.Join(root, newest.Format("2006"), newest.Format("01"), newest.Format("02"), activeID)
		if err := os.MkdirAll(activeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		activePath := filepath.Join(activeDir, museLogName)
		if err := os.WriteFile(activePath, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		var missStats compactionLookupStats
		if got := museSessionPathBounded("99999999-9999-4999-8999-999999999999", &missStats); got != "" {
			t.Fatalf("missing museSessionPathBounded = %q, want empty", got)
		}
		if missStats.DirEntries > compactionMaxDirEntries || missStats.StatCalls > compactionMaxStatCalls || missStats.ProbeBytes != 0 {
			t.Fatalf("muse miss stats = %+v, want dir<=%d stat<=%d probe=0", missStats, compactionMaxDirEntries, compactionMaxStatCalls)
		}

		var hitStats compactionLookupStats
		if got := museSessionPathBounded(activeID, &hitStats); got != activePath {
			t.Fatalf("active museSessionPathBounded = %q, want %q", got, activePath)
		}
		if hitStats.DirEntries > 128 || hitStats.StatCalls > 4 || hitStats.ProbeBytes != 0 {
			t.Fatalf("muse hit stats = %+v, want dir<=128 stat<=4 probe=0", hitStats)
		}
		if got := MuseSessionPath(activeID); got != activePath {
			t.Fatalf("MuseSessionPath = %q, want %q", got, activePath)
		}
	})
}

func TestReadCompactionSessionUsesBoundedTailForLargeCodexRolloutByID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CODEX_HOME", root)
	t.Setenv("DEJA_CODEX_ROOT", root)

	id := "019d8888-b00d-4000-8000-000000000042"
	dayDir := filepath.Join(root, "sessions", "2026", "11", "23")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(dayDir, "rollout-2026-11-23T12-00-00-"+id+".jsonl")

	f, err := os.Create(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	// Line 1: 96 KiB session_meta (> compactionHeaderBytes = 64 KiB, < 1 MiB).
	metaPad := strings.Repeat("m", 96<<10)
	metaLine := fmt.Sprintf(
		`{"timestamp":"2026-11-23T12:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":"/Users/alex/proj","timestamp":"2026-11-23T12:00:00Z","base_instructions":{"text":%q}}}`+"\n",
		id, metaPad,
	)
	if _, werr := f.WriteString(metaLine); werr != nil {
		t.Fatal(werr)
	}
	earlyLine := `{"timestamp":"2026-11-23T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"ANCIENT_TURN_OUTSIDE_4MIB"}]}}` + "\n"
	if _, werr := f.WriteString(earlyLine); werr != nil {
		t.Fatal(werr)
	}

	// Write ~6.5 MiB of padding lines so file exceeds CompactionTailBytes (4 MiB).
	padChunk := strings.Repeat("p", 4096)
	padLine := fmt.Sprintf(
		`{"timestamp":"2026-11-23T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pad %s"}]}}`+"\n",
		padChunk,
	)
	for written := int64(len(metaLine) + len(earlyLine)); written < CompactionTailBytes+(2<<20); written += int64(len(padLine)) {
		if _, werr := f.WriteString(padLine); werr != nil {
			t.Fatal(werr)
		}
	}
	tailUser := `{"timestamp":"2026-11-23T12:59:58Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"RECENT_COMPACTION_TAIL_MARKER"}]}}` + "\n"
	tailAsst := `{"timestamp":"2026-11-23T12:59:59Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ack tail marker"}]}}` + "\n"
	if _, werr := f.WriteString(tailUser + tailAsst); werr != nil {
		t.Fatal(werr)
	}
	if cerr := f.Close(); cerr != nil {
		t.Fatal(cerr)
	}

	trDirect, err := ReadCompactionTranscript(rolloutPath, id)
	if err != nil {
		t.Fatalf("ReadCompactionTranscript(%q) error = %v", rolloutPath, err)
	}
	if !trDirect.Truncated {
		t.Fatalf("ReadCompactionTranscript Truncated = false, want true")
	}
	directText := storeSessionText(trDirect)
	if !strings.Contains(directText, "RECENT_COMPACTION_TAIL_MARKER") {
		t.Fatalf("ReadCompactionTranscript missing tail marker in:\n%s", directText)
	}
	if strings.Contains(directText, "ANCIENT_TURN_OUTSIDE_4MIB") {
		t.Fatalf("ReadCompactionTranscript included ancient turn outside bounded 4 MiB tail")
	}

	trByID, err := ReadCompactionSession("codex", id, "", "/Users/alex/proj")
	if err != nil {
		t.Fatalf("ReadCompactionSession(codex, %q) error = %v", id, err)
	}
	if !trByID.Truncated {
		t.Fatalf("ReadCompactionSession Truncated = false, want true (bounded tail should be used before full-file parse)")
	}
	byIDText := storeSessionText(trByID)
	if !strings.Contains(byIDText, "RECENT_COMPACTION_TAIL_MARKER") {
		t.Fatalf("ReadCompactionSession missing tail marker in:\n%s", byIDText)
	}
	if strings.Contains(byIDText, "ANCIENT_TURN_OUTSIDE_4MIB") {
		t.Fatalf("ReadCompactionSession read whole file instead of bounded tail")
	}
}

func isolateRegistrySearchEnv(t *testing.T) string {
	t.Helper()
	tmp := hermeticSourcesEnv(t)
	cfg := filepath.Join(tmp, "xdg-config")
	data := filepath.Join(tmp, "xdg-data")
	state := filepath.Join(tmp, "xdg-state")
	cache := filepath.Join(tmp, "xdg-cache")
	appData := filepath.Join(tmp, "appdata")
	localAppData := filepath.Join(tmp, "localappdata")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("APPDATA", appData)
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("DEJA_HOME_ROOTS", "")
	t.Setenv("DEJA_EXCLUDED_HARNESSES", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", filepath.Join(tmp, "aider.chat.history.md"))
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", filepath.Join(tmp, "deepseek"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	t.Setenv("DEJA_QODER_ROOT", filepath.Join(tmp, "qoder"))
	t.Setenv("DEJA_HERMES_PG_DSN", "")
	return tmp
}

func TestEveryRegistryHarnessSearchPlanExecutesBounded(t *testing.T) {
	isolateRegistrySearchEnv(t)

	for _, h := range allHarnesses() {
		for _, id := range []string{"11111111-2222-4333-8444-555555555555", "../unsafe"} {
			cands, stats := compactionCandidatesByIDObserved(h.Name, id)
			if len(cands) != 0 {
				t.Errorf("%s (%q) returned %d candidates in empty env", h.Name, id, len(cands))
			}
			if stats.DirEntries > compactionMaxDirEntries || stats.StatCalls > compactionMaxStatCalls || stats.ProbeBytes != 0 {
				t.Errorf("%s (%q) exceeded bounds: %+v", h.Name, id, stats)
			}
		}
	}

	if cands := compactionCandidatesByID("unknown-harness", "id-1"); len(cands) != 0 {
		t.Fatalf("unknown-harness returned %d candidates, want 0", len(cands))
	}

	// Hermes PG DSN creates a virtual hermes-pg:<hash> candidate without dialing.
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://127.0.0.1:1/deja_test")
	cands, stats := compactionCandidatesByIDObserved("hermes", "hermes-id-1")
	if len(cands) != 1 || !IsHermesPGStore(cands[0].path) {
		t.Fatalf("hermes pg candidates = %+v, want single hermes-pg store", cands)
	}
	if stats.ProbeBytes != 0 {
		t.Fatalf("hermes pg stats = %+v, want ProbeBytes=0", stats)
	}
}

func TestCompactionRegistryHelpersAndBoundedProbes(t *testing.T) {
	tmp := isolateRegistrySearchEnv(t)

	// Layout and root-based harness identification.
	claudeDir := filepath.Join(tmp, "claude", claudeProjectsDirName(), "-Users-alex-proj")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeFile := filepath.Join(claudeDir, "aaaa1111-2222-4333-8444-555555555555.jsonl")
	if got := CompactionTranscriptHarness(claudeFile); got != "claude" {
		t.Fatalf("CompactionTranscriptHarness(claude) = %q, want claude", got)
	}
	if got := CompactionTranscriptHarness(filepath.Join(tmp, "random.txt")); got != "" {
		t.Fatalf("CompactionTranscriptHarness(unrelated) = %q, want empty", got)
	}
	if got := compactionHarnessByLayout(filepath.Join(tmp, "session-state", "cid-1", "events.jsonl")); got != "copilot" {
		t.Fatalf("compactionHarnessByLayout(copilot) = %q, want copilot", got)
	}
	copilotChatRel := filepath.Join(tmp, "custom", "GitHub.copilot-chat", "transcripts", "cid-2.jsonl")
	if got := compactionHarnessByLayout(copilotChatRel); got != "copilot-chat" {
		t.Fatalf("compactionHarnessByLayout(copilot-chat) = %q, want copilot-chat", got)
	}
	if cands := compactionCandidatesForPath("", copilotChatRel); len(cands) == 0 || cands[0].harness != "copilot-chat" {
		t.Fatalf("compactionCandidatesForPath(copilot-chat layout) = %+v", cands)
	}

	// ReadCompactionSession validation and parser fallback with workspace hint.
	if _, err := ReadCompactionSession("qwen", "   ", "", ""); !errors.Is(err, ErrTranscriptIdentity) {
		t.Fatalf("empty session id err = %v, want ErrTranscriptIdentity", err)
	}
	if _, err := ReadCompactionSession("nonexistent-harness", "sid-1", "", ""); !errors.Is(err, ErrUnsupportedCompactionTranscript) {
		t.Fatalf("unknown harness err = %v, want ErrUnsupportedCompactionTranscript", err)
	}

	qwenSID := "qwen-compaction-sid"
	qwenPath := filepath.Join(tmp, "qwen", "projects", "-org-demoproj", "chats", qwenSID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(qwenPath), 0o755); err != nil {
		t.Fatal(err)
	}
	qwenData := `{"uuid":"u1","sessionId":"` + qwenSID + `","timestamp":"2026-11-23T10:00:00Z","type":"user","message":{"role":"user","parts":[{"text":"qwen question"}]}}` + "\n"
	if err := os.WriteFile(qwenPath, []byte(qwenData), 0o600); err != nil {
		t.Fatal(err)
	}
	hintDir := filepath.Join(tmp, "org", "demoproj")
	tr, err := ReadCompactionSession("qwen", qwenSID, "", hintDir)
	if err != nil {
		t.Fatalf("ReadCompactionSession(qwen) error = %v", err)
	}
	if tr.Harness != "qwen" || tr.MetricComplete || tr.Workspace != hintDir {
		t.Fatalf("qwen compaction transcript = {harness:%q complete:%v workspace:%q}, want {qwen false %q}", tr.Harness, tr.MetricComplete, tr.Workspace, hintDir)
	}

	// Filename ID helpers.
	if !pathMatchesCompactionID(filepath.Join(tmp, "session_abc123", "wire.jsonl"), "abc123") {
		t.Fatal("pathMatchesCompactionID failed on session_<id> parent dir")
	}
	if pathMatchesCompactionID(filepath.Join(tmp, "wire.jsonl"), "") {
		t.Fatal("pathMatchesCompactionID matched empty id")
	}
	if !filenameCarriesDifferentSessionID("rollout-2026-11-23-other.jsonl", "want-id") {
		t.Fatal("expected rollout filename with different id to be flagged")
	}
	if filenameCarriesDifferentSessionID("session-2026-11-23T12-00-abcd1234.json", "abcd1234-9999-4000-8000-000000000000") {
		t.Fatal("matching 8-char gemini short prefix wrongly flagged as different session")
	}

	// Bounded token probing skips compressed archives and the unread middle of large files.
	if keep, stop := compactionCandidateProbeMatch("codex", filepath.Join(tmp, "r.jsonl.zst"), "id", []byte("id"), new(int64)); keep || stop {
		t.Fatalf("compressed probe = (%v, %v), want (false, false)", keep, stop)
	}
	var totalRead int64
	smallFile := filepath.Join(tmp, "small.jsonl")
	if err := os.WriteFile(smallFile, []byte(`{"id":"needle-small"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if keep, stop := compactionCandidateProbeMatch("claude", smallFile, "needle-small", []byte("needle-small"), &totalRead); !keep || stop {
		t.Fatalf("small hit probe = (%v, %v), want (true, false)", keep, stop)
	}
	largeFile := filepath.Join(tmp, "large.jsonl")
	largeBuf := make([]byte, 2*compactionProbeWindow+4096)
	for i := range largeBuf {
		largeBuf[i] = 'x'
	}
	copy(largeBuf[compactionProbeWindow+512:], "MID_ONLY_TOKEN")
	if err := os.WriteFile(largeFile, largeBuf, 0o600); err != nil {
		t.Fatal(err)
	}
	if found, _ := fileContainsTokenBounded(largeFile, []byte("MID_ONLY_TOKEN")); found {
		t.Fatal("fileContainsTokenBounded scanned unread middle beyond head/tail windows")
	}
	copy(largeBuf[len(largeBuf)-64:], "TAIL_TOKEN")
	if err := os.WriteFile(largeFile, largeBuf, 0o600); err != nil {
		t.Fatal(err)
	}
	if found, _ := fileContainsTokenBounded(largeFile, []byte("TAIL_TOKEN")); !found {
		t.Fatal("fileContainsTokenBounded missed token in tail window")
	}
	totalRead = compactionProbeTotalBytes
	if keep, stop := compactionCandidateProbeMatch("claude", smallFile, "needle-small", []byte("needle-small"), &totalRead); keep || !stop {
		t.Fatalf("budget exhaustion = (%v, %v), want (false, true)", keep, stop)
	}

	// compactionSessionWorkspace resolves directory fallback when recorded in Session.Path.
	wsDir := t.TempDir()
	if got := compactionSessionWorkspace("opencode", filepath.Join(tmp, "opencode.db"), model.Session{ID: "s1", Path: wsDir}); got != wsDir {
		t.Fatalf("compactionSessionWorkspace(dir Path) = %q, want %q", got, wsDir)
	}
	for _, harness := range []string{"pi", "cherrystudio", "prime", "kimi", "grok", "cursor", "copilot", "copilot-chat", "cline", "codex", "claude", "mcode", "devin", "omnigent", "orca", "ante"} {
		_ = compactionSessionWorkspace(harness, filepath.Join(tmp, "missing.jsonl"), model.Session{ID: "x"})
	}

	if _, err := exec.LookPath("sqlite3"); err == nil {
		for _, tc := range []struct {
			harness string
			ddl     string
			sid     string
		}{
			{"goose", "create table sessions(id text); insert into sessions values('s1');", "s1"},
			{"opencode", "create table session(id text); insert into session values('s1');", "s1"},
			{"teleagent", "create table session_v2(id text); insert into session_v2 values('s1');", "s1"},
			{"omnigent", "create table conversations(id text); insert into conversations values('s1');", "s1"},
			{"orca", "create table agent_session_records(session_id text); insert into agent_session_records values('s1');", "s1"},
			{"zed", "create table threads(id text); insert into threads values('s1');", "s1"},
			{"kiro", "create table conversations_v2(conversation_id text); insert into conversations_v2 values('s1');", "s1"},
			{"openclaw", "create table transcript_events(session_id text); insert into transcript_events values('s1');", "s1"},
		} {
			db := filepath.Join(tmp, tc.harness+".db")
			if out, err := exec.CommandContext(t.Context(), "sqlite3", db, tc.ddl).CombinedOutput(); err != nil {
				t.Fatalf("sqlite3 %s: %v %s", tc.harness, err, out)
			}
			if checked, found := compactionDBContainsID(tc.harness, db, tc.sid); !checked || !found {
				t.Errorf("compactionDBContainsID(%s, hit) = (%v, %v), want (true, true)", tc.harness, checked, found)
			}
			if checked, found := compactionDBContainsID(tc.harness, db, "missing"); !checked || found {
				t.Errorf("compactionDBContainsID(%s, miss) = (%v, %v), want (true, false)", tc.harness, checked, found)
			}
		}
		if checked, found := compactionDBContainsID("unknown", filepath.Join(tmp, "x.db"), "s1"); checked || found {
			t.Fatalf("unknown sqlite harness = (%v, %v), want (false, false)", checked, found)
		}
	}
}
