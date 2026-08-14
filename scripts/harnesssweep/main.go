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
	// wrapped means deja ships a `deja <name>` front end for this harness.
	// Those two invocations are different products: the wrapper refreshes the
	// context and says so, while the bare binary reads whatever was left on
	// disk in silence. A sweep that only ran one of them would describe the
	// harness wrongly whichever it chose.
	wrapped bool
	// resume continues the session the first turn opened, for the harnesses
	// that can do it without a terminal. One turn only ever tests the opening
	// move; the promise is memory for every question, and the machinery that
	// could break the second is real — deja suppresses recall it has already
	// injected into a session, and the plugins cache per prompt.
	resume func(prompt, base, model string) []string
	// wrongProduct reports that the binary on PATH is not the product deja
	// wires, and why. Two different tools ship as `grok`: xAI's Grok Build,
	// which reads Claude-shaped hooks out of ~/.grok, and a community CLI of
	// the same name that shares the directory and reads none. Running the
	// second and reporting "no recall" blames deja for a harness it was never
	// talking to.
	wrongProduct func() string
	// toolHook marks a harness deja wires a PreToolUse hook into. That hook
	// speaks at the moment of an action rather than at the start of a session,
	// and nothing exercised it until the mock learned to answer with a tool
	// call — the sweep only ever asked questions, so no tool was ever used.
	toolHook bool
	// mcp marks a harness deja installs its MCP server into. Everything else
	// here reads what deja pushed into the request; this is the other
	// direction — whether the harness can reach back to deja and get an
	// answer. It is the only channel some of them have for a follow-up.
	mcp bool
	// sessionScoped marks a harness with no way to recall against the prompt.
	// aider has neither hooks nor an MCP client; its read-only files are the
	// whole channel, and nothing tells deja what was typed. Recall that does
	// not answer the question is the best such a harness can do, so reporting
	// it as a defect would bury the harnesses where it is one.
	sessionScoped bool
}

func harnesses() []harness {
	return []harness{
		{"claude", "claude-auto", "claude",
			func(p, _, _ string) []string {
				return []string{"-p", p, "--permission-mode", "bypassPermissions"}
			}, nil, false,
			func(p, _, _ string) []string {
				return []string{"-p", p, "--continue", "--permission-mode", "bypassPermissions"}
			}, nil, true, true, false},
		{"opencode", "opencode-auto", "opencode",
			func(p, _, _ string) []string { return []string{"run", p} }, setupOpencode, false,
			func(p, _, _ string) []string { return []string{"run", "--continue", p} }, nil, false, true, false},
		{"codex", "codex-auto", "codex",
			func(p, _, _ string) []string { return []string{"exec", "--skip-git-repo-check", p} }, setupCodex, false, nil, nil, true, true, false},
		{"goose", "goose-auto", "goose",
			func(p, _, _ string) []string { return []string{"run", "-t", p} }, setupGoose, true,
			// Bare goose has no working prompt channel: it discards hook stdout,
			// and .goosehints is read once when the session opens. MOIM, which is
			// re-read every turn, is env-only — so the wrapper arm below is where
			// goose recalls for the question.
			nil, nil, false, true, true},
		{"qwen", "qwen-auto", "qwen",
			func(p, _, _ string) []string { return []string{"-p", p, "-y"} }, nil, false, nil, nil, false, true, false},
		{"grok", "grok-auto", "grok",
			func(p, base, model string) []string {
				return []string{"-p", p, "-u", base, "-k", "local", "-m", model}
			}, nil, false, nil, grokIsTheOtherProduct, true, true, false},
		{"aider", "aider", "aider",
			func(p, base, model string) []string {
				return []string{"--no-git", "--yes", "--openai-api-base", base,
					"--openai-api-key", "local", "--model", "openai/" + model,
					// Every arm gets an empty home, so every arm is a first run:
					// without these aider opened its release notes in the
					// browser once per arm, on the machine running the sweep.
					"--no-analytics", "--no-browser", "--no-check-update",
					"--no-show-release-notes", "--message", p}
				// aider has no MCP client at all.
			}, nil, true, nil, nil, false, false, true},
		// Both of these were assumed to need an account and were left out for
		// it. Neither does: cline takes any OpenAI-compatible base URL, and
		// openclaw takes a provider block in its own config.
		{"cline", "cline-auto", "cline",
			func(p, _, _ string) []string { return []string{"--auto-approve", "true", p} },
			setupCline, false, nil, nil, false, true, false},
		{"openclaw", "openclaw-auto", "openclaw",
			func(p, _, model string) []string {
				return []string{"agent", "--local", "-m", p, "--model", "mock/" + model,
					// Without a session key openclaw refuses to pick one and
					// exits before reaching the model.
					"--session-key", "harnesssweep"}
			}, setupOpenClaw, false,
			func(p, _, model string) []string {
				// The same key is the same session.
				return []string{"agent", "--local", "-m", p, "--model", "mock/" + model,
					"--session-key", "harnesssweep"}
			}, nil, false, true, false},
		{"kimi", "kimi-auto", "kimi",
			func(p, _, model string) []string {
				// -p is already non-interactive; kimi refuses both --auto and
				// --yolo alongside it.
				return []string{"-p", p, "-m", "mock/" + model}
			}, setupKimi, false,
			func(p, _, model string) []string {
				return []string{"-p", p, "-c", "-m", "mock/" + model}
			}, nil, false, true, false},
	}
}

