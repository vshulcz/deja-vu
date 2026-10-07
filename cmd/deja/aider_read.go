package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// aider takes its read list from exactly one place. configargparse adds a
// config file's read: only when no --read is on the command line yet, and it
// goes through them in the order command line, AIDER_READ, --config, the
// .aider.conf.yml in the working directory, the one at the git root, the one in
// home. A project with its own read: list therefore hides the home one, and
// with it deja's context file and the rules block in it (aider 0.86.2,
// configargparse 1.7.1 parse_known_args). AIDER_READ is no way round it: it
// replaces the lists too.
//
// So `deja aider` works out which list aider would take and hands it over on
// the command line with deja's file added.

// aiderReadArgs is what to append to aider's arguments so that it reads file
// alongside the read list it would otherwise take, or nil when it already does.
// Entries naming deja's shared context file are dropped when file is another
// one: that file stands in for it.
func aiderReadArgs(args []string, cwd, file string) []string {
	items, fromCLI := aiderSetting(args, cwd, "read", os.Getenv("AIDER_READ"))
	ctx := aiderContextPath()
	var keep []string
	for _, it := range items {
		p := aiderPath(it, cwd)
		if samePath(p, file) {
			return nil
		}
		if samePath(p, ctx) {
			continue
		}
		keep = append(keep, it)
	}
	var out []string
	if !fromCLI {
		for _, it := range keep {
			out = append(out, "--read", it)
		}
	}
	return append(out, "--read", file)
}

// aiderChatHistory is the chat history file the aider started with args writes
// to.
func aiderChatHistory(args []string, cwd string) string {
	if v, _ := aiderSetting(args, cwd, "chat-history-file", os.Getenv("AIDER_CHAT_HISTORY_FILE")); len(v) > 0 {
		return aiderPath(v[len(v)-1], cwd)
	}
	if root := aiderGitRoot(args, cwd); root != "" {
		return filepath.Join(root, ".aider.chat.history.md")
	}
	return filepath.Join(cwd, ".aider.chat.history.md")
}

// aiderSetting is the value aider would take for key, in its precedence order,
// and whether it came from the command line. env is the key's AIDER_ variable.
func aiderSetting(args []string, cwd, key, env string) ([]string, bool) {
	if v, ok := aiderCLIValues(args, "--"+key); ok {
		return v, true
	}
	if env != "" {
		return aiderEnvList(env), false
	}
	for _, conf := range aiderConfigFiles(args, cwd) {
		b, err := os.ReadFile(conf)
		if err != nil {
			continue
		}
		if v, ok := aiderConfValues(lfText(b), key); ok {
			return v, false
		}
	}
	return nil, false
}

// aiderConfigFiles are the config files aider reads, the one that wins first.
func aiderConfigFiles(args []string, cwd string) []string {
	var files []string
	if v, ok := aiderCLIValues(args, "--config"); ok {
		files = append(files, aiderPath(v[len(v)-1], cwd))
	} else if v, ok := aiderCLIValues(args, "-c"); ok {
		files = append(files, aiderPath(v[len(v)-1], cwd))
	}
	files = append(files, filepath.Join(cwd, ".aider.conf.yml"))
	if root := aiderGitRoot(args, cwd); root != "" && root != cwd {
		files = append(files, filepath.Join(root, ".aider.conf.yml"))
	}
	return append(files, aiderConfPath())
}

// aiderGitRoot is the git root aider would find, or "" when it would use none.
func aiderGitRoot(args []string, cwd string) string {
	if slices.Contains(args, "--no-git") {
		return ""
	}
	if v := strings.ToLower(os.Getenv("AIDER_GIT")); v == "false" || v == "0" || v == "no" {
		return ""
	}
	return gitRootOf(filepath.Join(cwd, "x"))
}

// aiderCLIValues collects every value given for flag, as `flag v` or
// `flag=v`. Nothing after `--` is a flag.
func aiderCLIValues(args []string, flag string) ([]string, bool) {
	var out []string
	found := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
			found = true
			i++
		} else if v, ok := strings.CutPrefix(a, flag+"="); ok {
			out = append(out, v)
			found = true
		}
	}
	return out, found
}

// aiderEnvList reads an AIDER_ list value the way configargparse does: JSON
// when it parses, else the legacy [a, b] form, else one value.
func aiderEnvList(v string) []string {
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return []string{v}
	}
	var items []string
	if json.Unmarshal([]byte(v), &items) == nil {
		return items
	}
	for _, it := range strings.Split(v[1:len(v)-1], ",") {
		items = append(items, strings.TrimSpace(it))
	}
	return items
}

// aiderConfValues is what a top-level key holds in an LF YAML config: a block
// list, a flow list or one scalar. ok is false when the key is absent or holds
// nothing, which leaves it to the next config.
func aiderConfValues(s, key string) ([]string, bool) {
	lines := strings.Split(s, "\n")
	// PyYAML keeps the last of a key written twice.
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimRight(lines[i], " \t"), key+":")
		if !ok || rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		v := strings.TrimSpace(rest)
		var items []string
		if v != "" && !strings.HasPrefix(v, "#") {
			// A comment after a flow list is not part of it.
			if end := strings.LastIndex(v, "]"); strings.HasPrefix(v, "[") && end > 0 {
				if rest := strings.TrimSpace(v[end+1:]); rest == "" || strings.HasPrefix(rest, "#") {
					v = v[:end+1]
				}
			}
			items, _ = aiderReadItems(v)
		} else {
			for _, next := range lines[i+1:] {
				t := strings.TrimSpace(next)
				if t == "" || strings.HasPrefix(t, "#") {
					continue
				}
				item, ok := strings.CutPrefix(t, "- ")
				if !ok {
					break
				}
				items = append(items, item)
			}
		}
		var out []string
		for _, it := range items {
			if it = yamlScalar(strings.TrimSpace(it)); it != "" {
				out = append(out, it)
			}
		}
		return out, len(out) > 0
	}
	return nil, false
}

// aiderPath resolves a path the way aider does: ~ expanded, relative to the
// directory aider runs in.
func aiderPath(p, cwd string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(homeDir(), strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// samePath reports that a and b name one file, links resolved: aider resolves
// every read path before it opens it.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}
