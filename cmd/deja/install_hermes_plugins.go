package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// setHermesPluginEnabled adds or removes deja from plugins.enabled, touching
// only that one list entry so the rest of the file — comments included —
// stays byte-identical.
func setHermesPluginEnabled(on bool) error {
	path := filepath.Join(sources.HermesHome(), "config.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) && !on {
		return nil
	}
	// readConfig, for the byte order mark: read with it, a `plugins:` on the
	// first line was not found and a second block was appended after it.
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	s := lfText(old)
	blockAdded := blockWasAdded(path, "plugins")
	e := hermesPluginsEdit{on: on, keyAdded: blockAdded || blockWasAdded(path, "plugins.enabled"), blockAdded: blockAdded}
	if !on {
		if names := blocksAddedWithPrefix(path, hermesNullBlock); len(names) > 0 {
			e.nullWas = strings.TrimPrefix(names[0], hermesNullBlock)
		}
	}
	next, how := e.apply(s)
	switch {
	case how == hermesPluginsUnreadable && on:
		return fmt.Errorf("%s: plugins.enabled is not written in a shape deja can edit safely; add deja to it by hand", path)
	case how == hermesPluginsUnreadable, !on && how == hermesPluginsMissing, next == s && how != hermesPluginsMissing:
		return nil
	case !on:
		s = next
		forgetBlockAdded(path, "plugins.enabled")
		forgetBlockAdded(path, "plugins")
		if e.nullWas != "" {
			forgetBlockAdded(path, hermesNullBlock+e.nullWas)
		}
	case how == hermesPluginsMissing:
		s = appendHermesPluginsBlock(s)
		noteBlockAdded(path, "plugins")
	default:
		s = next
		if how == hermesPluginsKeyAdded {
			noteBlockAdded(path, "plugins.enabled")
		}
		if e.nullWas != "" {
			noteBlockAdded(path, hermesNullBlock+e.nullWas)
		}
	}
	_, err = writeIfChanged(path, old, []byte(s))
	return err
}

// hermesPluginDisabled reports whether config.yaml lists deja under
// plugins.disabled, in a block list or a flow one.
func hermesPluginDisabled() bool {
	b, err := readConfig(filepath.Join(sources.HermesHome(), "config.yaml"))
	if err != nil {
		return false
	}
	in, list, child := false, false, -1
	for _, line := range strings.Split(lfText(b), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(line)
		if w == 0 {
			in, list, child = yamlKeyLine(line, "plugins:"), false, -1
			continue
		}
		if !in {
			continue
		}
		if child < 0 {
			child = w
		}
		if list && (w > child || w == child && strings.HasPrefix(t, "-")) {
			if strings.HasPrefix(t, "-") && hermesYAMLScalar(t[1:]) == "deja" {
				return true
			}
			continue
		}
		list = false
		key, rest, ok := hermesKeyLine(line)
		if !ok || w != child || hermesKeyName(key) != "disabled" {
			continue
		}
		v := strings.TrimSpace(stripYAMLComment(rest))
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			for _, item := range strings.Split(v[1:len(v)-1], ",") {
				if hermesYAMLScalar(item) == "deja" {
					return true
				}
			}
			continue
		}
		list = v == ""
	}
	return false
}

// hermesNullBlock records the null an `enabled: ~` held before deja listed
// itself under the key, with the space before it, so uninstall puts back the
// very bytes.
const hermesNullBlock = "plugins.enabled="

const (
	hermesPluginsEdited = iota
	hermesPluginsMissing
	hermesPluginsKeyAdded
	hermesPluginsUnreadable
)

// appendHermesPluginsBlock adds deja's own plugins block at the end of the
// document: before a `...` that ends it, since what follows one is not the
// config Hermes reads.
func appendHermesPluginsBlock(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == "..." {
			lines = slices.Insert(lines, i, "", "plugins:", "  enabled:", "    - deja")
			return strings.Join(lines, "\n")
		}
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s + "\nplugins:\n  enabled:\n    - deja\n"
}

