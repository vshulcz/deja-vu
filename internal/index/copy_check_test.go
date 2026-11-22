package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func buildMarathonTestSession(harness, id string, turns int, base time.Time) model.Session {
	pad := strings.Repeat("descriptor-table-padding-", 12)
	msgs := make([]model.Message, 0, turns*2)
	for i := range turns {
		ts := base.Add(time.Duration(i*5) * time.Second)
		uText := fmt.Sprintf("user task %d %s", i, pad)
		aText := fmt.Sprintf("completed step %d %s", i, pad)
		if i == 5 {
			uText = "investigate quokkaring buffer starvation"
			aText = "Decided to precompute the descriptor sidecar table so cold buffer starvation stops during startup."
		}
		msgs = append(msgs,
			model.Message{Role: "user", Text: uText, Time: ts},
			model.Message{Role: "assistant", Text: aText, Time: ts.Add(time.Second)},
		)
	}
	return model.Session{
		Harness:  harness,
		ID:       id,
		Project:  "work/proj",
		Path:     "/work/proj/" + id + ".jsonl",
		Started:  base,
		Updated:  base.Add(time.Duration(turns*5) * time.Second),
		Messages: msgs,
	}
}

func writeCopyCheckTestIndex(t *testing.T, dir string, ss []model.Session) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFindManyForCopyCheckMarathonAndFallbacks(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	t0 := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	src := buildMarathonTestSession("claude", "src-1", 135, t0)
	twin := buildMarathonTestSession("claude", "twin-1", 135, t0)
	copyS := model.Session{
		Harness: "claude",
		ID:      "copy-1",
		Project: "work/proj",
		Path:    "/work/proj/copy-1.jsonl",
		Started: t0,
		Updated: t0.Add(time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "start", Time: t0},
			src.Messages[len(src.Messages)-1],
			{Role: "user", Text: "follow", Time: t0.Add(time.Hour)},
		},
	}
	noteID := model.PromotedNotePrefix + "note-1"
	note := buildMarathonTestSession("deja", noteID, 135, t0)

	writeCopyCheckTestIndex(t, dir, []model.Session{src, twin, copyS, note})

	// Empty and unknown identities.
	if got, err := FindManyForCopyCheck(dir, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty ids: got %v, err=%v", got, err)
	}
	if got, err := FindManyForCopyCheck(dir, []Identity{{Harness: "claude", ID: "missing"}}); err != nil || len(got) != 0 {
		t.Fatalf("missing id: got %v, err=%v", got, err)
	}

	// Short session only uses full-read fallback cleanly.
	shortOnly, err := FindManyForCopyCheck(dir, []Identity{{Harness: "claude", ID: "copy-1"}})
	if err != nil || len(shortOnly) != 1 || len(shortOnly[0].Messages) != len(copyS.Messages) {
		t.Fatalf("shortOnly got=%v err=%v", shortOnly, err)
	}

	// Marathon + short + twin + promoted note path.
	got, err := FindManyForCopyCheck(dir, []Identity{
		{Harness: "claude", ID: "src-1"},
		{Harness: "claude", ID: "twin-1"},
		{Harness: "claude", ID: "copy-1"},
		{Harness: "deja", ID: noteID},
	})
	if err != nil || len(got) != 4 {
		t.Fatalf("FindManyForCopyCheck len=%d err=%v", len(got), err)
	}
	if got[0].Messages[0].Role != "summary" || !strings.HasPrefix(got[0].Messages[0].Text, "copy-shape:") {
		t.Fatalf("expected marathon copy-shape summary for src-1, got %+v", got[0].Messages[0])
	}
	if got[0].Messages[0].Text != got[1].Messages[0].Text {
		t.Fatalf("expected identical copy-shape digest for twins: %q vs %q", got[0].Messages[0].Text, got[1].Messages[0].Text)
	}
	if len(got[3].Messages) != len(note.Messages) {
		t.Fatalf("expected promoted note decoded in full (%d), got %d", len(note.Messages), len(got[3].Messages))
	}

	// Removing the offset table falls back cleanly to full decoding.
	if rerr := os.Remove(filepath.Join(dir, recordOffsetsFile)); rerr != nil {
		t.Fatal(rerr)
	}
	fallback, err := FindManyForCopyCheck(dir, []Identity{{Harness: "claude", ID: "src-1"}})
	if err != nil || len(fallback) != 1 || len(fallback[0].Messages) != len(src.Messages) {
		t.Fatalf("fallback without offset table: len=%d err=%v", len(fallback), err)
	}
}

func TestFindForRecallConclusionsBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	t0 := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	longS := buildMarathonTestSession("claude", "long-1", 135, t0)
	longS.Messages = append(longS.Messages, model.Message{Role: roleToolOutput, Text: "tool noise", Time: t0.Add(time.Hour)})
	shortS := buildMarathonTestSession("claude", "short-1", 10, t0)
	noteS := buildMarathonTestSession("deja", model.PromotedNotePrefix+"note-2", 135, t0)
	writeCopyCheckTestIndex(t, dir, []model.Session{longS, shortS, noteS})

	roles := map[string]bool{
		"user":             true,
		"assistant":        true,
		RoleStubOthers:     true,
		RoleStubToolOutput: true,
	}
	hitLong := model.Session{
		Harness:  longS.Harness,
		ID:       longS.ID,
		Messages: []model.Message{longS.Messages[10]}, // turn 5 user message
	}
	quoted := []string{longS.Messages[10].Text}

	// Marathon bounded episode decode keeps episode records and user-turn stubs.
	bounded, ok, err := FindForRecallConclusions(dir, hitLong, quoted, 20, false, roles)
	if err != nil || !ok {
		t.Fatalf("bounded FindForRecallConclusions: ok=%v err=%v", ok, err)
	}
	userCount := 0
	foundConclusion := false
	for _, m := range bounded.Messages {
		if m.Role == "user" {
			userCount++
		}
		if strings.Contains(m.Text, "precompute the descriptor sidecar") {
			foundConclusion = true
		}
	}
	if userCount != 135 || !foundConclusion || len(bounded.Messages) >= len(longS.Messages) {
		t.Fatalf("unexpected bounded result: users=%d msgs=%d conclusion=%v", userCount, len(bounded.Messages), foundConclusion)
	}

	// needAllChanges, longTurns <= 0, promoted note, quoted miss, short session, missing id.
	if s, ok, err := FindForRecallConclusions(dir, hitLong, quoted, 20, true, roles); err != nil || !ok || len(s.Messages) < len(longS.Messages)-1 {
		t.Fatalf("needAllChanges fallback failed: ok=%v len=%d err=%v", ok, len(s.Messages), err)
	}
	if s, ok, err := FindForRecallConclusions(dir, hitLong, quoted, 0, false, roles); err != nil || !ok || len(s.Messages) < len(longS.Messages)-1 {
		t.Fatalf("longTurns<=0 fallback failed: ok=%v len=%d err=%v", ok, len(s.Messages), err)
	}
	if s, ok, err := FindForRecallConclusions(dir, model.Session{Harness: noteS.Harness, ID: noteS.ID}, nil, 20, false, roles); err != nil || !ok || len(s.Messages) != len(noteS.Messages) {
		t.Fatalf("promoted note fallback failed: ok=%v len=%d err=%v", ok, len(s.Messages), err)
	}
	if s, ok, err := FindForRecallConclusions(dir, hitLong, []string{"not-in-hit"}, 20, false, roles); err != nil || !ok || len(s.Messages) < len(longS.Messages)-1 {
		t.Fatalf("quoted miss fallback failed: ok=%v len=%d err=%v", ok, len(s.Messages), err)
	}
	hitShort := model.Session{Harness: shortS.Harness, ID: shortS.ID, Messages: []model.Message{shortS.Messages[0]}}
	if s, ok, err := FindForRecallConclusions(dir, hitShort, []string{shortS.Messages[0].Text}, 20, false, roles); err != nil || !ok || len(s.Messages) != len(shortS.Messages) {
		t.Fatalf("short session fallback failed: ok=%v len=%d err=%v", ok, len(s.Messages), err)
	}
	if _, ok, err := FindForRecallConclusions(dir, model.Session{Harness: "claude", ID: "missing"}, nil, 20, false, roles); err != nil || ok {
		t.Fatalf("missing session: ok=%v err=%v", ok, err)
	}

	// Missing offset table falls back cleanly.
	if err := os.Remove(filepath.Join(dir, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	if s, ok, err := FindForRecallConclusions(dir, hitLong, quoted, 20, false, roles); err != nil || !ok || len(s.Messages) < len(longS.Messages)-1 {
		t.Fatalf("missing offsets fallback failed: ok=%v len=%d err=%v", ok, len(s.Messages), err)
	}
}

func variedPayloadText(size int) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var b strings.Builder
	b.Grow(size)
	state := uint64(0x9e3779b97f4a7c15)
	for range size {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		b.WriteByte(alpha[state&63])
	}
	return b.String()
}

