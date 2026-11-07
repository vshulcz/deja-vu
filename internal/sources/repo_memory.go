package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/model"
)

// Repo memory is a small file of picked entries committed in a repository:
// <root>/.deja/memory.jsonl. Notes and sync stay one person's; this file is
// how a decision, an approach that was dropped or a trap reaches the people
// who clone the repository. deja never writes it on its own — `deja repo add`
// and the MCP remember tool with a repo target do, and the person reviews the
// diff and commits it.
//
// One JSON object per line, sorted by id. Ids are a hash of the entry, so the
// same entry written by two people is one line, and two people adding entries
// at once usually touch different places in the file. The .gitattributes
// beside it asks git for a union merge when they do not.
const (
	RepoMemoryDir  = ".deja"
	RepoMemoryName = "memory.jsonl"
	// RepoGitattributes is written beside the file when deja makes .deja/.
	// linguist-generated collapses .deja/ diffs by default in GitHub PRs while
	// merge=union resolves parallel branch additions without conflicts.
	RepoGitattributes = "memory.jsonl merge=union linguist-generated=true\n"
)

const (
	// RepoTextMax bounds one entry. An entry is a sentence or two someone
	// picked, not a transcript; a longer one is refused when it is written and
	// cut when it is read.
	RepoTextMax = 600
	// repoFileMax and repoEntriesMax bound what a cloned repository can hand
	// every recall and every session start.
	repoFileMax    = 1 << 20
	repoEntriesMax = 500
	repoFilesMax   = 8
)

// RepoKinds are the kinds an entry may carry. "rejected" is the note state of
// the same name; "decision" is what an accepted note becomes in the repo.
// superseded and stale have no kind here: in a committed file the line is
// deleted instead.
var RepoKinds = map[string]bool{"decision": true, "rejected": true, "gotcha": true}

// RepoEntry is one line of the file, in the key order it is written.
type RepoEntry struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Files    []string `json:"files,omitempty"`
	Commit   string   `json:"commit,omitempty"`
	Sessions int      `json:"sessions,omitempty"`
	Date     string   `json:"date"`
	// From is the harness and day of the session the entry came from, never a
	// path or a session id: the file is read by people who do not have it.
	From string `json:"from,omitempty"`
}

// ErrRepoMemoryMalformed says the file holds a line deja cannot read: a
// conflict marker left by a merge, or a hand edit gone wrong. Writes refuse
// until it is fixed, so a rewrite never drops what someone put there.
var ErrRepoMemoryMalformed = errors.New("repo memory has a line deja cannot read")

// RepoEntryID is the id an entry gets: the kind and the text with case and
// spacing folded, hashed. FNV, not a cryptographic hash: the id only has to
// tell a few hundred entries apart and stay the same on every machine.
func RepoEntryID(kind, text string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(kind + "\x00" + strings.ToLower(strings.Join(strings.Fields(text), " "))))
	return fmt.Sprintf("%010x", h.Sum64()>>24)
}

// CleanRepoText folds an entry to the one line it is stored as: control and
// format characters out, whitespace runs to one space.
func CleanRepoText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// RepoMemoryPath is the file in a repository root.
func RepoMemoryPath(root string) string {
	return filepath.Join(root, RepoMemoryDir, RepoMemoryName)
}

// IsRepoMemoryFile reports whether p is a repo memory file.
func IsRepoMemoryFile(p string) bool {
	return filepath.Base(p) == RepoMemoryName && filepath.Base(filepath.Dir(p)) == RepoMemoryDir
}

// RepoMemoryProject is the project the entries of a checkout are filed under:
// "repo:" and the name this machine gives the checkout, so they scope with the
// reader's own sessions there and the trust policy can tell them apart.
func RepoMemoryProject(root string) string {
	return model.RepoProjectPrefix + ClaudeProjectName(root)
}

// ReadRepoMemory reads the entries of a file, deduped by id and sorted. bad is
// the count of lines it could not read; they are skipped.
func ReadRepoMemory(path string) (entries []RepoEntry, bad int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	return readRepoMemory(io.LimitReader(f, repoFileMax))
}

func readRepoMemory(r io.Reader) ([]RepoEntry, int, error) {
	seen := map[string]bool{}
	var out []RepoEntry
	bad := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), repoFileMax)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		e, ok := parseRepoLine(line)
		if !ok {
			bad++
			continue
		}
		if seen[e.ID] || len(out) >= repoEntriesMax {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return out, bad + 1, nil //nolint:nilerr // a line past the cap is one bad line, not an unreadable file
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, bad, nil
}

