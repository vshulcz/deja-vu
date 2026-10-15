package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func windsurfTestRoot(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "windsurf")
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	return root
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// install windsurf writes the guidance block into Cascade's global rules, the
// skill into its skill root, and /deja as a global workflow; uninstall takes
// all three back.
func TestInstallWindsurfWritesRuleSkillAndWorkflow(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	skill := filepath.Join(root, "skills", "deja-history", "SKILL.md")
	workflow := filepath.Join(root, "global_workflows", "deja.md")

	if _, err := installWindsurf("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, rules); got != guidanceText("append") {
		t.Fatalf("global_rules.md:\n%s", got)
	}
	if got := readFileT(t, skill); got != skillFile(skillBody) {
		t.Fatalf("skill:\n%s", got)
	}
	if r, err := installCommandFile("windsurf", "/bin/deja", false); err != nil || r.Path != workflow {
		t.Fatalf("workflow = %+v, %v", r, err)
	}
	if got := readFileT(t, workflow); got != windsurfWorkflow("/bin/deja") {
		t.Fatalf("workflow:\n%s", got)
	}
	if ownGuidanceFile("windsurf") != skill || guidanceStatus("windsurf") != "written" {
		t.Fatalf("guidance status = %s", guidanceStatus("windsurf"))
	}

	// Again: nothing changes.
	r, err := installWindsurfRules(false)
	if err != nil || r.Action != "unchanged" {
		t.Fatalf("second rules install = %+v, %v", r, err)
	}
	if r, err := installSkillFile(windsurfSkillPath(), false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second skill install = %+v, %v", r, err)
	}
	if r, err := installCommandFile("windsurf", "/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second workflow install = %+v, %v", r, err)
	}

	if _, err := installWindsurf("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("windsurf", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{rules, skill, workflow} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there after uninstall", p)
		}
	}
}

// The user's own rules come back byte for byte, CRLF and all, and only deja's
// block goes on uninstall.
func TestInstallWindsurfKeepsTheUsersRules(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "# My rules\r\n\r\n- tabs, not spaces — always\r\n- café stays NFC\r\n"
	if err := os.WriteFile(rules, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installWindsurfRules(false); err != nil {
		t.Fatal(err)
	}
	got := readFileT(t, rules)
	if !strings.HasPrefix(got, strings.TrimRight(mine, "\r\n")) || !strings.Contains(got, guidanceStart+"\r\n") {
		t.Fatalf("install changed the user's rules or the line endings:\n%q", got)
	}
	if r, err := installWindsurfRules(false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %+v, %v", r, err)
	}
	if _, err := installWindsurfRules(true); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, rules); got != mine {
		t.Fatalf("uninstall did not give the file back as it was:\n%q\nwant\n%q", got, mine)
	}
}

// Cascade drops what is past 6,000 characters, and the user's rules count
// toward it. A file that would pass the cap with the block is left alone and
// install says why; doctor says the same.
func TestInstallWindsurfRefusesRulesPastTheCap(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	// Multi-byte runes: the cap is characters, not bytes, so this one fits
	// with room even though it is over 6,000 bytes.
	fits := strings.Repeat("é", windsurfRulesCap-len([]rune(guidanceText("append")))-10)
	if err := os.WriteFile(rules, []byte(fits), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := installWindsurfRules(false); err != nil || r.Action == "unchanged" || r.Note != "" {
		t.Fatalf("a file under the cap was refused: %+v, %v", r, err)
	}

	long := strings.Repeat("x", windsurfRulesCap-100) + "\n"
	if err := os.WriteFile(rules, []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := installWindsurfRules(false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" || !strings.Contains(r.Note, "over Cascade's 6000") {
		t.Fatalf("result = %+v, want unchanged with a note about the cap", r)
	}
	if got := readFileT(t, rules); got != long {
		t.Fatal("install wrote past the cap")
	}
	if s := windsurfRulesState(); !strings.HasPrefix(s, "not written") {
		t.Fatalf("doctor state = %q", s)
	}
	// The rest of the install still happens.
	res, err := installWindsurf("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, "over Cascade's 6000") {
		t.Fatalf("install result does not carry the note: %+v", res)
	}
	if _, err := os.Stat(windsurfSkillPath()); err != nil {
		t.Fatal("the skill was not written beside the refused rules")
	}
}

// A start marker with no end cannot be bounded, so install refuses and names
// the file.
func TestInstallWindsurfRulesRefusesAnUnboundedBlock(t *testing.T) {
	root := windsurfTestRoot(t)
	rules := filepath.Join(root, "memories", "global_rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules, []byte("mine\n"+guidanceStart+"\nhalf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installWindsurfRules(false); err == nil || !strings.Contains(err.Error(), rules) {
		t.Fatalf("err = %v", err)
	}
	if s := windsurfRulesState(); s != "unbounded" {
		t.Fatalf("doctor state = %q", s)
	}
}

// Cascade substitutes nothing into a workflow, so the file names the query in
// words, runs a search rather than handing the query to deja as its first
// word, and ends deja's flags before it.
func TestWindsurfWorkflowRunsASearch(t *testing.T) {
	body := commandFileText("windsurf", "/bin/deja")
	if !strings.Contains(body, `/bin/deja --framed search -- "<the text after /deja>"`) {
		t.Fatalf("workflow does not run a search:\n%s", body)
	}
	if strings.Contains(body, "$ARGUMENTS") || strings.Contains(body, `/bin/deja "`) {
		t.Fatalf("workflow carries a placeholder Cascade never fills, or the bare-query form:\n%s", body)
	}
	if !strings.HasPrefix(body, "---\ndescription: ") || !isOurCommandFile([]byte(body)) {
		t.Fatalf("workflow frontmatter:\n%s", body)
	}
}

// doctor names the rules block under the windsurf row.
func TestDoctorReportsWindsurfRules(t *testing.T) {
	windsurfTestRoot(t)
	if _, err := installWindsurf("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("2.0.0", true), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "global_rules.md: written") {
		t.Fatalf("doctor does not report the rules block:\n%s", out.String())
	}
	if s := windsurfRulesState(); s != "written" {
		t.Fatalf("state = %q", s)
	}
	if err := os.Remove(windsurfRulesPath()); err != nil {
		t.Fatal(err)
	}
	if s := windsurfRulesState(); s != "missing" {
		t.Fatalf("state = %q", s)
	}
}
