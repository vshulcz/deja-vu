package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// registrySurfaces is the order the registry lists them in, and the order the
// parity table on the harnesses page prints them in.
var registrySurfaces = []string{
	"digest", "prompt", "pre_tool", "failure",
	"compaction_reset", "compaction_capture", "compaction_packet",
	"mcp", "handoff", "session_end", "rules", "statusline", "reader",
}

type surfaceClaim struct {
	Status string `json:"status"`
	Proof  string `json:"proof"`
	Note   string `json:"note"`
}

func (c surfaceClaim) claimed() bool { return c.Status == "yes" || c.Status == "partial" }

// surfaceHooks are the hook subcommands that carry each surface. A generated
// file names the subcommand it runs, so reading every file an install writes
// says which surfaces it wires without a list kept beside the installers.
// hook-antigravity is one command for every PreInvocation, and answers the
// digest, the question, a failed step and a new checkpoint (hook_antigravity.go).
// hook-codewhale is one command for message_submit, tool_call_before and
// session_end (hook_codewhale.go). hook-stop is goose's Stop block, which
// carries the edit line, the fix pair and a compaction (hook_deferred.go).
var surfaceHooks = map[string][]string{
	"digest":           {"hook-context", "hook-goose", "hook-antigravity", "hook-codewhale"},
	"prompt":           {"hook-prompt", "hook-goose-prompt", "hook-antigravity", "hook-codewhale"},
	"pre_tool":         {"hook-tool", "hook-codewhale", "hook-antigravity", "hook-stop"},
	"failure":          {"hook-tool-after", "hook-antigravity", "hook-stop"},
	"compaction_reset": {"hook-precompact", "hook-antigravity", "hook-stop"},
	"session_end":      {"hook-session-end", "hook-codewhale"},
}

// statusMarks are the calls a generated plugin makes to show something in the
// host's own UI: pi's footer, opencode 1.x and Kilo's toast, Hermes's recall
// indicator, Command Code's footer segment, Amp's status item. opencode's TUI
// plugin execs `deja statusline`.
var statusMarks = []string{"setStatus(", "showToast(", "RecallStatus", `["statusline"]`, "createStatusItem("}

// statuslineRun is a host's status line command running deja, through the
// launcher or the binary, quoted or not.
var statuslineRun = regexp.MustCompile(`deja[^\s"']*["']? statusline\b`)

// reasonixSurfaces maps the extension points deja's Reasonix extension
// subscribes to onto surfaces. The installed manifest is just "reasonix-ext",
// so the protocol's own list is what says what it does.
var reasonixSurfaces = map[string][]string{
	"session.start":      {"digest"},
	"input.receive":      {"prompt"},
	"tool.after":         {"pre_tool", "failure"},
	"compaction.prepare": {"compaction_reset"},
	"session.end":        {"session_end"},
}

var hookToken = regexp.MustCompile(`hook-[a-z]+(?:-[a-z]+)*`)