func parseRepoLine(line []byte) (RepoEntry, bool) {
	var e RepoEntry
	if json.Unmarshal(line, &e) != nil {
		return RepoEntry{}, false
	}
	e.Kind = strings.ToLower(strings.TrimSpace(e.Kind))
	e.Text = CleanRepoText(e.Text)
	if !RepoKinds[e.Kind] || e.Text == "" {
		return RepoEntry{}, false
	}
	if r := []rune(e.Text); len(r) > RepoTextMax {
		e.Text = string(r[:RepoTextMax]) + "…"
	}
	e.ID = strings.TrimSpace(e.ID)
	if e.ID == "" {
		e.ID = RepoEntryID(e.Kind, e.Text)
	}
	e.ID = CleanRepoText(e.ID)
	e.Files = CleanRepoFiles(e.Files)
	e.Commit = CleanRepoCommit(e.Commit)
	if e.Sessions < 0 {
		e.Sessions = 0
	}
	e.Date = CleanRepoText(e.Date)
	e.From = CleanRepoText(e.From)
	return e, true
}

// CleanRepoFiles normalizes, deduplicates and bounds repo-relative file paths.
func CleanRepoFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range files {
		p := filepath.ToSlash(strings.TrimSpace(CleanRepoText(raw)))
		p = strings.TrimPrefix(p, "./")
		if p == "" || p == "." || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") || strings.ContainsRune(p, '\\') || len(p) > 200 {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= repoFilesMax {
			break
		}
	}
	sort.Strings(out)
	return out
}