// hermesPluginsEdit puts deja into plugins.enabled, or takes it out, in
// whichever style the list is written. Matching only the block form sent a
// flow list — `enabled: [spotify]`, which Hermes' own docs use — to the branch
// that appends a whole second `plugins:`, and YAML keeps the last key, so the
// user's plugins went off (#4243). An `enabled: []` was rewritten as a block
// key, which uninstall then left as null (#4249). A flow list stays a flow
// list, and taking deja out gives back the bytes that were there.
//
// keyAdded says deja wrote the `enabled:` key itself and takes it away with
// its last entry; blockAdded, that it wrote the whole `plugins:` block, which
// goes too once nothing else is in it. Taking the block back whole whatever
// the reader had added to it since hung their `- spotify` off the line above,
// or left their `disabled:` under no parent at all. nullWas is the null token
// install took off the key, put back by uninstall.
//
// A shape deja cannot list itself under is refused rather than guessed at: a
// `plugins:` holding a list or a scalar rather than keys, a mapping or a
// scalar under `enabled:`, a flow list over several lines, a key spelled
// quoted or with a space before its colon, and a second `plugins:` or
// `enabled:` — Hermes reads the last one.
type hermesPluginsEdit struct {
	on, keyAdded, blockAdded bool
	nullWas                  string
}

func (e *hermesPluginsEdit) apply(s string) (string, int) {
	lines := strings.Split(s, "\n")
	p := -1
	for i, line := range lines {
		if yamlIndentWidth(line) != 0 {
			continue
		}
		key, rest, ok := hermesKeyLine(line)
		if !ok || hermesKeyName(key) != "plugins" {
			continue
		}
		if key != "plugins" || strings.TrimSpace(stripYAMLComment(rest)) != "" || p >= 0 {
			return s, hermesPluginsUnreadable
		}
		p = i
	}
	if p < 0 {
		return s, hermesPluginsMissing
	}
	end := len(lines)
	child := ""
	for i := p + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(lines[i])
		if child == "" && (strings.HasPrefix(t, "-") || w > 0 && !hermesIsKeyLine(lines[i])) {
			// A list or a scalar where the keys go: an `enabled:` put in
			// there makes the file one Hermes cannot load.
			return s, hermesPluginsUnreadable
		}
		if w == 0 {
			end = i
			break
		}
		if child == "" {
			child = lines[i][:w]
		}
	}
	if child == "" {
		child = "  "
	}
	k := -1
	val := ""
	for i := p + 1; i < end; i++ {
		key, rest, ok := hermesKeyLine(lines[i])
		if !ok || lines[i][:yamlIndentWidth(lines[i])] != child || hermesKeyName(key) != "enabled" {
			continue
		}
		if key != "enabled" || k >= 0 {
			return s, hermesPluginsUnreadable
		}
		k, val = i, strings.TrimSpace(stripYAMLComment(rest))
	}
	if k < 0 {
		if !e.on {
			return s, hermesPluginsEdited
		}
		lines = slices.Insert(lines, p+1, child+"enabled:", child+child+"- deja")
		return strings.Join(lines, "\n"), hermesPluginsKeyAdded
	}
	switch val {
	case "", "null", "Null", "NULL", "~":
	default:
		if !strings.HasPrefix(val, "[") {
			return s, hermesPluginsUnreadable
		}
		return e.flow(s, lines, k)
	}
	last, mine, others := k, -1, 0
	for i := k + 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		w := yamlIndentWidth(lines[i])
		if w < len(child) || w == len(child) && !strings.HasPrefix(t, "-") {
			break
		}
		if t != "-" && !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "-\t") {
			// A mapping or a scalar under the key, or an item running on
			// past its line: nothing a `- deja` can be put beside.
			return s, hermesPluginsUnreadable
		}
		last = i
		if hermesYAMLScalar(t[1:]) == "deja" {
			mine = i
		} else {
			others++
		}
	}
	if val != "" && last != k {
		return s, hermesPluginsUnreadable
	}
	// The key line split after its colon, for the null that goes or comes back.
	at := strings.Index(lines[k], "enabled:") + len("enabled:")
	head, rest := lines[k][:at], lines[k][at:]
	switch {
	case e.on && mine >= 0, !e.on && mine < 0:
		return s, hermesPluginsEdited
	case e.on:
		if val != "" {
			// `null` and `~` are the empty value a bare key is; the key
			// takes the list the same way, and the token is kept for
			// uninstall to put back.
			e.nullWas = rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))+len(val)]
			lines[k] = head + rest[len(e.nullWas):]
		}
		indent := child + child
		if last != k {
			indent = lines[last][:yamlIndentWidth(lines[last])]
		}
		lines = slices.Insert(lines, last+1, indent+"- deja")
	case others > 0 || !e.keyAdded:
		lines = slices.Delete(lines, mine, mine+1)
		if others == 0 && e.nullWas != "" && strings.TrimSpace(stripYAMLComment(rest)) == "" {
			lines[k] = head + e.nullWas + rest
		}
	case e.blockAdded && hermesBlockHoldsOnly(lines, p, end, k, mine):
		from := p
		if from > 0 && strings.TrimSpace(lines[from-1]) == "" {
			from--
		}
		lines = slices.Delete(lines, from, mine+1)
	default:
		lines = slices.Delete(lines, mine, mine+1)
		lines = slices.Delete(lines, k, k+1)
	}
	return strings.Join(lines, "\n"), hermesPluginsEdited
}

