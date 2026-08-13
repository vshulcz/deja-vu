// Command harnesssweep runs each installed harness against a recording
// endpoint and reports whether deja's memory reached the request.
//
// Everything else deja tests answers a narrower question. The install tests say
// the files are written and parse; the benchmarks say the ranking is good. Only
// this says whether a harness, given those files, actually carries the recall
// into what it sends the model — which is the whole product. The bug that
// prompted it was of exactly that shape: deja's opencode plugin appended its
// recall as a second system block, and every turn against a strict endpoint
// failed. Valid files, correct ranking, broken product.
//
// Each harness gets an empty home, its own index built from -corpus, and one
// turn. The control arm runs the same harness with deja not installed, because
// a harness that fails on its own proves nothing about deja.
//
//	go run ./scripts/mockmodel -port 18777 -log /tmp/reqs.jsonl &
//	go run ./scripts/harnesssweep -corpus ~/.claude/projects -log /tmp/reqs.jsonl
//
// Harnesses that need an account are skipped rather than reported as broken:
// a login wall is not a deja defect. Neither is a harness that runs no hooks
// headless — codex and grok both do that, which is why they come back with no
// recall here and are still correct in a terminal.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type harness struct {
	name   string
	target string // the deja install target
	bin    string
	args   func(prompt, base, model string) []string
	// setup writes whatever the harness needs to reach the mock: a provider
	// block, an endpoint, a model name. Without it most of them stop at a
	// login prompt and the sweep learns nothing.
	setup func(home, base, model string) error
}

func harnesses() []harness {
	return []harness{
		{"opencode", "opencode-auto", "opencode",
			func(p, _, _ string) []string { return []string{"run", p} }, setupOpencode},
		{"codex", "codex-auto", "codex",
			func(p, _, _ string) []string { return []string{"exec", "--skip-git-repo-check", p} }, setupCodex},
		{"goose", "goose-auto", "goose",
			func(p, _, _ string) []string { return []string{"run", "-t", p} }, setupGoose},
		{"qwen", "qwen-auto", "qwen",
			func(p, _, _ string) []string { return []string{"-p", p, "-y"} }, nil},
		{"grok", "grok-auto", "grok",
			func(p, base, model string) []string {
				return []string{"-p", p, "-u", base, "-k", "local", "-m", model}
			}, nil},
		{"aider", "aider", "aider",
			func(p, base, model string) []string {
				return []string{"--no-git", "--yes", "--openai-api-base", base,
					"--openai-api-key", "local", "--model", "openai/" + model, "--message", p}
			}, nil},
	}
}

func setupOpencode(home, base, model string) error {
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "opencode.json")
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	cfg["$schema"] = "https://opencode.ai/config.json"
	cfg["model"] = "local/" + model
	cfg["provider"] = map[string]any{"local": map[string]any{
		"npm": "@ai-sdk/openai-compatible", "name": "local",
		"options": map[string]any{"baseURL": base, "apiKey": "local"},
		"models":  map[string]any{model: map[string]any{"name": "mock"}},
	}}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func setupCodex(home, base, model string) error {
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.toml")
	// Keep whatever deja wrote (its MCP block) and add only the provider.
	var keep []string
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(line, "model =") && !strings.HasPrefix(line, "model_provider =") &&
				!strings.HasPrefix(line, "approval_policy") && !strings.HasPrefix(line, "sandbox_mode") &&
				!strings.HasPrefix(line, "[model_providers.") {
				keep = append(keep, line)
			}
		}
	}
	body := fmt.Sprintf("model = %q\nmodel_provider = \"mock\"\napproval_policy = \"never\"\n"+
		"sandbox_mode = \"read-only\"\n\n[model_providers.mock]\nname = \"mock\"\nbase_url = %q\n"+
		"wire_api = \"responses\"\nrequires_openai_auth = false\n\n%s\n",
		model, base, strings.Join(keep, "\n"))
	return os.WriteFile(path, []byte(body), 0o644)
}

func setupGoose(home, _, model string) error {
	dir := filepath.Join(home, ".config", "goose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "GOOSE_PROVIDER") {
		body = append([]byte(fmt.Sprintf("GOOSE_PROVIDER: openai\nGOOSE_MODEL: %s\n", model)), body...)
	}
	return os.WriteFile(path, body, 0o644)
}

type result struct {
	ran      bool
	requests int
	recall   bool
	rejected bool
	err      string
	took     time.Duration
}

