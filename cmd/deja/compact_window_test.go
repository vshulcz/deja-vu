package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// compactFixture is a Claude Code home with one project. Each entry of
// sessions is the peak to record for one session: positive is the last call's
// context with no compaction, negative is a compaction at that many tokens
// followed by a small call, 0 is a short session.
func compactFixture(t *testing.T, sessions []int) (cfg string) {
	t.Helper()
	cfg = t.TempDir()
	proj := filepath.Join(cfg, "projects", "-repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat(`{"type":"user","message":{"role":"user","content":"`+strings.Repeat("x", 900)+`"}}`+"\n", 1300)
	usage := func(n int) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"usage":{"input_tokens":3,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":50}}}`+"\n", 1000, n-1003)
	}
	for i, peak := range sessions {
		var b strings.Builder
		switch {
		case peak > 0:
			b.WriteString(pad)
			b.WriteString(usage(peak))
		case peak < 0:
			b.WriteString(usage(30_000))
			fmt.Fprintf(&b, `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":%d,"postTokens":20000}}`+"\n", -peak)
			b.WriteString(pad) // the boundary is far from the tail
			b.WriteString(usage(40_000))
		default:
			b.WriteString(usage(20_000))
		}
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i)), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A subagent transcript above the line is not a main session.
	sub := filepath.Join(proj, "s0", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "agent-1.jsonl"), []byte(pad+usage(900_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func compactDoctor(t *testing.T, cfg string) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("DEJA_CLAUDE_MANAGED_SETTINGS", filepath.Join(cfg, "no-managed.json"))
	for _, k := range []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "DISABLE_AUTO_COMPACT", "DISABLE_COMPACT"} {
		t.Setenv(k, "")
	}
	var out bytes.Buffer
	doctorCompactWindow(&out, time.Now())
	return out.String()
}

func TestDoctorCompactWindowCountsSessionsPastTheLine(t *testing.T) {
	cfg := compactFixture(t, []int{520_000, -960_000, 300_000, -150_000, 0})
	got := compactDoctor(t, cfg)
	if !strings.Contains(got, "2 of your 5 Claude Code sessions") {
		t.Fatalf("want the no-compaction peak and the compaction peak counted, not the subagent:\n%s", got)
	}
	if !strings.Contains(got, `"autoCompactWindow": 250000`) || !strings.Contains(got, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=250000") {
		t.Fatalf("hint must name the setting and the env var:\n%s", got)
	}
}

func TestDoctorCompactWindowSilentUnderTheLine(t *testing.T) {
	cfg := compactFixture(t, []int{300_000, -150_000, 0})
	if got := compactDoctor(t, cfg); got != "" {
		t.Fatalf("no session past 400k, want nothing:\n%s", got)
	}
}

func TestDoctorCompactWindowSilentWhenAlreadyLowered(t *testing.T) {
	for name, settings := range map[string]string{
		"top-level": `{"autoCompactWindow": 300000}`,
		"per-model": `{"modelSettings": {"claude-opus-5-5": {"autoCompactWindow": 200000}}}`,
		"env block": `{"env": {"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "250000"}}`,
		"pct":       `{"env": {"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "40"}}`,
		"disabled":  `{"env": {"DISABLE_AUTO_COMPACT": "1"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := compactFixture(t, []int{520_000})
			if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(settings), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := compactDoctor(t, cfg); got != "" {
				t.Fatalf("window already lowered, want nothing:\n%s", got)
			}
		})
	}
}

func TestDoctorCompactWindowShownWhenWindowIsHigh(t *testing.T) {
	cfg := compactFixture(t, []int{520_000})
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"autoCompactWindow": 800000, "modelSettings": {"x": {"autoCompactWindow": "auto"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compactDoctor(t, cfg); !strings.Contains(got, "1 of your 1") {
		t.Fatalf("a window above 400k still gets the hint:\n%s", got)
	}
}

func TestDoctorCompactWindowEnvVarSilences(t *testing.T) {
	cfg := compactFixture(t, []int{520_000})
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	got := compactDoctor(t, cfg)
	if got == "" {
		t.Fatal("precondition: hint expected without the env var")
	}
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "250000")
	var out bytes.Buffer
	doctorCompactWindow(&out, time.Now())
	if out.Len() != 0 {
		t.Fatalf("env var at 250000, want nothing:\n%s", out.String())
	}
}

func TestMaxPreTokensAcrossReadBoundary(t *testing.T) {
	// The key and its number split across the 1 MiB read must still be seen.
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	head := strings.Repeat("y", (1<<20)-6)
	body := head + `"preTokens":987654,` + strings.Repeat("z", 400<<10) + `"usage":{"input_tokens":1}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !transcriptPassedTokens(p, int64(len(body)), compactPeakTokens) {
		t.Fatal("a preTokens record split across reads was missed")
	}
}
