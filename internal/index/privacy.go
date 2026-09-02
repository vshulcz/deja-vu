package index

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/model"
)

type ForgetOptions struct {
	Session string
	Project string
	Before  time.Time
	DryRun  bool
}

type ForgetResult struct {
	Sessions   int
	Messages   int
	Tombstones int
	// Keys of the sessions that matched, harness:id. The caller needs them to
	// clean up what lives outside the index — the titles promoted notes
	// borrowed from these sessions (#666, #690).
	Keys []string
	// Notes is how many of Sessions were the user's own writing rather than raw
	// transcripts, so one combined number does not read as "four
	// conversations" when half of it is theirs. Promoted counts the part of it
	// that came from `deja promote`: calling a day of `remember` notes a
	// promoted note names something the reader never did (#957).
	Notes    int
	Promoted int
	// Peers names the hosts a forgotten session was already pushed to, and
	// Exported reports the same for unnamed directory exports. Forgetting
	// removes the local copy and keeps the session out of later pushes, but it
	// cannot reach a machine that already has it — and saying nothing read as
	// "it is gone everywhere" (#788).
	Peers    []string
	Exported bool
}

// pushedTo reports where records from the matched sessions have already gone.
// A watermark at or past a session's earliest record means that session was in
// a push: watermarks are keyed by peer and by the source file, and a session's
// Path is that source.
func pushedTo(m Manifest, matched map[string]bool) ([]string, bool) {
	if len(m.ExportWatermarks) == 0 {
		return nil, false
	}
	peers := map[string]bool{}
	unnamed := false
	for key := range matched {
		meta, ok := m.Sessions[key]
		if !ok {
			continue
		}
		from := meta.Started
		if from.IsZero() {
			from = meta.Updated
		}
		for wk, wm := range m.ExportWatermarks {
			peer, source, named := strings.Cut(wk, "\x00")
			if !named {
				peer, source = "", wk
			}
			if source != meta.Path && source != key {
				continue
			}
			if from.IsZero() || wm < from.UnixNano() {
				continue
			}
			if peer == "" {
				unnamed = true
			} else {
				peers[peer] = true
			}
		}
	}
	out := make([]string, 0, len(peers))
	for p := range peers {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, unnamed
}

// PushedTo reports where one session's records have already gone: named peers
// and whether an unnamed directory export carried it. forget says this when it
// drops a session (#788); taking a decision back is the same moment for the
// same reason, and promote said nothing (#898).
func PushedTo(dir, harness, id string) ([]string, bool) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil, false
	}
	key := harness + ":" + id
	if _, ok := m.Sessions[key]; !ok {
		return nil, false
	}
	return pushedTo(m, map[string]bool{key: true})
}

func privacyDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "deja")
}

func tombstonePath() string { return filepath.Join(privacyDir(), "tombstones") }

// tombstoneMirrorPath is the second copy, kept beside the index the record
// protects. One file in ~/.config is a single point of failure for the one
// operation people use on things they specifically do not want kept: a wiped
// config directory, a machine migration or a changed XDG_CONFIG_HOME and the
// next rebuild resurrects them from source history that is still on disk.
func tombstoneMirrorPath(dir string) string {
	if dir == "" {
		dir = DefaultDir()
	}
	return filepath.Join(dir, "tombstones")
}

// readTombstones merges both copies: whichever survived is authoritative,
// because a tombstone can only ever be removed deliberately through unforget.
func readTombstones() map[string]bool {
	out := readTombstoneFile(tombstonePath())
	for key := range readTombstoneFile(tombstoneMirrorPath("")) {
		out[key] = true
	}
	return out
}

func readTombstoneFile(path string) map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if v := strings.TrimSpace(s.Text()); v != "" && !strings.HasPrefix(v, "#") {
			out[v] = true
		}
	}
	return out
}

func writeTombstones(set map[string]bool) error {
	if err := os.MkdirAll(privacyDir(), 0o700); err != nil {
		return err
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Tombstones are privacy policy, not cache: a crash mid-write must never
	// truncate the set and let a later rebuild resurrect forgotten sessions.
	// Write a sibling temp file, fsync, then rename over the live one.
	tmp := tombstonePath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := fmt.Fprintln(f, key); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, tombstonePath()); err != nil {
		return err
	}
	// The mirror is best effort: an index directory that cannot be written
	// is a problem the caller already has, and failing here would leave the
	// forget half-done after the authoritative copy is already on disk.
	_ = writeTombstoneMirror(keys)
	return nil
}

