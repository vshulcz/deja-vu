package sources

import (
	"regexp"
	"strings"
)

// Roo and the legacy Cline extension do not send an edit as old_string and
// new_string the way the shared dialect expects. apply_diff and
// replace_in_file carry a SEARCH/REPLACE block under `diff`,
// search_and_replace names the two sides `search` and `replace`, and
// write_to_file and insert_content carry only the written side under
// `content`. Reading none of them left both readers with the paths a session
// touched and not a line of what it changed, so blame, restore and the
// moved-since annotation were silent on every Roo session (#595).
//
// Cline's replace_in_file spells the markers `------- SEARCH` and
// `+++++++ REPLACE`, and accepts Roo's too; read with Roo's alone, Cline's
// main edit tool recorded the path and neither side (#4504). Both match a
// marker as the whole line, so an indented `=======` inside the replaced code
// is code. Roo's apply_diff takes exactly seven; Cline takes a run of three
// or more (src/core/assistant-message/diff.ts), which in Roo's would cut a
// block at a `===` heading rule.
type diffMarkers struct{ search, middle, replace *regexp.Regexp }

var (
	rooMarkers = diffMarkers{
		search:  regexp.MustCompile(`^<{7} SEARCH>?$`),
		middle:  regexp.MustCompile(`^={7}$`),
		replace: regexp.MustCompile(`^>{7} REPLACE$`),
	}
	clineMarkers = diffMarkers{
		search:  regexp.MustCompile(`^(?:-{3,}|<{3,}) SEARCH>?$`),
		middle:  regexp.MustCompile(`^={3,}$`),
		replace: regexp.MustCompile(`^(?:\+{3,}|>{3,}) REPLACE>?$`),
	}
)

// rooEditTools are the calls that change a file, under both extensions' names.
var rooEditTools = map[string]bool{
	"apply_diff":         true,
	"replace_in_file":    true,
	"search_and_replace": true,
	"write_to_file":      true,
	"insert_content":     true,
	// Current Roo's tools: the last three take old_string and new_string under
	// `file_path`, apply_patch takes one patch for any number of files
	// (#4419).
	"search_replace": true,
	"edit_file":      true,
	"edit":           true,
	"apply_patch":    true,
}

// rooDiffSides splits one diff payload into the replaced and the written side
// of each of its blocks. A payload can hold several blocks for the same file;
// an unterminated one ends the walk, because guessing where it was meant to
// close would record text the file never held.
func rooDiffSides(diff string, m diffMarkers) (replaced, written []string) {
	lines := strings.Split(diff, "\n")
	for i := 0; i < len(lines); i++ {
		if !rooMarker(lines[i], m.search) {
			continue
		}
		i++
		i = rooSkipBlockHeader(lines, i)
		before, next, ok := rooCollect(lines, i, m.middle)
		if !ok {
			return replaced, written
		}
		after, next, ok := rooCollect(lines, next+1, m.replace)
		if !ok {
			return replaced, written
		}
		i = next
		replaced = append(replaced, strings.Join(before, "\n"))
		written = append(written, strings.Join(after, "\n"))
	}
	return replaced, written
}

// rooSkipBlockHeader steps over the line numbers a block may declare —
// `:start_line:12` and the `-------` rule under it belong to the marker, not
// to the file. The rule counts as a header only after a line number, so a
// replaced span that itself starts with dashes survives.
func rooSkipBlockHeader(lines []string, i int) int {
	numbered := false
	for i < len(lines) {
		s := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(s, ":start_line:"), strings.HasPrefix(s, ":end_line:"):
			numbered = true
		case numbered && s == "-------":
			numbered = false
		default:
			return i
		}
		i++
	}
	return i
}

// rooMarker reports whether a line is a marker, trailing whitespace and a
// CRLF's \r aside.
func rooMarker(line string, marker *regexp.Regexp) bool {
	return marker.MatchString(strings.TrimRight(line, " \t\r"))
}

// rooCollect reads lines until a marker, and reports whether it found one.
func rooCollect(lines []string, i int, markers *regexp.Regexp) (body []string, at int, ok bool) {
	for ; i < len(lines); i++ {
		if rooMarker(lines[i], markers) {
			return body, i, true
		}
		body = append(body, lines[i])
	}
	return nil, i, false
}

