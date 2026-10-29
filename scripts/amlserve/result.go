package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// turn is one message of a stored session.
type turn struct {
	role, text string
}

type change struct {
	file, old, new string
}

var (
	codePath     = regexp.MustCompile(`(?:\./)?(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+\.(?:py|pyi|go|js|jsx|ts|tsx|rb|java|kt|c|cc|cpp|h|hpp|rs|php|cs|scala|swift|sh|ya?ml|toml|cfg|ini|rst|md|txt|html|css)\b`)
	diffFile     = regexp.MustCompile(`(?m)^diff --git a/(\S+)`)
	jsonObjStart = regexp.MustCompile(`\{\s*"`)
)

// sessionChanges extracts what a coding session worked on: the opening task,
// the closing diagnosis, the folded code changes and the repository files it
// edited or inspected.
func sessionChanges(turns []turn) (task, end string, final []change, files []string) {
	var changes []change
	seenFile := map[string]bool{}
	var editFiles []string
	mentions := map[string]int{}
	addFile := func(f string) {
		if !seenFile[f] {
			seenFile[f] = true
			editFiles = append(editFiles, f)
		}
	}
	for _, t := range turns {
		if task == "" && t.role == "user" {
			task = t.text
		}
		if strings.Contains(t.text, "/") {
			for _, p := range codePath.FindAllString(t.text, -1) {
				if p = repoPath(p); !scratch(p) && !note(p) {
					mentions[p]++
				}
			}
		}
		if t.role != "assistant" {
			continue
		}
		if strings.Contains(t.text, "diff --git ") {
			for _, d := range diffFile.FindAllStringSubmatch(t.text, -1) {
				if f := repoPath(d[1]); !scratch(f) && !note(f) {
					addFile(f)
				}
			}
		}
		if !strings.Contains(t.text, "{") {
			continue
		}
		for _, obj := range jsonObjects(t.text) {
			file := firstString(obj, "file_path", "path", "filePath")
			if file == "" {
				continue
			}
			file = repoPath(file)
			if scratch(file) || note(file) || !strings.Contains(file, "/") && !strings.Contains(file, ".") {
				continue
			}
			if es, ok := obj["edits"].([]any); ok {
				for _, raw := range es {
					if em, ok := raw.(map[string]any); ok {
						o, n := firstString(em, "old_string", "old_str"), firstString(em, "new_string", "new_str")
						if n != "" || o != "" {
							addFile(file)
							changes = append(changes, change{file, o, n})
						}
					}
				}
				continue
			}
			o, n := firstString(obj, "old_string", "old_str"), firstString(obj, "new_string", "new_str")
			if n == "" && o == "" {
				n = firstString(obj, "content", "file_text")
			}
			if n != "" || o != "" {
				addFile(file)
				changes = append(changes, change{file, o, n})
			}
		}
	}
	end = ending(turns)
	// An edit that rewrites text an earlier edit put in is folded into it, so
	// each change reads from the original code to its final version.
	for _, c := range changes {
		folded := false
		for i := len(final) - 1; i >= 0 && c.old != ""; i-- {
			if final[i].file == c.file && strings.Contains(final[i].new, c.old) {
				final[i].new = strings.Replace(final[i].new, c.old, c.new, 1)
				folded = true
				break
			}
		}
		if !folded {
			final = append(final, c)
		}
	}
	for _, tests := range []bool{false, true} {
		for _, f := range editFiles {
			if testFile(f) == tests {
				files = append(files, f)
			}
		}
	}
	files = append(files, topMentions(mentions, editFiles, 12)...)
	return task, end, final, files
}

// allEdits returns the folded non-test changes followed by the added lines of
// any other Edit/MultiEdit/Write inside the checkout.
func allEdits(turns []turn, final []change) []change {
	var out []change
	for _, f := range final {
		if !testFile(f.file) {
			out = append(out, f)
		}
	}
	seen := map[string]bool{}
	for _, f := range out {
		for l := range strings.SplitSeq(f.new, "\n") {
			if l = strings.TrimSpace(l); len(l) >= 25 {
				seen[l] = true
			}
		}
	}
	for _, t := range turns {
		if t.role != "assistant" || !strings.Contains(t.text, "{") {
			continue
		}
		for _, obj := range jsonObjects(t.text) {
			raw := firstString(obj, "file_path", "path", "filePath")
			file := repoPath(raw)
			if file == "" || strings.Contains(file, "output/") || strings.HasSuffix(file, "preds.json") || note(file) ||
				strings.HasPrefix(file, "tests/") || strings.HasPrefix(file, "test/") {
				continue
			}
			oldStr := firstString(obj, "old_string", "old_str")
			newStr := firstString(obj, "new_string", "new_str", "content", "file_text")
			if newStr == "" {
				continue
			}
			var added []string
			for l := range strings.SplitSeq(newStr, "\n") {
				s := strings.TrimSpace(l)
				if len(s) >= 25 && !seen[s] && (oldStr == "" || !strings.Contains(oldStr, s)) {
					seen[s] = true
					added = append(added, l)
				}
			}
			if len(added) > 0 {
				out = append(out, change{file: file, new: strings.Join(added, "\n")})
			}
		}
	}
	return out
}

