package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// corruptRecordsContaining sets the encoding flag byte to 0xff on every
// record in records.bin whose payload text contains needle while keeping frame
// lengths intact.
func corruptRecordsContaining(t *testing.T, recPath string, needle []byte) {
	t.Helper()
	raw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := 0
	for off := 0; off+4 <= len(raw); {
		size := int(binary.LittleEndian.Uint32(raw[off : off+4]))
		off += 4
		if size <= 0 || off+size > len(raw) {
			break
		}
		payload := raw[off : off+size]
		off += size
		pos := 0
		validHdr := true
		for range 3 {
			_, n := binary.Uvarint(payload[pos:])
			if n <= 0 {
				validHdr = false
				break
			}
			pos += n
		}
		if !validHdr || pos >= len(payload) {
			continue
		}
		flagOff := off - size + pos
		flag := raw[flagOff]
		body := payload[pos+1:]
		if flag == 1 {
			zr := flate.NewReader(bytes.NewReader(body))
			dec, derr := io.ReadAll(zr)
			_ = zr.Close()
			if derr != nil {
				continue
			}
			body = dec
		} else if flag != 0 {
			continue
		}
		if bytes.Contains(body, needle) {
			raw[flagOff] = 0xff
			corrupted++
		}
	}
	if corrupted == 0 {
		t.Fatalf("found 0 records containing %q in %s", needle, recPath)
	}
	if err := os.WriteFile(recPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendClaudeTurnJSONL(sb *strings.Builder, sid, cwd, userText, assistantText string, ts time.Time) {
	fmt.Fprintf(sb, `{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"user","content":%q}}`+"\n",
		sid, ts.Format(time.RFC3339Nano), cwd, userText)
	fmt.Fprintf(sb, `{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"assistant","content":%q}}`+"\n",
		sid, ts.Add(time.Second).Format(time.RFC3339Nano), cwd, assistantText)
}

func TestForkCopiesFromFullMarathonSessionMatchesFull(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	slugDir := filepath.Join(claudeRoot, "-work-marathon-copy")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := "/work/marathon-copy"
	t0 := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	pad := strings.Repeat("alpha-beta-gamma-delta-", 30)

	var srcSB strings.Builder
	for i := range 140 {
		u := fmt.Sprintf("Ordinary user turn %d %s", i, pad)
		if i == 100 {
			u = fmt.Sprintf("MARATHON_MIDDLE_MARKER user turn %d %s", i, pad)
		}
		a := fmt.Sprintf("Ordinary assistant reply %d %s", i, pad)
		if i == 139 {
			a = "Marathon source final settled turn."
		}
		appendClaudeTurnJSONL(&srcSB, "m-src", cwd, u, a, t0.Add(time.Duration(i*5)*time.Second))
	}
	// Identical twin and continued copy both share m-src's last turn; copy adds
	// one later turn so CopyOf(copy, src) holds without holding the reverse.
	var copySB, twinSB, sibSB strings.Builder
	lastTS := t0.Add(time.Duration(139*5) * time.Second).Add(time.Second)
	twinSB.WriteString(strings.ReplaceAll(srcSB.String(), `"m-src"`, `"m-twin"`))
	fmt.Fprintf(&copySB, `{"type":"user","sessionId":"m-copy","timestamp":%q,"cwd":%q,"message":{"role":"user","content":"initial prompt"}}`+"\n",
		t0.Format(time.RFC3339Nano), cwd)
	fmt.Fprintf(&copySB, `{"type":"assistant","sessionId":"m-copy","timestamp":%q,"cwd":%q,"message":{"role":"assistant","content":"Marathon source final settled turn."}}`+"\n",
		lastTS.Format(time.RFC3339Nano), cwd)
	fmt.Fprintf(&copySB, `{"type":"user","sessionId":"m-copy","timestamp":%q,"cwd":%q,"message":{"role":"user","content":"follow up on forked branch"}}`+"\n",
		lastTS.Add(10*time.Second).Format(time.RFC3339Nano), cwd)
	fmt.Fprintf(&copySB, `{"type":"assistant","sessionId":"m-copy","timestamp":%q,"cwd":%q,"message":{"role":"assistant","content":"Continued work on copied branch."}}`+"\n",
		lastTS.Add(11*time.Second).Format(time.RFC3339Nano), cwd)
	appendClaudeTurnJSONL(&sibSB, "m-sib", cwd, "unrelated sibling session", "Sibling session finished.", t0)

	for name, body := range map[string]string{
		"m-src.jsonl":  srcSB.String(),
		"m-twin.jsonl": twinSB.String(),
		"m-copy.jsonl": copySB.String(),
		"m-sib.jsonl":  sibSB.String(),
	} {
		if err := os.WriteFile(filepath.Join(slugDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	ids := []index.Identity{
		{Harness: "claude", ID: "m-src"},
		{Harness: "claude", ID: "m-twin"},
		{Harness: "claude", ID: "m-copy"},
		{Harness: "claude", ID: "m-sib"},
	}
	fullWant, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		t.Fatalf("FindManyByIdentity: %v", err)
	}
	checkGot, err := index.FindManyForCopyCheck(dir, ids)
	if err != nil {
		t.Fatalf("FindManyForCopyCheck: %v", err)
	}
	if !reflect.DeepEqual(copyMessageShapes(checkGot), copyMessageShapes(fullWant)) {
		t.Fatalf("copyMessageShapes mismatch: got %v, want %v", copyMessageShapes(checkGot), copyMessageShapes(fullWant))
	}

	// Simulate search hits carrying only one early matched turn so copySourcesAmong
	// must reload full/bounded records via forkCopiesFromFull.
	partial := []model.Session{
		{Harness: "claude", ID: "m-src", Started: t0},
		{Harness: "claude", ID: "m-copy", Started: t0},
		{Harness: "claude", ID: "m-sib", Started: t0},
	}
	wantMap := copySourcesAmong(dir, partial, func(src, c int, _ bool) bool { return src != c }, nil, nil)
	if wantMap[1] != 0 {
		t.Fatalf("expected m-copy (idx 1) to match m-src (idx 0), got %v", wantMap)
	}
	if _, isCopy := wantMap[2]; isCopy {
		t.Fatalf("did not expect sibling m-sib (idx 2) to be marked copy: %v", wantMap)
	}

	// Corrupt deflated middle turn records of m-src; FindManyForCopyCheck and
	// forkCopiesFromFull must still resolve LastTurn without inflating them.
	corruptRecordsContaining(t, filepath.Join(dir, "records.bin"), []byte("MARATHON_MIDDLE_MARKER"))
	checkAfter, err := index.FindManyForCopyCheck(dir, []index.Identity{
		{Harness: "claude", ID: "m-src"},
		{Harness: "claude", ID: "m-copy"},
		{Harness: "claude", ID: "m-sib"},
	})
	if err != nil {
		t.Fatalf("FindManyForCopyCheck inflated middle record: %v", err)
	}
	if !search.CopyOf(checkAfter[1], checkAfter[0]) || search.CopyOf(checkAfter[2], checkAfter[0]) {
		t.Fatalf("CopyOf semantics changed under bounded read")
	}
	gotMap := copySourcesAmong(dir, partial, func(src, c int, _ bool) bool { return src != c }, nil, nil)
	if !reflect.DeepEqual(gotMap, wantMap) {
		t.Fatalf("copySourcesAmong after middle corruption = %v, want %v", gotMap, wantMap)
	}
}

func TestRecallConclusionLinesMarathonSessionBoundsDecode(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	slugDir := filepath.Join(claudeRoot, "-work-marathon-recall")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := "/work/marathon-recall"
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	pad := strings.Repeat("socket-retry-padding-", 35)

	var sb strings.Builder
	// Write 140 turns (280 messages > recallConclusionsFullCutoff=256 and >= longSessionTurns=20).
	// Use varied per-turn tokens so records.bin exceeds the three 4 KB fingerprint
	// windows, keeping turn 40 cleanly outside all fingerprint spans.
	for i := range 140 {
		ts := t0.Add(time.Duration(i*5) * time.Second)
		switch i {
		case 5:
			// Query hits the user message; the conclusion sits only in the unquoted assistant reply.
			appendClaudeTurnJSONL(&sb, "m-rec", cwd,
				"investigate quokkaring buffer starvation",
				"Decided to precompute the descriptor sidecar table so cold buffer starvation stops during startup.",
				ts)
		case 40:
			appendClaudeTurnJSONL(&sb, "m-rec", cwd,
				"adjust unrelated background socket interval",
				"UNRELATED_TURN_MARKER Decided to increase TCP retry backoff interval for socket checks. "+pad,
				ts)
		default:
			var varPad strings.Builder
			for j := range 28 {
				fmt.Fprintf(&varPad, "step%d-%d ", i*37+j, (i*97+j*53)%991)
			}
			appendClaudeTurnJSONL(&sb, "m-rec", cwd,
				fmt.Sprintf("unrelated housekeeping task %d %s", i, varPad.String()),
				fmt.Sprintf("Completed housekeeping step %d %s", i, varPad.String()),
				ts)
		}
	}
	if err := os.WriteFile(filepath.Join(slugDir, "m-rec.jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	q := "quokkaring buffer starvation"
	_, o, hits, _, err := recallRank(dir, q, search.Options{Query: q, All: true})
	if err != nil || len(hits) == 0 {
		t.Fatalf("recallRank: len=%d err=%v", len(hits), err)
	}
	h := hits[0]
	wantLines := recallConclusionLines(dir, q, h, o, 2000)
	joinedWant := strings.Join(wantLines, "")
	if !strings.Contains(joinedWant, "precompute the descriptor sidecar") {
		t.Fatalf("expected conclusion line from turn 5 before corruption, got:\n%s", joinedWant)
	}

	// Corrupt turn 40's deflated assistant record; bounded recall conclusion load
	// must skip inflating unrelated turns outside turn 5's episode.
	corruptRecordsContaining(t, filepath.Join(dir, "records.bin"), []byte("UNRELATED_TURN_MARKER"))

	quoted := make([]string, 0, len(h.QuotedMessages()))
	for _, qm := range h.QuotedMessages() {
		if !digest.IsCompactionSummary(qm) {
			quoted = append(quoted, qm)
		}
	}
	roles := map[string]bool{
		"user":               true,
		"assistant":          true,
		"edit":               true,
		"wrote":              true,
		"command":            true,
		index.RoleStubOthers: true,
	}
	full, ok, ferr := index.FindForRecallConclusions(dir, h.Session, quoted, longSessionTurns, false, roles)
	if ferr != nil || !ok {
		t.Fatalf("FindForRecallConclusions failed/inflated unrelated turn: ok=%v err=%v", ok, ferr)
	}
	gotLines := recallConclusionLines(dir, q, h, o, 2000)
	if !slices.Equal(gotLines, wantLines) {
		t.Fatalf("recallConclusionLines mismatch:\ngot:  %v\nwant: %v", gotLines, wantLines)
	}
	_ = full
}
