package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// aider has neither an MCP client nor hooks, but read-only files are re-read
// from disk on every message rather than pasted once — so a file deja keeps
// current is live context for the whole session.
//
// Refreshing it is what needs a trigger. aider's own --load can run a command
// at startup, but /run stops on "Add 0.0k tokens of command output to the
// chat?" even when the output is empty, so an installer cannot use it without
// costing a keystroke per session. `deja aider` does the refresh instead and
// hands the arguments straight to aider.
func aiderContextPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "deja", "aider-context.md")
}

func aiderConfPath() string {
	return filepath.Join(homeDir(), ".aider.conf.yml")
}

func installAider(_ string, uninstall bool) (installResult, error) {
	if _, err := os.UserHomeDir(); err != nil {
		return installResult{}, err
	}
	path := aiderConfPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	// LF while it is edited: a CRLF file never matched the block-list search,
	// so install wrote a second read: key and aider, which keeps the last,
	// dropped the reader's files (#4330). writeIfChanged puts the endings back.
	was := aiderReadWas(path)
	next, _ := removeAiderReadEntry(lfText(old), was)
	if !uninstall {
		// The line install turns into a block list, kept so uninstall can put
		// it back as it was (#4331).
		inline := aiderInlineRead(next)
		var aerr error
		if next, aerr = addAiderReadEntry(next, aiderContextPath()); aerr != nil {
			return installResult{}, fmt.Errorf("%s: %w", shortHome(path), aerr)
		}
		if inline != "" {
			// One record per config: the state is stored sorted, so an older
			// line left beside it could be the one uninstall reads.
			if was != "" && was != inline {
				forgetBlockAdded(path, aiderReadWasKey+was)
			}
			noteBlockAdded(path, aiderReadWasKey+inline)
		}
	} else if was != "" {
		forgetBlockAdded(path, aiderReadWasKey+was)
	}
	a, werr := writeIfChanged(path, old, []byte(next))
	if werr != nil {
		return installResult{}, werr
	}
	if uninstall {
		_ = os.Remove(aiderContextPath())
		_ = os.Remove(aiderContextPath() + ".lock")
		return installResult{Path: path, Action: a}, nil
	}
	// Write the file now: aider fails the read outright if it is missing, and
	// the first session should not be the one that discovers that. The
	// placeholder, not a digest: one built here is the history of whatever
	// directory install ran in, and every aider anywhere would read it as its
	// own (#4328).
	if err := writeAiderContext(aiderPlaceholder); err != nil {
		return installResult{}, err
	}
	return installResult{Path: path, Action: a}, nil
}

// aiderReadWasKey prefixes the record of a read: line install promoted to a
// block list: the scalar or flow form the reader wrote, which uninstall puts
// back. Without it the file came back as a block list, and a file that held
// only `read: []` came back empty and was deleted (#4331).
const aiderReadWasKey = "aider-read-was:"

// aiderReadWas is the read: line the last install promoted in this config, or
// "" when it promoted none.
func aiderReadWas(path string) string {
	prefix := blockKey(path, aiderReadWasKey)
	was := ""
	for _, b := range slices.Concat(readWiringState().Blocks, blocksAddedThisRun) {
		if rest, ok := strings.CutPrefix(b, prefix); ok && !blocksForgottenThisRun[b] {
			was = rest
		}
	}
	return was
}

// aiderInlineRead is the top-level `read: <value>` line addAiderReadEntry
// would rewrite as a block list, or "".
func aiderInlineRead(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(line, "read: "); ok && strings.TrimSpace(v) != "" && !strings.HasPrefix(strings.TrimSpace(v), "#") {
			return line
		}
	}
	return ""
}

