package index

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// writeFixTables writes the raw pairs and the table judged from them, and
// removes both when there are none, as a full build would not write them.
// Atomic, unlike buildFixes: this runs after carrySidecars has copied the live
// tables into the build directory, and the swap ships whatever is there. A
// truncating write that failed partway would ship an undecodable file, which
// ReadFixes reports as no pairs at all — silence until the next full rebuild.
func writeFixTables(dir string, raw []FixPair) {
	if len(raw) == 0 {
		_ = os.Remove(fixRawPath(dir))
		_ = os.Remove(fixesPath(dir))
		return
	}
	_ = writeGobAtomic(fixRawPath(dir), raw)
	_ = writeGobAtomic(fixesPath(dir), judgeFixes(raw))
}

// wholeSessionsForFixes returns the sessions in tails whose pairs can have
// changed, read whole from the index in dir, and their keys. A pair runs from
// an error to the remedy after it, so mined from a tail alone an error before
// the tail and its remedy in it were never paired, and a later repeat of the
// error moves the pair. Only a tail that brings a tool record can change them,
// or one of a shared id, whose file can take the row from the copy it named.
// On a read error the tails are mined as they are.
func wholeSessionsForFixes(dir string, m Manifest, tails []model.Session) ([]model.Session, map[string]bool) {
	keys := map[string]bool{}
	for _, s := range tails {
		if m.Sessions[s.Key()].Shared {
			keys[s.Key()] = true
			continue
		}
		for _, msg := range s.Messages {
			if msg.Role != "user" && msg.Role != "assistant" {
				keys[s.Key()] = true
				break
			}
		}
	}
	if len(keys) == 0 {
		return nil, keys
	}
	out, err := rowSessionsFromRecords(dir, m, keys)
	if err != nil {
		return tails, map[string]bool{}
	}
	return out, keys
}

// rowSessionsFromRecords is the sessions of keys as the record log in dir
// holds them, each the copy its row names, in key order.
func rowSessionsFromRecords(dir string, m Manifest, keys map[string]bool) ([]model.Session, error) {
	by := map[string]*model.Session{}
	if err := eachRecordForKeys(filepath.Join(dir, "records.bin"), tablesFromManifest(m), keys, func(r Record) {
		// The row's copy, as a build mines it (see commandSessions).
		if foreignPath(r.SourcePath, m.Sessions[r.Key]) {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := sessionFromMeta(m.Sessions[r.Key])
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}); err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(by))
	for _, k := range sortedKeys(by) {
		out = append(out, *by[k])
	}
	return out, nil
}

// fixSessionsForPass is what a replacing pass mines fixes from: the sessions
// it read again, as commandSessions gives them. A shared id's row is read back
// from the record log in tmp instead, whose manifest is m: the pass may have
// read only the copy the row does not name, or none, when the row's file was
// removed and the index keeps its turns, or a copy took the row from another
// (#5504).
func fixSessionsForPass(tmp string, old, m Manifest, replacements []model.Session, replaced map[string]bool) ([]model.Session, map[string]bool) {
	fromLog := map[string]bool{}
	for k, meta := range m.Sessions {
		was, ok := old.Sessions[k]
		if replaced[k] && (meta.Shared || was.Shared) || !replaced[k] && ok && was.Path != meta.Path {
			fromLog[k] = true
		}
	}
	if len(fromLog) == 0 {
		return commandSessions(replacements, m, nil), replaced
	}
	rows, err := rowSessionsFromRecords(tmp, m, fromLog)
	if err != nil {
		return commandSessions(replacements, m, nil), replaced
	}
	var read []model.Session
	for _, s := range replacements {
		if !fromLog[s.Key()] {
			read = append(read, s)
		}
	}
	keys := maps.Clone(replaced)
	for k := range fromLog {
		keys[k] = true
	}
	return append(commandSessions(read, m, nil), rows...), keys
}

