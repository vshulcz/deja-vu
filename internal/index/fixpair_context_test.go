package index

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestMineEditContextDeclarationsAndCalls(t *testing.T) {
	t.Parallel()

	// Out-of-bounds editIdx returns empty cleanly.
	if sym, why := mineEditContext(nil, 0, -1, "pkg/store.go", "panic: bad"); sym != "" || why != "" {
		t.Fatalf("expected empty for negative editIdx, got %q %q", sym, why)
	}

	// Go method declaration + adjacent edit body + causal sentence with inline code and code fence.
	ms := []model.Message{
		{Role: roleToolOutput, Text: "FAIL: lease quorum overflow in store"},
		{Role: "assistant", Text: "```go\n// ignored code block because of fence\n```\nLet me inspect the lease check first?"},
		{Role: roleEdit, Text: "pkg/lease_store.go\nfunc (s *LeaseStore) RenewQuorum(ctx context.Context) error {"},
		{Role: roleEdit, Text: "pkg/lease_store.go\n\treturn s.commitEpoch(ctx)"},
		{Role: "assistant", Text: "- Because `RenewQuorum` skipped monotonic epoch validation. Moving on!"},
		{Role: "user", Text: "now check another thing because RenewQuorum was mentioned"},
	}
	sym, why := mineEditContext(ms, 0, 2, "pkg/lease_store.go", "FAIL: lease quorum overflow")
	if sym != "LeaseStore.RenewQuorum" {
		t.Fatalf("expected LeaseStore.RenewQuorum, got %q", sym)
	}
	if !strings.Contains(why, "RenewQuorum") || !strings.Contains(why, "monotonic epoch") {
		t.Fatalf("expected causal why mentioning RenewQuorum, got %q", why)
	}

	// Python def, Rust pub fn, TypeScript export async function.
	langCases := []struct {
		path string
		body string
		want string
	}{
		{"worker/sync.py", "async def reconcile_shard(cursor):\n    return cursor", "reconcile_shard"},
		{"src/fencing.rs", "pub async fn verify_epoch_token(tok: u64) -> bool {", "verify_epoch_token"},
		{"src/router.ts", "export async function dispatchWebhookBatch(items: Item[]) {", "dispatchWebhookBatch"},
		{"pkg/types.go", "type SnapshotCursor struct {\n\tOffset int64\n}", "SnapshotCursor"},
	}
	for _, tc := range langCases {
		editMsgs := []model.Message{
			{Role: roleToolOutput, Text: "error: assertion failed"},
			{Role: roleEdit, Text: tc.path + "\n" + tc.body},
		}
		gotSym, _ := mineEditContext(editMsgs, 0, 1, tc.path, "error: assertion failed")
		if gotSym != tc.want {
			t.Errorf("%s: got symbol %q, want %q", tc.path, gotSym, tc.want)
		}
	}

	// Backtick symbol fallback and direct match in body, skipping extensions/stdlib/test symbols.
	btMsgs := []model.Message{
		{Role: roleToolOutput, Text: "panic: nil cursor"},
		{Role: "assistant", Text: "Checked `config.json`, `strings.Split`, `TestCursor`, and `CursorTracker.AdvanceOffset` before editing."},
		{Role: roleEdit, Text: "pkg/cursor.go\n\tif ok := tracker.AdvanceOffset(next); !ok {"},
	}
	if got := extractEditedSymbol(btMsgs, 0, 2, "pkg/cursor.go"); got != "CursorTracker.AdvanceOffset" {
		t.Fatalf("expected backtick match CursorTracker.AdvanceOffset, got %q", got)
	}

	// Qualified call fallback when no declaration or backtick symbol is present.
	callMsgs := []model.Message{
		{Role: roleToolOutput, Text: "error: bad frame"},
		{Role: roleEdit, Text: "pkg/codec.go\n\tfmt.Println(x)\n\tcodec. Close()\n\tdecoder.ValidateFrameHeader(buf)"},
	}
	if got := extractEditedSymbol(callMsgs, 0, 1, "pkg/codec.go"); got != "ValidateFrameHeader" {
		t.Fatalf("expected qualified call ValidateFrameHeader, got %q", got)
	}
}