// hermesBlockHoldsOnly reports whether the plugins block from p to end has
// nothing in it but the `enabled:` key at k and deja's entry at mine — the
// block exactly as deja wrote it.
func hermesBlockHoldsOnly(lines []string, p, end, k, mine int) bool {
	for i := p + 1; i < end; i++ {
		if i != k && i != mine && strings.TrimSpace(lines[i]) != "" {
			return false
		}
	}
	return mine == k+1
}

// flow does the edit on a flow list, `enabled: [a, b]`, inside its own
// brackets: deja goes in after the last item, and taking it out takes the
// comma and space that came with it, so `[ spotify ]` comes back as it was.
// Brackets, commas and comments are found outside quotes, so an item like
// 'x]y' does not end the list; a nested list or mapping is refused.
func (e *hermesPluginsEdit) flow(s string, lines []string, k int) (string, int) {
	line := lines[k]
	open := strings.Index(line, "[")
	type span struct{ from, to int }
	var items []span
	shut, from, quote := -1, open+1, byte(0)
	add := func(to int) {
		f, t := from, to
		for f < t && (line[f] == ' ' || line[f] == '\t') {
			f++
		}
		for t > f && (line[t-1] == ' ' || line[t-1] == '\t') {
			t--
		}
		if f < t {
			items = append(items, span{f, t})
		}
	}
	for i := open + 1; i < len(line) && shut < 0; i++ {
		switch c := line[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[' || c == '{' || c == '}':
			return s, hermesPluginsUnreadable
		case c == ',':
			add(i)
			from = i + 1
		case c == ']':
			add(i)
			shut = i
		}
	}
	if shut < 0 || strings.TrimSpace(stripYAMLComment(line[shut+1:])) != "" {
		return s, hermesPluginsUnreadable
	}
	mine := slices.IndexFunc(items, func(sp span) bool { return hermesYAMLScalar(line[sp.from:sp.to]) == "deja" })
	switch {
	case e.on && mine >= 0, !e.on && mine < 0:
		return s, hermesPluginsEdited
	case e.on && len(items) == 0:
		lines[k] = line[:shut] + "deja" + line[shut:]
	case e.on:
		at := items[len(items)-1].to
		lines[k] = line[:at] + ", deja" + line[at:]
	case len(items) == 1:
		lines[k] = line[:items[0].from] + line[items[0].to:]
	case mine == 0:
		lines[k] = line[:items[0].from] + line[items[1].from:]
	default:
		lines[k] = line[:items[mine-1].to] + line[items[mine].to:]
	}
	return strings.Join(lines, "\n"), hermesPluginsEdited
}

// hermesKeyLine splits a `key: value` line at the colon that ends the key —
// outside quotes, and followed by a space, a tab or the end of the line.
func hermesKeyLine(line string) (key, rest string, ok bool) {
	t := strings.TrimLeft(line, " \t")
	if t == "" || t[0] == '-' || t[0] == '#' {
		return "", "", false
	}
	quote := byte(0)
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ':' && (i+1 == len(t) || t[i+1] == ' ' || t[i+1] == '\t'):
			return t[:i], t[i+1:], true
		}
	}
	return "", "", false
}

func hermesIsKeyLine(line string) bool {
	_, _, ok := hermesKeyLine(line)
	return ok
}

// hermesKeyName is the key as YAML reads it, quotes and padding off.
func hermesKeyName(key string) string {
	return strings.Trim(strings.TrimSpace(key), `"'`)
}

// stripYAMLComment cuts a trailing comment: a # at the start or after a space
// or a tab, outside quotes.
func stripYAMLComment(v string) string {
	quote := byte(0)
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return v[:i]
		}
	}
	return v
}

// hermesYAMLScalar is a list item as a name: no comment, no quotes.
func hermesYAMLScalar(v string) string {
	return strings.Trim(strings.TrimSpace(stripYAMLComment(v)), `"'`)
}
