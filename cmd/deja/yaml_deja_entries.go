package main

import "strings"

// yamlDejaEntryNames lists the entries under a top-level YAML block that run
// deja under a name other than deja's own: hermes and goose key them by name
// (`mcp_servers:` / `extensions:`), Continue lists them with a `name:` field.
// The YAML writers key on `deja` alone, so a `deja-vu` someone wired by hand
// got deja beside it and the client started the server twice, with install
// saying only "updated" (#4556). Said, not adopted, as otherDejaEntriesNote
// does for the JSON writers.
func yamlDejaEntryNames(text, key string) []string {
	var names []string
	in, entryIndent := false, -1
	name, item, runs := "", false, false
	flush := func() {
		if name != "" && name != "deja" && runs {
			names = append(names, name)
		}
		name, item, runs = "", false, false
	}
	for line := range strings.SplitSeq(lfText([]byte(text)), "\n") {
		t := strings.TrimSpace(stripYAMLComment(line))
		if t == "" {
			continue
		}
		indent := yamlIndentWidth(line)
		if indent == 0 {
			flush()
			in, entryIndent = yamlKeyLine(line, key), -1
			continue
		}
		if !in {
			continue
		}
		if entryIndent < 0 {
			entryIndent = indent
		}
		if indent == entryIndent {
			flush()
			if rest, ok := strings.CutPrefix(t, "- "); ok {
				item, t = true, strings.TrimSpace(rest)
			} else if k, _, ok := strings.Cut(t, ":"); ok {
				name = yamlScalar(strings.TrimSpace(k))
				continue
			}
		}
		field, value, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		value = yamlScalar(strings.TrimSpace(value))
		switch strings.TrimSpace(field) {
		case "name":
			if item {
				name = value
			}
		case "command", "cmd":
			if isDejaBinaryToken(value) {
				runs = true
			}
		}
	}
	flush()
	return names
}
