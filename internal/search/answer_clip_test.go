package search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// clip cuts the answer line at the last word boundary before the cap. Chinese,
// Japanese and Korean put no spaces between words, so the walk back found no
// boundary at all, fell through to the cap, and split the character sitting on
// it — an invalid byte in the `→ ` line of a recall payload an agent reads.
func TestAnswerClipCutsOnACharacter(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"chinese", strings.Repeat("重试队列在预发环境卡住了工作进程同时醒来", 12)},
		{"japanese", strings.Repeat("リトライキューがステージングで詰まりワーカーが同時に起きる", 12)},
		{"korean", strings.Repeat("재시도큐가스테이징에서멈추고워커가동시에깨어난다", 12)},
		{"latin", strings.Repeat("the retry queue stalls on staging and the workers wake together ", 8)},
		{"mixed", strings.Repeat("重试队列 stalls 工作进程同时醒来 needs jitter ", 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clip(tc.text)
			if !utf8.ValidString(got) {
				t.Errorf("clip produced invalid UTF-8: %q", got[max(0, len(got)-24):])
			}
			if len(got) > answerCap+len("…") {
				t.Errorf("clip returned %d bytes, cap is %d", len(got), answerCap)
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("the cut is not marked: %q", got[max(0, len(got)-24):])
			}
			// What comes back is a prefix of the input, mark aside: a cut
			// through a character would be the right length and the wrong text.
			if !strings.HasPrefix(tc.text, strings.TrimSuffix(got, "…")) {
				t.Errorf("clip returned text that is not a prefix of its input: %q", got)
			}
		})
	}
}

// A line inside the cap is returned whole, with no mark.
func TestAnswerClipLeavesShortLines(t *testing.T) {
	const short = "重试队列卡住了，我们加了抖动。"
	if got := clip(short); got != short {
		t.Errorf("clip(%q) = %q", short, got)
	}
}

// The word boundary still wins where there is one: Latin answers are cut at a
// space, not mid-word.
func TestAnswerClipStillPrefersAWordBoundary(t *testing.T) {
	// The cap has to land inside a word, or the cut is at a boundary by
	// accident and the test says nothing: 35 repeats is 245 bytes, and the
	// long word after them runs across byte 260.
	text := strings.Repeat("stalls ", 35) + "backoffjitterhandler spreads the wakeups over a second"
	if text[answerCap] == ' ' || text[answerCap-1] == ' ' {
		t.Fatalf("wrong fixture: byte %d is at a word boundary", answerCap)
	}
	got := strings.TrimSuffix(clip(text), "…")
	if len(got) >= len(text) {
		t.Fatalf("wrong fixture, nothing was cut")
	}
	// The character the cut landed on has to be the space between two words:
	// anything else means it landed inside one.
	if c := text[len(got)]; c != ' ' {
		t.Errorf("cut inside a word at byte %d (%q): …%q", len(got), string(c), got[max(0, len(got)-24):])
	}
	if got[len(got)-1] == ' ' {
		t.Errorf("trailing space survived: %q", got[max(0, len(got)-24):])
	}
}

// Whatever comes in, what goes out is readable. Stored text is valid UTF-8, so
// the case below is defensive — but the old cut answered it by handing the
// agent 260 unreadable bytes.
func TestAnswerClipNeverEmitsInvalidUTF8(t *testing.T) {
	for _, name := range []string{"continuation bytes", "truncated character"} {
		var in string
		switch name {
		case "continuation bytes":
			in = strings.Repeat("\x80", 300)
		default:
			in = strings.Repeat("重", 100)[:299]
		}
		got := clip(in)
		if !utf8.ValidString(got) {
			t.Errorf("%s: clip returned invalid UTF-8: %q", name, got)
		}
		if len(got) > answerCap+len("…") {
			t.Errorf("%s: clip returned %d bytes", name, len(got))
		}
	}
	// And a real line keeps its content: the guard above must not be reached
	// by ordinary text.
	real := strings.Repeat("重试队列在预发环境卡住了工作进程同时醒来", 12)
	if got := clip(real); len(got) < answerCap-8 {
		t.Errorf("an ordinary line came back as %d bytes: %q", len(got), got)
	}
}
