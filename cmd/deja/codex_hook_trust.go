package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Codex pins every hook a user approves in its config.toml, one table per hook
// keyed by where the hook sits: [hooks.state."<hooks.json>:<event>:<i>:<j>"]
// with a trusted_hash under it. An uninstall that takes deja's hooks out of
// hooks.json left those pins behind, so the file did not come back as it was,
// and a later install that put the same entries in the same places started
// trusted without codex showing them to anyone (#4183).
//
// Taking an entry out also moves the reader's own hooks up, and their pins are
// keyed by the old position. So each pin follows its hook: a pin whose hook is
// still there is re-keyed to where it is now, and a pin whose hook is gone is
// dropped.

// codexHookPos is one hook's place in hooks.json.
type codexHookPos struct {
	event string // as codex writes it in a key: session_start
	group int
	hook  int
}

// codexHookPositions lists every hook in a parsed hooks.json with the text
// that identifies it: the group's matcher and the hook itself.
func codexHookPositions(root map[string]any) map[codexHookPos]string {
	out := map[codexHookPos]string{}
	hooks, _ := root["hooks"].(map[string]any)
	for event, groupsAny := range hooks {
		groups, _ := groupsAny.([]any)
		for g, groupAny := range groups {
			group, _ := groupAny.(map[string]any)
			matcher, _ := json.Marshal(group["matcher"])
			list, _ := group["hooks"].([]any)
			for h, hook := range list {
				b, _ := json.Marshal(hook)
				out[codexHookPos{codexEventKey(event), g, h}] = string(matcher) + "\x00" + string(b)
			}
		}
	}
	return out
}

// codexTrustMoves maps each old position to where the same hook sits now, or
// to nothing when it is gone. Equal hooks are matched in order, so two copies
// of one entry keep their order.
func codexTrustMoves(before, after map[string]any) map[codexHookPos]*codexHookPos {
	was, now := codexHookPositions(before), codexHookPositions(after)
	// Free positions per event and text, in order: one pass over each side.
	free := map[string][]codexHookPos{}
	for _, q := range sortedHookPositions(now) {
		k := q.event + "\x00" + now[q]
		free[k] = append(free[k], q)
	}
	moves := map[codexHookPos]*codexHookPos{}
	for _, p := range sortedHookPositions(was) {
		k := p.event + "\x00" + was[p]
		if qs := free[k]; len(qs) > 0 {
			q := qs[0]
			free[k] = qs[1:]
			moves[p] = &q
			continue
		}
		moves[p] = nil
	}
	return moves
}

func sortedHookPositions(m map[codexHookPos]string) []codexHookPos {
	out := make([]codexHookPos, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.event != b.event {
			return a.event < b.event
		}
		if a.group != b.group {
			return a.group < b.group
		}
		return a.hook < b.hook
	})
	return out
}

// moveCodexHookTrust rewrites the pins in codex's config.toml for the hooks
// file at hooksPath after it went from before to after.
func moveCodexHookTrust(hooksPath string, before, after map[string]any) error {
	cfgPath := filepath.Join(sources.CodexHome(), "config.toml")
	old, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}
	next := rewriteCodexHookTrust(string(old), codexHookPathSpellings(hooksPath), codexTrustMoves(before, after))
	if next == string(old) {
		return nil
	}
	if next == "" {
		// Nothing but codex's pins was in it. writeIfChanged takes an empty
		// result to mean the file should go, and this file is codex's.
		return os.WriteFile(cfgPath, nil, 0o600)
	}
	_, err = writeIfChanged(cfgPath, old, []byte(next))
	return err
}

// codexHookPathSpellings is the hooks file as a pin may name it: codex keys by
// the path it loaded, which on macOS can carry /private where HOME does not.
func codexHookPathSpellings(p string) map[string]bool {
	out := map[string]bool{p: true}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		out[filepath.Join(r, filepath.Base(p))] = true
	}
	return out
}