// grokIsTheOtherProduct returns a reason to skip when the `grok` on PATH is
// the community CLI rather than xAI's Grok Build. It has no hook system at
// all — only MCP — so deja's hooks sit in ~/.grok unread, and a sweep that
// does not say so reports a defect against the wrong program.
func grokIsTheOtherProduct() string {
	out, err := exec.Command("grok", "--help").CombinedOutput()
	if err != nil {
		return ""
	}
	help := strings.ToLower(string(out))
	if strings.Contains(help, "hook") {
		return ""
	}
	if strings.Contains(help, "conversational ai cli") || strings.Contains(help, "--base-url") {
		return "the `grok` on PATH is the community CLI, which has no hooks; " +
			"deja wires xAI's Grok Build"
	}
	return ""
}

func setupKimi(home, base, model string) error {
	dir := filepath.Join(home, ".kimi-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// kimi's own `provider add` imports from a models.dev-shaped registry over
	// HTTP, which a sweep would have to serve. The config it writes afterwards
	// is this, and writing it directly needs no server.
	body := fmt.Sprintf(`[providers.mock]
type = "openai"
api_key = "local"
base_url = %q

[models."mock/%s"]
provider = "mock"
model = %q
max_context_size = 128000
capabilities = [ "tool_use" ]
display_name = "Mock Model"
`, base, model, model)
	path := filepath.Join(dir, "config.toml")
	// Appended, never written over: kimi's hook lives in this same file, and
	// deja has already installed it by the time setup runs. Replacing the file
	// deleted that block, and the sweep then reported kimi as getting no
	// recall — a defect in the sweep that reads exactly like one in deja.
	old, _ := os.ReadFile(path)
	if strings.Contains(string(old), "[providers.") {
		return nil
	}
	next := string(old)
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	if next != "" {
		next += "\n"
	}
	return os.WriteFile(path, []byte(next+body), 0o644)
}

func setupCline(home, base, model string) error {
	// cline keeps provider credentials in its own store, written by its own
	// command; there is no file to drop in.
	cmd := exec.Command("cline", "auth", "-p", "openai", "-k", "local",
		"-b", base, "-m", model)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cline auth: %v: %s", err, firstLine(string(out)))
	}
	return nil
}