// addAiderReadEntry keeps whatever list is already under read: — a user with
// their own CONVENTIONS.md there must not lose it. s is LF text.
func addAiderReadEntry(s, ctx string) (string, error) {
	entry := "  - " + ctx + "\n"
	keys := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "read:") {
			keys++
		}
	}
	if keys > 1 {
		return "", fmt.Errorf("read: is written twice at the top level, and aider reads only the last one — keep one and run this again")
	}
	// The scalar form takes a single file; promote it to a list so both survive.
	for _, line := range strings.Split(s, "\n") {
		v, ok := strings.CutPrefix(line, "read: ")
		if !ok || strings.TrimSpace(v) == "" || strings.HasPrefix(strings.TrimSpace(v), "#") {
			continue
		}
		items, ok := aiderReadItems(v)
		if !ok {
			// A shape this cannot take apart safely. Refuse, the way the
			// goose and Continue writers do, rather than rewrite it wrong.
			return "", fmt.Errorf("read: %s is not a form deja can rewrite \u2014 move it to a block list and run this again", strings.TrimSpace(v))
		}
		var b strings.Builder
		b.WriteString("read:\n")
		for _, it := range items {
			b.WriteString("  - " + it + "\n")
		}
		b.WriteString(entry)
		return strings.Replace(s, line+"\n", b.String(), 1), nil
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	// The block form, found the way the other YAML writers find their keys: a
	// comment after the key or under it, and a second read: refused (#4290).
	at, err := yamlTopKeyEnd(s, "read:")
	if err != nil {
		return "", err
	}
	if at < 0 {
		return s + "read:\n" + entry, nil
	}
	// The reader's own indent, which may be none: one list cannot mix two.
	if line := s[at:]; strings.HasPrefix(strings.TrimLeft(line, " "), "- ") {
		entry = line[:len(line)-len(strings.TrimLeft(line, " "))] + "- " + ctx + "\n"
	}
	return s[:at] + entry + s[at:], nil
}

