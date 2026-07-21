package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

func TestStatsImpactCountsAndArithmetic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	usage.RecordServedSessions(dir, usage.KindRecall, 1000, 2, false, 50000, []string{"s1", "s2"})
	usage.RecordServedSessions(dir, usage.KindRecall, 500, 1, false, 25000, []string{"s1"})
	usage.RecordResultRaw(dir, usage.KindRecall, 0, 0, true, 0) // empty result must not count
	usage.RecordResultRaw(dir, usage.KindHook, 2000, 3, false, 100000)
	usage.RecordResult(dir, usage.KindDejaVu, 100, 1, false)

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"2 agent-initiated recalls",
		"1 session starts began with project memory",
		"1 sessions recalled 2+ times",
		"1 prompts matched work",
		"50× less",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("impact output missing %q:\n%s", want, got)
		}
	}

	out.Reset()
	if err := runStatsImpact(&out, dir, true); err != nil {
		t.Fatal(err)
	}
	var r usage.ImpactReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Recalls != 2 || r.Injections != 1 || r.ReusedTwice != 1 || r.DejaVuMoments != 1 || r.ServedBytes != 3500 || r.RawBytes != 175000 {
		t.Fatalf("json report wrong: %+v", r)
	}
}

func TestStatsImpactEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := runStatsImpact(&out, filepath.Join(t.TempDir(), "index.db"), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no recall activity recorded yet") {
		t.Fatalf("empty state wrong:\n%s", out.String())
	}
}
