package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
	for _, root := range claudeAutoMemoryRoots() {
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

func claudeAutoMemoryRoots() []string {
	var roots []string
	add := func(r string) {
		if r == "" {
			return
		}
		r = filepath.Clean(r)
		if !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	for _, r := range ClaudeRoots() {
		add(r)
	}
	claudePinned := os.Getenv("DEJA_CLAUDE_ROOT") != ""
	if !claudePinned || os.Getenv("DEJA_CHERRYSTUDIO_ROOTS") != "" {
		for _, r := range CherryStudioRoots() {
			add(r)
		}
	}
	if !claudePinned || os.Getenv("DEJA_QODER_ROOT") != "" {
		add(filepath.Join(QoderRoot(), "projects"))
	}
	return roots
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
	if de == nil {
		return readClaudeMemoryFile(full)
	}
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
	if err != nil || len(b) == 0 || bytes.IndexByte(b, 0) >= 0 {
		return "", time.Time{}, false
	}
	text := strings.ToValidUTF8(string(b), "\uFFFD")
	return strings.ReplaceAll(text, "\r\n", "\n"), mtime, true
}

func readClaudeMemoryFile(full string) (string, time.Time, bool) {
	f, err := os.Open(full)
	if err != nil {
		return "", time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return "", time.Time{}, false
	}
	b, err := io.ReadAll(io.LimitReader(f, claudeMemoryFileMaxBytes))
	if err != nil || len(b) == 0 || bytes.IndexByte(b, 0) >= 0 {
		return "", time.Time{}, false
	}
	text := strings.ToValidUTF8(string(b), "\uFFFD")
	return strings.ReplaceAll(text, "\r\n", "\n"), fi.ModTime(), true
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
	inMetadata := false
	for line := range strings.SplitSeq(header, "\n") {
		indented := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
		k, v, hasColon := strings.Cut(line, ":")
		if !hasColon {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if !indented {
			inMetadata = k == "metadata" && v == ""
		}
		if k != "" && v != "" {
			if !indented || (inMetadata && fm[k] == "") {
				fm[k] = v
			}
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

// IsClaudeAutoMemoryFile reports whether p is a markdown auto-memory note under
// a Claude Code project directory (<root>/<encoded-project>/memory/*.md).
func IsClaudeAutoMemoryFile(p string) bool {
	clean := filepath.Clean(p)
	if clean == "." || !strings.EqualFold(filepath.Ext(clean), ".md") {
		return false
	}
	if !strings.EqualFold(filepath.Base(filepath.Dir(clean)), "memory") {
		return false
	}
	return UnderClaudeRoot(clean)
}

// ClaudeAutoMemoryFiles lists markdown notes under Claude Code's per-project
// auto-memory directories (<root>/<encoded-project>/memory/*.md).
func ClaudeAutoMemoryFiles() []string {
	var out []string
	seen := map[string]bool{}
	for _, root := range claudeAutoMemoryRoots() {
		projEntries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, pe := range projEntries {
			if !pe.IsDir() && pe.Type()&os.ModeSymlink == 0 {
				continue
			}
			memDir := filepath.Join(root, pe.Name(), "memory")
			memEntries, err := os.ReadDir(memDir)
			if err != nil {
				continue
			}
			for _, me := range memEntries {
				if me.IsDir() || !strings.EqualFold(filepath.Ext(me.Name()), ".md") {
					continue
				}
				if me.Type()&os.ModeSymlink != 0 {
					if fi, err := os.Stat(filepath.Join(memDir, me.Name())); err != nil || fi.IsDir() {
						continue
					}
				}
				full := filepath.Clean(filepath.Join(memDir, me.Name()))
				if seen[full] {
					continue
				}
				seen[full] = true
				out = append(out, full)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ParseClaudeAutoMemoryFile parses one Claude Code auto-memory markdown file
// with its full redacted body intact so multi-paragraph notes survive transcript
// cleanup and remain searchable in deja.
func ParseClaudeAutoMemoryFile(path string) ([]model.Session, error) {
	raw, mtime, ok := readClaudeMemoryFile(path)
	if !ok {
		return nil, nil
	}
	fm, bodyText := splitClaudeFrontmatter(raw)
	if claudeMemoryVetoed(fm) {
		return nil, nil
	}

	cleanPath := filepath.Clean(path)
	name := strings.TrimSpace(fm["name"])
	desc := strings.TrimSpace(fm["description"])
	memType := strings.TrimSpace(fm["type"])

	var body string
	if strings.EqualFold(filepath.Base(cleanPath), "MEMORY.md") {
		filtered, hasContent := claudeMemoryIndexBody(filepath.Dir(cleanPath), bodyText)
		if !hasContent && name == "" && desc == "" {
			return nil, nil
		}
		body = filtered
	} else {
		body = strings.TrimSpace(bodyText)
		if body == "" && name == "" && desc == "" {
			return nil, nil
		}
	}

	var parts []string
	if name != "" {
		parts = append(parts, name)
	}
	if desc != "" {
		parts = append(parts, desc)
	}
	if memType != "" {
		parts = append(parts, "type: "+memType)
	}
	if body != "" {
		parts = append(parts, body)
	}
	if len(parts) == 0 {
		return nil, nil
	}
	redacted, _ := redact.Text(strings.Join(parts, "\n\n"))
	redacted = strings.TrimSpace(strings.ToValidUTF8(redacted, "\uFFFD"))
	if redacted == "" {
		return nil, nil
	}

	title := desc
	if name != "" {
		switch {
		case title == "":
			title = name
		case !strings.Contains(strings.ToLower(title), strings.ToLower(name)):
			title = name + " — " + title
		}
	}
	if title == "" {
		title = firstClaudeMemoryLine(body)
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(cleanPath), filepath.Ext(cleanPath))
	}
	title, _ = redact.Text(title)
	title = strings.TrimSpace(strings.ToValidUTF8(title, "\uFFFD"))
	if title == "" {
		title = filepath.Base(cleanPath)
	}

	projDir := filepath.Dir(cleanPath)
	if strings.EqualFold(filepath.Base(projDir), "memory") {
		projDir = filepath.Dir(projDir)
	}
	proj := strings.ToValidUTF8(claudeProjectName(projDir), "\uFFFD")
	if proj == "" {
		proj = "claude"
	}

	sum := sha256.Sum256([]byte(cleanPath))
	return []model.Session{{
		ID:      "claude-memory-" + hex.EncodeToString(sum[:8]),
		Harness: "claude",
		Project: proj,
		Path:    cleanPath,
		Title:   title,
		Started: mtime,
		Updated: mtime,
		Messages: []model.Message{{
			Role: "assistant",
			Text: redacted,
			Time: mtime,
		}},
	}}, nil
}

func claudeMemoryIndexBody(memDir, body string) (string, bool) {
	var kept []string
	hasContent := false
	inFence := false
	for line := range strings.SplitSeq(body, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "```") {
			inFence = !inFence
			kept = append(kept, line)
			continue
		}
		if inFence {
			if s != "" {
				hasContent = true
			}
			kept = append(kept, line)
			continue
		}
		if s == "" || strings.HasPrefix(s, "#") || s == "---" || strings.HasPrefix(s, "<!--") {
			kept = append(kept, line)
			continue
		}
		cleaned, target := cleanClaudeMemoryIndexLine(line)
		if target != "" && strings.HasSuffix(strings.ToLower(target), ".md") {
			if _, err := os.Stat(filepath.Join(memDir, filepath.Base(target))); err == nil {
				continue
			}
			if cleaned == "" {
				continue
			}
			kept = append(kept, cleaned)
			hasContent = true
			continue
		}
		if target != "" && cleaned == "" {
			continue
		}
		kept = append(kept, line)
		hasContent = true
	}
	if !hasContent {
		return "", false
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), true
}
