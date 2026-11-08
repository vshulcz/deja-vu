package index

import (
	"encoding/binary"
	"errors"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"io"
	"os"
)

// cjkIndexKeys is what keyer.cjkKeys emits, as strings.
func cjkIndexKeys(s string, emit func(tok string)) {
	var k keyer
	k.cjkKeys(s, func(b []byte) { emit(string(b)) })
}

func load(h string) []model.Session { return loadProgress(h, nil) }

func writeSessions(tmp, dir string, ss []model.Session, files map[string]FileState, scope string) error {
	return writeSessionsWithSync(tmp, dir, ss, files, scope, importedState{compactions: compactionsForRebuild(dir, readTombstones())})
}

// textKeys emits every key the text contributes, repeats included.
func textKeys(text string, emit func(tok string)) {
	var k keyer
	k.textKeys(text, func(b []byte) { emit(string(b)) })
}

// topTouchedFiles returns the files a session worked on most, busiest first.
func topTouchedFiles(ms []model.Message) []string {
	paths, _ := topTouchedCounted(ms)
	return paths
}

func sameProject(recorded, cwd string) bool { return projectDistance(recorded, cwd) >= 0 }

func stemPostings(dir string, terms, phrases []string) ([]posting, map[string][]string, error) {
	return stemPostingsFor(dir, terms, phrases, 0)
}

func intersectSubstringPostings(dir string, bare []string) ([]posting, error) {
	posts, _, err := intersectSubstringPostingsDetailed(dir, bare)
	return posts, err
}

// indexKeys collects what textKeys emits, in order and with its repeats. The
// build path streams the keys instead; this is what the tests read, so they
// read the same emitter the build does.
//
// Bigram keys fold Traditional to Simplified so the same word written either
// way lands on one key and a query in one script reaches content in the other.
// Folding only the key keeps the stored text untouched. The emitter folds runs
// in place and dedupes folded pairs — cheaper than folding each emitted
// bigram, and every caller collapses repeated keys anyway (#492).
func indexKeys(s string) []string {
	var out []string
	textKeys(s, func(tok string) {
		out = append(out, tok)
	})
	return out
}

// identifierParts is what keyer.identifierParts emits, as words.
func identifierParts(s string) []string {
	var out []string
	var k keyer
	k.identifierParts(s, func(b []byte) { out = append(out, string(b[1:])) })
	return out
}

func splitCompound(word []rune, out *[]string) {
	var k keyer
	k.splitCompound(word, func(b []byte) { *out = append(*out, string(b[1:])) })
}

func recordMatchesQuery(r Record, o query.Options) bool {
	return recordMatchesQueryVariants(r, o, nil)
}

func recordMatchesQueryVariants(r Record, o query.Options, variants map[string][]string) bool {
	return newRecordMatcher(o, variants).matches(r)
}

func sessionTitle(s model.Session) string {
	t, _ := sessionTitleFrom(s)
	return t
}

func writeRecord(f *os.File, r Record, t *recordTables) (int64, error) {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	b := encodeRecord(r, t)
	if len(b) > 1<<31 {
		return 0, errors.New("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b))) //nolint:gosec // a record is far below 4 GiB
	if _, err = f.Write(hdr[:]); err != nil {
		return 0, err
	}
	_, err = f.Write(b)
	return off, err
}
