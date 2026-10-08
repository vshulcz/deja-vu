package sources

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// aider writes the model's reply into the history as it came, and an edit is
// in that reply: a SEARCH/REPLACE block under the file's name in the diff
// formats, a unified diff in a ```diff fence in udiff, a codex-style patch in
// patch, and the whole new file under its name in whole. Which edits landed is
// said after the reply, one "> Applied edit to <file>" line per file, and a
// reply whose blocks did not match gets no such line. So the blocks are held
// until that line names their file and dropped at the next question. Read off
// histories aider 0.86.2 wrote against a stub model, one per format (#595).

var (
	aiderSearchHead    = regexp.MustCompile(`^<{5,9} SEARCH>?\s*$`)
	aiderSearchDivider = regexp.MustCompile(`^={5,9}\s*$`)
	aiderSearchTail    = regexp.MustCompile(`^>{5,9} REPLACE\s*$`)
	aiderEditFormat    = regexp.MustCompile(`with (\S+) edit format`)
)

// aiderEdits holds the changes a reply proposed, by the file they go to, until
// aider says which it applied.
type aiderEdits struct {
	resolve func(string) string
	// format is the edit format the session's "Model:" line names; it
	// decides whether a fenced block under a file name is the file (whole) or
	// only shown.
	format  string
	pending map[string][]model.Message
}

func newAiderEdits(resolve func(string) string) *aiderEdits {
	return &aiderEdits{resolve: resolve, pending: map[string][]model.Message{}}
}

// noteOutput reads one of aider's own lines for the edit format. In architect
// mode the "Editor model:" line names the format the edits are written in.
func (e *aiderEdits) noteOutput(said string) {
	if m := aiderEditFormat.FindStringSubmatch(said); m != nil &&
		(strings.HasPrefix(said, "Model:") || strings.HasPrefix(said, "Editor model:")) {
		e.format = m[1]
	}
}

// reply reads the edits out of the text of one reply.
func (e *aiderEdits) reply(text string, t time.Time) {
	if !IndexEdits() && !IndexWrites() {
		return
	}
	lines := strings.Split(text, "\n")
	file := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case aiderSearchHead.MatchString(line):
			if f := aiderBlockFile(lines[:i]); f != "" {
				file = f
			}
			var old, written []string
			j := i + 1
			for ; j < len(lines) && !aiderSearchDivider.MatchString(lines[j]); j++ {
				old = append(old, lines[j])
			}
			k := j + 1
			for ; k < len(lines) && !aiderSearchTail.MatchString(lines[k]); k++ {
				written = append(written, lines[k])
			}
			if k >= len(lines) {
				return
			}
			i = k
			if file != "" {
				e.hold(e.resolve(file), strings.Join(old, "\n"), strings.Join(written, "\n"), t)
			}
		case strings.HasPrefix(line, "```diff"):
			body, end := aiderFence(lines, i)
			i = end
			_, spans, wrote := unifiedPatch(body, "", e.resolve)
			e.holdRecords(spans, wrote, t)
		case strings.TrimSpace(line) == "*** Begin Patch":
			end := i
			for end < len(lines) && strings.TrimSpace(lines[end]) != "*** End Patch" {
				end++
			}
			_, spans, wrote := applyPatch(strings.Join(lines[i:min(end+1, len(lines))], "\n"), e.resolve)
			e.holdRecords(spans, wrote, t)
			i = end
		case strings.HasPrefix(line, "```") && (e.format == "whole" || e.format == "editor-whole"):
			body, end := aiderFence(lines, i)
			f := aiderBlockFile(lines[:i])
			i = end
			if f != "" {
				e.hold(e.resolve(f), "", body, t)
			}
		}
	}
}

// hold keeps one SEARCH/REPLACE block: the searched text is what the edit
// replaced, empty for a new file.
func (e *aiderEdits) hold(path, old, written string, t time.Time) {
	if path == "" || strings.ContainsAny(path, "\n\r") {
		return
	}
	if IndexEdits() && strings.TrimSpace(old) != "" {
		if len(old) > editSpanMax {
			old = old[:editSpanMax]
		}
		e.pending[path] = append(e.pending[path], model.Message{Role: RoleEdit, Text: path + "\n" + old, Time: t})
	}
	if IndexWrites() {
		if rec := WroteRecord(path, written); rec != "" {
			e.pending[path] = append(e.pending[path], model.Message{Role: RoleWrote, Text: rec, Time: t})
		}
	}
}

// holdRecords keeps the records a patch produced, each under its own file.
func (e *aiderEdits) holdRecords(spans, wrote []string, t time.Time) {
	if IndexEdits() {
		for _, s := range spans {
			p, _, _ := strings.Cut(s, "\n")
			e.pending[p] = append(e.pending[p], model.Message{Role: RoleEdit, Text: s, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wrote {
			p, _, _ := strings.Cut(w, "\n")
			e.pending[p] = append(e.pending[p], model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
}

// applied returns what was held for the file aider says it changed.
func (e *aiderEdits) applied(file string) []model.Message {
	p := e.resolve(file)
	out := e.pending[p]
	delete(e.pending, p)
	return out
}

// drop forgets what the last reply proposed: a new question means aider has
// said all it will about it.
func (e *aiderEdits) drop() {
	if len(e.pending) > 0 {
		e.pending = map[string][]model.Message{}
	}
}

// aiderBlockFile is the file name a block is under: the nearest of the three
// lines above it that is not a fence, stripped the way aider strips it
// (find_filename in coders/editblock_coder.py).
func aiderBlockFile(above []string) string {
	for n, i := 0, len(above)-1; i >= 0 && n < 3; i, n = i-1, n+1 {
		l := strings.TrimSpace(above[i])
		if strings.HasPrefix(l, "```") {
			continue
		}
		l = strings.TrimSuffix(l, ":")
		l = strings.TrimLeft(l, "#")
		l = strings.TrimSpace(l)
		l = strings.Trim(l, "`*")
		if l == "" || strings.ContainsAny(l, " \t") {
			return ""
		}
		return l
	}
	return ""
}

// aiderFence returns the body of the fence opened at lines[start] and the
// index of its closing line.
func aiderFence(lines []string, start int) (string, int) {
	end := start + 1
	for end < len(lines) && !strings.HasPrefix(lines[end], "```") {
		end++
	}
	return strings.Join(lines[start+1:min(end, len(lines))], "\n"), end
}

// aiderResolver puts a name aider printed in the form the other records use:
// joined to the history's directory, which is aider's git root, when it is
// relative.
func aiderResolver(root string) func(string) string {
	return func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" || strings.ContainsAny(p, "\n\r") {
			return ""
		}
		if root != "" && !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return filepath.Clean(p)
	}
}
