package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A Japanese session, searched the way its writer would search it. The word in
// the query is in the text verbatim, so the exact tier is what must answer —
// it fell through to the close tier, because the query asked for a token the
// index could never hold (#1319).
func TestJapaneseWordsAnswerOnTheExactTier(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, text := range map[string]string{
		"jp":  "リトライキューがステージングで止まった",
		"jp2": "サーバーのエラーを調べた",
	} {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ステージング", "サーバー", "エラー", "リトライ"} {
		res, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(res.Sessions) == 0 {
			t.Errorf("%q: no session, though the word is in the text", q)
			continue
		}
		if res.Tier != search.TierExact {
			t.Errorf("%q: answered on the %s tier, though the word is in the text verbatim", q, res.Tier)
		}
	}
}

// Chinese and Korean were already right, and must stay so.
func TestOtherScriptsStillAnswerExactly(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, text := range map[string]string{
		"cn": "调度器的重试队列在预发环境卡住了",
		"kr": "재시도 대기열이 스테이징에서 멈췄다",
	} {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"重试队列", "调度器", "재시도"} {
		res, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(res.Sessions) == 0 || res.Tier != search.TierExact {
			t.Errorf("%q: %d sessions on the %s tier", q, len(res.Sessions), res.Tier)
		}
	}
}

// The posting keys for Japanese change shape with this, so a store built before
// it holds the split keys while a query built after asks for the joined ones —
// the same situation the NFC fold was bumped for (version 24).
func TestTheFormatVersionRidesWithTheKeys(t *testing.T) {
	if version < 27 {
		t.Errorf("index version is %d — the CJK keys changed without the bump that re-derives them", version)
	}
}
