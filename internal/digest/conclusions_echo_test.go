package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The last message is always a candidate conclusion, and it was served
// whatever it said. On a 2,285-session store 132 of the lines served as what a
// session settled were deja's own recall echoed back ("deja-vu recalled: …"),
// which is how an agent's earlier answer from memory came back as a fact, and
// 104 were an acknowledgement — "ok", "done", "No response requested."
func TestConclusionsSkipTheRecallEchoAndAcknowledgements(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "why does the nightly export stall"},
		{Role: "assistant", Text: "Root cause: the exporter holds the table lock while it uploads, so the vacuum job waits. Fixed by releasing the lock before the upload."},
		{Role: "assistant", Text: "deja-vu recalled: nightly export stall — confirmed the lock is held for 47 minutes."},
		{Role: "assistant", Text: "ok"},
	}}
	got := Conclusions(s, 800, 3)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "recalled") || strings.Contains(joined, "47 minutes") {
		t.Errorf("deja's own recall was served as a conclusion:\n%s", joined)
	}
	for _, line := range got {
		if strings.TrimSpace(line) == "ok" {
			t.Errorf("an acknowledgement was served as a conclusion: %q", got)
		}
	}
	if !strings.Contains(joined, "Root cause") {
		t.Errorf("the session's real conclusion is missing: %q", got)
	}

	// The echo as the last word, which is where the real ones sit: a session
	// that asked deja and answered with what came back.
	s.Messages = s.Messages[:3]
	got = Conclusions(s, 800, 3)
	joined = strings.Join(got, "\n")
	if strings.Contains(joined, "recalled") || strings.Contains(joined, "47 minutes") {
		t.Errorf("deja's own recall, as the last message, was served as a conclusion:\n%s", joined)
	}
	if !strings.Contains(joined, "Root cause") {
		t.Errorf("the session's real conclusion is missing: %q", got)
	}
}

// The credit line is deja's words, the rest of the message is the agent's. A
// reply that opens with the credit and then does the work keeps the work.
func TestConclusionsKeepTheWorkAfterACreditLine(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "the golden test never runs"},
		{Role: "assistant", Text: "déjà vu: the golden test is gated by a build tag — reusing it (deja:8ab41c37).\n\nFixed: SVC_FIXTURES=$PWD/fixtures GOFLAGS_EXTRA=\"-tags golden\" make test passes, ok example.com/svc/internal/store."},
	}}
	got := Conclusions(s, 800, 2)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "déjà vu:") {
		t.Errorf("the credit line was served as the conclusion: %q", got)
	}
	if !strings.Contains(joined, "make test passes") {
		t.Errorf("the work after the credit line was dropped: %q", got)
	}
}