func main() {
	deja := flag.String("deja", "", "path to the deja binary (default: build one)")
	corpus := flag.String("corpus", "", "a directory of Claude-format transcripts to index")
	logPath := flag.String("log", "/tmp/harnesssweep.jsonl", "the mockmodel request log")
	base := flag.String("base", "http://127.0.0.1:18777/v1", "the mockmodel endpoint")
	model := flag.String("model", "mock-model", "the model name the mock answers to")
	repo := flag.String("repo", ".", "run the harnesses from here; recall is project-scoped")
	prompt := flag.String("prompt", "How often does the deploy token for the billing gateway rotate?",
		"the question to ask")
	only := flag.String("only", "", "comma-separated harness names")
	flag.Parse()

	if *corpus == "" {
		fmt.Fprintln(os.Stderr, "harnesssweep: -corpus is required")
		os.Exit(2)
	}
	exe := *deja
	if exe == "" {
		out, err := os.MkdirTemp("", "sweepbin")
		if err != nil {
			fmt.Fprintln(os.Stderr, "harnesssweep:", err)
			os.Exit(1)
		}
		defer os.RemoveAll(out)
		exe = filepath.Join(out, "deja")
		build := exec.Command("go", "build", "-o", exe, "./cmd/deja")
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "harnesssweep: build:", err)
			os.Exit(1)
		}
	}
	wanted := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			wanted[n] = true
		}
	}

	for _, h := range harnesses() {
		if len(wanted) > 0 && !wanted[h.name] {
			continue
		}
		if _, err := exec.LookPath(h.bin); err != nil {
			fmt.Printf("%-10s SKIP  %s is not installed\n", h.name, h.bin)
			continue
		}
		withDeja := run(h, exe, *corpus, *logPath, *base, *model, *repo, *prompt, true)
		control := run(h, exe, *corpus, *logPath, *base, *model, *repo, *prompt, false)
		report(h.name, withDeja, control)
	}
}

func run(h harness, exe, corpus, logPath, base, model, repo, prompt string, install bool) result {
	var out result
	home, err := os.MkdirTemp("", "sweep-"+h.name)
	if err != nil {
		return result{err: err.Error()}
	}
	defer os.RemoveAll(home)

	env := append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"DEJA_CLAUDE_ROOT="+corpus,
		// A fresh index per run: deja remembers what it injected into a
		// session and rate-limits its own notice, so a shared one makes the
		// second run disagree with the first for reasons of its own.
		"DEJA_INDEX_DIR="+filepath.Join(home, "idx"),
		"OPENAI_API_KEY=local", "OPENAI_BASE_URL="+base, "OPENAI_API_BASE="+base,
		"OPENAI_MODEL="+model, "OPENAI_HOST="+strings.TrimSuffix(base, "/v1"),
		"OPENAI_BASE_PATH=v1/chat/completions")

	deja := func(args ...string) {
		cmd := exec.Command(exe, args...)
		cmd.Env = env
		// From the repo: recall is scoped to the project, so an install run
		// from anywhere else finds nothing and writes an empty context.
		cmd.Dir = repo
		_ = cmd.Run()
	}
	deja("index")
	if install {
		deja("install", h.target, "--no-index")
	}
	if h.setup != nil {
		if err := h.setup(home, base, model); err != nil {
			return result{err: err.Error()}
		}
	}

	// Read the log by offset rather than truncating it: the mock holds the
	// file open, and truncating under it sends the next writes past the end.
	start := int64(0)
	if fi, err := os.Stat(logPath); err == nil {
		start = fi.Size()
	}
	t0 := time.Now()
	cmd := exec.Command(h.bin, h.args(prompt, base, model)...)
	cmd.Env = env
	cmd.Dir = repo
	stdout, runErr := cmd.CombinedOutput()
	out.took = time.Since(t0)
	out.ran = runErr == nil
	if runErr != nil {
		out.err = firstLine(string(stdout))
	}
	out.rejected = strings.Contains(string(stdout), "System message must be at the beginning")

	sent, n := readSince(logPath, start)
	out.requests = n
	out.recall = strings.Contains(sent, "deja-recall")
	return out
}

// readSince returns what was appended to the log after offset, and how many
// records that is. Reading the whole file and slicing is enough here: a sweep's
// log is small, and the alternative is holding a handle on a file another
// process is appending to.
func readSince(path string, offset int64) (string, int) {
	b, err := os.ReadFile(path)
	if err != nil || int64(len(b)) <= offset {
		return "", 0
	}
	body := strings.TrimSpace(string(b[offset:]))
	if body == "" {
		return "", 0
	}
	return body, strings.Count(body, "\n") + 1
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func report(name string, withDeja, control result) {
	// A harness that cannot complete a turn without deja cannot say anything
	// about deja: an account wall, a missing model, a flag this sweep got
	// wrong. Naming that separately keeps it out of the defect column.
	if control.requests == 0 {
		fmt.Printf("%-10s SKIP  the harness never reached the model on its own: %s\n", name, control.err)
		return
	}
	verdict := "recall reached the model"
	switch {
	case withDeja.rejected:
		verdict = "REJECTED the request deja produced"
	case !withDeja.recall:
		verdict = "NO RECALL in the request"
	}
	broke := ""
	if control.ran && !withDeja.ran {
		broke = "  · deja broke a turn that worked without it"
	}
	fmt.Printf("%-10s %-34s (%d request(s), %s)%s\n", name, verdict, withDeja.requests,
		withDeja.took.Round(time.Millisecond), broke)
}
