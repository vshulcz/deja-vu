package sources

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/redact"
)

// ClaudeMemoryEntry is a standing rule or note from Claude Code's per-project
// auto-memory directory (~/.claude/projects/<encoded-path>/memory/*.md).
type ClaudeMemoryEntry struct {
	Path        string
	File        string
	Dir         string
	Name        string
	Description string
	Type        string
	Body        string
	ModTime     time.Time
}

// Summary returns the concise one-line text for a Claude auto-memory entry,
// leading with its frontmatter description and appending the first body
// sentence when the description is a short title that does not already hold it.
func (e ClaudeMemoryEntry) Summary() string {
	desc := strings.TrimSpace(e.Description)
	body := strings.TrimSpace(e.Body)
	var out string
	switch {
	case desc != "" && body != "":
		if len(desc) >= 120 || strings.Contains(strings.ToLower(desc), strings.ToLower(body)) {
			out = desc
		} else {
			out = desc + " — " + body
		}
	case desc != "":
		out = desc
	default:
		out = body
	}
	out, _ = redact.Text(out)
	return strings.TrimSpace(out)
}

// Priority ranks rules and feedback ahead of project conventions, user notes,
// references, and fallback MEMORY.md index lines (#5095).
func (e ClaudeMemoryEntry) Priority() int {
	t := strings.ToLower(strings.TrimSpace(e.Type))
	base := strings.ToLower(strings.TrimSuffix(e.File, filepath.Ext(e.File)))
	switch {
	case t == "feedback" || t == "rule" || t == "rules" || strings.HasPrefix(base, "feedback") || strings.HasPrefix(base, "rule"):
		return 0
	case t == "project" || strings.HasPrefix(base, "project"):
		return 1
	case t == "user" || strings.HasPrefix(base, "user"):
		return 2
	case !strings.EqualFold(e.File, "MEMORY.md"):
		return 3
	default:
		return 4
	}
}

const claudeMemoryFileMaxBytes = 64 << 10

