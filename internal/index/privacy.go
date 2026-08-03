package index

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	// Notes is how many of Sessions were promoted notes rather than raw
	// transcripts. They are the decisions the user deliberately kept, so one
	// combined number reads as "four conversations" when half of it is their
	// own writing.
	Notes int
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

// selectorMatches reports whether a session answers to what someone typed or
// pasted. Beyond the id prefix and the elided form (#855), that includes the
// `harness:id` shape deja prints itself — in `forget --list`, in the undo line
// beside it, and in promote's receipts — which `show` refused and
// `forget --session` accepted while matching nothing (#921).
func selectorMatches(meta SessionMeta, sel string) bool {
	if strings.HasPrefix(meta.ID, sel) || idMatchesElided(meta.ID, sel) {
		return true
	}
	harness, id := splitSelector(sel)
	if harness == "" || !strings.EqualFold(meta.Harness, harness) {
		return false
	}
	return strings.HasPrefix(meta.ID, id) || idMatchesElided(meta.ID, id)
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
		}
		if !dead[key] {
			result.Tombstones++
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
	for _, r := range readRecordsForForget(dir) {
		if matched[r.Record.Key] {
			result.Messages++
		}
	}
	if o.DryRun {
		return result, nil
	}
	// Persist tombstones before the rebuild: a crash between the two must
	// leave sessions forgotten, not resurrect them on the next index pass.
	if err := writeTombstones(dead); err != nil {
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
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Unforget lifts tombstones and reports how many it lifted. The count is not
// bookkeeping: the command printed nothing at all, so "restored one session"
// and "that prefix matched nothing" looked identical (#672).
// Unforget lifts the tombstones matching prefix and rebuilds so the sessions
// come back. The rebuild runs first and the reduced tombstone set is persisted
// only once it succeeds: interrupted the other way round, the tombstone that
// would let a retry work was already gone while the index had not been rebuilt
// yet, and nothing on the machine could say the session was missing (#810).
// A crash in the remaining window leaves the session present but still listed
// by `forget --list`, which is visible and fixed by running unforget again.
func Unforget(dir, prefix string, progress io.Writer) (int, error) {
	set := readTombstones()
	// A prefix containing ':' is a key/harness-scoped prefix (claude:abc,
	// z:); a bare prefix is an id-prefix symmetric with forget --session, so
	// "c" cannot resurrect every claude/codex/cursor session at once.
	scoped := strings.ContainsRune(prefix, ':')
	lifted := 0
	for key := range set {
		var match bool
		if scoped {
			match = strings.HasPrefix(key, prefix)
		} else {
			id := key
			if i := strings.IndexByte(key, ':'); i >= 0 {
				id = key[i+1:]
			}
			// Same widening as the forget selector: the id a reader copies off
			// a result line carries the elision (#855).
			match = key == prefix || strings.HasPrefix(id, prefix) || idMatchesElided(id, prefix)
		}
		if match {
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
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
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