func TestCopyCheckAndRecallEpisodeEdgeBranches(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	if isCopyLastTurn("summary", "recap", t0) {
		t.Fatal("summary role must not qualify as CopyLastTurn")
	}
	if isCopyLastTurn("user", "   \t\n ", t0) {
		t.Fatal("whitespace-only message must not qualify as CopyLastTurn")
	}
	if isCopyLastTurn("assistant", "No response requested.", t0) {
		t.Fatal("canned assistant refusal must not qualify as CopyLastTurn")
	}
	if isCopyLastTurn("user", "ok", time.Time{}) {
		t.Fatal("zero time must not qualify as CopyLastTurn")
	}

	// Low-level readRecordPayloadAt / walkLiveOffsetsSpans error guards.
	badDir := t.TempDir()
	shortPath := filepath.Join(badDir, "short.bin")
	if err := os.WriteFile(shortPath, []byte{1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	shortF, err := openIndexFile(shortPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := readRecordPayloadAt(shortF, 0); !errors.Is(rerr, errOffsetsMismatch) {
		_ = shortF.Close()
		t.Fatalf("short readRecordPayloadAt err = %v, want errOffsetsMismatch", rerr)
	}
	_ = shortF.Close()

	oversizePath := filepath.Join(badDir, "oversize.bin")
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], maxRecordSize+1)
	if werr := os.WriteFile(oversizePath, hdr[:], 0o600); werr != nil {
		t.Fatal(werr)
	}
	overF, err := openIndexFile(oversizePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := readRecordPayloadAt(overF, 0); !errors.Is(rerr, errOffsetsMismatch) {
		_ = overF.Close()
		t.Fatalf("oversize readRecordPayloadAt err = %v, want errOffsetsMismatch", rerr)
	}
	if werr := walkLiveOffsetsSpans(overF, []int64{0}, func(int, []byte) error { return nil }); !errors.Is(werr, errOffsetsMismatch) {
		_ = overF.Close()
		t.Fatalf("oversize walkLiveOffsetsSpans err = %v, want errOffsetsMismatch", werr)
	}
	_ = overF.Close()

	truncPath := filepath.Join(badDir, "trunc.bin")
	var truncBuf [16]byte
	binary.LittleEndian.PutUint32(truncBuf[:4], readAheadSize+512)
	if werr := os.WriteFile(truncPath, truncBuf[:], 0o600); werr != nil {
		t.Fatal(werr)
	}
	truncF, err := openIndexFile(truncPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := readRecordPayloadAt(truncF, 0); !errors.Is(rerr, errOffsetsMismatch) {
		_ = truncF.Close()
		t.Fatalf("truncated large readRecordPayloadAt err = %v, want errOffsetsMismatch", rerr)
	}
	_ = truncF.Close()

	// Exercise DefaultDir(), large >readAheadSize record reads, summary tail skip,
	// shared non-last timestamp decoding in copy check, and episode role stubs.
	hermeticIndexEnv(t)
	defDir := DefaultDir()

	src := buildMarathonTestSession("claude", "edge-src", 135, t0)
	hugeText := "kept-huge-" + variedPayloadText(24*1024)
	quotedZero := model.Message{Role: "user", Text: "quote-zero-time"}
	// Replace turn 5 with user(zero time), tool-output, command(unwanted), huge assistant.
	src.Messages[10] = quotedZero
	src.Messages[11] = model.Message{Role: "assistant", Text: hugeText, Time: src.Messages[11].Time}
	turn5Extra := []model.Message{
		quotedZero,
		{Role: roleToolOutput, Text: "tool output trimmed stub target", Time: src.Messages[11].Time.Add(-200 * time.Millisecond)},
		{Role: "command", Text: "go test ./...", Time: src.Messages[11].Time.Add(-100 * time.Millisecond)},
		{Role: "assistant", Text: hugeText, Time: src.Messages[11].Time},
	}
	rebuilt := make([]model.Message, 0, len(src.Messages)+3)
	rebuilt = append(rebuilt, src.Messages[:10]...)
	rebuilt = append(rebuilt, turn5Extra...)
	rebuilt = append(rebuilt, src.Messages[12:]...)
	// Append a trailing summary after LastTurn so backward scan skips summary.
	rebuilt = append(rebuilt, model.Message{
		Role: "summary",
		Text: "tail summary",
		Time: src.Updated.Add(time.Second),
	})
	src.Messages = rebuilt

	// Non-marathon session whose LastTurn timestamp matches src's earlier assistant message.
	earlyMatchTime := src.Messages[4].Time
	other := model.Session{
		Harness: "claude",
		ID:      "edge-other",
		Project: "work/proj",
		Path:    "/work/proj/edge-other.jsonl",
		Started: t0,
		Updated: earlyMatchTime,
		Messages: []model.Message{
			{Role: "user", Text: "short trigger", Time: earlyMatchTime.Add(-time.Second)},
			{Role: "assistant", Text: "other reply", Time: earlyMatchTime},
		},
	}

	writeCopyCheckTestIndex(t, defDir, []model.Session{src, other})

	if emptyOut, emptyErr := FindManyForCopyCheck("", nil); emptyErr != nil || emptyOut != nil {
		t.Fatalf("empty ids: out=%v err=%v", emptyOut, emptyErr)
	}
	if missOut, missErr := FindManyForCopyCheck("", []Identity{{Harness: "claude", ID: "no-such-id"}}); missErr != nil || missOut != nil {
		t.Fatalf("missing ids: out=%v err=%v", missOut, missErr)
	}

	copyOut, err := FindManyForCopyCheck("", []Identity{
		{Harness: src.Harness, ID: src.ID},
		{Harness: other.Harness, ID: other.ID},
	})
	if err != nil {
		t.Fatalf("FindManyForCopyCheck(default dir): %v", err)
	}
	if len(copyOut) != 2 {
		t.Fatalf("len(FindManyForCopyCheck) = %d, want 2", len(copyOut))
	}

	hit := model.Session{
		Harness:  src.Harness,
		ID:       src.ID,
		Messages: []model.Message{quotedZero},
	}
	roles := map[string]bool{
		"user":             true,
		"assistant":        true,
		roleToolOutput:     true,
		RoleStubToolOutput: true,
		RoleStubOthers:     true,
	}
	gotRec, ok, err := FindForRecallConclusions("", hit, []string{"quote-zero-time"}, 20, false, roles)
	if err != nil || !ok {
		t.Fatalf("FindForRecallConclusions(default dir): ok=%v err=%v", ok, err)
	}
	var sawHuge, sawCmdStub, sawToolStub bool
	for _, m := range gotRec.Messages {
		if m.Role == "assistant" && m.Text == hugeText {
			sawHuge = true
		}
		if m.Role == "command" && m.Text == "" {
			sawCmdStub = true
		}
		if m.Role == roleToolOutput && m.Text == "" {
			sawToolStub = true
		}
	}
	if !sawHuge || !sawCmdStub || !sawToolStub {
		t.Fatalf("unexpected episode stub decoding: huge=%v cmdStub=%v toolStub=%v", sawHuge, sawCmdStub, sawToolStub)
	}
}

func TestCopyCheckEdgeAndFallbackCases(t *testing.T) {
	if got := wrapCorruptDecodeErr(errCorruptIndex); !errors.Is(got, errCorruptIndex) {
		t.Fatalf("wrapCorruptDecodeErr(sentinel) = %v", got)
	}
	if got := wrapCorruptDecodeErr(os.ErrInvalid); !errors.Is(got, errCorruptIndex) || !errors.Is(got, os.ErrInvalid) {
		t.Fatalf("wrapCorruptDecodeErr(other) = %v", got)
	}

	// Missing manifest returns an error cleanly.
	if _, err := FindManyForCopyCheck(t.TempDir(), []Identity{{Harness: "claude", ID: "x"}}); err == nil {
		t.Fatal("expected error when manifest is absent")
	}

	// Short sessions only: triggers !anyMarathon fallback to windowed tail.
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	shortDir := t.TempDir()
	shortSess := model.Session{
		Harness: "claude",
		ID:      "short-1",
		Project: "work/proj",
		Path:    "/work/proj/short-1.jsonl",
		Started: t0,
		Updated: t0.Add(time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: "short trigger prompt", Time: t0},
			{Role: "assistant", Text: "short reply", Time: t0.Add(time.Second)},
		},
	}
	writeCopyCheckTestIndex(t, shortDir, []model.Session{shortSess})

	out, err := FindManyForCopyCheck(shortDir, []Identity{{Harness: "claude", ID: "short-1"}})
	if err != nil || len(out) != 1 || len(out[0].Messages) != 2 {
		t.Fatalf("short-only FindManyForCopyCheck = (len=%d, %v), want 1 session with 2 msgs", len(out), err)
	}

	// Removing recoffsets.bin drops through !handled fallback without failing.
	_ = os.Remove(filepath.Join(shortDir, "recoffsets.bin"))
	out2, err := FindManyForCopyCheck(shortDir, []Identity{{Harness: "claude", ID: "short-1"}})
	if err != nil || len(out2) != 1 {
		t.Fatalf("missing recoffsets FindManyForCopyCheck = (len=%d, %v), want 1", len(out2), err)
	}

	// Removing records.bin returns open error cleanly from copyCheckSessionsIn.
	m, err := readManifest(shortDir)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(shortDir, "records.bin"))
	if _, _, derr := copyCheckSessionsIn(shortDir, m, []SessionMeta{m.Sessions["claude:short-1"]}); derr == nil {
		t.Fatal("expected open error when records.bin is missing")
	}
}