func setupOpenClaw(home, base, model string) error {
	dir := filepath.Join(home, ".openclaw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "openclaw.json")
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	// openai-completions, not the default: openclaw's openai provider speaks
	// the responses API, which the recording endpoint does not serve.
	cfg["models"] = map[string]any{"mode": "merge", "providers": map[string]any{
		"mock": map[string]any{
			"baseUrl": base, "apiKey": "local", "api": "openai-completions",
			"models": []any{map[string]any{"id": model, "name": "mock"}},
		},
	}}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(path, b, 0o644)
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
	// shown is whether the person running the harness was told any of this
	// happened. Memory that works and says nothing is the complaint the
	// product hears most, and it is invisible to every check that only reads
	// the request: on a real sweep the recall reached four harnesses and only
	// some of them mentioned it.
	shown bool
	// answered is whether what arrived is about the question. A harness can
	// pass every other check and still be blind: cline's plugin got a rule,
	// which is handed nothing, so it recalled whatever the session opened
	// with; gemini's prompt hook was handed the request with the previous
	// recall already inside it, so every search term came from deja's own
	// output. Both showed "recall reached the model" here.
	answered bool
	// echoed is the answer anywhere in the request, not only inside a recall
	// block. A tool result comes back as a tool result, in whatever shape the
	// protocol uses, so the stricter check would call a working MCP round trip
	// a failure.
	echoed bool
	// refused is the harness rejecting the call itself rather than deja
	// answering badly. codex declares its MCP tools inside a namespace and
	// routes none of the call shapes a mock can produce — reporting that as
	// "the tool returned nothing useful" blames deja for a call that never
	// reached it.
	refused  bool
	rejected bool
	err      string
	took     time.Duration
	// second is the follow-up turn in the same session, when the harness can
	// be resumed without a terminal. nil means it cannot.
	second *result
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
	answer := flag.String("answer", "rotates every",
		"a phrase from the corpus that answers -prompt; the recall has to carry "+
			"it, or the harness is getting memory that is not about the question")
	prompt2 := flag.String("prompt2", "why does the rate limiter reject valid traffic?",
		"a second, different question, asked in the same session")
	answer2 := flag.String("answer2", "per-process, not per-cluster",
		"a phrase from the corpus that answers -prompt2")
	toolBase := flag.String("tool-base", "",
		"a second mockmodel endpoint started with -tool-call; enables the tool arm")
	toolLog := flag.String("tool-log", "", "the request log of that second endpoint")
	toolPrompt := flag.String("tool-prompt", "Run `npm run build` and tell me what happens.",
		"the question that leads to the tool call")
	toolEvidence := flag.String("tool-evidence", "last time:",
		"what deja's PreToolUse hook should contribute for that command")
	mcpBase := flag.String("mcp-base", "",
		"a mockmodel endpoint started with -tool-call recall; enables the MCP arm")
	mcpLog := flag.String("mcp-log", "", "the request log of that endpoint")
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
		if h.wrongProduct != nil {
			if why := h.wrongProduct(); why != "" {
				fmt.Printf("%-10s SKIP  %s\n", h.name, why)
				continue
			}
		}
		withDeja := run(h, exe, *corpus, *logPath, *base, *model, *repo, *prompt, *answer, *prompt2, *answer2, true, false)
		control := run(h, exe, *corpus, *logPath, *base, *model, *repo, *prompt, *answer, "", "", false, false)
		report(h.name, withDeja, control, *answer != "", h.sessionScoped)
		if h.mcp && *mcpBase != "" && *mcpLog != "" {
			m := run(h, exe, *corpus, *mcpLog, *mcpBase, *model, *repo,
				"What did we settle about the billing gateway deploy token?",
				*answer, "", "", true, false)
			verdict := "MCP answered from inside the harness"
			switch {
			case m.requests < 2:
				verdict = "the harness never called deja's MCP tool"
			case m.refused:
				verdict = "the harness would not route the call"
			case !m.echoed:
				verdict = "MCP TOOL RETURNED NOTHING USEFUL"
			}
			fmt.Printf("%-10s %-34s %-14s (mcp)\n", "", verdict, "")
		}
		if h.toolHook && *toolBase != "" && *toolLog != "" {
			t := run(h, exe, *corpus, *toolLog, *toolBase, *model, *repo,
				*toolPrompt, *toolEvidence, "", "", true, false)
			verdict := "PreToolUse recall reached the model"
			switch {
			case t.requests < 2:
				verdict = "no tool call happened: " + firstLine(t.err)
			case !t.answered:
				verdict = "PRETOOLUSE RECALL MISSING"
			}
			fmt.Printf("%-10s %-34s %-14s (tool)\n", "", verdict, "")
		}
		// Uninstalling has to leave the harness exactly as it was found. The
		// failure this catches is not theoretical: grok's hooks survived
		// `uninstall --all` and went on calling a binary the user had just
		// removed, which is a harness that errors on every turn.
		if left, ok := checkUninstall(h, exe, *corpus, *base, *model, *repo); !ok {
			fmt.Printf("%-10s %-34s %-14s\n", "", "UNINSTALL LEFT FILES", strings.Join(left, " "))
		}
		for _, problem := range checkReinstall(h, exe, *corpus, *repo) {
			fmt.Printf("%-10s %-34s %s\n", "", "REINSTALL", problem)
		}
		if h.wrapped {
			w := run(h, exe, *corpus, *logPath, *base, *model, *repo, *prompt, *answer, "", "", true, true)
			seen := "silent"
			if w.shown {
				seen = "receipt shown"
			}
			// The wrapper is where a harness can gain a channel the bare
			// binary does not have: goose's MOIM file is re-read every turn
			// and is env-only, so only `deja goose` gets recall for the
			// question. Reporting only the receipt here hid that difference.
			verdict := ""
			switch {
			case !w.recall:
				verdict = "NO RECALL in the request"
			case *answer != "" && !w.answered:
				verdict = "session-scoped recall"
			case *answer != "":
				verdict = "recall answers the question"
			}
			fmt.Printf("%-10s %-34s %-14s (via `deja %s`)\n", "", verdict, seen, h.bin)
		}
	}
}

