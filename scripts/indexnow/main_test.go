package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadListKeepsOrderAndDropsNoise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	body := "b\n\n# a comment\na\n  b  \nc\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readList(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"b", "a", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A list with nothing in it is a caller that meant to submit something, so it is
// an error rather than a quiet no-op.
func TestReadListRefusesAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(path, []byte("\n# only a comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readList(path); err == nil {
		t.Fatal("an empty list was accepted")
	}
}

// The submitter is told which pages a push touched, and a push can touch a page
// that is not served: a rename leaves the old URL behind, a deletion leaves it
// in the diff. Announcing one spends the batch on a 404, so what the sitemap
// does not list does not go.
func TestOnlyPublishedDropsWhatTheSitemapDoesNotList(t *testing.T) {
	published := map[string]bool{"one": true, "two": true}
	var log strings.Builder
	got := onlyPublished([]string{"two", "gone", "one"}, published, &log)
	if len(got) != 2 || got[0] != "two" || got[1] != "one" {
		t.Fatalf("got %v, want [two one]", got)
	}
	if !strings.Contains(log.String(), "skipping gone") {
		t.Errorf("the dropped URL was not named: %q", log.String())
	}
}

func TestOnlyPublishedCanKeepNothing(t *testing.T) {
	if got := onlyPublished([]string{"gone"}, map[string]bool{"one": true}, io.Discard); len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}

// The key is served from the docs root and IndexNow fetches it to verify
// ownership, so a URL above that path fails the whole batch. The submitted host
// and the key's own location have to agree with where the file actually is.
func TestTheKeyLocationIsUnderTheDocsRoot(t *testing.T) {
	if !strings.HasPrefix(keyLocation, "https://"+host+"/deja-vu/") {
		t.Errorf("keyLocation %q is not under the path whose URLs it covers", keyLocation)
	}
	if !strings.Contains(keyLocation, keyFile) {
		t.Errorf("keyLocation %q does not name the key %q", keyLocation, keyFile)
	}
	if _, err := os.Stat(filepath.Join("..", "..", "docs", keyFile+".txt")); err != nil {
		t.Errorf("docs/%s.txt is not in the repository, so IndexNow cannot verify us: %v", keyFile, err)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", keyFile+".txt"))
	if err == nil && strings.TrimSpace(string(b)) != keyFile {
		t.Errorf("docs/%s.txt holds %q; it has to hold the key itself", keyFile, strings.TrimSpace(string(b)))
	}
}