// appendTombstones adds keys to the set on disk without reading or rewriting
// what is already there. Forget only ever grows the set, and the full rewrite
// was the whole cost of the command on a machine with a large history (#1029).
func appendTombstones(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := os.MkdirAll(privacyDir(), 0o700); err != nil {
		return err
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	for _, key := range keys {
		buf.WriteString(key)
		buf.WriteByte('\n')
	}
	// O_RDWR because the append has to read the last byte: a process killed
	// between a key and its newline leaves the file ending mid-line, and the
	// next key appended onto that line loses both. Losing the interrupted key
	// is the accepted cost of writing without a lock; losing the forget after
	// it is not (#2195).
	f, err := os.OpenFile(tombstonePath(), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if atomicfile.EndsMidLine(f) {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			_ = f.Close()
			return err
		}
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func writeTombstoneMirror(keys []string) error {
	return writeTombstoneMirrorAt("", keys)
}

func writeTombstoneMirrorAt(dir string, keys []string) error {
	path := tombstoneMirrorPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := fmt.Fprintln(f, key); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func filterTombstoned(ss []model.Session) []model.Session {
	return filterTombstonedSet(ss, readTombstones())
}

func filterTombstonedSet(ss []model.Session, dead map[string]bool) []model.Session {
	if len(dead) == 0 {
		return ss
	}
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if !dead[s.Harness+":"+s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func sessionMatches(meta SessionMeta, o ForgetOptions) bool {
	// The same reading of an id as everywhere else: a result line elides the
	// middle of a long one, and forget was the last command that would not take
	// what the reader copied off the screen — answering "nothing was changed"
	// on a session that is there (#855).
	if o.Session != "" && !selectorMatches(meta, o.Session) {
		return false
	}
	if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
		return false
	}
	if !o.Before.IsZero() && !meta.Updated.Before(o.Before) {
		return false
	}
	return o.Session != "" || o.Project != "" || !o.Before.IsZero()
}

// SelectorMatches is selectorMatches for callers outside the package: the CLI
// has to tell what a lifted tombstone actually brought back from what it could
// not (#967).
func SelectorMatches(meta SessionMeta, sel string) bool { return selectorMatches(meta, sel) }

// selectorMatches reports whether a session answers to what someone typed or
// pasted. Beyond the id prefix and the elided form (#855), that includes the
// `harness:id` shape deja prints itself — in `forget --list`, in the undo line
// beside it, and in promote's receipts — which `show` refused and
// `forget --session` accepted while matching nothing (#921).
func selectorMatches(meta SessionMeta, sel string) bool {
	if selectorMatchesID(meta.ID, sel) || selectorMatchesOrigID(meta, sel) {
		return true
	}
	harness, id := splitSelector(sel)
	if harness == "" || !strings.EqualFold(meta.Harness, harness) {
		return false
	}
	return selectorMatchesID(meta.ID, id) || selectorMatchesOrigID(meta, id)
}

func selectorMatchesID(have, sel string) bool {
	return strings.HasPrefix(have, sel) || idMatchesElided(have, sel)
}

// selectorMatchesOrigID takes the id a session had where it was recorded. A
// sync rewrites the id, so the one the reader knows — the one the other machine
// prints, the one a note promoted from it carries — named nothing here, and the
// session could only be forgotten by an id nobody chose (#2843).
//
// The whole id, not a prefix of it: the local id is what a reader resolves
// against, and a prefix of the original is a guess about a session this machine
// never named that way.
func selectorMatchesOrigID(meta SessionMeta, sel string) bool {
	return meta.OrigID != "" && meta.OrigID == sel
}

// splitSelector takes the `harness:` off a pasted selector. The whole string is
// always tried first, so an id that happens to carry a colon keeps working;
// this only widens what matches.
func splitSelector(sel string) (harness, id string) {
	i := strings.IndexByte(sel, ':')
	if i <= 0 || i == len(sel)-1 {
		return "", sel
	}
	for _, r := range sel[:i] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", sel
		}
	}
	return sel[:i], sel[i+1:]
}

// PastedSelector strips what a paste carries around an id: surrounding
// whitespace and one wrapping pair of quotes or backticks. An id copied out of
// a chat arrives dressed like that, and every reading command refused it
// (#921).
func PastedSelector(raw string) string {
	s := strings.TrimSpace(raw)
	for len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') || (first == '`' && last == '`') {
			s = strings.TrimSpace(s[1 : len(s)-1])
			continue
		}
		if q, ok := strings.CutPrefix(s, "\u201c"); ok {
			if q, ok := strings.CutSuffix(q, "\u201d"); ok {
				s = strings.TrimSpace(q)
				continue
			}
		}
		if q, ok := strings.CutPrefix(s, "\u2018"); ok {
			if q, ok := strings.CutSuffix(q, "\u2019"); ok {
				s = strings.TrimSpace(q)
				continue
			}
		}
		break
	}
	return s
}

func Forget(dir string, o ForgetOptions) (ForgetResult, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return ForgetResult{}, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return ForgetResult{}, err
	}
	dead := readTombstones()
	var added []string
	matched := map[string]bool{}
	result := ForgetResult{}
	// An id that names a session exactly means that session. Prefix matching
	// is documented and useful, but it also made `forget --session s1` drop
	// s1 and every s1x beside it — twelve sessions for a selector that named
	// one, reported only afterwards (#870).
	exact := ""
	if o.Session != "" {
		for _, meta := range m.Sessions {
			if meta.ID == o.Session {
				exact = o.Session
				break
			}
		}
	}
	for key, meta := range m.Sessions {
		if exact != "" && meta.ID != exact {
			continue
		}
		if !sessionMatches(meta, o) {
			continue
		}
		matched[key] = true
		result.Sessions++
		result.Keys = append(result.Keys, key)
		if meta.Harness == "deja" {
			result.Notes++
			if strings.HasPrefix(meta.ID, "deja-note-") {
				result.Promoted++
			}
		}
		if !dead[key] {
			result.Tombstones++
			if !o.DryRun {
				added = append(added, key)
			}
		}
		if !o.DryRun {
			dead[key] = true
		}
	}
	if result.Sessions == 0 {
		return result, nil
	}
	sort.Strings(result.Keys)
	result.Peers, result.Exported = pushedTo(m, matched)
	// Count the messages on the dry run too. It is the same single pass over
	// records, and without it `--dry-run` answers "0 messages" to the only
	// question it exists to answer: how much am I about to lose.
	// A note forgotten here is forgotten as text, not as a day: the peer who
	// still holds it sends it under their own bucket id, which was a key this
	// index had never seen — and deja announced the line the reader had just
	// deleted (#985). The content key is the one #977 already dedupes on.
	var contentKeys []string
	for _, r := range readRecordsForForget(dir) {
		if !matched[r.Record.Key] {
			continue
		}
		result.Messages++
		meta, ok := m.Sessions[r.Record.Key]
		if !ok || meta.Harness != "deja" || !strings.HasPrefix(meta.ID, "deja-20") {
			continue
		}
		th := fnv.New64a()
		_, _ = th.Write([]byte(r.Record.Text))
		key := dayBucketKey(SyncRecord{Harness: "deja", SessionID: meta.ID, Project: meta.Project,
			Role: r.Record.Role, Time: r.Record.Time}, th.Sum64())
		if key != "" {
			contentKeys = append(contentKeys, r.Record.Key+"\x00"+key)
		}
	}
	if o.DryRun {
		return result, nil
	}
	for _, k := range contentKeys {
		if !dead[k] {
			added = append(added, k)
		}
		dead[k] = true
	}
	// Persist tombstones before the rebuild: a crash between the two must
	// leave sessions forgotten, not resurrect them on the next index pass.
	// Appending the new keys rather than rewriting the whole set: the set only
	// grows here, and rewriting it cost 4.9 s of a 6.6 s forget once a machine
	// had forgotten a million note lines (#1029). A torn append loses the keys
	// it was in the middle of writing, which is the same outcome as crashing a
	// moment earlier; unforget, which removes keys, still rewrites in full.
	if err := appendTombstones(added); err != nil {
		return result, err
	}
	if err := rebuildWithTombstones(dir, "", "", currentFiles(""), nil, dead); err != nil {
		return result, err
	}
	// After the rebuild, not before: rebuilding recreates the index directory
	// and takes the mirror with it, which is how the second copy silently
	// failed to exist the first time this was written.
	keys := make([]string, 0, len(dead))
	for key := range dead {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_ = writeTombstoneMirrorAt(dir, keys)
	return result, nil
}

func readRecordsForForget(dir string) []OffsetRecord {
	r, err := ReadRecords(dir)
	if err != nil {
		return nil
	}
	return r
}

func Tombstones() []string {
	set := readTombstones()
	out := make([]string, 0, len(set))
	for key := range set {
		// Content keys hang off the session they were recorded with; the list
		// is what someone reads and unforgets by name.
		if strings.Contains(key, "\x00") {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Tombstoned reports whether one exact harness:id has been forgotten here.
func Tombstoned(key string) bool { return readTombstones()[key] }

// TombstonesMatching lists the forgotten keys a selector would restore, in the
// order `forget --list` prints them.
func TombstonesMatching(prefix string) []string {
	var out []string
	for key := range readTombstones() {
		if strings.Contains(key, "\x00") {
			continue
		}
		if tombstoneMatches(key, prefix) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// TombstoneMatches counts the forgotten sessions a selector would bring back,
// without touching anything. The undo has to be able to refuse the way forget
// does: one word restored three sessions and said so afterwards (#961).
func TombstoneMatches(prefix string) int {
	n := 0
	for key := range readTombstones() {
		// Content keys ride along with their session and are lifted with it;
		// counting them told the reader one note was two sessions.
		if strings.Contains(key, "\x00") {
			continue
		}
		if tombstoneMatches(key, prefix) {
			n++
		}
	}
	return n
}

// tombstoneMatches is the selector Unforget applies, factored out so the count
// and the action cannot drift apart.
func tombstoneMatches(key, prefix string) bool {
	// A prefix containing ':' is a key/harness-scoped prefix (claude:abc, z:);
	// a bare prefix is an id-prefix symmetric with forget --session, so "c"
	// cannot resurrect every claude/codex/cursor session at once.
	if strings.ContainsRune(prefix, ':') {
		return strings.HasPrefix(key, prefix)
	}
	id := key
	if i := strings.IndexByte(key, ':'); i >= 0 {
		id = key[i+1:]
	}
	// Same widening as the forget selector: the id a reader copies off a
	// result line carries the elision (#855), and a session that arrived by
	// sync answers to the id it came with — which forget now takes, so undo
	// has to take it too or the reader can drop a session and not put it back
	// without reading `forget --list` for an id they never chose (#2843).
	if key == prefix || strings.HasPrefix(id, prefix) || idMatchesElided(id, prefix) {
		return true
	}
	harness, _, ok := strings.Cut(key, ":")
	return ok && id == ImportedSessionID(harness, prefix)
}

// Unforget lifts tombstones and reports how many it lifted. The count is not
// bookkeeping: the command printed nothing at all, so "restored one session"
// and "that prefix matched nothing" looked identical (#672).
//
// Unforget lifts the tombstones matching prefix and rebuilds so the sessions
// come back. The rebuild runs first and the reduced tombstone set is persisted
// only once it succeeds: interrupted the other way round, the tombstone that
// would let a retry work was already gone while the index had not been rebuilt
// yet, and nothing on the machine could say the session was missing (#810).
// A crash in the remaining window leaves the session present but still listed
// by `forget --list`, which is visible and fixed by running unforget again.
func Unforget(dir, prefix string, progress io.Writer) (int, error) {
	// The set is read under the lock, the way Forget reads it. Read before
	// locking, two unforgets of different sessions each got the whole set,
	// dropped their own key from their own copy, and wrote it back in turn:
	// both said "restored 1 session" and only the last one's session came
	// back, the other's tombstone reappeared out of the loser's stale copy.
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	set := readTombstones()
	lifted := 0
	for key := range set {
		session, _, hasContent := strings.Cut(key, "\x00")
		if hasContent {
			// Bringing a note back has to bring back the copies a peer may
			// still send, or the next import would drop them silently.
			if tombstoneMatches(session, prefix) {
				delete(set, key)
			}
			continue
		}
		if tombstoneMatches(key, prefix) {
			delete(set, key)
			lifted++
		}
	}
	if lifted == 0 {
		return 0, nil
	}
	// The transcript on disk has not changed since it was indexed, so an
	// incremental pass would skip it and the session would stay invisible
	// (#672) — hence a full rebuild, with the lifted tombstones already gone
	// from the set this pass applies.
	if err := rebuildWithTombstones(dir, "", "", currentFiles(""), progress, set); err != nil {
		return 0, err
	}
	if err := writeTombstones(set); err != nil {
		return lifted, err
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_ = writeTombstoneMirrorAt(dir, keys)
	return lifted, nil
}

func RedactionReport(dir string) (RedactionStats, error) { return Redactions(dir) }