func run(h harness, exe, corpus, logPath, base, model, repo, prompt, answer, prompt2, answer2 string, install, wrapper bool) result {
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
		"OPENAI_BASE_PATH=v1/chat/completions",
		// Claude Code speaks the messages API, which the mock also serves, and
		// it takes the endpoint from the environment. It is the harness deja
		// is most used with and it was not in this sweep at all.
		"ANTHROPIC_BASE_URL="+strings.TrimSuffix(base, "/v1"),
		"ANTHROPIC_AUTH_TOKEN=local", "ANTHROPIC_API_KEY=local",
		"ANTHROPIC_MODEL=claude-mock", "ANTHROPIC_SMALL_FAST_MODEL=claude-mock",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		// Nothing in a sweep may open a browser on the machine running it.
		// aider's flags were not enough — it still reached for one — and
		// Python's webbrowser obeys BROWSER, so point it at a command that
		// does nothing. The rest are the per-tool switches, set as env so they
		// hold for any invocation, including the wrapper's.
		"BROWSER=true", "AIDER_ANALYTICS=false", "AIDER_ANALYTICS_DISABLE=1",
		"AIDER_CHECK_UPDATE=false", "AIDER_SHOW_RELEASE_NOTES=false",
		"DO_NOT_TRACK=1")

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
	bin, args := h.bin, h.args(prompt, base, model)
	if wrapper {
		bin, args = exe, append([]string{h.bin}, args...)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Dir = repo
	stdout, runErr := cmd.CombinedOutput()
	out.took = time.Since(t0)
	out.ran = runErr == nil
	if runErr != nil {
		out.err = firstLine(string(stdout))
	}
	out.rejected = strings.Contains(string(stdout), "System message must be at the beginning")
	out.refused = strings.Contains(string(stdout), "unsupported call")
	// The receipt as every host renders it: deja's own wording, whatever
	// framing the harness wraps around it.
	out.shown = strings.Contains(string(stdout), "deja recalled") ||
		strings.Contains(string(stdout), "deja-vu recalled") ||
		strings.Contains(string(stdout), "deja: recalled")

	sent, n := readSince(logPath, start)
	out.requests = n
	out.recall = strings.Contains(sent, "deja-recall")
	// The answer has to be inside the recall, not merely somewhere in the
	// request: the prompt itself quotes the question, and a harness that
	// echoes the corpus for other reasons would otherwise read as a pass.
	out.answered = answer != "" && strings.Contains(recallBlocks(sent), answer)
	out.echoed = answer != "" && strings.Contains(sent, answer)
	out.second = secondTurn(h, env, logPath, base, model, repo, prompt2, answer2)
	return out
}