// LoadClaudeAutoMemory reads Claude Code's auto-memory markdown files for the
// given project directories across ClaudeRoots(), ordered with rules and
// feedback first.
func LoadClaudeAutoMemory(dirs []string) []ClaudeMemoryEntry {
	memDirs := claudeAutoMemoryDirs(dirs)
	if len(memDirs) == 0 {
		return nil
	}
	var entries []ClaudeMemoryEntry
	seenPath := map[string]bool{}
	for _, memDir := range memDirs {
		dirEntries, err := os.ReadDir(memDir)
		if err != nil {
			continue
		}
		var topicFiles []os.DirEntry
		var indexFile os.DirEntry
		for _, de := range dirEntries {
			if de.IsDir() || !strings.HasSuffix(strings.ToLower(de.Name()), ".md") {
				continue
			}
			if strings.EqualFold(de.Name(), "MEMORY.md") {
				indexFile = de
				continue
			}
			topicFiles = append(topicFiles, de)
		}
		loadedFiles := map[string]bool{}
		for _, de := range topicFiles {
			full := filepath.Clean(filepath.Join(memDir, de.Name()))
			if seenPath[full] {
				continue
			}
			seenPath[full] = true
			if parsed, ok := parseClaudeMemoryFile(full, memDir, de); ok {
				loadedFiles[strings.ToLower(de.Name())] = true
				entries = append(entries, parsed)
			}
		}
		if indexFile != nil {
			full := filepath.Clean(filepath.Join(memDir, indexFile.Name()))
			if !seenPath[full] {
				seenPath[full] = true
				entries = append(entries, parseClaudeMemoryIndexFile(full, memDir, indexFile, loadedFiles)...)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		pi, pj := entries[i].Priority(), entries[j].Priority()
		if pi != pj {
			return pi < pj
		}
		if !entries[i].ModTime.Equal(entries[j].ModTime) {
			return entries[i].ModTime.After(entries[j].ModTime)
		}
		return entries[i].File < entries[j].File
	})
	return entries
}

func claudeAutoMemoryDirs(dirs []string) []string {
	var cands []string
	addCand := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		if !slices.Contains(cands, p) {
			cands = append(cands, p)
		}
	}
	for _, d := range dirs {
		addCand(d)
		if resolved, err := filepath.EvalSymlinks(d); err == nil && resolved != "" {
			addCand(resolved)
		}
		slash := filepath.ToSlash(filepath.Clean(d))
		if rest, ok := strings.CutPrefix(slash, "/private/tmp/"); ok {
			addCand("/tmp/" + rest)
		} else if rest, ok := strings.CutPrefix(slash, "/tmp/"); ok {
			addCand("/private/tmp/" + rest)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	var memDirs []string
	addMemDir := func(p string) {
		p = filepath.Clean(p)
		if slices.Contains(memDirs, p) {
			return
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			memDirs = append(memDirs, p)
		}
	}
	for _, root := range ClaudeRoots() {
		for _, cand := range cands {
			enc := claudeEncodePath(cand)
			if enc == "" {
				continue
			}
			addMemDir(filepath.Join(root, enc, "memory"))
			if trimmed := strings.TrimPrefix(enc, "-"); trimmed != "" && trimmed != enc {
				addMemDir(filepath.Join(root, trimmed, "memory"))
			}
			if len(enc) > claudeFolderNameMax {
				if entries, err := os.ReadDir(root); err == nil {
					for _, e := range entries {
						if e.IsDir() && claudeFolderIs(cand, e.Name()) {
							addMemDir(filepath.Join(root, e.Name(), "memory"))
						}
					}
				}
			}
		}
	}
	return memDirs
}

func parseClaudeMemoryFile(full, memDir string, de os.DirEntry) (ClaudeMemoryEntry, bool) {
	raw, mtime, ok := readClaudeMemoryBytes(full, de)
	if !ok {
		return ClaudeMemoryEntry{}, false
	}
	fm, bodyText := splitClaudeFrontmatter(raw)
	if claudeMemoryVetoed(fm) {
		return ClaudeMemoryEntry{}, false
	}
	desc := fm["description"]
	body := firstClaudeMemoryLine(bodyText)
	if desc == "" && body == "" {
		return ClaudeMemoryEntry{}, false
	}
	return ClaudeMemoryEntry{
		Path:        full,
		File:        de.Name(),
		Dir:         memDir,
		Name:        fm["name"],
		Description: desc,
		Type:        fm["type"],
		Body:        body,
		ModTime:     mtime,
	}, true
}

func parseClaudeMemoryIndexFile(full, memDir string, de os.DirEntry, loadedFiles map[string]bool) []ClaudeMemoryEntry {
	raw, mtime, ok := readClaudeMemoryBytes(full, de)
	if !ok {
		return nil
	}
	fm, bodyText := splitClaudeFrontmatter(raw)
	if claudeMemoryVetoed(fm) {
		return nil
	}
	if desc := fm["description"]; desc != "" {
		return []ClaudeMemoryEntry{{
			Path:        full,
			File:        de.Name(),
			Dir:         memDir,
			Name:        fm["name"],
			Description: desc,
			Type:        fm["type"],
			Body:        firstClaudeMemoryLine(bodyText),
			ModTime:     mtime,
		}}
	}
	var out []ClaudeMemoryEntry
	for line := range strings.SplitSeq(bodyText, "\n") {
		cleaned, target := cleanClaudeMemoryIndexLine(line)
		if cleaned == "" {
			continue
		}
		if target != "" && loadedFiles[strings.ToLower(filepath.Base(target))] {
			continue
		}
		out = append(out, ClaudeMemoryEntry{
			Path:    full,
			File:    de.Name(),
			Dir:     memDir,
			Body:    cleaned,
			ModTime: mtime,
		})
	}
	return out
}

func readClaudeMemoryBytes(full string, de os.DirEntry) (string, time.Time, bool) {
	f, err := os.Open(full)
	if err != nil {
		return "", time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	var mtime time.Time
	if info, infoErr := de.Info(); infoErr == nil {
		mtime = info.ModTime()
	}
	b, err := io.ReadAll(io.LimitReader(f, claudeMemoryFileMaxBytes))
	if err != nil || len(b) == 0 {
		return "", time.Time{}, false
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), mtime, true
}

func splitClaudeFrontmatter(text string) (map[string]string, string) {
	fm := map[string]string{}
	trimmed := strings.TrimLeft(text, " \t\n")
	if !strings.HasPrefix(trimmed, "---\n") {
		return fm, text
	}
	rest := trimmed[len("---\n"):]
	header, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return fm, text
	}
	if _, restAfter, hasNL := strings.Cut(after, "\n"); hasNL {
		after = restAfter
	} else {
		after = ""
	}
	for line := range strings.SplitSeq(header, "\n") {
		k, v, hasColon := strings.Cut(line, ":")
		if !hasColon {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if k != "" && v != "" {
			fm[k] = v
		}
	}
	return fm, after
}

func claudeMemoryVetoed(fm map[string]string) bool {
	switch strings.ToLower(fm["deja"]) {
	case "false", "ignore", "off", "no", "0":
		return true
	}
	for _, key := range []string{"state", "status"} {
		switch strings.ToLower(fm[key]) {
		case "rejected", "superseded", "stale", "ignored":
			return true
		}
	}
	return false
}

func firstClaudeMemoryLine(body string) string {
	inFence := false
	for line := range strings.SplitSeq(body, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "```") {
			inFence = !inFence
			continue
		}
		if inFence || s == "" || strings.HasPrefix(s, "#") || s == "---" || strings.HasPrefix(s, "<!--") {
			continue
		}
		s = trimMarkdownBullet(s)
		if s == "" {
			continue
		}
		return strings.Join(strings.Fields(s), " ")
	}
	return ""
}

func cleanClaudeMemoryIndexLine(line string) (text, linkTarget string) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") || s == "---" || strings.HasPrefix(s, "<!--") {
		return "", ""
	}
	s = trimMarkdownBullet(s)
	if strings.HasPrefix(s, "[") {
		if titlePart, afterBracket, hasClose := strings.Cut(s[1:], "]("); hasClose && titlePart != "" {
			if targetPart, tailPart, hasParen := strings.Cut(afterBracket, ")"); hasParen {
				title := strings.TrimSpace(titlePart)
				target := strings.TrimSpace(targetPart)
				tail := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(tailPart), "-–—: "))
				if tail != "" {
					return strings.Join(strings.Fields(tail), " "), target
				}
				if title != "" && !strings.HasSuffix(strings.ToLower(target), ".md") {
					return strings.Join(strings.Fields(title), " "), target
				}
				return "", target
			}
		}
	}
	return strings.Join(strings.Fields(s), " "), ""
}

func trimMarkdownBullet(s string) string {
	for _, prefix := range []string{"- ", "* ", "+ "} {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return s
}
