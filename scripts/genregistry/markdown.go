package main

import (
	"html"
	"regexp"
	"strings"
)

// A small markdown renderer for the registry pages and nothing else.
//
// The pages are written to one shape — headings, paragraphs, fenced code,
// lists, tables, one blockquote — so this covers those constructs and refuses
// to grow into a general parser. Anything it does not know is emitted as text,
// escaped, rather than guessed at.

var (
	reCode   = regexp.MustCompile("`([^`]+)`")
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reHeadID = regexp.MustCompile(`[^a-z0-9]+`)
)

// renderMarkdown returns the body HTML and the first level-one heading, which
// is the page's own name for itself.
func renderMarkdown(src string) (body, title string) {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var list []string
	var ordered bool
	var table []string

	flushList := func() {
		if len(list) == 0 {
			return
		}
		tag := "ul"
		if ordered {
			tag = "ol"
		}
		b.WriteString("<" + tag + ">\n")
		for _, item := range list {
			b.WriteString("<li>" + inline(item) + "</li>\n")
		}
		b.WriteString("</" + tag + ">\n")
		list = nil
	}
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		b.WriteString(`<div class="tablewrap"><table>` + "\n")
		for i, row := range table {
			// The second row of a markdown table is the alignment rule, not
			// data, and rendering it puts a row of dashes on the page.
			if i == 1 && strings.Trim(row, "|:- ") == "" {
				continue
			}
			cell := "td"
			if i == 0 {
				cell = "th"
			}
			b.WriteString("<tr>")
			for _, c := range splitRow(row) {
				b.WriteString("<" + cell + ">" + inline(strings.TrimSpace(c)) + "</" + cell + ">")
			}
			b.WriteString("</tr>\n")
		}
		b.WriteString("</table></div>\n")
		table = nil
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			flushList()
			flushTable()
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			cls := ""
			if lang != "" {
				cls = ` class="language-` + html.EscapeString(lang) + `"`
			}
			b.WriteString("<pre><code" + cls + ">" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
			continue
		}

		if strings.HasPrefix(trimmed, "|") {
			flushList()
			table = append(table, trimmed)
			continue
		}
		flushTable()

		switch {
		case trimmed == "":
			flushList()
		case strings.HasPrefix(trimmed, "#"):
			flushList()
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if level > 6 {
				level = 6
			}
			text := strings.TrimSpace(trimmed[level:])
			if level == 1 && title == "" {
				title = text
				// The h1 becomes the page heading below, once, from the title.
				continue
			}
			tag := "h" + string(rune('0'+level))
			b.WriteString("<" + tag + ` id="` + slug(text) + `">` + inline(text) + "</" + tag + ">\n")
		case strings.HasPrefix(trimmed, "> "):
			flushList()
			b.WriteString("<blockquote><p>" + inline(strings.TrimPrefix(trimmed, "> ")) + "</p></blockquote>\n")
		case strings.HasPrefix(trimmed, "- "):
			if ordered && len(list) > 0 {
				flushList()
			}
			ordered = false
			list = append(list, strings.TrimPrefix(trimmed, "- "))
		case isOrderedItem(trimmed):
			if !ordered && len(list) > 0 {
				flushList()
			}
			ordered = true
			list = append(list, trimmed[strings.Index(trimmed, ". ")+2:])
		default:
			flushList()
			b.WriteString("<p>" + inline(trimmed) + "</p>\n")
		}
	}
	flushList()
	flushTable()
	return b.String(), title
}

func isOrderedItem(s string) bool {
	i := strings.Index(s, ". ")
	if i <= 0 || i > 3 {
		return false
	}
	for _, r := range s[:i] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func splitRow(row string) []string {
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	return strings.Split(row, "|")
}

// inline escapes first and then puts back the few constructs the pages use, so
// a `<` in a store path cannot become a tag.
func inline(s string) string {
	s = html.EscapeString(s)
	s = reCode.ReplaceAllString(s, "<code>$1</code>")
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		g := reLink.FindStringSubmatch(m)
		href := g[2]
		// Registry pages link to each other as .md; the reader is on HTML.
		if strings.HasSuffix(href, ".md") && !strings.Contains(href, "://") {
			href = strings.TrimSuffix(href, ".md") + ".html"
		}
		return `<a href="` + href + `">` + g[1] + "</a>"
	})
	return s
}

func slug(s string) string {
	return strings.Trim(reHeadID.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
