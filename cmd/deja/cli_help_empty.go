package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

func parseSearch(args []string) (search.Options, error) {
	o := search.Options{}
	var q []string
	// --limit=100 used to fall through and be searched for as a query term,
	// silently returning results for a different question than the one asked.
	args = splitEqualsForms(args)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// End of options: the rest is the query verbatim, even a word that
			// spells a flag. Without this there is no way to search for the
			// literal text of a flag name — `deja -- --json` kept parsing
			// --json as the flag and left "--" stranded in the query.
			q = append(q, args[i+1:]...)
			break
		}
		switch a {
		case "--json":
			o.JSON = true
		case "--re":
			o.Regex = true
		case "--all":
			o.All = true
		case "--no-embed":
			o.NoEmbed = true
		case "--harness", "--project", "--since", "--role", "--limit", "--session", "--sort":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			v := args[i]
			// Empty is how "no filter" is spelled inside deja, so an empty
			// argument reached the search as no filter at all and a scripted
			// `--project "$PROJECT"` with the variable unset quietly returned
			// the whole store (#1612). It is the same mistake as leaving the
			// value off, so it gets the same sentence.
			if strings.TrimSpace(v) == "" {
				return o, fmt.Errorf("%s needs value", a)
			}
			switch a {
			case "--harness":
				o.Harness = v
			case "--project":
				o.Project = v
			case "--role":
				o.Role = v
			case "--session":
				o.Session = v
			case "--sort":
				switch strings.ToLower(v) {
				case search.SortDate, "newest":
					o.Sort = search.SortDate
				case search.SortRelevance:
					o.Sort = search.SortRelevance
				default:
					return o, fmt.Errorf("--sort takes date or relevance, not %q", v)
				}
			case "--limit":
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 100 {
					return o, errors.New("--limit needs an integer from 1 to 100")
				}
				o.Limit = n
			default:
				d, err := parseDur(v)
				if err != nil {
					return o, err
				}
				o.Since = d
			}
		default:
			// A query may legitimately start with a dash — `deja search
			// "--retry budget"` is a real search. A token one edit away from a
			// real flag is not: folding `--limti` into the query turned a
			// working search into "you have no such memory" (#755).
			if nearestSearchFlag(a) != "" {
				return o, unknownFlag("search", a, searchFlags)
			}
			// A flag deja takes elsewhere is not a typo and is nowhere near a
			// search flag by edit distance, so it went into the query and the
			// search that would have found everything reported nothing (#2249).
			if cmd := flagsOfOtherCommands[flagName(a)]; cmd != "" {
				return o, fmt.Errorf("%s is a flag of `deja %s`, not of search — put it after `--` to search for the text", flagName(a), cmd)
			}
			// A --word after the query is a flag deja does not have. Taken as
			// a query term it turned `deja goroutines --bogus` into a search
			// for both words that found nothing, where the word alone had 40
			// hits. A query that starts with one (`--retry budget`), a phrase
			// with a dash inside, or a single dash is still a query.
			if len(q) > 0 && flagShaped(flagName(a)) {
				return o, fmt.Errorf("search: unknown flag %q — `deja search --help` lists its flags; put it after `--` to search for the text", a)
			}
			q = append(q, a)
		}
	}
	// Match the NFC canonicalisation ingest applies to stored text, so a query
	// typed in either normalisation reaches the same records (#1098). Trim
	// surrounding space: a leading space left the exact-match tier hunting for
	// " token" and an exact-only term (an error code, a coined name) missed,
	// while a real word was rescued by its word-forms and hid the gap.
	o.Query = nfcfold.Compose(strings.TrimSpace(strings.Join(q, " ")))
	if o.Query == "" {
		return o, errors.New("query required")
	}
	return o, nil
}

