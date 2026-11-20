package index

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestVerifyCommandWrappersAndSetupSegments(t *testing.T) {
	positive := []string{
		`export PATH="/opt/venv/bin:$PATH" && pytest`,
		`source .venv/bin/activate && mypy src`,
		`. .venv/bin/activate && python3.12 -m ruff check .`,
		`env -i FOO=1 timeout 30 uv run --frozen pytest -q`,
		`gtimeout -s TERM 15 poetry run pyright`,
		`pipenv run flake8`,
		`pipx run ruff check .`,
		`uvx ruff@0.6 check .`,
		`npx --yes @biomejs/biome check .`,
		`bunx vitest run`,
		`pnpx tsc --noEmit`,
		`pnpm verify`,
		`npm run biome`,
		`PATH="$PWD/.venv/bin:$PATH" pytest -q`,
	}
	for _, cmd := range positive {
		if !VerifyCommand(cmd) {
			t.Errorf("VerifyCommand(%q) = false, want true", cmd)
		}
	}

	negative := []string{
		`export FOO=1`,
		`source .venv/bin/activate`,
		`. .venv/bin/activate`,
		`source ./evil.sh && pytest`,
		`export FOO=1 rm -rf build`,
		`uv sync`,
		`poetry install`,
		`env rm -rf build`,
		`timeout 30 python script.py`,
		`gtimeout 10 ls`,
	}
	for _, cmd := range negative {
		if VerifyCommand(cmd) {
			t.Errorf("VerifyCommand(%q) = true, want false", cmd)
		}
	}
}

