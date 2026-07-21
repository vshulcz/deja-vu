package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Four sessions tie "login" to "jwks": the rescue should let a query that
// says login find the session that only says jwks.
func cooccurCorpus(t *testing.T) string {
	t.Helper()
	texts := []string{
		"login broken again, jwks cache stale after rotation",
		"login failure traced to jwks refresh timing",
		"users report login errors, jwks endpoint returned old keys",
		"jwks rotation deployed, monitoring for regressions",
		"docker build cache oom unrelated filler session",
		"another unrelated session about frontend styling",
	}
	return nlIndex(t, texts...)
}

func TestCooccurMapBuiltOnFullRebuild(t *testing.T) {
	dir := cooccurCorpus(t)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("cooccur map missing after full build")
	}
	found := false
	for _, n := range m["login"] {
		if n == "jwks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("login neighbors = %v, want jwks", m["login"])
	}
}

func TestCooccurRescueSwapsOneToken(t *testing.T) {
	dir := cooccurCorpus(t)
	// "login" never appears with "monitoring" in one session; the rescue may
	// swap login -> jwks and land on session d.
	result, err := SearchDetailed(dir, search.Options{Query: "login monitoring regressions", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("cooccur rescue did not fire")
	}
	if vs := result.Variants["login"]; len(vs) != 1 || vs[0] == "login" {
		t.Fatalf("swap not narrated: %v", result.Variants)
	}
}

func TestCooccurRescueStaysQuietWhenMapMissing(t *testing.T) {
	dir := nlIndex(t, "one lonely session about nothing in particular")
	_ = os.Remove(filepath.Join(dir, cooccurFile))
	result, err := SearchDetailed(dir, search.Options{Query: "absent tokens here", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 0 {
		t.Fatalf("rescue invented results: %+v", result.Sessions)
	}
}
