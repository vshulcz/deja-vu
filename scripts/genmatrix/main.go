// Command genmatrix renders the harness capability matrix from
// docs/registry/registry.json into README.md and docs/guide/harnesses.html,
// between matrix markers. One source of truth; a CI test fails on drift.
package main

import (
	"encoding/json"
	"fmt"
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
		Resume  bool   `json:"resume"`
		Handoff string `json:"handoff"`
		Prereq  string `json:"prereq"`
	} `json:"capabilities"`
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
	b.WriteString("| Harness | Store | MCP recall | Auto-recall | Resume | Handoff | Needs |\n")
	b.WriteString("| --- | --- | :-: | :-: | :-: | :-: | --- |\n")
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
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			e.DisplayName, store, mark(e.Capabilities.MCP), mark(e.Capabilities.Auto),
			mark(e.Capabilities.Resume), handoffMark(e.Capabilities.Handoff), prereq)
	}
	return b.String()
}

func htmlTable(r registry) string {
	var b strings.Builder
	b.WriteString("<table>\n<tr><th>Harness</th><th>Store</th><th>MCP recall</th><th>Auto-recall</th><th>Resume</th><th>Handoff</th><th>Needs</th></tr>\n")
	for _, e := range r.Harnesses {
		if e.ID == "deja" {
			continue
		}
		prereq := e.Capabilities.Prereq
		if prereq == "" {
			prereq = "—"
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			e.DisplayName, strings.Join(e.StorePaths, "</code><br><code>"),
			mark(e.Capabilities.MCP), mark(e.Capabilities.Auto), mark(e.Capabilities.Resume),
			handoffMark(e.Capabilities.Handoff), prereq)
	}
	b.WriteString("</table>")
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
