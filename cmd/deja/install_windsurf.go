package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// windsurfMCPPath is Cascade's MCP file, beside the conversations under the
// Windsurf / Devin Desktop config root, in the {"mcpServers": …} shape. Not
// the Code OSS-level User/mcp.json that `devin-desktop --add-mcp` writes:
// Cascade does not read that one (#308).
func windsurfMCPPath() string {
	return filepath.Join(sources.WindsurfRoot(), "mcp_config.json")
}

// windsurfRulesPath is Cascade's global rules file, always in context
// (docs.devin.ai/desktop/cascade/memories). It is the user's own file, so deja
// adds a marked block and leaves the rest alone.
func windsurfRulesPath() string {
	return filepath.Join(sources.WindsurfRoot(), "memories", "global_rules.md")
}

// windsurfRulesCap is Cascade's limit on a rules file, in characters. What is
// past it does not reach the model, and the user's own rules count toward it.
const windsurfRulesCap = 6000

// windsurfSkillPath is the global skill root Cascade reads
// (docs.devin.ai/desktop/cascade/skills), invoked on a description match or
// as @deja-history.
func windsurfSkillPath() string {
	return filepath.Join(sources.WindsurfRoot(), "skills", "deja-history", "SKILL.md")
}

// windsurfWorkflowPath is the global workflow root; a file there runs as
// /<name> (docs.devin.ai/desktop/cascade/workflows).
func windsurfWorkflowPath() string {
	return filepath.Join(sources.WindsurfRoot(), "global_workflows", "deja.md")
}

// windsurfWorkflow is the /deja workflow. Cascade substitutes nothing into a
// workflow — no $ARGUMENTS — so the query is named in words and the model
// fills it in from what followed /deja.
func windsurfWorkflow(exe string) string {
	return markdownCommandWith(exe, "<the text after /deja>")
}

// Cascade hooks (~/.codeium/windsurf/hooks.json) are not wired: none of their
// output reaches the model. Exit 2 only blocks, and show_output is shown in
// the UI and is not offered for pre_user_prompt
// (docs.devin.ai/desktop/cascade/hooks). The rule and the skill are what tell
// Cascade to call the MCP tool instead.
func installWindsurf(exe string, uninstall bool) (installResult, error) {
	mcp, err := installMCPJSON(windsurfMCPPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	rules, err := installWindsurfRules(uninstall)
	if err != nil {
		return installResult{}, err
	}
	skill, err := installSkillFile(windsurfSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, rules, skill), nil
}

// installWindsurfRules writes deja's guidance block into global_rules.md, the
// same block AGENTS.md hosts get, or takes it out. A file that would pass the
// cap with the block in it is left as it is, with a note: a block Cascade cuts
// off half-way, or one that pushes the user's own rules past the cut, is worse
// than none, and the skill still carries the guidance.
func installWindsurfRules(uninstall bool) (installResult, error) {
	path := windsurfRulesPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	next, err := updateGuidanceBlock(string(old), uninstall)
	if err != nil {
		return installResult{}, markerErrorFor(path, err)
	}
	if uninstall {
		if len(old) == 0 {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if strings.TrimSpace(next) == "" {
			if rerr := os.Remove(path); rerr != nil {
				return installResult{}, rerr
			}
			pruneCreatedDir(filepath.Dir(path))
			return installResult{Path: path, Action: "removed"}, nil
		}
		a, werr := writeIfChanged(path, old, []byte(next))
		return installResult{Path: path, Action: a}, werr
	}
	if n := utf8.RuneCountInString(next); n > windsurfRulesCap {
		return installResult{Path: path, Action: "unchanged", Note: windsurfRulesOverNote(n)}, nil
	}
	a, err := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, err
}

func windsurfRulesOverNote(n int) string {
	return fmt.Sprintf("would be %d characters with deja's block, over Cascade's %d — left as it is; the deja-history skill still tells Cascade to recall",
		n, windsurfRulesCap)
}

// windsurfRulesState is doctor's word on the block: written, over the cap, or
// missing.
func windsurfRulesState() string {
	b, err := os.ReadFile(windsurfRulesPath())
	if err != nil && !os.IsNotExist(err) {
		return "unreadable"
	}
	next, err := updateGuidanceBlock(string(b), false)
	if err != nil {
		return "unbounded"
	}
	if strings.Contains(string(b), guidanceStart) {
		return "written"
	}
	if n := utf8.RuneCountInString(next); n > windsurfRulesCap {
		return fmt.Sprintf("not written: %d characters with deja's block, over Cascade's %d", n, windsurfRulesCap)
	}
	return "missing"
}