func TestStripViewerPipeAndCanonicalRecurringCommand(t *testing.T) {
	pipeCases := []struct {
		in, want string
	}{
		{"$ golangci-lint run | head -40", "$ golangci-lint run"},
		{"go test ./... 2>&1 | tail -50", "go test ./..."},
		{"pytest -q | head -n 20 | tail -10", "pytest -q"},
		{`awk '{print $1 | "head"}' foo`, `awk '{print $1 | "head"}' foo`},
		{"tail -f server.log", "tail -f server.log"},
		{"go test ./... || tail -5", "go test ./... || tail -5"},
		{"go test ./... | tail -10 && echo done", "go test ./... | tail -10 && echo done"},
	}
	for _, tc := range pipeCases {
		if got := StripViewerPipe(tc.in); got != tc.want {
			t.Errorf("StripViewerPipe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	canonCases := []struct {
		in, want string
	}{
		{"cd /Users/alex/repo/.worktrees/wt1 && go test ./... 2>&1 | tail -50", "go test ./..."},
		{"cd /Users/alex/repo/.claude/worktrees/sub && git status --short", "git status --short"},
		{"cd /Users/alex/repo/.codex/worktrees/sub && ruff check .", "ruff check ."},
		{"cd /Users/alex/repo/.cursor/worktrees/sub && pytest -q", "pytest -q"},
		{"cd /Users/alex/repo && go test ./... | tail -40", "go test ./..."},
		{"cd ~/code/app && pytest -q", "pytest -q"},
		{"cd /Users/alex/repo && rm -rf build", "cd /Users/alex/repo && rm -rf build"},
		{"cd packages/web && npm test | tail -20", "cd packages/web && npm test"},
		{"$ cd /Users/alex/repo/.worktrees/wt && go test ./...", "$ go test ./..."},
	}
	for _, tc := range canonCases {
		if got := CanonicalRecurringCommand(tc.in); got != tc.want {
			t.Errorf("CanonicalRecurringCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildCommandsMultiLineOutcomesAndHighFailRate(t *testing.T) {
	tmp := t.TempDir()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ss := []model.Session{
		{
			ID:      "s1",
			Harness: "claude",
			Project: "work/app",
			Updated: base,
			Messages: []model.Message{
				{Role: roleCommand, Text: "pytest \\\n  -q  → exit 1", Time: base},
				{Role: roleCommand, Text: "go test ./...  → exit 0", Time: base.Add(time.Minute)},
			},
		},
		{
			ID:      "s2",
			Harness: "claude",
			Project: "work/app",
			Updated: base.Add(time.Hour),
			Messages: []model.Message{
				{Role: roleCommand, Text: "pytest \\\n  -q\n  → exit 0", Time: base.Add(time.Hour)},
				{Role: roleCommand, Text: "go test ./...  → exit 0", Time: base.Add(time.Hour + time.Minute)},
			},
		},
	}
	buildCommands(tmp, ss)
	got := ReadCommands(tmp)
	byCmd := map[string]CommandUse{}
	for _, cu := range got {
		byCmd[cu.Command] = cu
	}
	py, ok := byCmd[`pytest \`]
	if !ok {
		t.Fatalf("missing multi-line pytest command in %#v", got)
	}
	if py.Outcomes != 2 || py.Failures != 1 || !HighFailRate(py.Outcomes, py.Failures) {
		t.Fatalf("multi-line pytest = %+v, want Outcomes=2 Failures=1 HighFailRate=true", py)
	}
	gt, ok := byCmd["go test ./..."]
	if !ok || gt.Outcomes != 2 || gt.Failures != 0 || HighFailRate(gt.Outcomes, gt.Failures) {
		t.Fatalf("go test = %+v, want Outcomes=2 Failures=0 HighFailRate=false", gt)
	}
}

func TestCommandsOutcomesAndIncrementalRebuildParity(t *testing.T) {
	writeSubagent := func(t *testing.T, claudeRoot, parentID, agentID, cwd string, cmds []struct {
		cmd     string
		isError bool
		out     string
	}, at time.Time) {
		t.Helper()
		subPath := filepath.Join(claudeRoot, "-work-app", parentID, "subagents", agentID+".jsonl")
		var lines strings.Builder
		for i, c := range cmds {
			ts := at.Add(time.Duration(i*2) * time.Minute).Format(time.RFC3339)
			tsRes := at.Add(time.Duration(i*2+1) * time.Minute).Format(time.RFC3339)
			toolID := fmt.Sprintf("tool-%s-%d", agentID, i)
			fmt.Fprintf(&lines, `{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]}}`+"\n",
				agentID, ts, cwd, toolID, c.cmd)
			fmt.Fprintf(&lines, `{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%q,"is_error":%v}]}}`+"\n",
				agentID, tsRes, cwd, toolID, c.out, c.isError)
		}
		parityWrite(t, subPath, lines.String(), false)
	}

	root := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	t.Setenv(ManualEnv, "")
	claudeRoot := filepath.Join(root, "claude")
	inc := filepath.Join(root, "idx-inc")
	fresh := filepath.Join(root, "idx-fresh")

	t1 := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)

	writeSubagent(t, claudeRoot, "p1", "a1", "/work/app/.worktrees/wt1", []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "cd /work/app/.worktrees/wt1 && go test ./... 2>&1 | tail -50", isError: false, out: "ok"},
		{cmd: "pytest -k broken", isError: true, out: "Exit code 2\nfailed"},
	}, t1)
	parityPass(t, inc, false)

	writeSubagent(t, claudeRoot, "p2", "a2", "/work/app/.worktrees/wt2", []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "cd /work/app/.worktrees/wt2 && go test ./... | head -40", isError: true, out: "Exit code 1\nfail"},
		{cmd: "cd /work/app/.worktrees/wt2 && go test ./...", isError: false, out: "ok"},
		{cmd: "pytest -k broken", isError: false, out: "ok"},
	}, t2)
	parityPass(t, inc, false)
	parityPass(t, fresh, true)

	incCmds := ReadCommands(inc)
	freshCmds := ReadCommands(fresh)
	if fmt.Sprint(incCmds) != fmt.Sprint(freshCmds) {
		t.Fatalf("incremental vs rebuild mismatch:\ninc:   %#v\nfresh: %#v", incCmds, freshCmds)
	}

	byCmd := map[string]CommandUse{}
	for _, cu := range incCmds {
		byCmd[strings.TrimPrefix(cu.Command, "$ ")] = cu
	}
	gt, ok := byCmd["go test ./..."]
	if !ok {
		t.Fatalf("missing canonicalized go test ./... in %#v", incCmds)
	}
	if gt.Sessions != 2 || gt.Outcomes != 3 || gt.Failures != 1 {
		t.Fatalf("go test ./... = %+v, want Sessions=2 Outcomes=3 Failures=1", gt)
	}
	pUse, ok := gt.ByProject["work/app"]
	if !ok || pUse.Sessions != 2 || pUse.Outcomes != 3 || pUse.Failures != 1 {
		t.Fatalf("work/app ProjectUse = %+v (ok=%v), want Sessions=2 Outcomes=3 Failures=1", pUse, ok)
	}

	py, ok := byCmd["pytest -k broken"]
	if !ok {
		t.Fatalf("missing pytest -k broken in %#v", incCmds)
	}
	if py.Sessions != 2 || py.Outcomes != 2 || py.Failures != 1 || !HighFailRate(py.Outcomes, py.Failures) {
		t.Fatalf("pytest -k broken = %+v, want Sessions=2 Outcomes=2 Failures=1 HighFailRate=true", py)
	}
}