// result renders a past session by what it settled rather than how it got
// there: the task, how it ended, the files it touched and the code it changed,
// each change folded to its final state. The head of a coding session is the
// task and directory exploration; the change that solved it sits in the second
// half, past any budget that cuts from the top.
func result(id string, turns []turn, full string, snippets []string, limit int, tail func(int) string) string {
	task, end, final, files := sessionChanges(turns)
	var b strings.Builder
	fmt.Fprintf(&b, "Past task %s\n\nTask:\n%s\n", id, clip(task, 900))
	if end != "" {
		fmt.Fprintf(&b, "\nHow it ended:\n%s\n", clip(end, 1600))
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "\nFiles: %s\n", strings.Join(files, ", "))
	}
	if len(final) > 0 {
		b.WriteString("\nWhat it changed in the code:\n")
		// The library change first, then its tests.
		for _, tests := range []bool{false, true} {
			for _, c := range final {
				if testFile(c.file) != tests || b.Len() >= limit {
					continue
				}
				if c.old != "" {
					fmt.Fprintf(&b, "\n%s, replaced:\n%s\nwith:\n%s\n", c.file, clip(c.old, 600), clip(c.new, 3000))
				} else {
					fmt.Fprintf(&b, "\n%s, wrote:\n%s\n", c.file, clip(c.new, 3000))
				}
			}
		}
	}
	if rest := limit - b.Len(); rest > 500 {
		if tail == nil {
			b.WriteString("\nThe session from the start:\n")
			b.WriteString(renderText(full, snippets, rest-len("\nThe session from the start:\n")))
		} else if t := tail(rest - len("\nWhere it matches the question:\n")); t != "" {
			b.WriteString("\nWhere it matches the question:\n")
			b.WriteString(t)
		}
	}
	return cut(b.String(), limit)
}

// windows is the session's messages that best match the query, each with
// its neighbours, chosen best first until budget and shown in the session's
// order.
func (c *corpus) windows(sid string, sc []float64, budget int) string {
	d, ok := c.byID[sid]
	if !ok || budget <= 0 || c.docs[d].msgTo <= c.docs[d].msgFrom {
		return ""
	}
	first, last := c.docs[d].msgFrom, c.docs[d].msgTo-1
	idx := make([]int, 0, last-first+1)
	for i := first; i <= last; i++ {
		idx = append(idx, i)
	}
	order := append([]int(nil), idx...)
	sort.SliceStable(order, func(a, b int) bool { return sc[order[a]] > sc[order[b]] })
	pick := map[int]bool{}
	used := 0
	for _, k := range order {
		if sc[k] <= 0 {
			break
		}
		add := 0
		var win []int
		for j := max(k-msgNeighbours, first); j <= min(k+msgNeighbours, last); j++ {
			if !pick[j] {
				win = append(win, j)
				add += len(showMsg(c.msgs[j])) + 1
			}
		}
		if used+add > budget {
			continue
		}
		used += add
		for _, j := range win {
			pick[j] = true
		}
	}
	var b strings.Builder
	prev := -2
	for _, j := range idx {
		if !pick[j] {
			continue
		}
		if prev >= 0 && j != prev+1 {
			b.WriteString("...\n")
		}
		b.WriteString(showMsg(c.msgs[j]))
		b.WriteByte('\n')
		prev = j
	}
	return b.String()
}

// jsonObjects decodes every JSON object that starts in s, skipping what does
// not parse.
func jsonObjects(s string) []map[string]any {
	var out []map[string]any
	for _, loc := range jsonObjStart.FindAllStringIndex(s, -1) {
		dec := json.NewDecoder(strings.NewReader(s[loc[0]:]))
		var m map[string]any
		if dec.Decode(&m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func topMentions(m map[string]int, skip []string, n int) []string {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	var ps []string
	for p := range m {
		if !skipped[p] {
			ps = append(ps, p)
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if m[ps[i]] != m[ps[j]] {
			return m[ps[i]] > m[ps[j]]
		}
		return ps[i] < ps[j]
	})
	if len(ps) > n {
		ps = ps[:n]
	}
	return ps
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return cut(s, n) + " …"
}

// cut shortens s to at most n bytes without splitting a character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// repoPath drops what a sandbox put in front of a repository path: "./",
// a diff's a/ or b/, and everything up to a "testbed/" checkout root.
func repoPath(p string) string {
	p = strings.TrimPrefix(p, "./")
	if i := strings.LastIndex(p, "testbed/"); i >= 0 {
		p = p[i+len("testbed/"):]
	}
	for _, pre := range []string{"a/", "b/"} {
		p = strings.TrimPrefix(p, pre)
	}
	return p
}

// scratch is a file an agent wrote to try something out rather than a change
// to the project: a script at the repository root, or output it saved.
func scratch(p string) bool {
	if strings.Contains(p, "output/") || strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, "preds.json") ||
		strings.Contains(p, "swebench_") || strings.HasPrefix(p, "..") {
		return true
	}
	base := p[strings.LastIndex(p, "/")+1:]
	for _, pre := range []string{"reproduce", "debug", "verify", "check", "demo", "scratch", "test_issue", "test_fix", "test_bug"} {
		if strings.HasPrefix(base, pre) {
			return true
		}
	}
	if !strings.Contains(p, "/") && strings.HasPrefix(base, "test") {
		return true
	}
	return false
}

func testFile(p string) bool {
	base := p[strings.LastIndex(p, "/")+1:]
	return strings.Contains(p, "/tests/") || strings.HasPrefix(p, "tests/") || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py")
}

// note is prose the agent wrote for itself, not a change to the project.
func note(p string) bool {
	return strings.HasSuffix(p, ".md") && !strings.Contains(p, "docs/")
}