// flagShaped reports whether a token reads as a long flag: two dashes, then
// letters, digits and inner dashes, nothing else.
func flagShaped(a string) bool {
	if len(a) < 3 || !strings.HasPrefix(a, "--") {
		return false
	}
	for i, r := range a[2:] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9', r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// flagsOfOtherCommands names the command each flag belongs to, for the tokens
// that are real deja flags somewhere but not here. Only exact matches: a query
// may legitimately start with a dash, and `--` still ends option parsing.
var flagsOfOtherCommands = map[string]string{
	"--offset":           "show",
	"--span":             "restore",
	"--out":              "restore",
	"--force":            "restore",
	"--tag":              "remember",
	"--repo":             "remember",
	"--deep":             "doctor",
	"--offline":          "doctor",
	"--dry-run":          "forget",
	"--all-matches":      "forget",
	"--list":             "forget",
	"--unforget":         "forget",
	"--before":           "forget",
	"--to":               "handoff",
	"--exec":             "resume",
	"--write-back":       "resume",
	"--app":              "resume",
	"--plain":            "hook-prompt",
	"--crush":            "hook-tool",
	"--junie":            "hook-prompt",
	"--no-open":          "view",
	"--full":             "sync export",
	"--include-imported": "sync export",
	"--peer":             "sync export",
	"--pull":             "sync ssh",
	"--both":             "sync ssh",
	"--from":             "last",
	"--last":             "log",
	"--seed":             "bench",
	"--settle":           "friction",
	"--unsettle":         "friction",
	"--state":            "promote",
	"--note":             "promote",
	"--html":             "stats",
	"--redaction":        "stats",
	"--impact":           "stats",
	"--card":             "stats",
	"--year":             "stats",
	"--attribution":      "blame",
	"--git-note":         "blame",
	"--all-projects":     "how",
	"--scrub":            "secrets",
	"--kind":             "repo add",
	"--yes":              "delete",
	"--quiet":            "index",
	"--context-search":   "mcp",
	"--http":             "mcp",
}

// searchFlags is every flag the bare search form accepts, for the typo check.
// The flags each converted command takes, in the order its own parser lists
// them. A list that drifts from its parser costs a wrong suggestion, not a
// wrong refusal, and unknown_flag_lists_test.go holds them together.
var (
	indexFlags  = []string{"--rebuild", "--quiet"}
	showFlags   = []string{"--json", "--harness", "--offset", "--limit"}
	forgetFlags = []string{
		"--list", "--dry-run", "--all-matches",
		"--session", "--project", "--before", "--unforget",
	}
	statsFlags = []string{
		"--json", "--html", "--redaction", "--impact", "--card", "--year",
		"--harness", "--project", "--since", "--role",
	}
	doctorFlags   = []string{"--json", "--offline", "--deep", "--all"}
	viewFlags     = []string{"--out", "--no-open"}
	logFlags      = []string{"--json", "--last"}
	rememberFlags = []string{"--project", "--tag", "--repo"}
	restoreFlags  = []string{"--force", "--span", "--out", "--all"}
	promoteFlags  = []string{"--state", "--note", "--to", "--tag", "--repo"}
	handoffFlags  = []string{"--to", "--exec"}
)

var searchFlags = []string{
	"--json", "--re", "--all", "--no-embed", "--rebuild",
	"--harness", "--project", "--since", "--role", "--limit", "--session", "--sort",
}

// nearestSearchFlag names the search flag a token was probably meant to be.
// The rule it introduced is now every command's, in nearestKnownFlag; this is
// the search-shaped name for it, kept because search is the one command where
// the answer also decides whether a dashed token is a query term.
//
// --sort is left out of the near misses: --soft, --port and --short are one
// edit from it and are things people search for (`git reset --soft`).
func nearestSearchFlag(a string) string {
	return nearestKnownFlag(a, searchNearMissFlags)
}

var searchNearMissFlags = func() []string {
	var out []string
	for _, f := range searchFlags {
		if f != "--sort" {
			out = append(out, f)
		}
	}
	return out
}()

// parseDur is the window a reader asked to look back over, so it has to be one:
// zero or less used to parse and then disappear, because every caller cuts on
// `Since > 0` — `--since -1d` searched the whole store and nothing in the answer
// said the filter had been dropped (#1610). parseDurAny is the raw form, for
// `forget --before`, which has its own word for the same mistake.
func parseDur(s string) (time.Duration, error) {
	d, err := parseDurAny(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q is not a window — --since counts back from now, so it needs a positive duration like 30d", s)
	}
	return d, nil
}

// maxDurationDays is how many whole days fit in a time.Duration: past it the
// multiplication wraps, and `--since 365000d` — the way to say "all of it" —
// came out negative and dropped the filter it was asked for.
const maxDurationDays = int(math.MaxInt64 / (24 * int64(time.Hour)))

func parseDurAny(s string) (time.Duration, error) {
	if before, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(before)
		if err != nil {
			return 0, durationError(s)
		}
		if n > maxDurationDays {
			return time.Duration(math.MaxInt64), nil
		}
		if n < -maxDurationDays {
			return time.Duration(math.MinInt64), nil
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// time.ParseDuration's own message names Go's syntax, not deja's, and
		// it does not mention days — which is the unit people reach for here.
		return 0, durationError(s)
	}
	return d, nil
}

func durationError(s string) error {
	return fmt.Errorf("%q is not a duration deja understands — try 30d, 12h, or 90m", s)
}

func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// wrapTargets lays install target names out over as many indented lines as they
// need. Naming them inline in the usage text is what let help drift: eleven of
// the thirty-one were listed, so `deja install aider` and `deja install
// openclaw-auto` — both of them in the README — read as invalid (#1106).
func wrapTargets(names []string, indent string, width int) string {
	var b strings.Builder
	line := indent
	for i, n := range names {
		piece := n
		if i < len(names)-1 {
			piece += ","
		}
		if len(line) > len(indent) && len(line)+1+len(piece) > width {
			b.WriteString(line + "\n")
			line = indent
		}
		if len(line) > len(indent) {
			line += " "
		}
		line += piece
	}
	return b.String() + line
}

func printUsage() {
	fmt.Print(wrapUsage(usageText(), printableWidth(os.Stdout)))
}

// usageWidth is the column budget for the parts of help that are laid out
// rather than typed. It follows the terminal, as the brief, files, restore and
// search already do — the list was computed to a fixed 76 and so came out the
// same six lines on a 30-column pane and a 120-column one (#1660).
//
// Off a terminal printableWidth answers 0, and the fixed 76 stands: piped and
// redirected help keeps the byte-identical output that goldens and CI compare.
func usageWidth() int {
	if w := printableWidth(os.Stdout); w > 0 {
		return w
	}
	return 76
}

// wantsHelp reports whether a command line asks for help rather than work.
func wantsHelp(rest []string) bool {
	for _, a := range rest {
		if a == "--help" || a == "-h" {
			return true
		}
		if a == "--" {
			return false
		}
	}
	return false
}

// movedBucketHint explains a note-bucket id that stopped resolving. The id
// carries the day it was minted in, so a machine that changed zone renames its
// buckets on the next build — and every id refusal then read as "that note is
// gone" while the note sat under the neighbouring day (#1039).
func movedBucketHint(dir, id string) string {
	if !strings.HasPrefix(id, "deja-") || len(id) < 15 {
		return ""
	}
	day, rest := id[5:15], id[15:]
	when, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	pol := policy.Load()
	for _, shift := range []int{-1, 1} {
		want := "deja-" + when.AddDate(0, 0, shift).Format("2006-01-02") + rest
		for _, m := range metas {
			if m.ID != want {
				continue
			}
			// Naming it is recalling it: a rule that hides the session hides
			// the fact that it moved too, or the hint becomes the way around
			// the rule (#1043).
			if !metaVisible(pol, policy.ActivationSearch, m) {
				return ""
			}
			return fmt.Sprintf(" — the days regrouped when this machine's zone changed; it is %#q now", want)
		}
	}
	return ""
}

// showNeedsID is the refusal show gives with no argument. "id-prefix" names a
// thing the reader has no way to produce on their own; promote has pointed at
// `deja last` all along, and show, share and resume are the three commands
// reached for right after a search result (#1063).
const showNeedsID = "show needs an id prefix (see `deja last`)"

// noSessionMatches is the refusal for an id that names no session: the bucket
// that moved when there is one, otherwise where the ids are listed (#1063).
func noSessionMatches(dir, id string) error {
	if hint := movedBucketHint(dir, id); hint != "" {
		return fmt.Errorf("no session matches %q%s", id, hint)
	}
	return fmt.Errorf("no session matches %q — `deja last` lists recent ones", id)
}

// idPrefixNeeded is the refusal for a command that needs a session named on the
// command line. "see `deja last`" is a step the reader can take and learn
// nothing from when the store is empty: the listing answers with the same
// emptiness deja already knows about here (#992).
func idPrefixNeeded(dir, subject, refusal string) error {
	if n, err := index.SessionCount(dir); err == nil && n == 0 {
		return errors.New(strings.TrimPrefix(emptyIndexHint(subject+", and nothing is indexed yet"), "deja: "))
	}
	return errors.New(refusal)
}

// indexIsEmpty reports whether the index holds no session at all. An index
// that cannot be counted counts as empty on purpose, so the caller falls back
// to the empty-index hint it printed before.
func indexIsEmpty(dir string) bool {
	n, err := index.SessionCount(dir)
	return err != nil || n == 0
}

// emptyIndexReason opens the empty-index sentence. "Nothing to index yet" is
// for a machine deja has never seen history from; a run that has just evicted a
// store says what went away instead, because the line above it has already told
// the reader what was lost and the two must not contradict each other (#1762).
func emptyIndexReason(_ index.BuildSummary, evicted int) string {
	if evicted > 0 {
		return emptyIndexHint(fmt.Sprintf("%d indexed file%s went away with the store that held %s, and nothing is left to index",
			evicted, pluralS(evicted), pluralWhich(evicted)))
	}
	return emptyIndexHint("nothing to index yet")
}

// deniedStoresLine names the stores deja could not read on a pass whose index
// still holds sessions, with the files the pass evicted when there were any.
func deniedStoresLine(denied, evicted int) string {
	line := fmt.Sprintf("%d store%s could not be read (permission denied); `deja doctor` names %s",
		denied, pluralS(denied), pluralWhich(denied))
	if evicted > 0 {
		line = fmt.Sprintf("%d indexed file%s went away with the store that held %s — %s",
			evicted, pluralS(evicted), pluralWhich(evicted), line)
	}
	return "deja: " + line
}

// emptyIndexHint phrases the nothing-here answer the same way everywhere, and
// points at the next command rather than leaving the user to guess.
//
// Which command depends on why it is empty. Every path that reaches here has
// already built the index — the build narration prints a line above this one —
// so telling someone to run `deja index` sends them to do again what just
// happened, and they are left where they started. When no agent history was
// found at all, the useful next step is finding out where deja looked.
func emptyIndexHint(what string) string {
	// A store deja is not allowed to open, before anything else. The sessions
	// are there, behind a permission wall doctor and sources both name (#1020)
	// — and the branch used to sit inside the no-history one, so it was
	// unreachable in the case that matters: a store whose files deja can see
	// and cannot read counts as history, so the answer was "run `deja index`",
	// which is what the reader had just done and cannot help (#3585).
	if denied := deniedStoreCount(); denied > 0 {
		return fmt.Sprintf("deja: %s — %d store%s could not be read (permission denied); `deja doctor` names %s",
			what, denied, pluralS(denied), pluralWhich(denied))
	}
	if noAgentHistoryFound() {
		return "deja: " + what + " — no agent history was found on this machine; `deja sources` shows where deja looked"
	}
	return "deja: " + what + " — run `deja index`, or `deja doctor` to see which agent stores were found"
}

// emptyStoreLine is emptyIndexHint for a command whose answer goes to stdout
// without deja's prefix, when the index holds no session at all. tests,
// secrets, recap and rules candidates each said their own nothing there —
// "no provider keys … in your agent history" read as an all-clear on a machine
// with no history to check.
func emptyStoreLine(dir, what string) (string, bool) {
	if n, err := index.SessionCount(dir); err != nil || n > 0 {
		return "", false
	}
	// At a terminal, at its width and between words; piped, as one line.
	return fitLine(os.Stdout, strings.TrimPrefix(emptyIndexHint(what), "deja: ")), true
}

// newestSessionNote is the tail of an empty windowed answer: when the newest
// session it may show is older than the window, the date and the --since that
// reaches it. The demo store of a machine idle for three months answered
// `deja recap` with "nothing in the last 7d" and nothing else.
func newestSessionNote(dir, command string, window time.Duration) string {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	pol := policy.Load()
	var newest time.Time
	for _, m := range metas {
		if m.Updated.After(newest) && metaVisible(pol, policy.ActivationSearch, m) {
			newest = m.Updated
		}
	}
	if newest.IsZero() || time.Since(newest) <= window {
		return ""
	}
	days := int(time.Since(newest).Hours()/24) + 1
	return fmt.Sprintf(" — the newest session is from %s; `deja %s --since %dd` reaches it",
		newest.Local().Format("2006-01-02"), command, days)
}

// fitLine wraps a sentence between words at the width of the terminal w is,
// and leaves it whole for a pipe.
func fitLine(w io.Writer, line string) string {
	if n := printableWidth(w); n > 0 {
		return wrapProse(line, n)
	}
	return line
}

// sinceArg is the --since value as the reader typed it.
func sinceArg(args []string) string {
	for i, a := range args {
		if a == "--since" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// countNoun is "1 session" or "3 sessions"; the forget dry run printed
// "1 session(s)".
func countNoun(n int, noun string) string {
	return fmt.Sprintf("%d %s%s", n, noun, pluralS(n))
}

func forgetCounts(sessions, messages int) string {
	return countNoun(sessions, "session") + ", " + countNoun(messages, "message")
}

// deniedStoreCount reports how many harness stores exist but cannot be opened.
// Opened, not parsed: the probe stops before doctor's parser (#4272).
func deniedStoreCount() int {
	n := 0
	for _, check := range deniedStoreChecks() {
		if store, _, _ := probeDoctorStore(check); store.State == "denied" {
			n++
		}
	}
	return n
}

// deniedStoreChecks is doctorStoreChecks, swappable so a test can count what
// the probe parses.
var deniedStoreChecks = doctorStoreChecks

// noAgentHistoryFound reports whether the stores themselves are empty, as
// opposed to an index that merely has not been built yet.
func noAgentHistoryFound() bool {
	for _, check := range doctorStoreChecks() {
		// The count, not the inspection. `store.Files` is `len(check.files)`
		// and nothing else ever sets it (doctor_report.go:471), so asking
		// `inspectDoctorStore` for it paid a stat per path, a listing per
		// directory, and the newest file of the store opened and run through
		// that store's parser — SQLite for opencode and cursor — to learn a
		// number already in hand. Measured on a real home, 514 ms against
		// 6.6 ms for the same answer (#1991).
		if len(check.files) == 0 {
			continue
		}
		// By content, not by existence: an empty notes file — one `deja
		// remember` later forgotten, or a file someone touched — counted as
		// history and turned the answer into "run `deja index`", which is the
		// one piece of advice this branch exists to avoid (#996).
		for _, f := range check.files {
			if fi, err := os.Stat(f); err == nil && fi.Size() > 0 {
				return false
			}
		}
	}
	return true
}

// pluralNote words the notes in the failure line above.
func pluralNote(n int) string {
	if n == 1 {
		return "the promoted note"
	}
	return "the promoted notes"
}

// forgetTitleFix names the fix by the cause. The first version of this line
// said "permissions" whatever had happened, so a full disk sent the reader to
// chmod a file that was already writable (#808).
func forgetTitleFix(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "fix that file's permissions"
	case errors.Is(err, syscall.ENOSPC):
		return "free some space on that filesystem"
	}
	return "clear the problem above"
}

// sortedHarnesses orders the ingest-health entries so a run reports them the
// same way twice.
func sortedHarnesses(m map[string]index.HarnessIngest) []string {
	out := make([]string, 0, len(m))
	for h := range m {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// pluralSessionWord words the tail of the empty-transcript line.
func pluralSessionWord(n int) string {
	if n == 1 {
		return "a session"
	}
	return "sessions"
}

// pluralWhich matches the pronoun to the count in the ingest warning.
func pluralWhich(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// staleUnwritableIndex reports that a build could not run because the store
// cannot be written, while an index that can still answer is right there.
// Refusing the whole search then is deja withholding what it has: the reader
// gets nothing instead of slightly old memory and a line saying why (#904).
// A full disk belongs here next to a denied one: it is the commoner of the
// two, and it took every answer with it — empty stdout and exit 1 while a
// complete index sat in the store.
func staleUnwritableIndex(dir string, err error) bool {
	if !index.Unwritable(err) && !errors.Is(err, syscall.ENOSPC) {
		return false
	}
	return index.HasManifest(dir)
}

// deniedPath names the file a permission error was actually about, so the fix
// deja suggests points at it rather than at the index directory (#1031).
func deniedPath(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Path
	}
	return ""
}

// existingNonDirAncestor names the first ancestor of p that exists and is not
// a directory. Such a path can never hold an index, and the errno differs by
// platform, so the shape is worth naming rather than the syscall.
func existingNonDirAncestor(p string) string {
	for cur := filepath.Clean(p); ; {
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		if fi, err := os.Stat(parent); err == nil && !fi.IsDir() {
			return parent
		}
		cur = parent
	}
}

// nearestExistingDir walks up until it finds a directory that is there, so a
// refusal can name the thing that actually refused rather than the path that
// could not be created under it.
func nearestExistingDir(p string) string {
	for cur := filepath.Clean(p); ; {
		parent := filepath.Dir(cur)
		if parent == cur {
			if dirExists(cur) {
				return cur
			}
			return ""
		}
		if dirExists(parent) {
			return parent
		}
		cur = parent
	}
}

// dirWritable reports whether this process can create something in dir.
// Permission bits alone answer for the wrong user on the wrong platform, so it
// asks the filesystem.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".deja-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// ensureError turns a failed build into something the reader can act on.
// A denied write surfaced as `ensure: open /…/index.db.lock: permission
// denied` — the path of an internal lock file and a syscall error, which says
// nothing about what to change.
func ensureError(dir string, err error) error {
	if dir == "" {
		dir = index.DefaultDir()
	}
	// Worded for the reader already, command included (#5385).
	if index.IsManualStop(err) {
		return err
	}
	if errors.Is(err, fs.ErrPermission) {
		// An ejected volume takes its mount point with it, and creating that
		// point back fails with EPERM on macOS — so the errno says permissions
		// while the disk is simply gone. The same trap the notes and export
		// paths hit (#893, #906), and the reader is sent to check the
		// permissions of a directory that no longer exists (#931).
		if parent := filepath.Dir(dir); !dirExists(dir) && !dirExists(parent) {
			// Unless something above it is there and simply refuses: a
			// locked-down ~/.cache cannot be written into either, and reading
			// that as an ejected volume sent the reader to reconnect a disk
			// that never left (#2267).
			if a := nearestExistingDir(parent); a != "" && !dirWritable(a) {
				return fmt.Errorf("cannot create the index directory (%s) — %s is not writable; check its permissions, or point DEJA_INDEX_DIR somewhere writable", parent, a)
			}
			return fmt.Errorf("the index directory is not there (%s) — the disk it lives on may have been unmounted; reconnect it, or point DEJA_INDEX_DIR somewhere local", parent)
		}
		// The denial is not always about the index: forget writes the
		// tombstone file first, and a read-only ~/.config/deja arrived here
		// as "check the index directory", which was writable — the reader was
		// sent to change a permission that was already right (#1031, #808).
		if p := deniedPath(err); p != "" && !strings.HasPrefix(p, dir) {
			return fmt.Errorf("cannot write %s — check that file's permissions", p)
		}
		return fmt.Errorf("cannot write the index at %s — check the directory's permissions, or point DEJA_INDEX_DIR somewhere writable", dir)
	}
	// A full disk arrived as `ensure: write /…/index.db.tmp/records.bin: no
	// space left on device`: an internal path nobody can act on, and the same
	// shape #798 replaced for permissions. The build needs room beside the
	// index, so the directory to free is the one named here (#888).
	// A volume that went away mid-write — an unmounted disk, a network share
	// that dropped — arrives as `write /…/index.db.tmp/records.bin:
	// input/output error`: the same internal path as #888, and a reader who
	// cannot tell that the disk is simply gone (#899).
	if errors.Is(err, syscall.EIO) || errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
		return fmt.Errorf("the index directory is not reachable (%s) — the disk it lives on may have been unmounted or dropped; reconnect it, or point DEJA_INDEX_DIR somewhere local", filepath.Dir(dir))
	}
	if errors.Is(err, syscall.ENOSPC) {
		return fmt.Errorf("no space left where the index is built (%s) — free some room there, or point DEJA_INDEX_DIR at a disk that has it", filepath.Dir(dir))
	}
	// A volume ejected cleanly mid-build leaves its mount point behind as an
	// empty directory, so the write fails with ENOENT rather than the EIO of a
	// disk yanked out (#899) — an internal `idx.tmp/buckets/…` path and a
	// syscall for what is simply a disconnected disk. The index that was
	// already there is untouched, since the build writes beside it and
	// renames, and saying so is the part that decides whether the reader goes
	// looking for damage (#1068).
	// An index path that points inside a file is not a disconnected disk: on
	// unix the write fails with ENOTDIR and fell through to the raw syscall,
	// on windows it fails with ENOENT and read as an unmounted volume (found
	// by CI on windows after #1068).
	if p := existingNonDirAncestor(dir); p != "" {
		return fmt.Errorf("the index path runs through %s, which is a file — point DEJA_INDEX_DIR at a directory", p)
	}
	if errors.Is(err, fs.ErrNotExist) && !dirExists(dir) {
		// Only a write inside the build directory is a build the disk went
		// away from. A read of an index never built fails the same way, and
		// sent a fresh machine to reconnect a disk that was fine (#5057).
		var pe *fs.PathError
		if errors.As(err, &pe) && !pathUnder(pe.Path, dir+".tmp") {
			return fmt.Errorf("nothing is indexed yet (%s) — `deja index` builds it", dir)
		}
		return fmt.Errorf("the index directory went away mid-build (%s) — the disk it lives on may have been unmounted; the index already there is unharmed, so reconnect it and run `deja index` again, or point DEJA_INDEX_DIR somewhere local", dir)
	}
	// Already worded where it was raised — the leftover-swap case names the
	// directory to remove and the command to rerun, the refused index path
	// names the file it will not delete, and "ensure:" in front of either is
	// internal noise (#1009).
	for _, worded := range []string{"an earlier index swap left ", "the index path "} {
		if strings.HasPrefix(err.Error(), worded) {
			return err
		}
	}
	return fmt.Errorf("ensure: %w", err)
}

// pathUnder reports whether p is dir or lies inside it.
func pathUnder(p, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// indexLooksUnmounted reports an index whose directory and parent are both
// missing where a build could not simply create them: a volume that went
// away, not a machine that never built an index. It returns the parent to
// name.
func indexLooksUnmounted(dir string) (string, bool) {
	if dir == "" {
		dir = index.DefaultDir()
	}
	parent := filepath.Dir(dir)
	if dirExists(dir) || dirExists(parent) {
		return parent, false
	}
	return parent, !freshHomePath(parent)
}

// searchValueFlags take a value, so "--flag=value" has to become two arguments
// before the parser sees it. Anything else keeps its equals sign: a query may
// legitimately contain one.
var searchValueFlags = map[string]bool{
	"--harness": true, "--project": true, "--since": true,
	"--role": true, "--limit": true, "--session": true, "--sort": true,
}

func splitEqualsForms(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		name, value, found := strings.Cut(a, "=")
		if found && searchValueFlags[name] {
			out = append(out, name, value)
			continue
		}
		out = append(out, a)
	}
	return out
}

// flagName is the flag without its value, so `--limit=3` and `--limit 3` are
// recognised as the same spelling.
func flagName(a string) string {
	if before, _, ok := strings.Cut(a, "="); ok {
		return before
	}
	return a
}

// capTierHits bounds the two tiers that build their own hits — the ranked
// relevance list and the error signature — which never went through the cap in
// RunDetailed: `--limit 3` printed the whole retrieval window of fifty
// sessions there while the exact tier printed three (#3345). With no flag they
// keep the window they have always served, which the JSON envelope's own test
// pins; only what the reader asked for binds them.
func capTierHits(hits []search.Hit, o search.Options) ([]search.Hit, bool) {
	if o.Limit == 0 {
		return hits, false
	}
	// A limit the reader typed binds even beside --all, which is how the exact
	// tier has always read the pair: --all lifts the default, and the number
	// asked for is still the number wanted. Letting --all win here made the
	// same two flags mean opposite things depending on which tier answered
	// (review of #3345).
	return search.CapHits(hits, o.Limit, false)
}

// newestFirstNote is the line above a date-ordered answer. The exact tiers
// read the newest of the whole match, so the line says only the order; the
// relevance and error tiers rank a window first, and the newest of that
// window is not the newest of the match when it ran past it.
func newestFirstNote(o search.Options, shown int, tierWindow bool, window int) string {
	switch {
	case tierWindow && window > 0:
		return fmt.Sprintf("newest first among the %d closest of %s", window, grouped(o.Total))
	case o.Total > shown:
		// Under --limit the list is the top of the order, and "all 5
		// matches" sat under "showing 5 of 40".
		return "newest first"
	}
	return fmt.Sprintf("all %d matches, newest first", shown)
}