// yamlPlainSafe reports that s reads as the same string unquoted in a block
// list item.
func yamlPlainSafe(s string) bool {
	if s == "" || s != strings.TrimSpace(s) || strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@`") {
		return false
	}
	return !strings.Contains(s, ": ") && !strings.Contains(s, " #") && !strings.HasSuffix(s, ":") && !strings.ContainsAny(s, "\\")
}

// aiderReadItems is what a top-level `read: <v>` line holds, as the block list
// install turns it into: a flow list's items, nothing for a null, or the one
// file a scalar names. ok is false for a value it cannot take apart.
//
// A flow list is not a scalar. Taken as one, `read: [a.md, b.md]` became a
// single entry `- [a.md, b.md]` and aider looked for a file with that name —
// the reader's two files gone from its view, reported as a successful install
// (#3197). Nor is a null, a block scalar, an alias, an anchor, a tag or a
// mapping: promoted, `read: ~` became a null item and `read: |` an item `- |`
// with its text left dangling under deja's entry.
func aiderReadItems(v string) (items []string, ok bool) {
	v = strings.TrimSpace(v)
	if items, isFlow := yamlFlowItems(v); isFlow {
		return items, items != nil
	}
	bare := v
	if i := strings.Index(bare, " #"); i >= 0 {
		bare = strings.TrimSpace(bare[:i])
	}
	switch strings.ToLower(bare) {
	case "~", "null", "''", `""`:
		return []string{}, true
	}
	if v == "" || strings.ContainsAny(v[:1], "|>&*!{") {
		return nil, false
	}
	return []string{v}, true
}

// yamlFlowItems takes apart a YAML flow list \u2014 `[a, b]` \u2014 into its items, and
// says whether the value was one at all. It gives nil items for a flow list
// holding anything but plain scalars: a nested list or mapping, or a quote that
// never closes. The caller refuses those rather than guess.
func yamlFlowItems(v string) ([]string, bool) {
	if !strings.HasPrefix(v, "[") {
		return nil, false
	}
	if !strings.HasSuffix(v, "]") {
		return nil, true
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	if inner == "" {
		return []string{}, true
	}
	var items []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == '[' || c == ']' || c == '{' || c == '}':
			return nil, true
		case c == ',':
			items = append(items, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if quote != 0 {
		return nil, true
	}
	items = append(items, strings.TrimSpace(cur.String()))
	for i, it := range items {
		if it == "" {
			return nil, true
		}
		// A name quoted in the flow list is the same file unquoted, and the
		// block list deja writes takes it plain — when plain reads the same:
		// unquoted, "#a.md" is a comment and 'k: v.md' a mapping, and aider
		// loses the file.
		if len(it) > 1 && (it[0] == '"' || it[0] == '\'') && it[len(it)-1] == it[0] && yamlPlainSafe(it[1:len(it)-1]) {
			items[i] = it[1 : len(it)-1]
		}
	}
	return items, true
}

// aiderConfReadsContext reports whether the top-level read: key of an aider
// config names deja's context file, as a block-list item, a scalar or inside a
// flow list.
func aiderConfReadsContext(s string) bool {
	inRead := false
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, " \t\r")
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' && !strings.HasPrefix(t, "- ") {
			v, ok := strings.CutPrefix(l, "read:")
			inRead = ok
			if ok && strings.Contains(v, "aider-context.md") {
				return true
			}
			continue
		}
		if inRead && strings.HasPrefix(t, "- ") && strings.Contains(t, "aider-context.md") {
			return true
		}
	}
	return false
}

// removeAiderReadEntry takes deja's entry out of an LF config, and puts back
// was — the read: line install promoted — when the list left is exactly what
// that line held. It reports whether it did.
func removeAiderReadEntry(s, was string) (string, bool) {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "- ") && strings.Contains(l, "aider-context.md") {
			continue
		}
		out = append(out, l)
	}
	restored := false
	if was != "" {
		out, restored = restoreAiderRead(out, was)
	}
	// A read: key with nothing under it is not the file we found: drop the key
	// as well, or the next aider start reads a null list. A comment under the
	// key is not an item.
	for i := 0; i < len(out); i++ {
		if yamlIndentWidth(out[i]) != 0 || !yamlKeyLine(out[i], "read:") {
			continue
		}
		j := i + 1
		for j < len(out) && (strings.TrimSpace(out[j]) == "" || strings.HasPrefix(strings.TrimSpace(out[j]), "#")) {
			j++
		}
		if j < len(out) && strings.HasPrefix(strings.TrimSpace(out[j]), "- ") {
			continue
		}
		out = append(out[:i], out[i+1:]...)
		i--
	}
	return strings.Join(out, "\n"), restored
}

// restoreAiderRead swaps the top-level read: block back for the line install
// made it from, when the block holds the same files in the same order. A list
// the reader has changed since stays as they left it.
func restoreAiderRead(lines []string, was string) ([]string, bool) {
	want, ok := aiderReadItems(strings.TrimPrefix(was, "read: "))
	if !ok {
		return lines, false
	}
	for i, l := range lines {
		if l != "read:" {
			continue
		}
		var got []string
		j := i + 1
		for ; j < len(lines); j++ {
			item, ok := strings.CutPrefix(strings.TrimSpace(lines[j]), "- ")
			if !ok || yamlIndentWidth(lines[j]) == 0 && !strings.HasPrefix(lines[j], "- ") {
				break
			}
			got = append(got, item)
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != len(want) {
			return lines, false
		}
		return append(append(append([]string{}, lines[:i]...), was), lines[j:]...), true
	}
	return lines, false
}

// refreshAiderContext regenerates the read-only file from the same digest the
// hooks inject elsewhere, so aider users get what every other harness gets.
func refreshAiderContext(dir string) error {
	return writeAiderContext(aiderContextBody(dir))
}

// aiderPlaceholder is the file when it holds no digest. An empty file still has
// to exist: aider refuses to start when a configured read file is missing.
//
// The line says what to do about it because of where it is read. Only `deja
// aider` fills the file, and only while the aider it started runs. Someone who
// runs plain `aider` therefore sees this text in every session — driven through
// the real interface, "No matching history yet." read as "deja has nothing",
// when what it means is "nothing has refreshed this".
const aiderPlaceholder = "No matching history yet — start aider as `deja aider` and this file fills with what this project already knows.\n"

func aiderContextBody(dir string) string {
	// In-process rather than shelling out to hook-context: the wrapper stands
	// between the user and their editor, and a subprocess here would also make
	// the installer depend on its own binary being runnable.
	digest, sessions, _, _, _, _, _ := cachedHookDigest(dir)
	body := digest
	if sessions > 0 {
		body = frameRecall(startLead(aiderLeadFor(hookCWD(""))) + digest)
	}
	if strings.TrimSpace(body) == "" {
		return aiderPlaceholder
	}
	return body
}

func writeAiderContext(body string) error {
	path := aiderContextPath()
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	_, err = writeIfChanged(path, old, []byte(body))
	return err
}

// aiderLeadFor names the directory the digest was built for. Every aider on
// the machine reads the same file, and a lead saying "this project's" handed
// one project's sessions to another as its own (#4328).
func aiderLeadFor(cwd string) string {
	return "The sessions below are from the recent history of " + cwd + ", where `deja aider` started this aider. " +
		"If the user is working there and any is relevant to what they ask next, say so and use it. " +
		"If it genuinely helps, tell the user in one short line what was recalled; otherwise do not mention it.\n"
}

// cmdAider refreshes the context file and then becomes aider. Everything after
// the subcommand belongs to aider, including flags deja also has.
func cmdAider(dir string, rest []string, sourceInstance string) error {
	// `deja aider` with nothing after it is ambiguous, and the harmless
	// reading is the right default: search for the word rather than start
	// an editor the user did not ask for.
	if len(rest) == 0 {
		return cmdSearch(dir, []string{"aider"}, sourceInstance)
	}
	// aider writes its history at the git root it runs in, where nothing
	// else would look for it: note the project so the next index reads it
	// (#4326).
	if wd, err := os.Getwd(); err == nil {
		root := gitRootOf(filepath.Join(wd, ".aider.chat.history.md"))
		if root == "" {
			root = wd
		}
		if err := sources.RecordAiderProject(root); err != nil {
			fmt.Fprintf(os.Stderr, "deja: could not note this project for indexing: %v\n", err)
		}
	}
	// Every `deja aider` running holds this, so the last one out knows it is
	// the last; taken before the digest is written, so one starting while
	// another restores the placeholder writes after it.
	done, held := holdAiderRun(aiderContextPath() + ".lock")
	body := aiderContextBody(dir)
	if err := writeAiderContext(body); err != nil {
		// A failed recall is not a reason to keep the user out of their editor.
		fmt.Fprintf(os.Stderr, "deja: could not refresh recall: %v\n", err)
	} else if n := aiderRecallCount(); n > 0 {
		// The digest lands silently inside aider's read-only files, so this
		// line is the only thing telling the user memory is in there.
		fmt.Fprintf(os.Stderr, "deja: recalled %d past sessions into aider's read-only context\n", n)
	}
	// The config points every aider on the machine at this one file, so the
	// digest leaves with the aider it was built for: plain aider started next,
	// in any project, reads the placeholder rather than this project's sessions
	// as its own (#4328). Only once no other `deja aider` is running: two in
	// one project write the same digest, and the first out took it from the
	// one still running. Without a lock, only while the file is still ours.
	defer func() {
		restore := func() {
			if b, err := os.ReadFile(aiderContextPath()); err == nil && string(b) != aiderPlaceholder {
				_ = writeAiderContext(aiderPlaceholder)
			}
		}
		if held {
			done(restore)
		} else if b, err := os.ReadFile(aiderContextPath()); err == nil && string(b) == body {
			restore()
		}
	}()
	bin, err := exec.LookPath("aider")
	if err != nil {
		return fmt.Errorf("aider is not on PATH: %w", err)
	}
	// Ctrl-C reaches the whole foreground group and is aider's own key for
	// stopping a reply; left to its default it ended this process instead, and
	// the file kept the digest. A SIGTERM or a closed terminal ended it too,
	// and left aider running with nobody waiting on it: those go on to aider,
	// and this waits for it to exit.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer func() {
		signal.Stop(sig)
		close(sig)
	}()
	cmd := exec.Command(bin, rest...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		for s := range sig {
			if s != os.Interrupt {
				_ = cmd.Process.Signal(s)
			}
		}
	}()
	return cmd.Wait()
}

func aiderRecallCount() int {
	b, err := os.ReadFile(aiderContextPath())
	if err != nil {
		return 0
	}
	return bytes.Count(b, []byte("\n  - Session:"))
}
