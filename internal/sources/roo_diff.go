package sources

import "strings"

// Roo and the legacy Cline extension do not send an edit as old_string and
// new_string the way the shared dialect expects. apply_diff and
// replace_in_file carry a SEARCH/REPLACE block under `diff`,
// search_and_replace names the two sides `search` and `replace`, and
// write_to_file and insert_content carry only the written side under
// `content`. Reading none of them left both readers with the paths a session
// touched and not a line of what it changed, so blame, restore and the
// moved-since annotation were silent on every Roo session (#595).
const (
	rooSearchMarker  = "<<<<<<< SEARCH"
	rooMiddleMarker  = "======="
	rooReplaceMarker = ">>>>>>> REPLACE"
)

// rooEditTools are the calls that change a file, under both extensions' names.
var rooEditTools = map[string]bool{
	"apply_diff":         true,
	"replace_in_file":    true,
	"search_and_replace": true,
	"write_to_file":      true,
	"insert_content":     true,
}

// rooDiffSides splits one diff payload into the replaced and the written side
// of each of its blocks. A payload can hold several blocks for the same file;
// an unterminated one ends the walk, because guessing where it was meant to
// close would record text the file never held.
func rooDiffSides(diff string) (replaced, written []string) {
	lines := strings.Split(diff, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), rooSearchMarker) {
			continue
		}
		i++
		i = rooSkipBlockHeader(lines, i)
		before, next, ok := rooCollect(lines, i, rooMiddleMarker)
		if !ok {
			return replaced, written
		}
		after, next, ok := rooCollect(lines, next+1, rooReplaceMarker)
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

// rooCollect reads lines until the marker, and reports whether it found one.
func rooCollect(lines []string, i int, marker string) (body []string, at int, ok bool) {
	for ; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), marker) {
			return body, i, true
		}
		body = append(body, lines[i])
	}
	return nil, i, false
}

// rooEditRecords turns the edit calls in one message into the replaced side
// ("path\nspan") and the written side (WroteRecord), in the order the calls
// were made.
func rooEditRecords(blocks []any) (spans, wrote []string) {
	for _, it := range blocks {
		name, in, ok := toolPart(it, rooDialect)
		if !ok || !rooEditTools[name] {
			continue
		}
		path, _ := in["path"].(string)
		// "path\nspan" cannot hold a path with a newline in it, the same
		// reason the shared helper drops those edits (#2042).
		if path == "" || strings.ContainsAny(path, "\n\r") {
			continue
		}
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
	case "apply_diff", "replace_in_file":
		diff, _ := in["diff"].(string)
		return rooDiffSides(diff)
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
