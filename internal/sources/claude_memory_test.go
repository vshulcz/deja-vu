package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadClaudeAutoMemoryOrdersRulesFirstAndDedupsIndex(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude", "projects")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)

	work := "/tmp/tuip/payments"
	memDir := filepath.Join(claudeRoot, claudeEncodePath(work), "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, content string, mtime time.Time) {
		t.Helper()
		p := filepath.Join(memDir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	write("reference_dashboard.md", "---\nname: dashboard\ndescription: Grafana dashboard link for payments\ntype: reference\n---\nhttps://grafana.internal/d/pay\n", now)
	write("user_role.md", "---\nname: role\ndescription: User is a backend payments engineer\ntype: user\n---\nBackend focus.\n", now.Add(-time.Minute))
	write("project_cutover.md", "---\nname: cutover\ndescription: Stripe cutover freeze starts Friday\ntype: project\n---\nNo schema migrations after Thursday.\n", now.Add(-2*time.Minute))
	write("feedback_retry.md", "---\nname: retry-backoff\ndescription: Retries in payments use internal/retry.Backoff; a hand-rolled sleep loop is rejected in review (zebra-quartz-17)\ntype: feedback\n---\nUse internal/retry.Backoff for all payment retries.\n", now.Add(-3*time.Minute))
	write("vetoed.md", "---\nname: vetoed\ndescription: Do not hand this rule on (veto-marker-99)\ntype: feedback\ndeja: false\n---\nIgnored.\n", now)
	write("MEMORY.md", "# Memory Index\n- [Retry Backoff](feedback_retry.md) — duplicate index description\n- [Standalone Note](missing.md) — Standalone index note kept when target file is absent\n", now)

	entries := LoadClaudeAutoMemory([]string{work})
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5: %#v", len(entries), entries)
	}
	if entries[0].File != "feedback_retry.md" {
		t.Fatalf("first entry = %q, want feedback_retry.md (rules first)", entries[0].File)
	}
	if !strings.Contains(entries[0].Summary(), "zebra-quartz-17") {
		t.Fatalf("first entry summary missing zebra-quartz-17: %q", entries[0].Summary())
	}
	if entries[1].File != "project_cutover.md" {
		t.Fatalf("second entry = %q, want project_cutover.md", entries[1].File)
	}
	if entries[2].File != "user_role.md" {
		t.Fatalf("third entry = %q, want user_role.md", entries[2].File)
	}
	if entries[3].File != "reference_dashboard.md" {
		t.Fatalf("fourth entry = %q, want reference_dashboard.md", entries[3].File)
	}
	if entries[4].File != "MEMORY.md" || !strings.Contains(entries[4].Summary(), "Standalone index note") {
		t.Fatalf("fifth entry = %#v, want standalone MEMORY.md line", entries[4])
	}
	for _, e := range entries {
		if strings.Contains(e.Summary(), "veto-marker-99") || strings.Contains(e.Summary(), "duplicate index description") {
			t.Fatalf("unexpected vetoed or duplicate entry in %q: %q", e.File, e.Summary())
		}
	}
}

func TestLoadClaudeAutoMemoryEdgeCases(t *testing.T) {
	if got := LoadClaudeAutoMemory(nil); len(got) != 0 {
		t.Fatalf("LoadClaudeAutoMemory(nil) = %v, want empty", got)
	}
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude", "projects")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)

	longWork := "/private/tmp/" + strings.Repeat("segment-", 30) + "app"
	enc := claudeEncodePath(longWork)
	memDir := filepath.Join(claudeRoot, enc[:claudeFolderNameMax]+"-abc123", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// File without frontmatter, with code fence and bullet.
	plainMd := "# Heading\n```sh\necho skip\n```\n- Plain bullet rule from body\n"
	if err := os.WriteFile(filepath.Join(memDir, "rule_plain.md"), []byte(plainMd), 0o644); err != nil {
		t.Fatal(err)
	}
	// File vetoed by state: rejected.
	rejectedMd := "---\ndescription: Rejected rule\nstate: rejected\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(memDir, "rejected.md"), []byte(rejectedMd), 0o644); err != nil {
		t.Fatal(err)
	}
	// Empty file skipped.
	if err := os.WriteFile(filepath.Join(memDir, "empty.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// MEMORY.md with frontmatter description.
	memIndex := "---\ndescription: Index frontmatter rule\nmetadata:\n  type: rule\n---\nBody line\n"
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte(memIndex), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := LoadClaudeAutoMemory([]string{longWork})
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %#v", len(entries), entries)
	}
	if entries[0].Type != "rule" {
		t.Fatalf("nested metadata.type was not parsed as rule: %#v", entries[0])
	}
	if !strings.Contains(entries[0].Summary(), "Plain bullet rule") && !strings.Contains(entries[1].Summary(), "Plain bullet rule") {
		t.Fatalf("missing plain bullet rule in %#v", entries)
	}
	if !strings.Contains(entries[0].Summary(), "Index frontmatter rule — Body line") && !strings.Contains(entries[1].Summary(), "Index frontmatter rule — Body line") {
		t.Fatalf("missing combined description+body in %#v", entries)
	}
}

func TestParseClaudeAutoMemoryFileFullBodyAndRedaction(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude", "projects")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(claudeRoot, claudeEncodePath(proj), "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}

	topicPath := filepath.Join(memDir, "deploy_notes.md")
	topicContent := strings.Join([]string{
		"---",
		"name: deploy-notes",
		"description: Production deployment constraints",
		"type: project",
		"---",
		"First paragraph mentions StagingRolloutGate.",
		"",
		"Second paragraph holds deep token sk-ant-api03-1234567890abcdefghij1234567890abcdefghij and keyword MidnightCanary.",
	}, "\n")
	if err := os.WriteFile(topicPath, []byte(topicContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sibling MEMORY.md: link to existing deploy_notes.md should be skipped,
	// while standalone prose stays indexed.
	indexPath := filepath.Join(memDir, "MEMORY.md")
	indexContent := "# Memory Index\n- [Deploy](deploy_notes.md) — duplicate summary\n- Standalone index fact OrangeFalcons\n"
	if err := os.WriteFile(indexPath, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pure link index MEMORY.md in another project should yield zero sessions.
	otherMemDir := filepath.Join(claudeRoot, "-Users-alex-other", "memory")
	if err := os.MkdirAll(otherMemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherMemDir, "rule.md"), []byte("Keep strict checks."), 0o644); err != nil {
		t.Fatal(err)
	}
	pureIndexPath := filepath.Join(otherMemDir, "MEMORY.md")
	if err := os.WriteFile(pureIndexPath, []byte("# Index\n- [Rule](rule.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Vetoed and binary files return no session.
	vetoedPath := filepath.Join(memDir, "vetoed.md")
	if err := os.WriteFile(vetoedPath, []byte("---\nstate: rejected\n---\nSecret ignored body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(memDir, "binary.md")
	if err := os.WriteFile(binaryPath, []byte("header\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := ClaudeAutoMemoryFiles()
	if len(files) != 6 {
		t.Fatalf("ClaudeAutoMemoryFiles got %d files, want 6: %v", len(files), files)
	}
	for _, f := range files {
		if !IsClaudeAutoMemoryFile(f) {
			t.Fatalf("IsClaudeAutoMemoryFile(%q) = false, want true", f)
		}
	}
	if IsClaudeAutoMemoryFile(filepath.Join(filepath.Dir(memDir), "transcript.jsonl")) {
		t.Fatal("IsClaudeAutoMemoryFile unexpectedly matched .jsonl transcript")
	}

	ss, err := ParseClaudeAutoMemoryFile(topicPath)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseClaudeAutoMemoryFile(topic) = %#v, err=%v", ss, err)
	}
	s := ss[0]
	if s.Harness != "claude" || s.Project != "work/payments" || s.Path != topicPath {
		t.Fatalf("unexpected session metadata: %+v", s)
	}
	if len(s.Messages) != 1 || s.Messages[0].Role != "assistant" {
		t.Fatalf("unexpected messages: %#v", s.Messages)
	}
	txt := s.Messages[0].Text
	if !strings.Contains(txt, "StagingRolloutGate") || !strings.Contains(txt, "MidnightCanary") {
		t.Fatalf("multi-paragraph body missing in indexed message: %q", txt)
	}
	if strings.Contains(txt, "sk-ant-api03-1234567890abcdefghij") {
		t.Fatalf("secret token was not redacted: %q", txt)
	}

	idxSS, err := ParseClaudeAutoMemoryFile(indexPath)
	if err != nil || len(idxSS) != 1 {
		t.Fatalf("ParseClaudeAutoMemoryFile(MEMORY.md with standalone note) = %#v, err=%v", idxSS, err)
	}
	idxText := idxSS[0].Messages[0].Text
	if strings.Contains(idxText, "duplicate summary") || !strings.Contains(idxText, "OrangeFalcons") {
		t.Fatalf("MEMORY.md filtering unexpected: %q", idxText)
	}

	if pureSS, err := ParseClaudeAutoMemoryFile(pureIndexPath); err != nil || len(pureSS) != 0 {
		t.Fatalf("pure link MEMORY.md = %#v, err=%v, want empty", pureSS, err)
	}
	if vSS, err := ParseClaudeAutoMemoryFile(vetoedPath); err != nil || len(vSS) != 0 {
		t.Fatalf("vetoed note = %#v, err=%v, want empty", vSS, err)
	}
	if bSS, err := ParseClaudeAutoMemoryFile(binaryPath); err != nil || len(bSS) != 0 {
		t.Fatalf("binary note = %#v, err=%v, want empty", bSS, err)
	}
}
