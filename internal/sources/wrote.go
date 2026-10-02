package sources

import (
	"hash/fnv"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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

// withoutElisions drops the placeholder lines a model writes for the code it
// left alone — "// ... existing code ...", "# ... rest of code ...",
// "<!-- ... -->" — from an edit that sends only the changed stretches, as
// Continue's edit_existing_file and Kilo Code's fast_edit_file do. They are
// not lines the file was given (#4529).
func withoutElisions(text string) string {
	if !strings.Contains(text, "...") && !strings.Contains(text, "…") {
		return text
	}
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, line := range lines {
		if !elisionLine(line) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// elisionLine is a line that is only an elision: a comment that opens with an
// ellipsis, or text that opens and closes with one. A bare "..." is code in
// Python and is kept.
var elisionLine = regexp.MustCompile(`^\s*(?:(?:\{/\*|<!--|//|/\*|--|#|;)\s*(?:\.\.\.|…)(?:.*(?:\.\.\.|…))?|(?:\.\.\.|…)\s*\S.*(?:\.\.\.|…))\s*(?:\*/\}|\*/|-->)?\s*$`).MatchString

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

// applyPatch is what one apply_patch body says about the files it touched:
// each file its headers name, the spans it removed ("path\nspan", as an
// edit's replaced side) and the lines it added (WroteRecord). One reading of
// the format for every harness that carries it, so a patch from Copilot CLI,
// OpenClaw or Cline means what one from codex or opencode does (#4491).
// resolve puts a header's path in the form the surfaces compare; nil keeps it
// as written.
func applyPatch(patch string, resolve func(string) string) (files, spans, wrote []string) {
	if resolve == nil {
		resolve = func(p string) string { return p }
	}
	head := func(rec string) string {
		path, rest, _ := strings.Cut(rec, "\n")
		return resolve(path) + "\n" + rest
	}
	seen := map[string]bool{}
	for _, m := range codexPatchFile.FindAllStringSubmatch(patch, -1) {
		if p := resolve(strings.TrimSpace(m[1])); p != "" && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	for _, span := range patchSpans(patch) {
		spans = append(spans, head(span))
	}
	for _, rec := range addedLinesOfPatch(patch) {
		wrote = append(wrote, head(rec))
	}
	return files, spans, wrote
}

// unifiedPatch is applyPatch for a unified diff, the `--- a/x` / `+++ b/x`
// format CodeWhale's apply_patch takes (#4538): the files its headers name,
// the lines each hunk removed and the lines it added. A non-empty override is
// the file every hunk goes to, whatever the headers say, the way a call's own
// path argument retargets the patch. Hunk bodies are read by the line counts
// in their headers, so a removed line that starts with "--" is not taken for
// the next file's header unless a "+++ " line follows it; a hunk ends early at
// the next @@ or file header, and one without counts runs to them.
func unifiedPatch(patch, override string, resolve func(string) string) (files, spans, wrote []string) {
	if resolve == nil {
		resolve = func(p string) string { return p }
	}
	lines := strings.Split(patch, "\n")
	cur, oldHeader := "", ""
	var added []string
	seen := map[string]bool{}
	flush := func() {
		if rec := WroteRecord(cur, strings.Join(added, "\n")); cur != "" && rec != "" {
			wrote = append(wrote, rec)
		}
		added = nil
	}
	open := func(p string) {
		flush()
		cur = p
		if p != "" && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	if override != "" {
		open(resolve(override))
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "--- "):
			oldHeader = line[4:]
		case strings.HasPrefix(line, "+++ "):
			p := unifiedDiffPath(line[4:])
			if p == "" {
				p = unifiedDiffPath(oldHeader)
			}
			oldHeader = ""
			if override == "" && p != "" {
				open(resolve(p))
			}
		case strings.HasPrefix(line, "@@"):
			oldN, newN, counted := unifiedHunkCounts(line)
			var removed []string
			j := i + 1
			for ; j < len(lines); j++ {
				l := lines[j]
				if counted && oldN <= 0 && newN <= 0 {
					break
				}
				// A miscounted header does not carry the hunk past the next
				// one or the next file's headers; CodeWhale applies such a
				// patch the same way (apply_patch.rs parse_hunk_header).
				if strings.HasPrefix(l, "@@") || strings.HasPrefix(l, "diff ") ||
					(strings.HasPrefix(l, "--- ") && j+1 < len(lines) && strings.HasPrefix(lines[j+1], "+++ ")) {
					break
				}
				switch {
				case strings.HasPrefix(l, "-"):
					removed = append(removed, l[1:])
					oldN--
				case strings.HasPrefix(l, "+"):
					added = append(added, l[1:])
					newN--
				case strings.HasPrefix(l, `\`):
					// "\ No newline at end of file"
				default:
					oldN--
					newN--
				}
			}
			i = j - 1
			if span := strings.Join(removed, "\n"); cur != "" && strings.TrimSpace(span) != "" {
				if len(span) > editSpanMax {
					span = span[:editSpanMax]
				}
				spans = append(spans, cur+"\n"+span)
			}
		}
	}
	flush()
	return files, spans, wrote
}

// unifiedHunkCounts reads the old and new line counts off "@@ -a,b +c,d @@";
// a count left out is 1. counted is false for a bare "@@".
func unifiedHunkCounts(header string) (oldN, newN int, counted bool) {
	m := unifiedHunkHeader.FindStringSubmatch(header)
	if m == nil {
		return 0, 0, false
	}
	count := func(s string) int {
		if s == "" {
			return 1
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	return count(m[1]), count(m[2]), true
}

var unifiedHunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// unifiedDiffPath is the file a `---` or `+++` header names: the timestamp
// after a tab and the a/ or b/ prefix off, and "" for /dev/null.
func unifiedDiffPath(raw string) string {
	raw, _, _ = strings.Cut(raw, "\t")
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" || raw == "dev/null" {
		return ""
	}
	if p, ok := strings.CutPrefix(raw, "a/"); ok {
		return p
	}
	if p, ok := strings.CutPrefix(raw, "b/"); ok {
		return p
	}
	return raw
}

// applyPatchRecords is applyPatch as the records a reader appends, under the
// switches every reader honours.
func applyPatchRecords(patch string, resolve func(string) string, t time.Time) []model.Message {
	files, spans, wrote := applyPatch(patch, resolve)
	return patchRecords(files, spans, wrote, t)
}

// patchRecords turns what a patch says into the records a reader appends.
func patchRecords(files, spans, wrote []string, t time.Time) []model.Message {
	var out []model.Message
	if IndexToolPaths() && len(files) > 0 {
		out = append(out, model.Message{Role: RoleFiles, Text: strings.Join(files, "\n"), Time: t})
	}
	if IndexWrites() {
		for _, w := range wrote {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range spans {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	return out
}

// applyPatchInputs is the patch body of every apply_patch call among blocks.
// Roo names the argument `patch`; Cline's CLI and extension name it `input`
// (#4503, #4504), Amp `patchText` (#4527).
func applyPatchInputs(blocks []any, d toolDialect) []string {
	var out []string
	for _, it := range blocks {
		name, in, ok := toolPart(it, d)
		if !ok || name != "apply_patch" {
			continue
		}
		for _, k := range []string{"patch", "input", "patchText"} {
			if p, _ := in[k].(string); p != "" {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
