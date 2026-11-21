package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func buildLargeLineageStore(t *testing.T, sharedTurns, divergedTurns int) (dir string, forkPoint, forkStarted time.Time) {
	t.Helper()
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	pad := strings.Repeat("alpha beta gamma delta epsilon ", 240) // ~7.4 KB compressible payload
	turn := func(role, text string, step int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(step) * time.Second)}
	}
	base := make([]model.Message, 0, sharedTurns+3)
	base = append(base,
		turn("user", "retry loop spins on timeout", 0),
		turn("assistant", "backoff never resets", 1),
	)
	for i := range sharedTurns {
		role := "tool-output"
		if i%5 == 0 {
			role = "assistant"
		}
		base = append(base, turn(role, fmt.Sprintf("turn-%04d %s", i, pad), 2+i))
	}
	forkPoint = base[len(base)-1].Time

	askerMsgs := append(slices.Clone(base), turn("user", "asker continued work", 2+sharedTurns+1))
	copyMsgs := append(slices.Clone(base),
		turn("assistant", "No response requested.", 2+sharedTurns+2),
		turn("summary", "compacted summary frame", 2+sharedTurns+3),
	)
	forkMsgs := slices.Clone(base)
	for i := range divergedTurns {
		forkMsgs = append(forkMsgs, turn("assistant", fmt.Sprintf("fork-work-%04d %s", i, pad), 2+sharedTurns+10+i))
	}
	siblingMsgs := slices.Clone(base)
	for i := range divergedTurns {
		siblingMsgs = append(siblingMsgs, turn("user", fmt.Sprintf("sibling-work-%04d %s", i, pad), 2+sharedTurns+1000+i))
	}
	crossHarness := append(slices.Clone(base), turn("assistant", "cross-harness branch", 2+sharedTurns+2000))

	forkStarted = at.Add(time.Duration(2+sharedTurns+3000) * time.Second)
	rewrittenEndedParent := []model.Message{
		turn("user", "original root ask", 0),
		turn("assistant", "original root answer", 1),
		{Role: roleFiles, Text: "file.go", Time: forkStarted.Add(time.Hour)},
		{Role: roleWrote, Text: "wrote file.go", Time: forkStarted.Add(time.Hour)},
		{Role: roleEdit, Text: "edit file.go", Time: forkStarted.Add(time.Hour)},
	}
	rewrittenOngoingParent := []model.Message{
		turn("user", "ongoing parent ask", 0),
		turn("assistant", "ongoing parent reply", 1),
		{Role: "assistant", Text: "work after fork started", Time: forkStarted.Add(time.Minute)},
	}
	rewrittenForkEnded := model.Session{
		Harness: "hermes", ID: "rewritten-fork-ended", Project: "p", Kind: "fork", Parent: "ended-parent",
		Messages: []model.Message{
			{Role: "user", Text: "copied with new timestamps", Time: forkStarted},
		},
	}
	rewrittenForkOngoing := model.Session{
		Harness: "hermes", ID: "rewritten-fork-ongoing", Project: "p", Kind: "fork", Parent: "ongoing-parent",
		Messages: []model.Message{
			{Role: "user", Text: "copied while parent continued", Time: forkStarted},
		},
	}

	ss := []model.Session{
		{Harness: "claude", ID: "asker", Project: "p", Messages: askerMsgs},
		{Harness: "claude", ID: "copy-with-noise", Project: "p", Messages: copyMsgs},
		{Harness: "claude", ID: "fork-diverged", Project: "p", Kind: "fork", Parent: "asker", Messages: forkMsgs},
		{Harness: "claude", ID: "sibling-diverged", Project: "p", Messages: siblingMsgs},
		{Harness: "codex", ID: "other-harness", Project: "p", Messages: crossHarness},
		{Harness: "hermes", ID: "ended-parent", Project: "p", Messages: rewrittenEndedParent},
		rewrittenForkEnded,
		{Harness: "hermes", ID: "ongoing-parent", Project: "p", Messages: rewrittenOngoingParent},
		rewrittenForkOngoing,
		{Harness: "claude", ID: "sub-child", Project: "p", Kind: "subagent", Parent: "asker", Messages: []model.Message{turn("user", "sub work", 5)}},
	}
	dir = filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir, forkPoint, forkStarted
}