// readFixRaw loads the raw pairs. An index built before them has only the
// served table, whose rows stand in for them: one sighting each, which is all
// the incremental path ever counted. ok is false when neither can be read.
func readFixRaw(dir string) (pairs []FixPair, ok bool) {
	if err := readGob(fixRawPath(dir), &pairs); err == nil {
		return pairs, true
	}
	if _, err := os.Stat(fixRawPath(dir)); err == nil {
		return nil, false
	}
	served, torn := readFixesChecked(dir)
	return served, !torn
}

// sessionKeys is the set of session keys m holds.
func sessionKeys(m Manifest) map[string]bool {
	keys := make(map[string]bool, len(m.Sessions))
	for k := range m.Sessions {
		keys[k] = true
	}
	return keys
}

// readFixesChecked is ReadFixes that tells a missing table from a torn one:
// torn is a file that is there and will not decode.
func readFixesChecked(dir string) (pairs []FixPair, torn bool) {
	if err := readGob(fixesPath(dir), &pairs); err != nil {
		_, statErr := os.Stat(fixesPath(dir))
		return nil, statErr == nil
	}
	return pairs, false
}

// remineFixes mines the pair table of the store in dir from its records, the
// way a full build mines it from the sessions it read. Records of one session
// sit in the log in the order they happened, which is the order mining reads.
func remineFixes(dir string) {
	m, err := readManifest(dir)
	if err != nil {
		return
	}
	by := map[string]*model.Session{}
	var order []string
	if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok || foreignPath(r.SourcePath, meta) {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := sessionFromMeta(meta)
			s = &cp
			by[r.Key] = s
			order = append(order, r.Key)
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}); err != nil {
		return
	}
	ss := make([]model.Session, 0, len(order))
	for _, k := range order {
		ss = append(ss, *by[k])
	}
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	buildFixes(dir, ss, func(s model.Session) string { return s.Key() })
}

// ReadFixes loads the mined pairs. An index built before they existed simply
// has none.
func ReadFixes(dir string) []FixPair {
	var out []FixPair
	if err := readGob(fixesPath(dir), &out); err != nil {
		return nil
	}
	return out
}

// fixSignaturesFor is the set of error signatures the text asks about.
//
// Ordinarily every line is hashed the way it was at ingest. The fallback is for
// a line deja stored and could not read back: the shell-position rule (#3445)
// recognises `zsh:1: no matches found: …` and stores the normalised line
// without the marker, so the stored line is not friction on its own — and 106
// of the 1,310 error lines on a real store are that shape. `deja fix` then
// answered "nothing recorded for that line" and suggested, as the closest it
// held, the very line it had just been given.
func fixSignaturesFor(dir, text string) map[uint64]bool {
	sigs := map[uint64]bool{}
	want := map[string]bool{}
	for raw := range strings.SplitSeq(text, "\n") {
		if line, ok := FrictionLine(raw); ok {
			sigs[frictionHash(line)] = true
			// `go test` separates `FAIL` and the package path with a tab,
			// which SafeLine flattens to a space when `deja friction` or
			// `deja fix`'s own hint prints the line.
			if rest, ok := strings.CutPrefix(line, "FAIL "); ok {
				sigs[frictionHash("FAIL\t"+strings.TrimSpace(rest))] = true
			}
		}
		if l := strings.Join(strings.Fields(strings.ToLower(raw)), " "); l != "" {
			want[l] = true
		}
	}
	if len(want) == 0 {
		return sigs
	}
	for _, p := range ReadFixes(dir) {
		if want[strings.Join(strings.Fields(strings.ToLower(p.Error)), " ")] {
			sigs[p.Sig] = true
		}
	}
	return sigs
}

func fixIgnoreGate(dir string) func(FixPair) bool {
	var (
		loaded   bool
		m        Manifest
		pol      policy.Policy
		excluded map[string]bool
		touched  map[string]bool
	)
	return func(p FixPair) bool {
		if !loaded {
			if dir == "" {
				dir = DefaultDir()
			}
			m, _ = readManifestCached(dir)
			pol = policy.Load()
			excluded = excludedHarnesses()
			loaded = true
		}
		if p.Edit != "" && pol.Ignored(p.Edit, p.Project) {
			return true
		}
		if p.Key != "" {
			if meta, ok := m.Sessions[p.Key]; ok {
				return metaIgnoredOrExcluded(pol, excluded, meta)
			}
		}
		if touched == nil {
			touched = ProjectsTouchedByIgnore(dir)
		}
		return touched[p.Project]
	}
}

