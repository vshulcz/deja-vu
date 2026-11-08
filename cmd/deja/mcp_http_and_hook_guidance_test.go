package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func seedDay1MagicStore(t *testing.T) string {
	t.Helper()
	_ = hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-payments")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	writeSession := func(id string, lines ...string) {
		body := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userMsg := func(id string, at time.Time, text string) string {
		return fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"user","content":%q}}`,
			id, at.Format(time.RFC3339), text)
	}
	asstMsg := func(id string, at time.Time, text string) string {
		return fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"assistant","content":%q}}`,
			id, at.Format(time.RFC3339), text)
	}
	editTool := func(id, toolID, path string, at time.Time) string {
		return fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Edit","input":{"file_path":%q,"old_string":"a","new_string":"b"}}]}}`,
			id, at.Format(time.RFC3339), toolID, path)
	}
	bashTool := func(id, toolID, cmd string, at time.Time) string {
		return fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]}}`,
			id, at.Format(time.RFC3339), toolID, cmd)
	}
	toolRes := func(id, toolID, content string, isErr bool, at time.Time) string {
		return fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%q,"is_error":%v}]}}`,
			id, at.Format(time.RFC3339), toolID, content, isErr)
	}

	// Four focused sessions touching both charge.go and charge_test.go, running
	// both a non-verify command (curl, 4 sessions) and a verify suite
	// (go test ./internal/charge/... -race, 3 sessions), plus a confirmed
	// 2-session error->fix pair (bind: address already in use :8080 ->
	// lsof -ti:8080 | xargs kill -9) and a 1-session unconfirmed candidate.
	for i := range 4 {
		id := fmt.Sprintf("pay-%d", i)
		t0 := base.Add(time.Duration(i) * time.Hour)
		lines := []string{
			userMsg(id, t0, "update currency rounding in charge.go"),
			editTool(id, "e1", "/work/payments/charge.go", t0.Add(time.Minute)),
			editTool(id, "e2", "/work/payments/charge_test.go", t0.Add(2*time.Minute)),
			bashTool(id, "b1", "git log -n 3 --oneline", t0.Add(3*time.Minute)),
			toolRes(id, "b1", "ok\nExit code 0", false, t0.Add(4*time.Minute)),
		}
		if i < 3 {
			lines = append(lines,
				bashTool(id, "b2", "go test ./internal/charge/... -race", t0.Add(5*time.Minute)),
				toolRes(id, "b2", "ok  \tpayments/internal/charge\t0.12s\nExit code 0", false, t0.Add(6*time.Minute)),
			)
		}
		if i < 2 {
			lines = append(lines,
				asstMsg(id, t0.Add(7*time.Minute), "ran it and got:\npanic: sql: database is closed"),
				bashTool(id, "b3", "make clean && make CGO_ENABLED=0", t0.Add(8*time.Minute)),
				asstMsg(id, t0.Add(9*time.Minute), "that did it, the build is green now"),
			)
		}
		if i == 3 {
			lines = append(lines,
				asstMsg(id, t0.Add(7*time.Minute), "ran it and got:\npanic: transient staging timeout"),
				bashTool(id, "b4", "echo candidate-only", t0.Add(8*time.Minute)),
			)
		}
		lines = append(lines, asstMsg(id, t0.Add(10*time.Minute), " We decided that charge.go must round half-even on JPY amounts."))
		writeSession(id, lines...)
	}

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallProjectXRayAndOrientSurfaces(t *testing.T) {
	dir := seedDay1MagicStore(t)

	xray := projectXRayLine(dir, "/work/payments")
	if xray == "" {
		t.Fatal("projectXRayLine returned empty for a 4-session project")
	}
	for _, want := range []string{
		"this repo (work/payments): 4 sessions",
		"check: `go test ./internal/charge/... -race`",
		"hot: charge.go, charge_test.go",
		"coupled: charge.go ↔ charge_test.go (4 of 4 sessions)",
		"1 error→fix pair ready",
	} {
		if !strings.Contains(xray, want) {
			t.Errorf("projectXRayLine missing %q:\n  %s", want, xray)
		}
	}
	if strings.Contains(xray, "[claude") {
		t.Errorf("projectXRayLine must not emit [claude brackets that look like session rows: %s", xray)
	}

	digestBlock := orientDigestBlock(dir, "/work/payments", []string{"payments"}, policy.ActivationAuto)
	for _, want := range []string{
		"- go test ./internal/charge/... -race · 3 sessions",
		"- the files its sessions keep opening: charge.go, charge_test.go",
		"- coupled files (usually edited together): charge.go ↔ charge_test.go (4 of 4 sessions)",
		"- known trap here: `panic: sql: database is closed` → `make clean && make CGO_ENABLED=0`",
	} {
		if !strings.Contains(digestBlock, want) {
			t.Errorf("orientDigestBlock missing %q:\n%s", want, digestBlock)
		}
	}
	if strings.Contains(digestBlock, "candidate-only") {
		t.Errorf("orientDigestBlock leaked unconfirmed candidate fix pair:\n%s", digestBlock)
	}
	// Verification command (go test, 3 sessions) must rank ahead of non-verify
	// command (git log, 4 sessions).
	idxVerify := strings.Index(digestBlock, "go test ./internal/charge/... -race")
	idxGitLog := strings.Index(digestBlock, "git log -n 3 --oneline")
	if idxVerify < 0 || idxGitLog < 0 || idxVerify > idxGitLog {
		t.Errorf("orientDigestBlock did not rank verify command ahead of git log:\n%s", digestBlock)
	}

	mcpOut, err := callMCPTool(dir, "orient", json.RawMessage(`{"mode":"orient","project":"payments"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"coupled files (usually edited together):",
		"charge.go ↔ charge_test.go (4 of 4 sessions)",
		"known traps and verified fixes here:",
		"`panic: sql: database is closed` → `make clean && make CGO_ENABLED=0`",
	} {
		if !strings.Contains(mcpOut, want) {
			t.Errorf("mcpOrient missing %q:\n%s", want, mcpOut)
		}
	}
}

func TestPointOfActionCombinesDecisionVerifyAndCoChange(t *testing.T) {
	dir := seedDay1MagicStore(t)
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)

	if err := sources.AppendPromoted("payments",
		"charge.go must round half-even on JPY amounts",
		"charge.go must round half-even on JPY amounts",
		"claude:pay-3", "accepted", now); err != nil {
		t.Fatal(err)
	}

	line := fileHookLine(dir, "/work/payments", "/work/payments/charge.go")
	if line == "" {
		t.Fatal("fileHookLine returned empty")
	}
	if len(line) > toolHookMaxBytes {
		t.Errorf("fileHookLine length %d > toolHookMaxBytes %d: %s", len(line), toolHookMaxBytes, line)
	}
	for _, want := range []string{
		"charge.go has been worked on in 4 sessions",
		"decision in this project: charge.go must round half-even on JPY amounts",
		"ran here and passed: `go test ./internal/charge/... -race` (3 sessions)",
		"usually edited alongside charge_test.go (4 of 4 sessions)",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("fileHookLine missing %q:\n  %s", want, line)
		}
	}

	// Even when DEJA_COMMAND_HINTS is off (default), an explicit promoted command
	// decision that names `go test` must fire before Bash.
	t.Setenv("DEJA_COMMAND_HINTS", "")
	if err := sources.AppendPromoted("payments",
		"always pass -race when running go test in payments",
		"always pass -race when running go test in payments",
		"claude:pay-2", "accepted", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	in := toolHookInput{ToolName: "Bash"}
	in.ToolInput.Command = "go test ./internal/charge/... -race"
	cmdLine := toolHookLine(dir, "/work/payments", in)
	if !strings.Contains(cmdLine, "always pass -race when running go test in payments") {
		t.Errorf("promoted command decision did not fire when DEJA_COMMAND_HINTS is off: %q", cmdLine)
	}
	// Ordinary unpromoted command remains silent when DEJA_COMMAND_HINTS is off.
	inGitLog := toolHookInput{ToolName: "Bash"}
	inGitLog.ToolInput.Command = "git log -n 3 --oneline"
	if got := toolHookLine(dir, "/work/payments", inGitLog); got != "" {
		t.Errorf("unpromoted command spoke with DEJA_COMMAND_HINTS off: %q", got)
	}
}

func TestStatuslineShowsDistillationRatioOnHookOnlyDay(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "index.db")
	for range 5 {
		usage.RecordDigest(dir, usage.KindHook, strings.Repeat("x", 400), 2, 8000)
	}
	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"no recalls yet today",
		"2.0 KB injected",
		"~20× less than replaying",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("statusline missing %q: %q", want, got)
		}
	}
}

func TestLocalQwenABAgentOrientationAndTrapAvoidance(t *testing.T) {
	ctx := context.Background()
	client := &http.Client{Timeout: 25 * time.Second}
	healthReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:18081/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	healthResp, err := client.Do(healthReq)
	if err != nil {
		t.Skipf("local Qwen server not reachable at 127.0.0.1:18081: %v", err)
	}
	_ = healthResp.Body.Close()

	dir := seedDay1MagicStore(t)
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	if err := sources.AppendPromoted("payments",
		"charge.go must round half-even on JPY amounts",
		"charge.go must round half-even on JPY amounts",
		"claude:pay-3", "accepted", now); err != nil {
		t.Fatal(err)
	}

	orientBlock := orientDigestBlock(dir, "/work/payments", []string{"payments"}, policy.ActivationAuto)
	hookLine := fileHookLine(dir, "/work/payments", "/work/payments/charge.go")

	taskPrompt := `You are working in /work/payments. Task: update JPY currency rounding in charge.go, update any companion test file, and run the project's test check. If you hit "panic: sql: database is closed", run the project's known remedy.
Reply in compact JSON only:
{"files_to_edit":["..."],"rounding_rule":"...","test_command":"...","sql_closed_fix":"...","exploratory_commands_needed":0}`

	type qwenMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type qwenTemplateKwargs struct {
		EnableThinking bool `json:"enable_thinking"`
	}
	type qwenReq struct {
		Model              string             `json:"model"`
		Messages           []qwenMsg          `json:"messages"`
		Temperature        int                `json:"temperature"`
		MaxTokens          int                `json:"max_tokens"`
		ChatTemplateKwargs qwenTemplateKwargs `json:"chat_template_kwargs"`
	}
	askQwen := func(sysContext string) (string, int) {
		payload := qwenReq{
			Model: "qwen3.8-27b",
			Messages: []qwenMsg{
				{Role: "system", Content: sysContext},
				{Role: "user", Content: taskPrompt},
			},
			Temperature:        0,
			MaxTokens:          220,
			ChatTemplateKwargs: qwenTemplateKwargs{EnableThinking: false},
		}
		b, mErr := json.Marshal(payload)
		if mErr != nil {
			t.Fatal(mErr)
		}
		req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:18081/v1/chat/completions", bytes.NewReader(b))
		if rErr != nil {
			t.Fatal(rErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			t.Skipf("local Qwen completion call failed: %v", reqErr)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var parsed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
			t.Skipf("unexpected local Qwen response: %s", string(raw))
		}
		return parsed.Choices[0].Message.Content, parsed.Usage.TotalTokens
	}

	withoutDeja, tokWithout := askQwen("No prior session memory is available. If you do not know the exact test flags, rounding rule, or sql: database is closed remedy, set exploratory_commands_needed to the number of grep/read commands you would need first.")
	withDeja, tokWith := askQwen("Prior project memory from deja:\n" + orientBlock + "\nPoint-of-action hook on charge.go:\n" + hookLine)

	t.Logf("Local Qwen WITHOUT deja (%d tokens): %s", tokWithout, strings.TrimSpace(withoutDeja))
	t.Logf("Local Qwen WITH deja (%d tokens): %s", tokWith, strings.TrimSpace(withDeja))

	lowWith := strings.ToLower(withDeja)
	for _, want := range []string{
		"charge_test.go",
		"half-even",
		"go test ./internal/charge/... -race",
		"cgo_enabled=0",
	} {
		if !strings.Contains(lowWith, strings.ToLower(want)) {
			t.Errorf("local Qwen WITH deja missed %q in response:\n%s", want, withDeja)
		}
	}
}

func BenchmarkOrientDigestAndFileHookLatency(b *testing.B) {
	t := &testing.T{}
	_ = t
	tmp := b.TempDir()
	b.Setenv("HOME", tmp)
	b.Setenv("USERPROFILE", tmp)
	b.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	b.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	proj := filepath.Join(tmp, "claude", "-work-payments")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for i := range 4 {
		id := fmt.Sprintf("pay-%d", i)
		t0 := base.Add(time.Duration(i) * time.Hour)
		body := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"user","content":"update currency rounding in charge.go"}}`+"\n"+
			`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/work/payments/charge.go","old_string":"a","new_string":"b"}}]}}`+"\n"+
			`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":"/work/payments/charge_test.go","old_string":"a","new_string":"b"}}]}}`+"\n"+
			`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"go test ./internal/charge/... -race"}}]}}`+"\n"+
			`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/payments","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","content":"ok\nExit code 0","is_error":false}]}}`+"\n",
			id, t0.Format(time.RFC3339),
			id, t0.Add(time.Minute).Format(time.RFC3339),
			id, t0.Add(2*time.Minute).Format(time.RFC3339),
			id, t0.Add(3*time.Minute).Format(time.RFC3339),
			id, t0.Add(4*time.Minute).Format(time.RFC3339))
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	_ = sources.AppendPromoted("payments",
		"charge.go must round half-even on JPY amounts",
		"charge.go must round half-even on JPY amounts",
		"claude:pay-3", "accepted", now)

	b.Run("orientDigestBlock", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if out := orientDigestBlock(dir, "/work/payments", []string{"payments"}, policy.ActivationAuto); out == "" {
				b.Fatal("empty orientDigestBlock")
			}
		}
	})
	b.Run("fileHookLine", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if out := fileHookLine(dir, "/work/payments", "/work/payments/charge.go"); out == "" {
				b.Fatal("empty fileHookLine")
			}
		}
	})
	b.Run("projectXRayLine", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if out := projectXRayLine(dir, "/work/payments"); out == "" {
				b.Fatal("empty projectXRayLine")
			}
		}
	})
}
