package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/vshulcz/deja-vu/internal/hideconsole"
)

const kimiBinaryTimeout = 500 * time.Millisecond

// kimiSupportsHookOutputs reports whether Kimi Code on this machine is >= 2.9.0,
// which consumes SessionStart hookSpecificOutput.additionalContext and
// PostToolUse hookSpecificOutput.updatedToolOutput (#6198).
func kimiSupportsHookOutputs() bool {
	if raw := strings.TrimSpace(os.Getenv("DEJA_KIMI_VERSION")); raw != "" {
		return kimiSemverAtLeast29(raw)
	}
	bin, err := exec.LookPath("kimi")
	if err != nil || bin == "" {
		return false
	}
	if ver := kimiPackageVersionNear(bin); ver != "" {
		return kimiSemverAtLeast29(ver)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kimiBinaryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	hideconsole.Apply(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return kimiSemverAtLeast29(string(out))
}

func kimiPackageVersionNear(bin string) string {
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		resolved = bin
	}
	dir := filepath.Dir(resolved)
	for range 5 {
		candidates := []string{
			filepath.Join(dir, "package.json"),
			filepath.Join(dir, "node_modules", "@moonshot-ai", "kimi-code", "package.json"),
			filepath.Join(dir, "lib", "node_modules", "@moonshot-ai", "kimi-code", "package.json"),
		}
		for _, p := range candidates {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var pkg struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if json.Unmarshal(b, &pkg) == nil && pkg.Name == "@moonshot-ai/kimi-code" && pkg.Version != "" {
				return pkg.Version
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func kimiSemverAtLeast29(raw string) bool {
	major, minor, _, ok := parseKimiSemver(raw)
	if !ok {
		return false
	}
	return major > 2 || (major == 2 && minor >= 9)
}

func parseKimiSemver(s string) (major, minor, patch int, ok bool) {
	for i := 0; i < len(s); i++ {
		if !unicode.IsDigit(rune(s[i])) {
			continue
		}
		// Require a word boundary so hashes or dates glued to letters don't match.
		if i > 0 && (unicode.IsLetter(rune(s[i-1])) || unicode.IsDigit(rune(s[i-1]))) {
			continue
		}
		j := i
		for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
			j++
		}
		parts := strings.Split(s[i:j], ".")
		if len(parts) < 2 {
			i = j
			continue
		}
		maj, err1 := strconv.Atoi(parts[0])
		min, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			i = j
			continue
		}
		var pat int
		if len(parts) >= 3 && parts[2] != "" {
			pat, _ = strconv.Atoi(parts[2])
		}
		return maj, min, pat, true
	}
	return 0, 0, 0, false
}

// runKimiHook handles Kimi Code's version-gated SessionStart and PostToolUse
// hooks while keeping <= 2.8.x behavior intact on older installs.
func runKimiHook(dir, name string, rest []string, cmd command) error {
	clean, _ := withoutFlag(rest, "--kimi")
	if stdinIsTerminal() {
		return cmd(dir, clean)
	}
	switch name {
	case "hook-context":
		return runKimiContextHook(dir, clean, cmd)
	case "hook-tool-after":
		return runKimiToolAfterHook(dir, clean, cmd)
	default:
		return runHookDeferred(dir, name, clean, cmd)
	}
}

func runKimiToolAfterHook(dir string, rest []string, cmd command) error {
	raw := readHookStdin()
	key, _ := readDeferredPayload(raw)
	var payload struct {
		HookEventName string          `json:"hook_event_name"`
		ToolName      string          `json:"tool_name"`
		LLMToolName   string          `json:"llm_tool_name"`
		IsError       bool            `json:"is_error"`
		ToolOutput    json.RawMessage `json:"tool_output"`
		ToolOutputCam json.RawMessage `json:"toolOutput"`
	}
	_ = json.Unmarshal(raw, &payload)
	toolName := adoptGrok(payload.ToolName, payload.LLMToolName)
	toolOutput, hasOutput := parseKimiToolOutput(payload.ToolOutput, payload.ToolOutputCam)

	var out []byte
	err := withStdin(raw, func() error {
		var cerr error
		out, cerr = captureDeferredStdout(func() error { return cmd(dir, rest) })
		return cerr
	})
	afterText := hookOutputContext(out)

	// Kimi >= 2.9 passes output.slice(0, 2000) in UTF-16 code units; replacing at
	// the 2000-unit boundary would drop the tail of longer tool results (#6198).
	canInline := kimiSupportsHookOutputs() &&
		(payload.HookEventName == "" || payload.HookEventName == "PostToolUse") &&
		!payload.IsError &&
		(toolName == "Edit" || toolName == "Write") &&
		hasOutput &&
		len(utf16.Encode([]rune(toolOutput))) < 2000
	if !canInline {
		if afterText != "" {
			deferText(dir, key, "hook-tool-after", afterText)
		}
		return err
	}

	note := joinKimiRecallParts(takeDeferred(dir, key), afterText)
	if note == "" {
		return err
	}
	combined := note
	if toolOutput != "" {
		combined = toolOutput + "\n\n" + note
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			UpdatedToolOutput string `json:"updatedToolOutput"`
		} `json:"hookSpecificOutput"`
	}
	resp.HookSpecificOutput.HookEventName = "PostToolUse"
	resp.HookSpecificOutput.UpdatedToolOutput = combined
	b, merr := json.Marshal(resp)
	if merr != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(b))
	return err
}

func parseKimiToolOutput(candidates ...json.RawMessage) (string, bool) {
	for _, raw := range candidates {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '"' {
			continue
		}
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s, true
		}
	}
	return "", false
}

func joinKimiRecallParts(parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}

func runKimiContextHook(dir string, rest []string, cmd command) error {
	if !kimiSupportsHookOutputs() {
		return nil
	}
	raw := readHookStdin()
	key, event := readDeferredPayload(raw)
	args := rest
	if !hasFlag(args, "--once") {
		args = append(append([]string(nil), args...), "--once")
	}
	var out []byte
	err := withStdin(raw, func() error {
		var cerr error
		out, cerr = captureDeferredStdout(func() error { return cmd(dir, args) })
		return cerr
	})
	plain := hasFlag(args, "--plain")
	if len(bytes.TrimSpace(out)) > 0 || hasDeferredFor(dir, key) {
		_, _ = os.Stdout.Write(withDeferred(out, takeDeferred(dir, key), plain, claudeEventName(event, "hook-context")))
	}
	return err
}
