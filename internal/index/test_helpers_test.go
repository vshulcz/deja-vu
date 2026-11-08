package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
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

// Search returns the sessions in the index at dir that answer o.
func Search(dir string, o query.Options) ([]model.Session, error) {
	r, err := SearchDetailed(dir, o)
	return r.Sessions, err
}

func CommandSettled(dir, cmd string, projects []string, allow func(string) bool) string {
	return CommandSettledOutside(dir, cmd, projects, allow, "")
}

func CommandSettledOutside(dir, cmd string, projects []string, allow func(string) bool, self string) string {
	return CommandSettledUsableOutside(dir, cmd, projects, allow, nil, self)
}

func Lineage(dir string, ids map[string]bool, heads ...model.Session) map[string]bool {
	out, _ := lineage(dir, ids, heads, nil)
	return out
}

func LineageWhole(dir string, ids map[string]bool, wholes ...model.Session) map[string]bool {
	out, _ := lineage(dir, ids, nil, wholes)
	return out
}

func OlderFormat(dir string) bool { return FormatDirection(dir) != 0 }

func RecentProject(dir, project string, n int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	project = strings.ToLower(project)
	var metas []SessionMeta
	for _, meta := range m.Sessions {
		p := strings.ToLower(meta.Project)
		if p == project || (project != "" && strings.Contains(p, project)) {
			metas = append(metas, meta)
		}
	}
	metas = metasNotIgnored(metas)
	sort.Slice(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
	if n > 0 && len(metas) > n {
		metas = metas[:n]
	}
	return sessionsForMetas(dir, metas)
}

func RecentProjects(dir string, projects []string, perName int) ([]model.Session, error) {
	return RecentProjectsUnder(dir, projects, "", perName)
}

func FirstMatch(dir string, queries []string, limit int) ([]model.Session, string, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, "", err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, "", fmt.Errorf("manifest: %w", err)
	}
	if !recordsReadable(dir, m) {
		return nil, "", fmt.Errorf("%w: records.bin size does not match the manifest (crash-truncated or uncommitted tail)", errCorruptIndex)
	}
	for _, q := range queries {
		keys := queryKeys(q)
		if len(keys) == 0 {
			continue
		}
		posts, err := intersectPostings(dir, retrievalKeys(keys))
		if err != nil {
			return nil, "", fmt.Errorf("postings: %w", err)
		}
		o := query.Options{Query: q}
		posts = cutPostingsBySession(posts, m, o)
		if len(posts) == 0 {
			continue
		}
		ss, err := scanRecords(dir, m, o, postingOffsets(posts))
		if err != nil {
			return nil, "", fmt.Errorf("records: %w", err)
		}
		if len(ss) == 0 {
			continue
		}
		if len(ss) > limit {
			ss = ss[:limit]
		}
		return ss, q, nil
	}
	return nil, "", nil
}

func SpanInventory(dir string) (spans, files int, err error) {
	return SpanInventoryWhere(dir, nil)
}
