package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A pasted issue is a query of fifty-odd words. Scored like a question, the
// session that brushes most of them across a long day won over the short one
// that is the same bug: every extra word it happened to hold was paid in full,
// and nothing charged it for its length.
func TestPastedIssueFindsTheSameBugOverALongSession(t *testing.T) {
	// Past bestMessageStore sessions the result is re-read by its best
	// message, which must not undo what the long query ranked.
	for _, filler := range []int{80, bestMessageStore} {
		t.Run(fmt.Sprint(filler), func(t *testing.T) { pastedIssue(t, filler) })
	}
}

func pastedIssue(t *testing.T, filler int) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(root, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	issue := strings.Fields(`csv exporter drops trailing delimiter when quoting multiline cells,
		utf8 bom header duplicated after gzip stream flush, spreadsheet importer rejects file,
		reproduced with pandas reader and libreoffice calc, regression since writer buffering refactor,
		affects nightly billing ledger exports invoice reconciliation finance dashboard
		scheduler cron worker retries timeout s3 upload multipart checksum mismatch
		encoding latin1 fallback newline crlf normalization column ordering schema`)
	q := strings.Join(issue, " ")
	if n := len(RelevanceTerms(q)); n < longQueryTerms {
		t.Fatalf("the issue has %d terms, below the %d this test is about", n, longQueryTerms)
	}
	// The same bug, said once and fixed.
	writeMessages(t, proj, "same-bug", `csv exporter drops trailing delimiter when quoting multiline cells and the utf8 bom header is duplicated after gzip stream flush
fixed in the writer buffering refactor: flush the quote state before the gzip stream closes, spreadsheet importer accepts the file again`)
	// A long day that touched half the issue's words one or two at a time.
	var day strings.Builder
	for i := range 120 {
		a, b := issue[(i*7)%len(issue)], issue[(i*11+3)%len(issue)]
		fmt.Fprintf(&day, "step %d checked %s then %s while tidying unrelated module %d notes\n", i, a, b, i)
	}
	writeMessages(t, proj, "long-day", day.String())
	seedFiller(t, proj, filler)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "same-bug" {
		var ids []string
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("pasted issue ranked %v, want same-bug first", ids)
	}
}

func TestShortCJKQuestionIsNotADocument(t *testing.T) {
	q := "我们上周在支付服务里把对账任务的超时时间改成了多少秒以及为什么要这样改动配置文件还有重试次数和告警阈值"
	if n := len(RelevanceTerms(q)); n < longQueryTerms {
		t.Fatalf("only %d terms, the question no longer shows the inflation", n)
	}
	if isLongQuery(RelevanceTerms(q)) {
		t.Fatal("a one-sentence Chinese question was taken for a pasted document")
	}
	if !isLongQuery(strings.Fields(strings.Repeat("word ", longQueryTerms))) {
		t.Fatal("forty plain words are a document")
	}
}
