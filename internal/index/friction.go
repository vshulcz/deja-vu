package index

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Friction is what this machine keeps tripping over: one specific error, in
// several separate sessions.
//
// The detection lives here rather than in the command because it runs twice —
// once over a session being ingested, to fingerprint what it hit, and once
// over the record log when someone asks. Two copies of these rules would drift,
// and the first symptom would be a first-screen line the command cannot
// reproduce.
type Friction struct {
	Text     string
	Sessions []SessionMeta
	Last     time.Time
}

const (
	// frictionSessionCap bounds what one session contributes to the manifest,
	// and is set high on purpose. The first screen names a count and points at
	// `deja friction`; a cap low enough to lose a hit makes the two disagree,
	// which reads as the tool being broken. Measured on 1149 sessions: at 6 the
	// screen said 8 where the command said 11, at 100 it said 10, and one
	// session alone carries over a hundred distinct error lines. The whole
	// field costs 2.7 KB across that corpus — the exactness is nearly free.
	frictionSessionCap = 256
	frictionLineMin    = 20
	frictionLineMax    = 120
	// FrictionMinSessions is how many separate sessions must hit an error
	// before it is worth saying. Twice is a coincidence.
	FrictionMinSessions = 3
)

// FrictionLine reports whether a line of tool output names something specific
// that went wrong, and returns it in the form two sessions can be compared on.
func FrictionLine(l string) (string, bool) {
	l = normalizeFriction(l)
	return l, isFriction(l)
}

// normalizeFriction strips the shell's position prefix so the same missing
// command counts once. `zsh:1: command not found: timeout` and
// `(eval):2: command not found: timeout` are one piece of friction; left
// alone they land below the threshold separately and none is ever reported.
func normalizeFriction(l string) string {
	l = strings.TrimSpace(l)
	// The prefix is `<where>:<line>: `, where <where> is a shell name or an
	// `(eval)`/`(anon)` marker. Only strip it when the middle field is a
	// number — `Error: cannot find x: y` must keep its shape.
	first := strings.Index(l, ":")
	if first <= 0 || first > 16 {
		return l
	}
	rest := l[first+1:]
	second := strings.Index(rest, ": ")
	if second <= 0 {
		return l
	}
	if _, err := strconv.Atoi(rest[:second]); err != nil {
		return l
	}
	return strings.TrimSpace(rest[second+2:])
}

// isFriction keeps the error shapes that name something specific. The generic
// ones carry no information — every Python failure prints `Traceback (most
// recent call last):`, and clustering those put an empty line at the top of
// every measurement this was built from.
func isFriction(l string) bool {
	// Characters, not bytes. The bound is about how much of a line a person can
	// recognise, and in bytes it held a Russian error to 60 characters and a
	// Chinese one to 40 while giving English 120: an 89-character Russian line
	// was dropped where an 80-character English one was kept, so the wall a
	// Russian-speaking user keeps hitting never reached `deja friction`, the
	// environment block or `deja fix` (#1319).
	if n := utf8.RuneCountInString(l); n < frictionLineMin || n > frictionLineMax {
		return false
	}
	for _, generic := range []string{
		"Traceback (most recent", "Error: ", "error: ", "FAIL\t", "--- FAIL",
	} {
		if strings.HasPrefix(l, generic) {
			return false
		}
	}
	// Tool output carries source as often as it carries results — a `cat` of a
	// script, a diff, a heredoc. An `echo "App not found: $APP"` inside a
	// deploy script reached second place on the first run: it is a line about
	// an error, not an error.
	for _, source := range []string{"echo ", "\"", "$(", "=~", "print("} {
		if strings.Contains(l, source) {
			return false
		}
	}
	// A comment about an error is source too, and the wider marker list in
	// #729 made these reachable: `// panic: this is a comment about panics`
	// became the top wall on a store of shell snippets.
	for _, comment := range []string{"//", "#", "/*", "*", "--"} {
		// normalizeFriction has already trimmed the line.
		if strings.HasPrefix(l, comment) {
			return false
		}
	}
	// deja's own report is tool output in the next session, and every line of
	// it contains an error by construction. Drop the report shape so running
	// the command does not slowly teach it about itself.
	if i := strings.Index(l, " sessions  "); i > 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(l[:i])); err == nil {
			return false
		}
	}
	// The list was nine phrases about things not being found or permitted, and
	// it matched 3 of 12 ordinary errors on measurement — missing runtime
	// panics, database timeouts, auth failures and build failures, which is
	// most of what a machine actually trips over. One miss was capitalisation
	// alone: curl writes "Connection refused" (#729).
	low := strings.ToLower(l)
	for _, p := range []string{
		// Not found, not permitted — the original list.
		"command not found", "modulenotfounderror", "no module named",
		"not found: ", "cannot find", "no such file or directory",
		"undefined:", "connection refused", "permission denied",
		// Crashed.
		"panic:", "segmentation fault", "exception in thread",
		"nullpointerexception", "index out of range", "stack overflow",
		"typeerror:", "referenceerror:", "keyerror:", "attributeerror:",
		// Refused by a server or a tool, with the tool named.
		"fatal:", "npm err!", "rpc failed", "timeout expired",
		"statement timeout", "connection reset", "connection timed out",
		// Failed to build or run.
		"] error ", "build failed", "compilation failed", "cannot be resolved",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func frictionHash(line string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(line))
	return h.Sum64()
}

