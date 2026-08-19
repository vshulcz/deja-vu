package digest

import (
	"strings"
	"testing"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

func hostileSession() model.Session {
	return model.Session{
		Harness: "claude",
		Project: "app\x1b[31mred\x1b[0m",
		ID:      "x1\nfake",
		Messages: []model.Message{
			{Role: "user", Text: "the retry queue stalls on staging and every worker wakes at once"},
			{Role: "assistant", Text: "We spread the wakeups over a second. The jitter is bounded by the poll interval."},
		},
	}
}

// The share document's header is markdown: "# deja share: <id>" and
// "- Project: <name>". Both fields are text deja did not author — a directory
// name and a harness-assigned id — and a break in either split the header in
// two, leaving the stray half standing as a line of the document.
func TestShareHeaderFieldsStayOnTheirLines(t *testing.T) {
	out := Share(hostileSession(), 4000)
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "# deja share: ") {
		t.Fatalf("no header: %q", lines[0])
	}
	if strings.Contains(lines[0], "\n") || lines[1] != "" {
		t.Errorf("the header spilled: %q / %q", lines[0], lines[1])
	}
	if !strings.Contains(lines[0], "fake") {
		t.Errorf("the id lost its tail rather than joining it: %q", lines[0])
	}
	var project string
	for _, l := range lines {
		if strings.HasPrefix(l, "- Project: ") {
			project = l
		}
	}
	if project == "" {
		t.Fatalf("no project line: %q", out)
	}
	for _, r := range project {
		if unicode.IsControl(r) {
			t.Errorf("a control character reached the project line: %q in %q", r, project)
			break
		}
	}
}

// The handoff framing strips the share header by cutting at the first newline.
// An id carrying one left the rest of that id standing at the top of what
// another agent is handed.
func TestHandoffDropsTheWholeShareHeader(t *testing.T) {
	out := Handoff(hostileSession(), 4000)
	if strings.Contains(out, "deja share") {
		t.Errorf("the share header survived into the handoff: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "fake" {
			t.Errorf("the tail of the id is standing on its own line:\n%s", out)
		}
	}
	first := strings.SplitN(out, "\n", 2)[0]
	for _, r := range first {
		if unicode.IsControl(r) {
			t.Errorf("a control character reached the framing line: %q in %q", r, first)
			break
		}
	}
}

// A plain session is unchanged.
func TestShareHeaderUnchangedForPlainFields(t *testing.T) {
	s := hostileSession()
	s.Project, s.ID = "app", "x1"
	out := Share(s, 4000)
	if !strings.HasPrefix(out, "# deja share: x1\n\n- Project: app\n") {
		t.Errorf("plain header changed: %q", strings.SplitN(out, "\n\n", 2)[0])
	}
}

// The id in the handoff's closing advice is meant to be typed back, and the
// lookup matches on a prefix: the head of an id with a break in it finds the
// session, the joined form matches nothing.
func TestHandoffAdviceIDIsTypeable(t *testing.T) {
	s := hostileSession()
	s.ID = "x1abcdef\nfake"
	out := Handoff(s, 4000)
	if !strings.Contains(out, "deja show x1abcdef`") {
		t.Errorf("the advice does not name a usable id:\n%s", out)
	}
	if strings.Contains(out, "x1abcdef fake`") {
		t.Errorf("the advice names the joined form, which matches nothing:\n%s", out)
	}
}
