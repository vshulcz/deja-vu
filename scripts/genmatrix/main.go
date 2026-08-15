// Command genmatrix renders the harness capability matrix from
// docs/registry/registry.json into README.md and docs/guide/harnesses.html,
// between matrix markers. One source of truth; a CI test fails on drift.
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"strings"
)

type entry struct {
	ID           string   `json:"id"`
	DisplayName  string   `json:"display_name"`
	StorePaths   []string `json:"store_paths"`
	Capabilities struct {
		MCP     bool   `json:"mcp"`
		Auto    bool   `json:"auto"`
		Skill   bool   `json:"skill"`
		Command bool   `json:"command"`
		Resume  bool   `json:"resume"`
		Handoff string `json:"handoff"`
		Prereq  string `json:"prereq"`
	} `json:"capabilities"`
	Gaps map[string]struct {
		State  string `json:"state"`
		Why    string `json:"why"`
		Source string `json:"source"`
	} `json:"gaps"`
}

// gapMark distinguishes the four reasons a capability is missing. A single dash
// for all of them is what let coverage this thin go unnoticed: work and dead
// ends looked identical.
func gapMark(e entry, cap string, have bool) string {
	if have {
		return "✅"
	}
	switch e.Gaps[cap].State {
	case "impossible":
		return "✕"
	case "blocked":
		return "⚠"
	case "todo":
		return "—"
	default:
		return "?"
	}
}

type registry struct {
	Harnesses []entry `json:"harnesses"`
}

func mark(b bool) string {
	if b {
		return "✅"
	}
	return "—"
}

func handoffMark(kind string) string {
	switch kind {
	case "exec":
		return "✅"
	case "paste":
		return "paste"
	default:
		return "—"
	}
}

func markdownTable(r registry) string {
	var b strings.Builder
	b.WriteString("| Harness | Store | MCP recall | Auto-recall | Skill | Command | Resume | Handoff | Needs |\n")
	b.WriteString("| --- | --- | :-: | :-: | :-: | :-: | :-: | :-: | --- |\n")
	for _, e := range r.Harnesses {
		if e.ID == "deja" {
			continue
		}
		quoted := make([]string, 0, len(e.StorePaths))
		for _, sp := range e.StorePaths {
			quoted = append(quoted, "`"+sp+"`")
		}
		store := strings.Join(quoted, "<br>")
		prereq := e.Capabilities.Prereq
		if prereq == "" {
			prereq = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			e.DisplayName, store, gapMark(e, "mcp", e.Capabilities.MCP), gapMark(e, "auto", e.Capabilities.Auto),
			gapMark(e, "skill", e.Capabilities.Skill), gapMark(e, "command", e.Capabilities.Command),
			gapMark(e, "resume", e.Capabilities.Resume), handoffMark(e.Capabilities.Handoff), prereq)
	}
	b.WriteString("\n✅ works &middot; — possible, not built yet &middot; ✕ the harness has no such mechanism &middot; ⚠ blocked by an upstream bug &middot; ? not investigated\n")
	b.WriteString(notes(r, func(s string) string { return "`" + s + "`" }, func(s string) string { return s }))
	return b.String()
}

// notes prints the reasons worth reading: what a harness cannot do, and what is
// waiting on someone else's fix. "todo" and "unknown" are backlog and stay out
// of the published table.
func notes(r registry, code, esc func(string) string) string {
	var b strings.Builder
	for _, e := range r.Harnesses {
		if e.ID == "deja" {
			continue
		}
		for _, cap := range []string{"mcp", "auto", "skill", "command"} {
			g, ok := e.Gaps[cap]
			if !ok || (g.State != "impossible" && g.State != "blocked") {
				continue
			}
			// esc guards the registry-authored halves only: the display name,
			// the reason and the link. The markup around them is ours.
			line := fmt.Sprintf("\n- %s %s — %s", esc(e.DisplayName), code(cap), esc(g.Why))
			if g.Source != "" {
				line += " (" + esc(g.Source) + ")"
			}
			b.WriteString(line)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n" + b.String() + "\n"
}

func htmlTable(r registry) string {
	var b strings.Builder
	b.WriteString("<table>\n<tr><th>Harness</th><th>Store</th><th>MCP recall</th><th>Auto-recall</th><th>Skill</th><th>Command</th><th>Resume</th><th>Handoff</th><th>Needs</th></tr>\n")
	for _, e := range r.Harnesses {
		if e.ID == "deja" {
			continue
		}
		prereq := e.Capabilities.Prereq
		if prereq == "" {
			prereq = "—"
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			e.DisplayName, strings.Join(e.StorePaths, "</code><br><code>"),
			gapMark(e, "mcp", e.Capabilities.MCP), gapMark(e, "auto", e.Capabilities.Auto),
			gapMark(e, "skill", e.Capabilities.Skill), gapMark(e, "command", e.Capabilities.Command),
			gapMark(e, "resume", e.Capabilities.Resume), handoffMark(e.Capabilities.Handoff), prereq)
	}
	b.WriteString("</table>\n")
	b.WriteString("<p>✅ works &middot; — possible, not built yet &middot; ✕ the harness has no such mechanism &middot; ⚠ blocked by an upstream bug &middot; ? not investigated</p>")
	if n := notes(r, func(s string) string { return "<code>" + s + "</code>" }, html.EscapeString); n != "" {
		b.WriteString("\n<ul>")
		for _, line := range strings.Split(strings.TrimSpace(n), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			if line == "" {
				continue
			}
			b.WriteString("\n<li>" + line + "</li>")
		}
		b.WriteString("\n</ul>")
	}
	return b.String()
}

func replaceBetween(path, start, end, content string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(b)
	i := strings.Index(s, start)
	j := strings.Index(s, end)
	if i < 0 || j < 0 || j < i {
		return fmt.Errorf("%s: markers %q/%q not found", path, start, end)
	}
	out := s[:i+len(start)] + "\n" + content + s[j:]
	return os.WriteFile(path, []byte(out), 0o644)
}

func main() {
	b, err := os.ReadFile("docs/registry/registry.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var r registry
	if err := json.Unmarshal(b, &r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := replaceBetween("README.md", "<!-- matrix:start -->", "<!-- matrix:end -->", markdownTable(r)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := replaceBetween("docs/guide/harnesses.html", "<!-- matrix:start -->", "<!-- matrix:end -->", htmlTable(r)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("matrix rendered")
}