// CleanRepoCommit keeps only a short lowercase hexadecimal commit SHA.
func CleanRepoCommit(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 7 || len(s) > 40 {
		return ""
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	if len(s) > 12 {
		s = s[:12]
	}
	return s
}

// RepoEntryBody is the text an entry is indexed and shown as. The bracket
// is the kind, the way a promoted note shows its state; the tail says where it
// lives so no reader takes it for their own session.
func RepoEntryBody(e RepoEntry) string {
	tail := "in " + RepoMemoryDir + "/" + RepoMemoryName
	if e.From != "" {
		tail += ", from " + e.From
	}
	return "[" + e.Kind + "] " + e.Text + " (" + tail + ")"
}

// ParseRepoMemoryFile reads a repo memory file as sessions, one per entry.
func ParseRepoMemoryFile(path string) ([]model.Session, error) {
	entries, bad, err := ReadRepoMemory(path)
	if err != nil {
		return nil, err
	}
	for range bad {
		diagMalformedLine(path)
	}
	root := filepath.Dir(filepath.Dir(path))
	project := RepoMemoryProject(root)
	idProject := noteIDProject(strings.TrimPrefix(project, model.RepoProjectPrefix))
	var mod time.Time
	if fi, statErr := os.Stat(path); statErr == nil {
		mod = fi.ModTime()
	}
	out := make([]model.Session, 0, len(entries))
	for _, e := range entries {
		t, perr := time.Parse("2006-01-02", e.Date)
		if perr != nil {
			t = mod
		}
		s := model.Session{
			ID:      "deja-repo-" + idProject + "-" + e.ID,
			Harness: "deja", Project: project, Path: path,
			Title: "[" + e.Kind + "] " + truncateRunes(e.Text, 80),
		}
		s.Touch(t)
		msgs := make([]model.Message, 0, 1+len(e.Files))
		msgs = append(msgs, model.Message{Role: "user", Text: RepoEntryBody(e), Time: t})
		for _, f := range e.Files {
			msgs = append(msgs, model.Message{Role: "edit", Text: filepath.Join(root, filepath.FromSlash(f)), Time: t})
		}
		s.Messages = msgs
		out = append(out, s)
	}
	return out, nil
}

// AddRepoEntries writes entries into root's file and returns the ones that
// were not already there. The file is rewritten sorted and deduped through a
// temp file; the .gitattributes beside it is written when it is missing.
// Entries are taken as given: the caller has already redacted them.
func AddRepoEntries(root string, entries []RepoEntry) ([]RepoEntry, error) {
	dir := filepath.Join(root, RepoMemoryDir)
	path := RepoMemoryPath(root)
	for _, p := range []string{dir, path} {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symlink", p)
		}
	}
	var existing []RepoEntry
	if b, err := os.ReadFile(path); err == nil {
		if len(b) > repoFileMax {
			return nil, fmt.Errorf("%s is over %d bytes", path, repoFileMax)
		}
		var bad int
		existing, bad, _ = readRepoMemory(bytes.NewReader(b))
		if bad > 0 {
			return nil, ErrRepoMemoryMalformed
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	have := map[string]int{}
	for i, e := range existing {
		have[e.ID] = i + 1
	}
	var added []RepoEntry
	all := existing
	updatedExisting := false
	for _, e := range entries {
		if !RepoKinds[e.Kind] || strings.TrimSpace(e.Text) == "" {
			return nil, errors.New("an entry needs a kind (decision, rejected or gotcha) and text")
		}
		if e.ID == "" {
			e.ID = RepoEntryID(e.Kind, e.Text)
		}
		e.Files = CleanRepoFiles(e.Files)
		e.Commit = CleanRepoCommit(e.Commit)
		if e.Sessions < 0 {
			e.Sessions = 0
		}
		if idx := have[e.ID]; idx > 0 {
			merged, changed := mergeRepoEntry(all[idx-1], e)
			if changed {
				all[idx-1] = merged
				updatedExisting = true
			}
			continue
		}
		have[e.ID] = len(all) + 1
		added = append(added, e)
		all = append(all, e)
	}
	if len(added) == 0 && !updatedExisting {
		return nil, nil
	}
	if len(all) > repoEntriesMax {
		return nil, fmt.Errorf("%s would hold more than %d entries", path, repoEntriesMax)
	}
	if err := writeRepoMemorySlice(root, dir, path, all); err != nil {
		return nil, err
	}
	return added, nil
}

func mergeRepoEntry(cur, inc RepoEntry) (RepoEntry, bool) {
	changed := false
	files := CleanRepoFiles(append(slices.Clone(cur.Files), inc.Files...))
	if !slices.Equal(files, cur.Files) {
		cur.Files = files
		changed = true
	}
	if cur.Commit == "" && inc.Commit != "" {
		cur.Commit = inc.Commit
		changed = true
	}
	if inc.Sessions > cur.Sessions {
		cur.Sessions = inc.Sessions
		if strings.HasSuffix(cur.Text, "sessions)") && strings.HasSuffix(inc.Text, "sessions)") && cur.Text != inc.Text {
			cur.Text = inc.Text
			cur.ID = RepoEntryID(cur.Kind, cur.Text)
		}
		changed = true
	}
	if cur.From == "" && inc.From != "" {
		cur.From = inc.From
		changed = true
	}
	return cur, changed
}

func writeRepoMemorySlice(root, dir, path string, all []RepoEntry) error {
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, e := range all {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	if prev, err := os.ReadFile(path); err == nil && bytes.Equal(prev, buf.Bytes()) {
		RememberRepoCheckout(root)
		return nil
	}
	_, statErr := os.Lstat(dir)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := atomicfile.Write(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	// The two files beside it are written once, when deja makes the
	// directory. Someone who deletes or edits either afterwards meant to.
	if created {
		for name, body := range map[string]string{".gitattributes": RepoGitattributes, "README.md": RepoReadme} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				return err
			}
		}
	}
	RememberRepoCheckout(root)
	return nil
}

// EvolveRepoEntries updates root's .deja/memory.jsonl by pruning entries whose
// tracked files were all deleted from the repository, merging file/commit/session
// reinforcement into existing entries, adding new high-signal entries, and
// capping total entries to maxEntries (when > 0) by signal rank.
func EvolveRepoEntries(root string, incoming []RepoEntry, maxEntries int) (added, updated, pruned int, err error) {
	dir := filepath.Join(root, RepoMemoryDir)
	path := RepoMemoryPath(root)
	for _, p := range []string{dir, path} {
		if fi, statErr := os.Lstat(p); statErr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return 0, 0, 0, fmt.Errorf("%s is a symlink", p)
		}
	}
	var existing []RepoEntry
	if b, readErr := os.ReadFile(path); readErr == nil {
		if len(b) > repoFileMax {
			return 0, 0, 0, fmt.Errorf("%s is over %d bytes", path, repoFileMax)
		}
		var bad int
		existing, bad, _ = readRepoMemory(bytes.NewReader(b))
		if bad > 0 {
			return 0, 0, 0, ErrRepoMemoryMalformed
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return 0, 0, 0, readErr
	}
	// 1. Self-heal: prune entries whose bound files were all removed from root.
	var surviving []RepoEntry
	for _, e := range existing {
		if len(e.Files) > 0 {
			live := filterExistingRepoFiles(root, e.Files)
			if len(live) == 0 {
				pruned++
				continue
			}
			if !slices.Equal(live, e.Files) {
				e.Files = live
				updated++
			}
		}
		surviving = append(surviving, e)
	}
	byID := map[string]int{}
	byText := map[string]int{}
	for i, e := range surviving {
		byID[e.ID] = i + 1
		if tk := repoNormTextKey(e.Text); tk != "" {
			byText[tk] = i + 1
		}
	}
	for _, e := range incoming {
		if !RepoKinds[e.Kind] || strings.TrimSpace(e.Text) == "" {
			continue
		}
		if e.ID == "" {
			e.ID = RepoEntryID(e.Kind, e.Text)
		}
		e.Files = filterExistingRepoFiles(root, CleanRepoFiles(e.Files))
		e.Commit = CleanRepoCommit(e.Commit)
		if e.Sessions < 0 {
			e.Sessions = 0
		}
		idx := byID[e.ID]
		tk := repoNormTextKey(e.Text)
		if idx == 0 && tk != "" {
			idx = byText[tk]
		}
		if idx > 0 {
			merged, changed := mergeRepoEntry(surviving[idx-1], e)
			if changed {
				surviving[idx-1] = merged
				updated++
			}
			continue
		}
		byID[e.ID] = len(surviving) + 1
		if tk != "" {
			byText[tk] = len(surviving) + 1
		}
		surviving = append(surviving, e)
		added++
	}
	if maxEntries > 0 && len(surviving) > maxEntries {
		rank := map[string]int{"rejected": 0, "gotcha": 1, "decision": 2}
		sort.SliceStable(surviving, func(i, j int) bool {
			a, b := surviving[i], surviving[j]
			if (len(a.Files) > 0) != (len(b.Files) > 0) {
				return len(a.Files) > 0
			}
			if rank[a.Kind] != rank[b.Kind] {
				return rank[a.Kind] < rank[b.Kind]
			}
			if a.Sessions != b.Sessions {
				return a.Sessions > b.Sessions
			}
			if (a.Commit != "") != (b.Commit != "") {
				return a.Commit != ""
			}
			return a.Date > b.Date
		})
		surviving = surviving[:maxEntries]
	}
	if added == 0 && updated == 0 && pruned == 0 {
		return 0, 0, 0, nil
	}
	if len(surviving) == 0 && !regularFile(path) {
		return 0, 0, 0, nil
	}
	if err := writeRepoMemorySlice(root, dir, path, surviving); err != nil {
		return 0, 0, 0, err
	}
	return added, updated, pruned, nil
}

func filterExistingRepoFiles(root string, files []string) []string {
	if root == "" || len(files) == 0 {
		return nil
	}
	var out []string
	for _, f := range files {
		if regularFile(filepath.Join(root, filepath.FromSlash(f))) {
			out = append(out, f)
		}
	}
	return out
}

func repoNormTextKey(text string) string {
	norm := strings.ToLower(strings.Join(strings.Fields(CleanRepoText(text)), " "))
	if idx := strings.LastIndex(norm, " ("); idx > 20 && strings.HasSuffix(norm, "sessions)") {
		norm = strings.TrimSpace(norm[:idx])
	}
	r := []rune(norm)
	if len(r) > 65 {
		r = r[:65]
	}
	return string(r)
}

func repoOffListPath() string {
	return filepath.Join(filepath.Dir(NotesFile()), "repo-off.txt")
}

// RepoAutoDisabled reports whether automatic .deja/memory.jsonl evolution is
// turned off for root (via DEJA_REPO=off or `deja repo off`).
func RepoAutoDisabled(root string) bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_REPO"))); v == "off" || v == "0" || v == "false" {
		return true
	}
	if root == "" {
		return false
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	b, err := os.ReadFile(repoOffListPath())
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.TrimSpace(line) == root {
			return true
		}
	}
	return false
}