func TestCausalHelpersAndMultiWordIdentifiers(t *testing.T) {
	t.Parallel()

	for _, okIdent := range []string{"snake_case", "camelCase", "TwoWords", "LeaseStore"} {
		if !isMultiWordIdent(okIdent) {
			t.Errorf("expected isMultiWordIdent(%q) = true", okIdent)
		}
	}
	for _, badIdent := range []string{"_leading", "trailing_", "UPPERONLY", "Close", "split"} {
		if isMultiWordIdent(badIdent) {
			t.Errorf("expected isMultiWordIdent(%q) = false", badIdent)
		}
	}

	for _, badSym := range []string{"short", "file.go", "data.jsonl", "fmt.Sprintf", "TestHelper", "pkg.TestHelper", "Close"} {
		if isPlausibleBacktickSymbol(badSym) {
			t.Errorf("expected isPlausibleBacktickSymbol(%q) = false", badSym)
		}
	}
	for _, goodSym := range []string{"store.FlushWalBatch", "reconcile_offsets", " AdvanceCursor"[1:]} {
		if !isPlausibleBacktickSymbol(goodSym) {
			t.Errorf("expected isPlausibleBacktickSymbol(%q) = true", goodSym)
		}
	}

	// Russian causal marker + long sentence clipping on word boundary.
	longWhy := "Проблема возникала потому что в worker_lease происходило повторное чтение устаревшего курсора очереди при высокой конкурентной нагрузке воркеров кластера и отсутствии блокировки чтения."
	ms := []model.Message{
		{Role: roleToolOutput, Text: "FAIL: worker_lease duplicate delivery"},
		{Role: roleEdit, Text: "pkg/worker_lease.go\nreturn renew()"},
		{Role: "assistant", Text: "See https://example.com/pr/1 (merged).\n" + longWhy},
	}
	_, why := mineEditContext(ms, 0, 1, "pkg/worker_lease.go", "FAIL: worker_lease duplicate delivery")
	if !strings.HasSuffix(why, "…") || len([]rune(why)) > 164 {
		t.Fatalf("expected clipped Russian causal why ending with '…', got %q (len=%d)", why, len([]rune(why)))
	}

	// Anchor via error term only (short file stem "io.go" and no symbol).
	errTermMsgs := []model.Message{
		{Role: roleToolOutput, Text: "panic: deadlock detected in coordinator"},
		{Role: roleEdit, Text: "io.go\nx = 1"},
		{Role: "assistant", Text: "* **Root cause**: The panic occurred because deadlock avoidance skipped lock ordering."},
	}
	if _, errWhy := mineEditContext(errTermMsgs, 0, 1, "io.go", "panic: deadlock detected in coordinator"); !strings.Contains(errWhy, "deadlock") {
		t.Fatalf("expected error-term anchored why, got %q", errWhy)
	}
}

func TestAssistantTurnsEdgeCases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Empty / unindexed directory and empty key sets return nil cleanly.
	recs, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"ab", "quorum"}, map[string]bool{}, 5)
	if err != nil || len(recs) != 0 {
		t.Fatalf("expected empty records on empty allowedKeys, got %d, err=%v", len(recs), err)
	}
	recs, err = RecentSpokenAssistantTurnsForTerms(dir, []string{"short"}, nil, 0)
	if err != nil || len(recs) != 0 {
		t.Fatalf("expected empty records when all terms < 5 chars, got %d, err=%v", len(recs), err)
	}
	giveUps, err := GiveUpRecordsForKeys(dir, map[string]bool{})
	if err != nil || len(giveUps) != 0 {
		t.Fatalf("expected empty give-up records for empty keys, got %d, err=%v", len(giveUps), err)
	}
	if _, err := GiveUpRecordsForKeys(dir, map[string]bool{"missing": true}); err == nil {
		t.Fatalf("expected error on missing manifest")
	}
	if !isTestFileEditPath("pkg/relay_test.go") || !isTestFileEditPath("tests/test_worker.py") || isTestFileEditPath("pkg/relay.go") {
		t.Errorf("unexpected isTestFileEditPath behavior")
	}
	if !looksLikeEditedPath(`C:\Users\runneradmin\work\project\retry.go`) || !looksLikeEditedPath(`c:/Users/runneradmin/work/project/retry.go`) {
		t.Errorf("expected looksLikeEditedPath to accept Windows paths with drive letter")
	}
	if looksLikeEditedPath(`C:\Users\runneradmin\.claude\projects\s1.jsonl`) || looksLikeEditedPath("bad:path:with:colons") {
		t.Errorf("expected looksLikeEditedPath to reject claude project paths and non-drive colons")
	}
	if !preferEditRemedyCandidate("pkg/relay_test.go", "pkg/relay.go", 1, 2, false) || preferEditRemedyCandidate("pkg/relay.go", "pkg/relay_test.go", 1, 2, false) {
		t.Errorf("unexpected preferEditRemedyCandidate behavior")
	}
	priorMsgs := []model.Message{
		{Role: roleEdit, Text: "./pkg/relay.go\nold"},
		{Role: roleToolOutput, Text: "FAIL: TestRelay"},
	}
	if !hadPriorSameFileEdit(priorMsgs, 1, "pkg/relay.go") || hadPriorSameFileEdit(priorMsgs, 1, "pkg/other.go") {
		t.Errorf("unexpected hadPriorSameFileEdit behavior")
	}
}
