package sources

import (
	"hash/fnv"
	"os"
	"strconv"
	"strings"
)

// RoleWrote is the record kind holding what a session wrote, as hashes: one
// 64-bit hash per distinct line long enough to be evidence, under the path the
// lines went into.
//
// The replaced side — RoleEdit, the exact bytes that stopped existing — is what
// `deja restore` needs and it is backwards for attribution. Measured over 150
// random committed lines of this repository (#3773):
//
//	rule                                             attributed
//	the session replaced what the commit deleted        7.3%
//	the commit deleted nothing at all                  71.3% of the silence
//	plus: some session wrote this line, in this file,
//	before the commit                                  36.7%
//
// A line a commit added has no replaced text to match, and that is most
// commits. What a session wrote is the only evidence such a line was ever in a
// session at all.
//
// Hashes rather than the text for two reasons. Attribution needs equality, not
// the bytes: 204,363 distinct written lines on this machine are 1.6 MB of
// hashes. And no new user text enters the index — the written side is the one
// place where a secret an agent typed into a file would otherwise land in a
// second store.
const RoleWrote = "wrote"

// IndexWrites reports whether the written side is indexed. On by default, for
// the same reason as the replaced side: attribution nobody enabled attributes
// nothing.
func IndexWrites() bool { return os.Getenv("DEJA_INDEX_WRITES") != "0" }

// WrittenLineMinRunes is where a line stops being evidence. Measured on this
// repository's diffs: below it the matches are braces, `return err` and import
// lines, present in every commit and in every session. The floor is applied
// before hashing, so a short line costs nothing to store either.
const WrittenLineMinRunes = 24

// wroteHashesMax bounds one record the way editSpanMax bounds one span: a
// single `Write` of a generated file cannot put a hash per line of it into the
// index. The bound is in hashes, at the same order as the 32 KB span bound.
const wroteHashesMax = 1900

// WrittenLineKey is the form a written line, a recorded span and a line of a
// git diff are all compared in: one space between words, and nothing shorter
// than a line that could only have been written on purpose. Empty when the line
// is not evidence.
//
// Both sides of the comparison call this, because a normalisation that differs
// by a space attributes nothing and looks like a ranking problem.
func WrittenLineKey(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) < WrittenLineMinRunes {
		return ""
	}
	return s
}

// WrittenLineHash hashes a key from WrittenLineKey. FNV-1a because it is in the
// standard library and this is an equality test, not a defence: a collision
// attributes a line to the wrong session, which is the same failure a
// boilerplate line already has.
func WrittenLineHash(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

// HashWrittenLine is WrittenLineKey and WrittenLineHash together, with false
// when the line is too short to be evidence.
func HashWrittenLine(line string) (uint64, bool) {
	key := WrittenLineKey(line)
	if key == "" {
		return 0, false
	}
	return WrittenLineHash(key), true
}

// WroteRecord builds the record text for one write: "path\n" and the hashes of
// its lines, in the order they were written, without repeats. Empty when the
// write holds no line long enough to be evidence — an empty record would say a
// session wrote a file and give nothing to match.
func WroteRecord(path, written string) string {
	if path == "" || strings.ContainsAny(path, "\n\r") {
		// "path\nhashes" cannot hold a path with a newline in it, which is the
		// same reason the replaced side drops those edits.
		return ""
	}
	seen := make(map[uint64]bool)
	var b strings.Builder
	for _, line := range strings.Split(written, "\n") {
		h, ok := HashWrittenLine(line)
		if !ok || seen[h] {
			continue
		}
		seen[h] = true
		if len(seen) > wroteHashesMax {
			break
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatUint(h, 16))
	}
	if b.Len() == 0 {
		return ""
	}
	return path + "\n" + b.String()
}

// WroteRecordHas reports whether a "path\nhashes" record carries a hash, and
// what path it is about. The reader of the record, kept next to its writer so
// the two formats cannot drift.
func WroteRecordHas(record string, want uint64) (path string, has bool) {
	path, hashes, ok := strings.Cut(record, "\n")
	if !ok {
		return "", false
	}
	target := strconv.FormatUint(want, 16)
	for _, h := range strings.Fields(hashes) {
		if h == target {
			return path, true
		}
	}
	return path, false
}

// addedLinesOfPatch is the written side of an apply_patch payload: the lines it
// adds, per file. The mirror of patchSpans, which takes the removed ones.
func addedLinesOfPatch(patch string) []string {
	var out []string
	path := ""
	var added []string
	flush := func() {
		if path != "" && len(added) > 0 {
			if rec := WroteRecord(path, strings.Join(added, "\n")); rec != "" {
				out = append(out, rec)
			}
		}
		added = nil
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Update File:"),
			strings.HasPrefix(line, "*** Delete File:"),
			strings.HasPrefix(line, "*** Add File:"):
			flush()
			path = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		case line == "*** End Patch":
			flush()
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			added = append(added, line[1:])
		}
	}
	flush()
	return out
}
