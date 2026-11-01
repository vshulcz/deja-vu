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
	memIndex := "---\ndescription: Index frontmatter rule\ntype: rule\n---\nBody line\n"
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte(memIndex), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := LoadClaudeAutoMemory([]string{longWork})
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %#v", len(entries), entries)
	}
	if !strings.Contains(entries[0].Summary(), "Plain bullet rule") && !strings.Contains(entries[1].Summary(), "Plain bullet rule") {
		t.Fatalf("missing plain bullet rule in %#v", entries)
	}
	if !strings.Contains(entries[0].Summary(), "Index frontmatter rule — Body line") && !strings.Contains(entries[1].Summary(), "Index frontmatter rule — Body line") {
		t.Fatalf("missing combined description+body in %#v", entries)
	}
}