// SetRepoAutoDisabled turns automatic .deja/memory.jsonl evolution off or on
// for root.
func SetRepoAutoDisabled(root string, off bool) error {
	if root == "" {
		return errors.New("empty repository root")
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	p := repoOffListPath()
	var current []string
	if b, err := os.ReadFile(p); err == nil {
		for line := range strings.SplitSeq(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && line != root {
				current = append(current, line)
			}
		}
	}
	if off {
		current = append(current, root)
	}
	sort.Strings(current)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	body := ""
	if len(current) > 0 {
		body = strings.Join(current, "\n") + "\n"
	}
	return atomicfile.Write(p, []byte(body), 0o600)
}

// RemoveRepoEntries deletes entries matching ids from root's .deja/memory.jsonl
// and returns the entries that were removed.
func RemoveRepoEntries(root string, ids []string) ([]RepoEntry, error) {
	path := RepoMemoryPath(root)
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink", path)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > repoFileMax {
		return nil, fmt.Errorf("%s is over %d bytes", path, repoFileMax)
	}
	existing, bad, _ := readRepoMemory(bytes.NewReader(b))
	if bad > 0 {
		return nil, ErrRepoMemoryMalformed
	}
	drop := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "#")
		if id != "" {
			drop[id] = true
		}
	}
	var kept, removed []RepoEntry
	for _, e := range existing {
		if drop[strings.ToLower(e.ID)] {
			removed = append(removed, e)
		} else {
			kept = append(kept, e)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, e := range kept {
		if err := enc.Encode(e); err != nil {
			return nil, err
		}
	}
	if err := atomicfile.Write(path, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return removed, nil
}

// RepoReadme is what a person who opens .deja/ in the repository reads.
const RepoReadme = `# .deja

This folder is project memory for deja. Each line of memory.jsonl is a decision, a dropped approach or a gotcha that someone picked from past coding agent sessions and reviewed before committing.

With deja installed, your coding agent gets these entries at session start and in recall after you clone. Without it, memory.jsonl is plain JSON lines you can read and edit; deleting a line removes an entry.

https://github.com/vshulcz/deja-vu
`

// repoListPath is where deja keeps the repo memory files it has seen, beside
// the notes file. The index reads every one of them, whatever directory a
// pass runs from; a file set that changed with the cwd would drop the entries
// from the index each time deja ran somewhere else.
func repoListPath() string {
	return filepath.Join(filepath.Dir(NotesFile()), "repos.txt")
}

func readRepoList() []string {
	b, err := os.ReadFile(repoListPath())
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" && filepath.IsAbs(line) && IsRepoMemoryFile(line) {
			out = append(out, line)
		}
	}
	return out
}

