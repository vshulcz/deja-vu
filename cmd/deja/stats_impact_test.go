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
		"1 session start began with project memory",
		"1 session recalled 2+ times",
		"1 prompt matched work",
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

// TestStatsImpactAgreesWithCount covers #1586: the impact screen hardcoded
// plural nouns, so a usage log holding exactly one of each — the state a new
// user is in — rendered "1 agent-initiated recalls returned matches".
func TestStatsImpactAgreesWithCount(t *testing.T) {
	tests := []struct {
		name    string
		recalls int
		want    []string
		absent  []string
	}{
		{
			name:    "singular",
			recalls: 1,
			want: []string{
				"1 agent-initiated recall returned matches",
				"1 session start began with project memory",
				"1 prompt matched work you had already done",
			},
			absent: []string{"1 agent-initiated recalls", "1 session starts", "1 prompts"},
		},
		{
			name:    "plural",
			recalls: 2,
			want: []string{
				"2 agent-initiated recalls returned matches",
				"1 session start began with project memory",
				"1 prompt matched work you had already done",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "index.db")
			for i := 0; i < tt.recalls; i++ {
				usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"s1"})
			}
			usage.RecordResultRaw(dir, usage.KindHook, 100, 1, false, 1000)
			usage.RecordResult(dir, usage.KindDejaVu, 100, 1, false)

			var out bytes.Buffer
			if err := runStatsImpact(&out, dir, false); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("impact output missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("impact output still contains hardcoded plural %q:\n%s", absent, got)
				}
			}
		})
	}
}