// wiredSurfaces installs a harness's targets into the test's home and reads
// back which surfaces the files they wrote reach.
func wiredSurfaces(t *testing.T, harness string) map[string]bool {
	t.Helper()
	tmp := filepath.Dir(os.Getenv("DEJA_INDEX_DIR"))
	base := harness
	if v, ok := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}[harness]; ok {
		base = v
	}
	auto := base + "-auto"
	if harness == "claude" {
		auto = "claude-auto"
	}
	for _, target := range []string{base, auto} {
		if !slices.Contains(installTargetNames(), target) {
			continue
		}
		if _, err := installTarget(target, "/bin/deja", false); err != nil {
			t.Fatalf("%s: install %s: %v", harness, target, err)
		}
	}
	var text strings.Builder
	_ = filepath.Walk(tmp, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() > 4<<20 {
			return nil
		}
		b, _ := os.ReadFile(p)
		text.Write(b)
		text.WriteByte('\n')
		return nil
	})
	// opencode writes the plugin its installed major version loads; with
	// none installed that is 2.x, and the 1.x plugin is what a 1.x user gets.
	if harness == "opencode" {
		text.WriteString(opencodeLegacyPluginJS("/bin/deja"))
	}
	all := text.String()
	tokens := map[string]bool{}
	for _, m := range hookToken.FindAllString(all, -1) {
		tokens[m] = true
	}
	got := map[string]bool{}
	for surface, subs := range surfaceHooks {
		for _, sub := range subs {
			if tokens[sub] {
				got[surface] = true
			}
		}
	}
	for _, m := range statusMarks {
		if strings.Contains(all, m) {
			got["statusline"] = true
		}
	}
	if statuslineRun.MatchString(all) {
		got["statusline"] = true
	}
	switch harness {
	case "aider":
		// No hooks: the target puts deja's context file in aider's read:
		// list, which aider re-reads on every message and `deja aider` fills.
		got["digest"] = strings.Contains(all, "aider-context.md")
	case "claude":
		// A target of its own, deliberately outside --auto.
		got["statusline"] = slices.Contains(installTargetNames(), "statusline")
	case "crush":
		// PreToolUse is Crush's only event; `hook-tool --crush` carries the
		// digest, the newest message, the previous failure and a summary
		// read from crush.db (hook_crush.go).
		if strings.Contains(all, "hook-tool --crush") {
			for _, s := range []string{"digest", "prompt", "failure", "compaction_reset"} {
				got[s] = true
			}
		}
	case "goose":
		// The SessionStart hook prints deja's status line as goose's banner.
		got["statusline"] = tokens["hook-goose"]
	case "gemini":
		// PreCompress fires on every attempt and nothing fires after one, so
		// the prompt hook catches a compaction up from the transcript.
		got["compaction_reset"] = tokens["hook-prompt"]
	case "zcode":
		// No compaction event: the prompt and tool hooks catch one up from
		// the CLI database (hook_compaction.go catchUpZCodeCompaction).
		got["compaction_reset"] = tokens["hook-prompt"]
	case "codewhale":
		// No compaction event: hook-codewhale catches one up from the history
		// CodeWhale saves before it compacts.
		got["compaction_reset"] = tokens["hook-codewhale"]
	case "commandcode":
		// No compaction shell hook: PreToolUse reads a compaction out of the
		// transcript it names (runCommandCodeTool).
		got["compaction_reset"] = tokens["hook-tool"]
	case "reasonix":
		if strings.Contains(all, "reasonix-ext") {
			for _, point := range reasonixIntercepts {
				for _, s := range reasonixSurfaces[point] {
					got[s] = true
				}
			}
			// The extension publishes its status through host/ui/publish.
			got["statusline"] = true
		}
	}
	return got
}

