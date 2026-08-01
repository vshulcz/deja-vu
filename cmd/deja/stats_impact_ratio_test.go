package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// The frame costs more than the text when sessions are short, and calling that
// "distilled (0× less)" claims a saving that did not happen (#731).
func TestImpactLineMatchesTheRatio(t *testing.T) {
	cases := []struct {
		name          string
		served        int
		raw           int64
		want, notWant string
	}{
		{"real distillation", 4_000, 200_000, "50× less", ""},
		{"barely any saving", 4_000, 5_000, "3.9 KB from 4.9 KB", "× less"},
		{"frame costs more", 1_404, 50, "the digest frame costs more", "context distilled"},
	}
	for _, c := range cases {
		var b strings.Builder
		r := usage.ImpactReport{Recalls: 1, ServedBytes: c.served, RawBytes: c.raw}
		if err := printImpact(&b, r, false); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, b.String(), c.want)
		}
		if c.notWant != "" && strings.Contains(b.String(), c.notWant) {
			t.Errorf("%s: %q still says %q", c.name, b.String(), c.notWant)
		}
		if strings.Contains(b.String(), "(0× less)") {
			t.Errorf("%s printed an unreadable ratio: %q", c.name, b.String())
		}
	}
}
