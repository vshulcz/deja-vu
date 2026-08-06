package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// Each of these surfaces prints transcript text. Before this they each brought
// their own filter and each covered a different subset: the status bar stripped
// everything, snippets stripped CSI only, and show, share and last stripped
// nothing at all.
const ctrlProbeUser = "control probe \x1b[2J\x1b[H\x1b[31mCSIPAYLOAD\x1b[0m \x1b]0;OSCPAYLOAD\x07 realtext\rSPOOFEDLINE bell\x07here"

const ctrlProbeAssistant = "reply passwd\x08\x08\x08\x08\x08\x08SPOOF safe \u202egnp.exe\u202c tail vert\x0btab"

// actedOn matches what a terminal executes instead of printing.
var actedOn = regexp.MustCompile("[\x00-\x08\x0b-\x1f\x7f\u202a-\u202e\u2066-\u2069]")

func ctrlProbeSession() model.Session {
	when := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	return model.Session{
		ID: "ctrlsess", Harness: "claude", Project: "tmp/app", Started: when, Updated: when,
		Messages: []model.Message{
			{Role: "user", Text: ctrlProbeUser, Time: when},
			{Role: "assistant", Text: ctrlProbeAssistant, Time: when},
		},
	}
}

func TestTranscriptControlCharactersDoNotReachAnySurface(t *testing.T) {
	s := ctrlProbeSession()

	var show, ctxOut bytes.Buffer
	search.PrintSession(&show, s)
	search.PrintContext(&ctxOut, s, "control probe")

	surfaces := map[string]string{
		"deja show (search.PrintSession)": show.String(),
		"deja share (digest)":             digest.Share(s, 4000),
		"deja handoff (digest)":           digest.Handoff(s, 4000),
		"deja last / stats title":         stats.Title(s),
		"deja ctx (search.PrintContext)":  ctxOut.String(),
		"snippet (search.Snippet)":        search.Snippet(ctrlProbeUser, "control probe"),
	}
	for name, out := range surfaces {
		if loc := actedOn.FindStringIndex(out); loc != nil {
			a := loc[0] - 30
			if a < 0 {
				a = 0
			}
			b := loc[1] + 30
			if b > len(out) {
				b = len(out)
			}
			t.Errorf("%s prints %q, which a terminal acts on rather than prints — near %q",
				name, out[loc[0]:loc[1]], out[a:b])
		}
		// The payload text is data and must still be readable; only the
		// sequences go.
		if !strings.Contains(out, "CSIPAYLOAD") {
			t.Errorf("%s lost the text along with the escapes: %q", name, out)
		}
	}
}