// RememberRepoCheckout records the repo memory file of the checkout dir sits
// in, when it has one. Best effort: a list that cannot be written only means
// the entries are read when deja runs from that checkout.
func RememberRepoCheckout(dir string) {
	if dir == "" {
		return
	}
	root := RepoRoot(dir)
	if root == "" {
		return
	}
	p := RepoMemoryPath(root)
	if !regularFile(p) {
		return
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	known := readRepoList()
	if slices.Contains(known, p) {
		return
	}
	var keep []string
	for _, k := range known {
		if regularFile(k) {
			keep = append(keep, k)
		}
	}
	keep = append(keep, p)
	sort.Strings(keep)
	list := repoListPath()
	if err := os.MkdirAll(filepath.Dir(list), 0o700); err != nil {
		return
	}
	_ = atomicfile.Write(list, []byte(strings.Join(keep, "\n")+"\n"), 0o600)
}

func regularFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// RepoMemoryFiles is every repo memory file the index reads: the ones deja has
// seen, and the one of the checkout this process runs in.
func RepoMemoryFiles() []string {
	if cwd, err := os.Getwd(); err == nil && allowImplicitRepoCwd(cwd) {
		RememberRepoCheckout(cwd)
	}
	var out []string
	for _, p := range readRepoList() {
		if regularFile(p) {
			out = append(out, p)
		}
	}
	return out
}

// LoadRepoMemory reads every known repo memory file.
func LoadRepoMemory() []model.Session {
	var out []model.Session
	for _, p := range RepoMemoryFiles() {
		ss, err := ParseRepoMemoryFile(p)
		diagFileError(p, err)
		out = append(out, ss...)
	}
	return out
}