// The registry records each hook surface per harness, and the parity rule is
// that every one is wired or carries a proven blocker. A bool per harness
// called Cline wired with three surfaces missing, and Crush wired with one
// (#4801). This holds every surface claim to what install writes, both ways.
func TestEverySurfaceClaimMatchesWhatInstallWires(t *testing.T) {
	hermeticEnv(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID           string                  `json:"id"`
			ParserSource string                  `json:"parser_source"`
			Surfaces     map[string]surfaceClaim `json:"surfaces"`
			Capabilities *struct {
				MCP     bool   `json:"mcp"`
				Handoff string `json:"handoff"`
			} `json:"capabilities"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		checked++
		t.Run(h.ID, func(t *testing.T) {
			hermeticEnv(t)
			if len(h.Surfaces) != len(registrySurfaces) {
				t.Fatalf("surfaces lists %d entries, want exactly %v", len(h.Surfaces), registrySurfaces)
			}
			for _, s := range registrySurfaces {
				c, ok := h.Surfaces[s]
				if !ok {
					t.Fatalf("no %s entry", s)
				}
				checkSurfaceProof(t, s, c)
			}
			s := h.Surfaces
			wired := wiredSurfaces(t, h.ID)
			for _, surface := range []string{"digest", "prompt", "pre_tool", "failure", "compaction_reset", "session_end", "statusline"} {
				if wired[surface] && !s[surface].claimed() {
					t.Errorf("install wires %s, the registry says %s (%s)", surface, s[surface].Status, s[surface].Note)
				}
				if !wired[surface] && s[surface].claimed() {
					t.Errorf("the registry claims %s, nothing install writes reaches it", surface)
				}
			}
			// Capture and the packet hang off the reset: nothing captures a
			// compaction the host was never seen doing, and the packet needs a
			// later hook to ride.
			if s["compaction_capture"].claimed() && !s["compaction_reset"].claimed() {
				t.Errorf("compaction_capture is claimed without a compaction event")
			}
			if s["compaction_packet"].claimed() {
				if !s["compaction_reset"].claimed() {
					t.Errorf("compaction_packet is claimed without a compaction event")
				}
				if !s["digest"].claimed() && !s["prompt"].claimed() && !s["pre_tool"].claimed() {
					t.Errorf("compaction_packet is claimed with no later hook to carry it")
				}
			}
			if got := s["mcp"].claimed(); got != h.Capabilities.MCP {
				t.Errorf("mcp surface %s, capabilities.mcp %v", s["mcp"].Status, h.Capabilities.MCP)
			}
			want := map[string]string{"exec": "yes", "paste": "partial"}[h.Capabilities.Handoff]
			if s["handoff"].Status != want {
				t.Errorf("handoff is %s and the surface says %s, want %s", h.Capabilities.Handoff, s["handoff"].Status, want)
			}
			id := h.ID
			if v, ok := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}[id]; ok {
				id = v
			}
			if got := slices.Contains(rulesHarnesses, id); got != s["rules"].claimed() {
				t.Errorf("rules sync writes for this harness: %v, the registry says %s", got, s["rules"].Status)
			}
			// Every harness in the registry has a reader; it can lose
			// something, it cannot be missing.
			if h.ParserSource == "" || !s["reader"].claimed() {
				t.Errorf("reader is %s with parser %q", s["reader"].Status, h.ParserSource)
			}
		})
	}
	if checked < 30 {
		t.Fatalf("only %d harnesses read; the test is looking at the wrong field", checked)
	}
}

// checkSurfaceProof holds every cell to the rule: a claim says how it was
// shown, a gap says whether it is a proven blocker or work, and both say why.
func checkSurfaceProof(t *testing.T, surface string, c surfaceClaim) {
	t.Helper()
	note := strings.TrimSpace(c.Note)
	switch c.Status {
	case "yes", "partial":
		if c.Proof != "stand" && c.Proof != "fixture" {
			t.Errorf("%s is %s with proof %q, want stand or fixture", surface, c.Status, c.Proof)
		}
		if c.Status == "partial" && len(note) < 40 {
			t.Errorf("%s is partial and the note (%q) does not say what is missing", surface, note)
		}
	case "no":
		switch c.Proof {
		case "blocked":
			// A blocker is a claim about the host, so it names where in the
			// host it was found.
			if len(note) < 50 {
				t.Errorf("%s is blocked and the note (%q) does not say where in the host", surface, note)
			}
		case "todo":
			if !strings.Contains(note, "#") {
				t.Errorf("%s is todo and the note (%q) cites no issue", surface, note)
			}
		default:
			t.Errorf("%s is no with proof %q, want blocked or todo", surface, c.Proof)
		}
	default:
		t.Errorf("%s status %q is not yes, partial or no", surface, c.Status)
	}
}

// The parity table on the harnesses page is rendered from the registry, so a
// claim changed without re-running the generator shows up here rather than on
// the published page.
func TestTheParityTableIsRenderedFromTheRegistry(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "harnesses.html"))
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(page), "<!-- parity:start -->")
	j := strings.Index(string(page), "<!-- parity:end -->")
	if i < 0 || j < i {
		t.Fatal("the harnesses page has no parity markers — run `go run ./scripts/genmatrix`")
	}
	table := string(page[i:j])
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID       string                  `json:"id"`
			Surfaces map[string]surfaceClaim `json:"surfaces"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	for _, h := range reg.Harnesses {
		for s, c := range h.Surfaces {
			cell := `data-surface="` + h.ID + "/" + s + `" data-status="` + c.Status + `"`
			if !strings.Contains(table, cell) {
				t.Errorf("parity table is missing %s %s=%s — run `go run ./scripts/genmatrix`", h.ID, s, c.Status)
			}
		}
	}
}