// trustTable is one table of the config: its lines, and for a pin the key and
// where the quoted key sits in the header line.
type trustTable struct {
	start, end   int // lines[start:end], header first
	header       string
	key          string
	keyAt, keyTo int  // byte offsets of the quoted key in lines[start]
	literal      bool // written as a TOML literal string, 'like this'
	pin          bool
}

// rewriteCodexHookTrust re-keys or drops each pin for the given hooks file and
// takes out a [hooks.state] header nothing is left under. Everything else in
// the file — comments included — is passed through as it was.
func rewriteCodexHookTrust(cfg string, paths map[string]bool, moves map[codexHookPos]*codexHookPos) string {
	lines := strings.SplitAfter(cfg, "\n")
	var tables []trustTable
	inString := ""
	for i, line := range lines {
		// A line inside a multi-line string is text, whatever it starts
		// with: reading `[hooks.state."…"]` in a prompt as a header cut the
		// file from there on.
		opened := inString
		inString = multilineStringAfter(line, inString)
		if opened != "" || !strings.HasPrefix(strings.TrimSpace(line), "[") {
			continue
		}
		if len(tables) > 0 {
			tables[len(tables)-1].end = i
		}
		t := trustTable{start: i, end: len(lines), header: strings.TrimSpace(tomlCode(line))}
		t.key, t.keyAt, t.keyTo, t.literal, t.pin = codexTrustKey(line)
		tables = append(tables, t)
	}
	// A comment written just above a header belongs to that header's table,
	// not to the table that ends there, and one at the end of the file belongs
	// to the file: dropping a pin must take neither.
	for ti := range tables {
		t := &tables[ti]
		end := t.end
		for end > t.start+1 {
			l := strings.TrimSpace(lines[end-1])
			if l == "" || strings.HasPrefix(l, "#") {
				end--
				continue
			}
			break
		}
		// end is past the last line of content; keep the blanks right after it
		// with this table, and leave the comment run (and any blanks inside
		// it) in front of the next header.
		firstComment := -1
		for i := end; i < t.end; i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
				firstComment = i
				break
			}
		}
		if firstComment >= 0 {
			t.end = firstComment
		}
	}

	drop := map[int]bool{}
	rename := map[int]string{}
	targets := map[string]bool{}
	for ti, t := range tables {
		if !t.pin {
			continue
		}
		pos, file, ok := parseCodexTrustKey(t.key)
		if !ok || !paths[file] {
			continue
		}
		to, known := moves[pos]
		if !known {
			continue
		}
		if to == nil {
			drop[ti] = true
			continue
		}
		if *to != pos {
			nk := file + ":" + to.event + ":" + strconv.Itoa(to.group) + ":" + strconv.Itoa(to.hook)
			rename[ti] = nk
			targets[nk] = true
		}
	}
	if len(drop) == 0 && len(rename) == 0 {
		return cfg
	}
	// A pin already sitting where another is moving to — a stale one codex
	// never cleared — would make the key appear twice, and codex refuses a
	// config with a duplicate table.
	for ti, t := range tables {
		if t.pin && targets[t.key] && !drop[ti] {
			if _, moving := rename[ti]; !moving {
				drop[ti] = true
			}
		}
	}
	// A [hooks.state] header with nothing of its own and no pin left under it
	// was written by codex for the pins just taken out.
	children := 0
	for ti, t := range tables {
		if strings.HasPrefix(t.header, "[hooks.state.") && !drop[ti] {
			children++
		}
	}
	if children == 0 {
		for ti, t := range tables {
			if t.header == "[hooks.state]" && blankLines(lines[t.start+1:t.end]) {
				drop[ti] = true
			}
		}
	}

	skip := map[int]bool{}
	// Tables at the end of the file have no next one whose leading blank line
	// they would take: there the blank codex put in front of the first of
	// them goes.
	lastKept := -1
	for ti := range tables {
		if !drop[ti] {
			lastKept = ti
		}
	}
	for ti, t := range tables {
		if !drop[ti] {
			continue
		}
		for i := t.start; i < t.end; i++ {
			skip[i] = true
		}
		if ti == lastKept+1 && t.start > 0 && strings.TrimSpace(lines[t.start-1]) == "" {
			skip[t.start-1] = true
		}
	}
	var b strings.Builder
	renameAt := map[int]int{}
	for ti := range rename {
		renameAt[tables[ti].start] = ti
	}
	for i, line := range lines {
		if skip[i] {
			continue
		}
		if ti, ok := renameAt[i]; ok {
			t := tables[ti]
			line = line[:t.keyAt] + quoteTOMLKey(rename[ti], t.literal) + line[t.keyTo:]
		}
		b.WriteString(line)
	}
	out := b.String()
	if strings.HasSuffix(cfg, "\n") && !strings.HasSuffix(out, "\n") && out != "" {
		out += "\n"
	}
	return out
}