func TestLineageStubReaderMatchesFullScan(t *testing.T) {
	dir, forkPoint, forkStarted := buildLargeLineageStore(t, 40, 12)

	verifyIndexed := func(label string) {
		t.Helper()
		out, cut := LineageCut(dir, map[string]bool{
			"asker":                  true,
			"rewritten-fork-ended":   true,
			"rewritten-fork-ongoing": true,
		})
		if got, want := lineageKeys(out), []string{
			"asker", "copy-with-noise", "ended-parent",
			"rewritten-fork-ended", "rewritten-fork-ongoing", "sub-child",
		}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: out = %v, want %v", label, got, want)
		}
		for _, id := range []string{"fork-diverged", "sibling-diverged"} {
			if out[id] || !cut[id].Equal(forkPoint) {
				t.Errorf("%s: %s out=%v cut=%v, want kept with cut %v", label, id, out[id], cut[id], forkPoint)
			}
		}
		if out["other-harness"] || !cut["other-harness"].IsZero() {
			t.Errorf("%s: cross-harness out=%v cut=%v, want untouched", label, out["other-harness"], cut["other-harness"])
		}
		if !cut["ongoing-parent"].Equal(forkStarted) {
			t.Errorf("%s: ongoing-parent cut=%v, want %v", label, cut["ongoing-parent"], forkStarted)
		}

		// Whole asker path matches wholeHolds + wholeShared.
		whole := model.Session{
			Harness: "claude", ID: "whole-asker", Project: "p", Kind: "fork", Parent: "sibling-diverged",
			Messages: []model.Message{
				{Role: "user", Text: "retry loop spins on timeout", Time: forkPoint.Add(-41 * time.Second)},
				{Role: "tool-output", Text: "whole shared turn", Time: forkPoint},
				{Role: "user", Text: "whole diverged turn", Time: forkPoint.Add(24 * time.Hour)},
			},
		}
		wOut, wCut := LineageWholeCut(dir, map[string]bool{"whole-asker": true}, whole)
		if !wOut["copy-with-noise"] {
			t.Errorf("%s: wholeHolds missed copy-with-noise: %v", label, wOut)
		}
		if wOut["sibling-diverged"] || !wCut["sibling-diverged"].Equal(forkPoint) {
			t.Errorf("%s: wholeShared sibling=%v cut=%v, want kept at %v", label, wOut["sibling-diverged"], wCut["sibling-diverged"], forkPoint)
		}
	}

	verifyIndexed("with-recoffsets")

	if err := os.Remove(filepath.Join(dir, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	verifyIndexed("without-recoffsets")

	// Uncovered tail: build index with recoffsets.bin, then append a new copy
	// below recordOffsetsRefreshFloor so records.bin extends past covered.
	h := newTwoWayEnv(t)
	h.put("p", "base", `{"type":"user","sessionId":"base","cwd":"/w/p","timestamp":"2026-10-02T07:00:00Z","message":{"role":"user","content":"the retry loop spins"}}`+"\n"+
		`{"type":"assistant","sessionId":"base","cwd":"/w/p","timestamp":"2026-10-02T07:00:05Z","message":{"role":"assistant","content":"reset on success"}}`+"\n")
	h.put("p", "copy", `{"type":"user","sessionId":"copy","cwd":"/w/p","timestamp":"2026-10-02T07:00:00Z","message":{"role":"user","content":"the retry loop spins"}}`+"\n")
	tailDir := filepath.Join(h.tmp, "tail.db")
	if err := Ensure(tailDir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// Append a tail copy after recoffsets.bin was written.
	h.put("p", "copy-tail", `{"type":"user","sessionId":"copy-tail","cwd":"/w/p","timestamp":"2026-10-02T07:00:00Z","message":{"role":"user","content":"the retry loop spins"}}`+"\n"+
		`{"type":"assistant","sessionId":"copy-tail","cwd":"/w/p","timestamp":"2026-10-02T07:00:05Z","message":{"role":"assistant","content":"reset on success"}}`+"\n")
	if err := Ensure(tailDir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	tailOut, tailCut := LineageCut(tailDir, map[string]bool{"copy-tail": true})
	if !tailOut["base"] || !tailOut["copy"] || len(tailCut) != 0 {
		t.Errorf("uncovered tail: out=%v cut=%v, want base+copy hidden", tailOut, tailCut)
	}

	// Removing records.bin must invalidate any cached lineage entry so
	// unprovable copies are kept while manifest spawned edges remain.
	if err := os.Remove(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
	noRecOut, _ := LineageCut(dir, map[string]bool{"asker": true})
	if got, want := lineageKeys(noRecOut), []string{"asker", "sub-child"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missing records.bin after cache warmup: out=%v, want %v", got, want)
	}
}

func TestLineageAvoidsFullSessionBodyInflation(t *testing.T) {
	dir, forkPoint, _ := buildLargeLineageStore(t, 1200, 300)

	lineageBodyDecodes.Store(0)
	out, cut := LineageCut(dir, map[string]bool{"asker": true})
	decodes := lineageBodyDecodes.Load()

	if !out["copy-with-noise"] || !out["sub-child"] {
		t.Fatalf("unexpected out: %v", out)
	}
	if !cut["fork-diverged"].Equal(forkPoint) || !cut["sibling-diverged"].Equal(forkPoint) {
		t.Fatalf("unexpected cuts: %v (want %v)", cut, forkPoint)
	}
	const maxAllowedDecodes = 16
	if decodes > maxAllowedDecodes {
		t.Fatalf("LineageCut decoded %d full record bodies across marathon sessions, want <= %d", decodes, maxAllowedDecodes)
	}
}

func TestLineageAvoidsFullSessionStubInflation(t *testing.T) {
	dir, forkPoint, _ := buildLargeLineageStore(t, 2000, 15)

	lineageBodyDecodes.Store(0)
	lineageStubDecodes.Store(0)
	out, cut := LineageCut(dir, map[string]bool{"asker": true})
	bodyDecodes := lineageBodyDecodes.Load()
	stubDecodes := lineageStubDecodes.Load()

	if !out["copy-with-noise"] || !out["sub-child"] {
		t.Fatalf("unexpected out: %v", out)
	}
	if out["fork-diverged"] || !cut["fork-diverged"].Equal(forkPoint) {
		t.Fatalf("fork-diverged out=%v cut=%v, want kept at %v", out["fork-diverged"], cut["fork-diverged"], forkPoint)
	}
	if out["sibling-diverged"] || !cut["sibling-diverged"].Equal(forkPoint) {
		t.Fatalf("sibling-diverged out=%v cut=%v, want kept at %v", out["sibling-diverged"], cut["sibling-diverged"], forkPoint)
	}
	const maxAllowedBodyDecodes = 16
	if bodyDecodes > maxAllowedBodyDecodes {
		t.Fatalf("LineageCut decoded %d full record bodies across 2k-turn sessions, want <= %d", bodyDecodes, maxAllowedBodyDecodes)
	}
	const maxAllowedStubDecodes = 250
	if stubDecodes > maxAllowedStubDecodes {
		t.Fatalf("LineageCut decoded %d stubs across 2k-turn sessions, want <= %d", stubDecodes, maxAllowedStubDecodes)
	}

	// Shifted-index siblings share the opening and later fork point after extra
	// early messages shift subsequent record indices out of alignment.
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	pad := strings.Repeat("alpha beta gamma delta epsilon ", 240)
	turn := func(role, text string, ms int64) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(ms) * time.Millisecond)}
	}
	const sharedN = 320
	base := make([]model.Message, 0, sharedN+1)
	base = append(base, turn("user", "retry loop spins on timeout", 0))
	for i := range sharedN {
		role := "tool-output"
		if i%5 == 0 {
			role = "assistant"
		}
		base = append(base, turn(role, fmt.Sprintf("turn-%04d %s", i, pad), int64(2000+i*1000)))
	}
	shiftedCut := base[len(base)-1].Time

	askerMsgs := slices.Clone(base)
	for i := range 8 {
		askerMsgs = append(askerMsgs, turn("user", fmt.Sprintf("asker-tail-%02d %s", i, pad), int64(2000+(sharedN+i)*1000)))
	}
	copyExtra := make([]model.Message, 0, 5)
	for i := range 5 {
		copyExtra = append(copyExtra, turn("assistant", fmt.Sprintf("early-shift-%02d %s", i, pad), int64(100+i*100)))
	}
	shiftedCopy := slices.Concat(base[:1], copyExtra, base[1:])
	shiftedSibling := slices.Concat(
		base[:1],
		[]model.Message{turn("assistant", "early-sibling-shift "+pad, 500)},
		base[1:],
		[]model.Message{
			turn("user", "shifted-sibling-diverged-0 "+pad, int64(2000+(sharedN+200)*1000)),
			turn("assistant", "shifted-sibling-diverged-1 "+pad, int64(2000+(sharedN+201)*1000)),
		},
	)

	shiftDir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(shiftDir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	shiftSessions := []model.Session{
		{Harness: "claude", ID: "asker", Project: "p", Messages: askerMsgs},
		{Harness: "claude", ID: "shifted-copy", Project: "p", Messages: shiftedCopy},
		{Harness: "claude", ID: "shifted-sibling", Project: "p", Messages: shiftedSibling},
	}
	if err := writeSessions(shiftDir+".tmp", shiftDir, shiftSessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	sOut, sCut := LineageCut(shiftDir, map[string]bool{"asker": true})
	if !sOut["shifted-copy"] {
		t.Fatalf("shifted-copy should be hidden by asker: out=%v", sOut)
	}
	if sOut["shifted-sibling"] || !sCut["shifted-sibling"].Equal(shiftedCut) {
		t.Fatalf("shifted-sibling out=%v cut=%v, want kept at %v", sOut["shifted-sibling"], sCut["shifted-sibling"], shiftedCut)
	}
}