// secondTurn continues the session the first turn opened and asks about
// something else, in the home the first turn left behind — so whatever the
// harness and deja wrote is still there.
func secondTurn(h harness, env []string, logPath, base, model, repo, prompt, answer string) *result {
	if h.resume == nil || prompt == "" {
		return nil
	}
	var out result
	start := int64(0)
	if fi, err := os.Stat(logPath); err == nil {
		start = fi.Size()
	}
	cmd := exec.Command(h.bin, h.resume(prompt, base, model)...)
	cmd.Env = env
	cmd.Dir = repo
	stdout, runErr := cmd.CombinedOutput()
	out.ran = runErr == nil
	if runErr != nil {
		out.err = firstLine(string(stdout))
	}
	sent, n := readSince(logPath, start)
	out.requests = n
	out.recall = strings.Contains(sent, "deja-recall")
	out.answered = answer != "" && strings.Contains(recallBlocks(sent), answer)
	return &out
}

// recallBlocks returns just what deja put in the request. The block is JSON
// encoded by the time it reaches the log, so the closing tag is matched in both
// spellings; an unterminated block is taken to run to the end, which is the
// reading that cannot hide a missing answer.
func recallBlocks(sent string) string {
	var b strings.Builder
	rest := sent
	for {
		i := strings.Index(rest, "deja-recall")
		if i < 0 {
			return b.String()
		}
		rest = rest[i+len("deja-recall"):]
		end := len(rest)
		for _, close := range []string{"/deja-recall", `</deja-recall`} {
			if j := strings.Index(rest, close); j >= 0 && j < end {
				end = j
			}
		}
		b.WriteString(rest[:end])
		rest = rest[end:]
	}
}

// readSince returns what was appended to the log after offset, and how many
// records that is. Reading the whole file and slicing is enough here: a sweep's
// log is small, and the alternative is holding a handle on a file another
// process is appending to.
// checkUninstall installs, uninstalls, and reports any deja file still on disk.
// Files are matched by content rather than by name: a harness config deja
// edited in place keeps its own name, and what matters is whether deja's
// command is still in it.
// checkReinstall covers the two things that happen to an install after it is
// made: it is run again, and the binary moves. Running it again must not leave
// two of anything, and moving the binary must not leave a config calling the
// old path — a hook pointing at a binary that is no longer there fails on
// every turn, and the harness reports it as deja being broken.
func checkReinstall(h harness, exe, corpus, repo string) []string {
	home, err := os.MkdirTemp("", "sweep-reinstall-"+h.name)
	if err != nil {
		return nil
	}
	defer os.RemoveAll(home)

	// A second binary at a different path, standing in for an upgrade that
	// moved it: brew to go install, a version directory, a rename.
	moved := filepath.Join(home, "moved-deja")
	if b, err := os.ReadFile(exe); err == nil {
		_ = os.WriteFile(moved, b, 0o755)
	} else {
		return nil
	}

	env := append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"DEJA_CLAUDE_ROOT="+corpus,
		"DEJA_INDEX_DIR="+filepath.Join(home, "idx"))
	install := func(bin string) {
		cmd := exec.Command(bin, "install", h.target, "--no-index")
		cmd.Env = env
		cmd.Dir = repo
		_ = cmd.Run()
	}

	install(exe)
	first := snapshot(home)
	install(exe)
	again := snapshot(home)

	var problems []string
	for path, before := range first {
		after, ok := again[path]
		if !ok {
			continue
		}
		if n, m := strings.Count(before, "hook-"), strings.Count(after, "hook-"); m > n {
			problems = append(problems, fmt.Sprintf("%s gained a second wiring on reinstall (%d then %d)",
				path, n, m))
		}
	}

	install(moved)
	for path, body := range snapshot(home) {
		if strings.Contains(body, exe) {
			problems = append(problems, path+" still calls the old binary path after it moved")
		}
	}
	return problems
}

