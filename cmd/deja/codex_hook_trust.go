package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	taken := map[codexHookPos]bool{}
	moves := map[codexHookPos]*codexHookPos{}
	for _, p := range sortedHookPositions(was) {
		moves[p] = nil
		for _, q := range sortedHookPositions(now) {
			if q.event == p.event && !taken[q] && now[q] == was[p] {
				taken[q] = true
				q := q
				moves[p] = &q
				break
			}
		}
	}
	return moves
}

func sortedHookPositions(m map[codexHookPos]string) []codexHookPos {
	out := make([]codexHookPos, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && hookPosLess(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func hookPosLess(a, b codexHookPos) bool {
	if a.event != b.event {
		return a.event < b.event
	}
	if a.group != b.group {
		return a.group < b.group
	}
	return a.hook < b.hook
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

// rewriteCodexHookTrust re-keys or drops each pin for the given hooks file and
// takes out a [hooks.state] header nothing is left under. Everything else in
// the file is passed through as it was.
func rewriteCodexHookTrust(cfg string, paths map[string]bool, moves map[codexHookPos]*codexHookPos) string {
	lines := strings.SplitAfter(cfg, "\n")
	type table struct {
		start, end int // lines[start:end], header first
		header     string
	}
	var tables []table
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			if len(tables) > 0 {
				tables[len(tables)-1].end = i
			}
			tables = append(tables, table{start: i, end: len(lines), header: strings.TrimSpace(line)})
		}
	}
	drop := map[int]bool{}
	rename := map[int]string{}
	for ti, t := range tables {
		key, ok := codexTrustKey(t.header)
		if !ok {
			continue
		}
		pos, file, ok := parseCodexTrustKey(key)
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
			eol := lines[t.start][len(strings.TrimRight(lines[t.start], "\r\n")):]
			rename[t.start] = `[hooks.state.` + strconv.Quote(nk) + `]` + eol
		}
	}
	if len(drop) == 0 && len(rename) == 0 {
		return cfg
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
	var b strings.Builder
	skip := map[int]bool{}
	// A table runs to the next header, so the blank line that separates it
	// from the next table goes with it. Tables at the end of the file have no
	// next one: there the blank codex put in front of the first of them goes.
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
	for i, line := range lines {
		if skip[i] {
			continue
		}
		if r, ok := rename[i]; ok {
			line = r
		}
		b.WriteString(line)
	}
	out := b.String()
	// What was dropped from the end leaves the file ending where its last
	// table ended, with the newline it had.
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

// codexTrustKey is the quoted key of a [hooks.state."…"] header.
func codexTrustKey(header string) (string, bool) {
	header = tomlCode(header)
	if !strings.HasPrefix(header, `[hooks.state."`) || !strings.HasSuffix(header, `"]`) {
		return "", false
	}
	key, err := strconv.Unquote(header[len(`[hooks.state.`) : len(header)-1])
	return key, err == nil
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
