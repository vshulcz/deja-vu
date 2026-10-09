package main

import (
	"fmt"
	"strings"
)

// sourcesScreen turns the tab-separated rows of `deja sources` into a table
// for a terminal: columns that line up, paths under ~, and the stores that
// hold nothing counted in one line instead of forty rows of zeros. The rows
// themselves are what a pipe gets, unchanged.
func sourcesScreen(tsv string, colour bool, width int) string {
	type row struct {
		name, sessions, messages, size, location, notes string
	}
	var rows []row
	empty, total := 0, 0
	for _, line := range strings.Split(strings.TrimSuffix(tsv, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		total++
		r := row{name: f[0], location: reportPath(f[1])}
		// A store the reader excluded has a sentence where the counts go.
		if strings.HasPrefix(f[2], "excluded") {
			r.notes = f[2]
			if p := sourcesExcludePath(f[2]); p != "" {
				r.notes = strings.TrimSuffix(f[2], p) + reportPath(p)
			}
			rows = append(rows, r)
			continue
		}
		var notes []string
		redacted := "0"
		for _, kv := range strings.Fields(f[2]) {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "sessions":
				r.sessions = v
			case "messages":
				r.messages = v
			case "size":
				r.size = v
			case "redacted":
				redacted = v
			}
		}
		// size=109.9 KB splits into two fields above.
		if i := strings.Index(f[2], "size="); i >= 0 {
			r.size, _, _ = strings.Cut(f[2][i+len("size="):], " redacted=")
		}
		hint := false
		for _, extra := range f[3:] {
			k, v, ok := strings.Cut(extra, "=")
			switch {
			case ok && k == "excluded-patterns":
				notes = append(notes, v+" exclude patterns")
			case ok && k == "excluded-sessions":
				notes = append(notes, v+" sessions excluded")
			case ok && k == "note":
				// aider's advice for a machine with no history at home.
				notes = append(notes, v)
				hint = true
			default:
				notes = append(notes, strings.TrimSuffix(strings.TrimPrefix(extra, "("), ")"))
			}
		}
		if redacted != "0" {
			notes = append([]string{redacted + " redacted"}, notes...)
		}
		if r.sessions == "0" && r.messages == "0" && (len(notes) == 0 || (hint && len(notes) == 1)) {
			empty++
			continue
		}
		r.notes = strings.Join(notes, ", ")
		rows = append(rows, r)
	}
	var b strings.Builder
	if len(rows) == 0 {
		fmt.Fprintf(&b, "no sessions in the %s deja reads — `deja sources | cat` lists them\n", doctorCount(total, "store"))
		return b.String()
	}
	head := row{name: "store", sessions: "sessions", messages: "messages", size: "size", location: "location"}
	wn, ws, wm, wz := len(head.name), len(head.sessions), len(head.messages), len(head.size)
	for _, r := range rows {
		wn = max(wn, len(r.name))
		ws = max(ws, len(r.sessions))
		wm = max(wm, len(r.messages))
		wz = max(wz, len(r.size))
	}
	format := func(r row) string {
		s := fmt.Sprintf("%-*s  %*s  %*s  %*s  %s", wn, r.name, ws, r.sessions, wm, r.messages, wz, r.size, r.location)
		if r.notes != "" {
			s += "  (" + r.notes + ")"
		}
		return strings.TrimRight(s, " ")
	}
	hang := wn + ws + wm + wz + 8
	h := format(head)
	if colour {
		h = statDim + h + statReset
	}
	b.WriteString(h + "\n")
	for _, r := range rows {
		for _, l := range wrapHanging(format(r), width, hang) {
			b.WriteString(l + "\n")
		}
	}
	if empty > 0 {
		line := fmt.Sprintf("%s with no sessions — `deja sources | cat` lists every store", doctorCount(empty, "more store"))
		for _, l := range wrapHanging(line, width, 2) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// sourcesExcludePath is the path at the end of an excluded row's sentence.
func sourcesExcludePath(s string) string {
	if i := strings.LastIndex(s, " is in "); i >= 0 {
		return s[i+len(" is in "):]
	}
	return ""
}
