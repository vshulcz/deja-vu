package main

import (
	"strings"
	"testing"
)

// Two dozen targets differ by a few characters, so a bare "unknown target"
// left someone who typed `claud` with nowhere to go — while `deja completion`
// has always listed its three valid values.
func TestUnknownTargetSuggestsWhatWasMeant(t *testing.T) {
	for typed, want := range map[string]string{
		"claud":   "claude-code",
		"opencde": "opencode",
		"cursorr": "cursor",
		"gemin":   "gemini",
		"qwn":     "qwen",
	} {
		got := unknownTargetError(typed).Error()
		if !strings.Contains(got, want) {
			t.Errorf("install %q should suggest %q: %s", typed, want, got)
		}
	}
	// Nothing close: list what would work rather than guessing.
	full := unknownTargetError("zzzz").Error()
	if strings.Contains(full, "did you mean") {
		t.Errorf("no near match should mean no guess: %s", full)
	}
	for _, want := range []string{"claude-code", "codex", "--all"} {
		if !strings.Contains(full, want) {
			t.Errorf("the list should name %q: %s", want, full)
		}
	}
}

func TestEditDistance(t *testing.T) {
	cases := map[[2]string]int{
		{"", ""}: 0, {"a", ""}: 1, {"", "ab"}: 2,
		{"cursor", "cursor"}: 0, {"cursorr", "cursor"}: 1,
		{"qwen", "qwn"}: 1, {"kitten", "sitting"}: 3,
	}
	for in, want := range cases {
		if got := editDistance(in[0], in[1]); got != want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", in[0], in[1], got, want)
		}
	}
}
