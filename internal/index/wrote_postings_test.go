package index

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A written side is 200,000 lines of hex on this machine and no query will ever
// be a hash, so only the path earns postings — the same trade a replaced span
// makes with its body. Left in, the hashes would be the largest set of distinct
// tokens in the index and would match nothing anybody types.
func TestTheWrittenSideEarnsPostingsForItsPathOnly(t *testing.T) {
	const path = "/w/internal/pool/pool.go"
	record := path + "\n7faceff4c3c97294 af63f04c86020e88"
	if got := tokenizedPart(roleWrote, record); got != path {
		t.Errorf("tokenizedPart indexed %q; only the path should earn postings", got)
	}
	// And the body is still stored: blame reads the hashes back from the record
	// log, so dropping them from the text would break attribution rather than
	// save postings.
	if strings.Contains(tokenizedPart(roleWrote, record), "7faceff4") {
		t.Error("a hash earned postings")
	}
}

// Nothing serves them either. A reader asking about a file has no use for a
// line of hex, and --role wrote would hand back one record per write.
func TestTheWrittenSideIsNeverServed(t *testing.T) {
	for _, role := range []string{"", roleWrote, roleEdit} {
		if recordServable(roleWrote, query.Options{Role: role}) {
			t.Errorf("a written-side record is served under --role %q", role)
		}
	}
	// The replaced side is the comparison: asked for by name, it is served.
	if !recordServable(roleEdit, query.Options{Role: roleEdit}) {
		t.Error("--role edit stopped serving replaced spans")
	}
	// And it is work rather than speech, so a per-session read budget spends
	// itself on what someone said first.
	if !isToolRole(roleWrote) {
		t.Error("the written side counts as speech")
	}
}