func blankLines(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

// codexTrustKey reads the key of a [hooks.state.<key>] header line, in either
// TOML string form: codex writes "…" where it can and '…' for a path with a
// backslash in it, which is every path on Windows.
func codexTrustKey(line string) (key string, at, to int, literal, ok bool) {
	i := strings.Index(line, "[")
	rest := line[i+1:]
	trimmed := strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(trimmed, "hooks.state.") {
		return "", 0, 0, false, false
	}
	at = i + 1 + (len(rest) - len(trimmed)) + len("hooks.state.")
	if at >= len(line) {
		return "", 0, 0, false, false
	}
	switch line[at] {
	case '\'':
		end := strings.IndexByte(line[at+1:], '\'')
		if end < 0 {
			return "", 0, 0, false, false
		}
		to = at + 1 + end + 1
		key, literal = line[at+1:to-1], true
	case '"':
		end := endOfJSONString([]byte(line), at)
		if end < 0 {
			return "", 0, 0, false, false
		}
		to = end
		k, err := strconv.Unquote(line[at:to])
		if err != nil {
			return "", 0, 0, false, false
		}
		key = k
	default:
		return "", 0, 0, false, false
	}
	if !strings.HasPrefix(strings.TrimSpace(line[to:]), "]") {
		return "", 0, 0, false, false
	}
	return key, at, to, literal, true
}

// quoteTOMLKey writes a key back in the form it was read in.
func quoteTOMLKey(key string, literal bool) string {
	if literal && !strings.ContainsAny(key, "'\n\r") {
		return "'" + key + "'"
	}
	// A TOML basic string: Go's quoting writes \x7f, which TOML has no
	// escape for.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range key {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// multilineStringAfter says which multi-line string delimiter, if any, is
// still open at the end of line, given the one open at its start.
func multilineStringAfter(line, open string) string {
	for i := 0; i < len(line); {
		if open != "" {
			j := strings.Index(line[i:], open)
			if j < 0 {
				return open
			}
			i += j + 3
			open = ""
			continue
		}
		switch {
		case strings.HasPrefix(line[i:], `"""`):
			open = `"""`
			i += 3
		case strings.HasPrefix(line[i:], "'''"):
			open = "'''"
			i += 3
		case line[i] == '#':
			return ""
		case line[i] == '"':
			// A one-line basic string: skip it whole, escapes and all.
			end := endOfJSONString([]byte(line), i)
			if end < 0 {
				return ""
			}
			i = end
		case line[i] == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return ""
			}
			i += end + 2
		default:
			i++
		}
	}
	return open
}

// parseCodexTrustKey splits "<file>:<event>:<i>:<j>" from the right, so a path
// with a colon in it — C:\ on Windows — stays whole.
func parseCodexTrustKey(key string) (codexHookPos, string, bool) {
	parts := strings.Split(key, ":")
	if len(parts) < 4 {
		return codexHookPos{}, "", false
	}
	n := len(parts)
	g, err1 := strconv.Atoi(parts[n-2])
	h, err2 := strconv.Atoi(parts[n-1])
	if err1 != nil || err2 != nil {
		return codexHookPos{}, "", false
	}
	return codexHookPos{parts[n-3], g, h}, strings.Join(parts[:n-3], ":"), true
}