// rooAbsPath puts a recorded path in the form the surfaces compare. Roo's
// tools take a path relative to the workspace, so an edit at the root of a
// checkout was recorded as `loop.go` — and line-level blame matches a record
// against a file by their last two segments, which a one-segment path can
// never have. The workspace comes from the task's own metadata
// (`history_item.json`, `cwdOnTaskInitialization`); without it the path stays
// as it was recorded rather than being resolved against the wrong root.
func rooAbsPath(p, workspace string) string {
	if p == "" || workspace == "" || isAbsolutePath(p) {
		return p
	}
	rel := strings.TrimPrefix(slashed(p), "./")
	return strings.TrimSuffix(slashed(workspace), "/") + "/" + rel
}

// rooResolvePaths is rooAbsPath over a files record, which is one path per
// line.
func rooResolvePaths(record, workspace string) string {
	if record == "" || workspace == "" {
		return record
	}
	lines := strings.Split(record, "\n")
	for i, p := range lines {
		lines[i] = rooAbsPath(p, workspace)
	}
	return strings.Join(lines, "\n")
}

// rooPatchPaths adds the files an apply_patch call names to a files record.
// The patch carries them in its own headers, not under an argument (#4419),
// and the call names it `patch` in Roo, `input` in Cline (#4504).
func rooPatchPaths(blocks []any, record string) string {
	seen := map[string]bool{}
	var out []string
	if record != "" {
		out = strings.Split(record, "\n")
		for _, p := range out {
			seen[p] = true
		}
	}
	for _, patch := range applyPatchInputs(blocks, rooDialect) {
		files, _, _ := applyPatch(patch, nil)
		for _, p := range files {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return strings.Join(out, "\n")
}

// rooEditRecords turns the edit calls in one message into the replaced side
// ("path\nspan") and the written side (WroteRecord), in the order the calls
// were made.
func rooEditRecords(blocks []any, workspace string) (spans, wrote []string) {
	for _, it := range blocks {
		name, in, ok := toolPart(it, rooDialect)
		if !ok || !rooEditTools[name] {
			continue
		}
		if name == "apply_patch" {
			for _, patch := range applyPatchInputs([]any{it}, rooDialect) {
				_, sp, wr := applyPatch(patch, func(p string) string { return rooAbsPath(p, workspace) })
				spans = append(spans, sp...)
				wrote = append(wrote, wr...)
			}
			continue
		}
		path, _ := in["path"].(string)
		if path == "" {
			path, _ = in["file_path"].(string)
		}
		// "path\nspan" cannot hold a path with a newline in it, the same
		// reason the shared helper drops those edits (#2042).
		if path == "" || strings.ContainsAny(path, "\n\r") {
			continue
		}
		path = rooAbsPath(path, workspace)
		replaced, written := rooCallSides(name, in)
		for _, span := range replaced {
			if span == "" {
				continue
			}
			if len(span) > editSpanMax {
				span = span[:editSpanMax]
			}
			spans = append(spans, path+"\n"+span)
		}
		for _, w := range written {
			if rec := WroteRecord(path, w); rec != "" {
				wrote = append(wrote, rec)
			}
		}
	}
	return spans, wrote
}

func rooCallSides(name string, in map[string]any) (replaced, written []string) {
	switch name {
	case "apply_diff":
		diff, _ := in["diff"].(string)
		return rooDiffSides(diff, rooMarkers)
	case "replace_in_file":
		diff, _ := in["diff"].(string)
		return rooDiffSides(diff, clineMarkers)
	case "search_and_replace":
		// A regular expression is not the text that stopped existing, so only
		// a literal search is recorded as the replaced side. The written side
		// of a regex replacement carries $1 and friends, which is not a line
		// the file holds either.
		if rooTruthy(in["use_regex"]) {
			return nil, nil
		}
		search, _ := in["search"].(string)
		replace, _ := in["replace"].(string)
		return []string{search}, []string{replace}
	case "search_replace", "edit_file", "edit":
		old, _ := in["old_string"].(string)
		neu, _ := in["new_string"].(string)
		return []string{old}, []string{neu}
	case "write_to_file", "insert_content":
		content, _ := in["content"].(string)
		return nil, []string{content}
	}
	return nil, nil
}

// rooTruthy reads a flag that arrives as a bool from the JSON history and as a
// string from the XML the model writes.
func rooTruthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	}
	return false
}