// snapshot reads every file under home except deja's own state and index.
func snapshot(home string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() ||
			strings.HasPrefix(path, filepath.Join(home, "idx")) ||
			strings.HasPrefix(path, filepath.Join(home, ".config", "deja")) ||
			strings.HasSuffix(path, ".bak") {
			return nil
		}
		if b, rerr := os.ReadFile(path); rerr == nil {
			if rel, rerr := filepath.Rel(home, path); rerr == nil {
				out[rel] = string(b)
			}
		}
		return nil
	})
	return out
}

func checkUninstall(h harness, exe, corpus, base, model, repo string) ([]string, bool) {
	home, err := os.MkdirTemp("", "sweep-uninstall-"+h.name)
	if err != nil {
		return nil, true
	}
	defer os.RemoveAll(home)
	env := append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"DEJA_CLAUDE_ROOT="+corpus,
		"DEJA_INDEX_DIR="+filepath.Join(home, "idx"))
	deja := func(args ...string) {
		cmd := exec.Command(exe, args...)
		cmd.Env = env
		cmd.Dir = repo
		_ = cmd.Run()
	}
	deja("install", h.target, "--no-index")
	deja("uninstall", h.target)

	var left []string
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		// deja's own state directory is not residue: uninstall clears the
		// target list inside it and the file is deja's to keep.
		if err != nil || d.IsDir() ||
			strings.HasPrefix(path, filepath.Join(home, "idx")) ||
			strings.HasPrefix(path, filepath.Join(home, ".config", "deja")) {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		// The binary's own path is what a stale hook would still be calling.
		if strings.Contains(string(b), exe) || strings.Contains(string(b), "deja hook-") {
			if rel, rerr := filepath.Rel(home, path); rerr == nil {
				left = append(left, rel)
			} else {
				left = append(left, path)
			}
		}
		return nil
	})
	return left, len(left) == 0
}

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

func report(name string, withDeja, control result, wantAnswer, sessionScoped bool) {
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
	case wantAnswer && !withDeja.answered && sessionScoped:
		verdict = "session-scoped recall (no prompt channel)"
	case wantAnswer && !withDeja.answered:
		verdict = "RECALL MISSED THE QUESTION"
	}
	seen := "silent"
	if withDeja.shown {
		seen = "receipt shown"
	}
	broke := ""
	if control.ran && !withDeja.ran {
		broke = "  · deja broke a turn that worked without it"
	}
	fmt.Printf("%-10s %-34s %-14s (%d request(s), %s)%s\n", name, verdict, seen,
		withDeja.requests, withDeja.took.Round(time.Millisecond), broke)
	if s := withDeja.second; s != nil {
		follow := "recall answers the follow-up"
		switch {
		case !s.ran && s.requests == 0:
			follow = "could not resume: " + s.err
		case !s.recall:
			follow = "NO RECALL on the follow-up"
		case !s.answered:
			follow = "FOLLOW-UP GOT THE FIRST QUESTION'S MEMORY"
		}
		fmt.Printf("%-10s %-34s %-14s (turn 2)\n", "", follow, "")
	}
}