// FixesFor returns the commands that followed this error before, newest first.
// The text can be a whole pasted stack trace: every line is tried, so the
// caller does not have to know which one carries the signature. allow, when
// non-nil, gates each pair by its project — the caller applies its trust
// policy here, since this package sits below policy.
func FixesFor(dir, text string, limit int, allow func(project string) bool) []FixPair {
	return FixesForCommand(dir, "", text, limit, allow)
}

// FixesForCommand is FixesFor for a failure whose command is known. A pair
// that matches only on what the shell said about the command line (a parse
// error, a program it could not find that this command does not run) is kept
// only when its command is this one, or this one corrected: the shell's
// complaint is the same for any command that trips it, and the next command
// of another session that tripped it is that session's work.
func FixesForCommand(dir, command, text string, limit int, allow func(project string) bool) []FixPair {
	if limit <= 0 {
		limit = 3
	}
	sigs := fixSignaturesFor(dir, text)
	if len(sigs) == 0 {
		return nil
	}
	shell := shellOnlySigs(text, command, sigs)
	var out, alsoOut, held, alsoHeld []FixPair
	isIgnored := fixIgnoreGate(dir)
	for _, p := range ReadFixes(dir) {
		if !p.answers(sigs) {
			continue
		}
		if len(shell) > 0 && !p.answers(without(sigs, shell)) && !sameFailingCommand(p, command) {
			continue
		}
		if remedyIsTheFailure(p) || remedyIsIrreversible(p.Command) || (p.Edit != "" && !looksLikeEditedPath(p.Edit)) {
			continue
		}
		if !sigs[p.Sig] && (p.Repaired || p.Substitute) {
			continue
		}
		// One session doing something after an error is not evidence that it
		// worked; it is half of it. Held back, not thrown away: a caller that
		// says so when it speaks can have them once the confirmed pairs are
		// exhausted, which is most of the time — measured over 1057 real
		// failures on one machine, 112 had a confirmed pair and 373 more had
		// only this.
		if p.Candidate {
			if sigs[p.Sig] {
				held = append(held, p)
			} else {
				alsoHeld = append(alsoHeld, p)
			}
			continue
		}
		if allow != nil && !allow(p.Project) {
			continue
		}
		if isIgnored(p) {
			continue
		}
		if sigs[p.Sig] {
			out = append(out, p)
			if len(out) >= limit {
				break
			}
		} else {
			alsoOut = append(alsoOut, p)
		}
	}
	for _, p := range alsoOut {
		if len(out) >= limit {
			break
		}
		out = append(out, p)
	}
	// Confirmed first, always. What is held goes behind them and only when
	// nothing was confirmed, so a pair two sessions agree on is never displaced
	// by a single sighting.
	if len(out) == 0 {
		for _, p := range append(held, alsoHeld...) {
			if allow != nil && !allow(p.Project) {
				continue
			}
			// The ignore rule holds for a sighting as well as for a pair:
			// search and `deja how` refuse the session, and `deja fix` handed
			// back what it ran there (#3403). #2660 fixed the confirmed half;
			// the candidates arrived after it.
			if isIgnored(p) {
				continue
			}
			out = append(out, p)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// remedyIsTheFailure drops a pair whose remedy is the command that produced
// the error. A repaired pair keeps the failing command beside the corrected
// one, and when the two are the same command the line reads "ran next: X /
// after this failed: X" — deja telling an agent to run again what just failed,
// which is a loop rather than a remedy. The two strings are not equal:
// a stored command carries its exit status, so the comparison is on the
// command alone. In real-world stores, 1 of the 7 served pairs that carry
// a failing command (#3720).
func remedyIsTheFailure(p FixPair) bool {
	if p.Failed == "" || p.Command == "" {
		return false
	}
	return normalizeCommand(p.Failed) == normalizeCommand(p.Command)
}

// remedyIsIrreversible drops a pair whose remedy cannot be taken back: a merge,
// a deleted branch, a force push, a hard reset, a dropped stash, a recursive
// delete, a change to a cluster. What a session did next after an error is
// often just the next step of its own work, and when that step is one of these
// it gets handed to an agent at the moment it is stuck. Over 396 real hints, 27
// were such commands and 24 of them had nothing to do with the error.
func remedyIsIrreversible(cmd string) bool {
	for _, seg := range strings.FieldsFunc(normalizeCommand(cmd), func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n'
	}) {
		w := strings.Fields(seg)
		has := func(words ...string) bool {
			for _, x := range words {
				if slices.Contains(w, x) {
					return true
				}
			}
			return false
		}
		for len(w) > 0 && (strings.Contains(w[0], "=") || w[0] == "rtk" || w[0] == "sudo") {
			w = w[1:]
		}
		if len(w) < 2 {
			continue
		}
		sub := ""
		for _, y := range w[1:] {
			if !strings.HasPrefix(y, "-") {
				sub = y
				break
			}
		}
		switch w[0] {
		case "git":
			switch sub {
			case "push":
				if has("--delete", "-d", "--force", "-f", "--force-with-lease", "--mirror") {
					return true
				}
			case "branch":
				if has("-D", "-d", "--delete") {
					return true
				}
			case "reset":
				if has("--hard") {
					return true
				}
			case "stash":
				if has("drop", "clear") {
					return true
				}
			case "clean":
				return true
			}
		case "gh":
			if has("merge", "delete", "--admin", "--delete-branch") {
				return true
			}
		case "rm":
			if has("-rf", "-fr", "-r", "-R", "--recursive") {
				return true
			}
		case "kubectl", "helm":
			if has("delete", "apply", "patch", "scale", "drain", "rollout", "uninstall", "upgrade", "install") {
				return true
			}
			// Anything at all against a production namespace.
			for _, y := range w {
				if strings.Contains(y, "prod") {
					return true
				}
			}
		}
	}
	return false
}

// FixCandidateSeen reports whether deja is holding an unconfirmed sighting for
// this error: something WAS run after it once, which is not yet evidence that
// it worked. The command that says "nothing ran after that error" asks first,
// so it does not deny holding what it is holding (#2282).
func FixCandidateSeen(dir, text string, allow func(project string) bool) bool {
	sigs := fixSignaturesFor(dir, text)
	if len(sigs) == 0 {
		return false
	}
	isIgnored := fixIgnoreGate(dir)
	for _, p := range ReadFixes(dir) {
		if !p.answers(sigs) || !p.Candidate {
			continue
		}
		// A sighting FixesFor would drop is not one a second session can
		// promote into an answer, so it is not "waiting for a second sighting".
		if remedyIsTheFailure(p) || remedyIsIrreversible(p.Command) || (p.Edit != "" && !looksLikeEditedPath(p.Edit)) {
			continue
		}
		if !sigs[p.Sig] && (p.Repaired || p.Substitute) {
			continue
		}
		if allow != nil && !allow(p.Project) {
			continue
		}
		// The same rule as FixesFor: saying deja holds a sighting it will not
		// show is worse than saying nothing (#2660).
		if isIgnored(p) {
			continue
		}
		return true
	}
	return false
}

// FixWithheldAsIrreversible reports whether something did run after this error
// and every such remedy was a command FixesFor refuses to hand over — a merge,
// a force push, a deletion. The empty answer said "no session ran a command
// after that error", which is false exactly then: the store holds the pair and
// deja chose not to show it.
func FixWithheldAsIrreversible(dir, text string, allow func(project string) bool) bool {
	sigs := fixSignaturesFor(dir, text)
	if len(sigs) == 0 {
		return false
	}
	isIgnored := fixIgnoreGate(dir)
	for _, p := range ReadFixes(dir) {
		if !p.answers(sigs) || !remedyIsIrreversible(p.Command) {
			continue
		}
		if allow != nil && !allow(p.Project) {
			continue
		}
		if isIgnored(p) {
			continue
		}
		return true
	}
	return false
}