// frictionHashes fingerprints what a session tripped over, for the manifest.
// Hashes rather than text, for the reason SessionMeta.Asked gives: the
// manifest is read on every search and the only thing a caller needs from it
// is whether two sessions hit the same wall.
func frictionHashes(ms []model.Message) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, m := range ms {
		if m.Role != roleToolOutput {
			continue
		}
		for _, line := range strings.Split(m.Text, "\n") {
			line, ok := FrictionLine(line)
			if !ok {
				continue
			}
			h := frictionHash(line)
			if seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
			if len(out) >= frictionSessionCap {
				return out
			}
		}
	}
	return out
}

// hitFromRecords is frictionHashes for the import path, which holds a session
// as records rather than messages. Imported sessions used to carry no Hit, so
// the brief's one friction line — which reads meta.Hit — never counted a wall a
// peer kept hitting, while `deja friction` (which reads the record log) and
// stats both did. Same gap the asked-twice line had, same shape of fix.
func hitFromRecords(recs []Record) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, r := range recs {
		if r.Role != roleToolOutput {
			continue
		}
		for _, line := range strings.Split(r.Text, "\n") {
			line, ok := FrictionLine(line)
			if !ok {
				continue
			}
			h := frictionHash(line)
			if seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
			if len(out) >= frictionSessionCap {
				return out
			}
		}
	}
	return out
}

// FindFriction picks the wall worth showing on a screen with room for one. See
// TopFriction for allow.
func FindFriction(dir string, allow func(project string) bool) (Friction, bool) {
	out := TopFriction(dir, 1, allow)
	if len(out) == 0 {
		return Friction{}, false
	}
	return out[0], true
}

// TopFriction returns the walls this machine keeps running into, most-hit
// first. It runs over the manifest alone, so a caller on the first screen or
// in a session-start hook pays nothing for the search; only the sessions
// carrying a winning hash are read back, and only to recover the text the
// hash stands for.
//
// allow gates a session's project by the caller's activation, so an all-imported
// wall stays off a screen whose trust rule withholds imported memory. A nil
// allow counts every session; callers that filter per wall themselves (the
// environment block) pass nil and keep their own gate.
func TopFriction(dir string, n int, allow func(project string) bool) []Friction {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	byHash := map[uint64][]SessionMeta{}
	for _, meta := range m.Sessions {
		if allow != nil && !allow(meta.Project) {
			continue
		}
		for _, h := range meta.Hit {
			byHash[h] = append(byHash[h], meta)
		}
	}
	type cluster struct {
		hash  uint64
		metas []SessionMeta
	}
	var cs []cluster
	for h, metas := range byHash {
		if len(metas) < FrictionMinSessions {
			continue
		}
		sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
		cs = append(cs, cluster{h, metas})
	}
	sort.Slice(cs, func(i, j int) bool {
		if len(cs[i].metas) != len(cs[j].metas) {
			return len(cs[i].metas) > len(cs[j].metas)
		}
		// A wall the machine stopped running into is history, not friction.
		if !cs[i].metas[0].Updated.Equal(cs[j].metas[0].Updated) {
			return cs[i].metas[0].Updated.After(cs[j].metas[0].Updated)
		}
		return cs[i].hash < cs[j].hash
	})
	if n > 0 && len(cs) > n {
		cs = cs[:n]
	}
	// Recover every winning text in one pass per session rather than one pass
	// per wall: the same session usually carries several of them.
	want := map[uint64]string{}
	for _, c := range cs {
		want[c.hash] = ""
	}
	for _, c := range cs {
		if want[c.hash] != "" {
			continue
		}
		frictionTexts(dir, m, c.metas, want, c.hash)
	}
	var out []Friction
	for _, c := range cs {
		if want[c.hash] == "" {
			continue
		}
		out = append(out, Friction{Text: want[c.hash], Sessions: c.metas, Last: c.metas[0].Updated})
	}
	return out
}

func newestOf(ms []SessionMeta) time.Time {
	var out time.Time
	for _, m := range ms {
		if m.Updated.After(out) {
			out = m.Updated
		}
	}
	return out
}

// frictionTexts recovers what the wanted hashes stood for by reading back the
// sessions that carry one of them, filling in every hash a session yields —
// one session usually carries several walls, and reading it once per wall was
// the difference between one lookup and N.
func frictionTexts(dir string, m Manifest, metas []SessionMeta, want map[uint64]string, target uint64) {
	for _, meta := range metas {
		s, ok, err := loadSessionMeta(dir, m, meta)
		if err != nil || !ok {
			continue
		}
		for _, msg := range s.Messages {
			if msg.Role != roleToolOutput {
				continue
			}
			for _, line := range strings.Split(msg.Text, "\n") {
				line, ok := FrictionLine(line)
				if !ok {
					continue
				}
				h := frictionHash(line)
				if cur, wanted := want[h]; wanted && cur == "" {
					want[h] = line
				}
			}
		}
		// Stop on this cluster's own text, not on any text: a session can fill
		// in a neighbour's hash while carrying nothing for this one.
		if want[target] != "" {
			return
		}
	}
}
