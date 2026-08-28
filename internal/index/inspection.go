package index

import "strings"

// InspectionCommand reports whether the command only looks at state rather than
// changing it — the class whose reuse-count says nothing worth an injection.
func InspectionCommand(cmd string) bool {
	for _, part := range commandParts(cmd) {
		if !inspectionOne(part) {
			return false
		}
	}
	return true
}

// commandParts splits a shell line into the commands it runs. A line that
// changes something is not an inspection because it also ran `cd` first, and
// `cd /tmp && git grep x` was reaching the classifier as `cd`.
func commandParts(cmd string) []string {
	fields := strings.FieldsFunc(cmd, func(r rune) bool { return r == ';' || r == '\n' })
	var out []string
	for _, f := range fields {
		for _, part := range strings.Split(f, "&&") {
			for _, part := range strings.Split(part, "||") {
				if p := strings.TrimSpace(part); p != "" {
					out = append(out, p)
				}
			}
		}
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

func inspectionOne(cmd string) bool {
	f := strings.Fields(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cmd), "$ ")))
	if len(f) == 0 {
		return true
	}
	f = dropShellKeywords(f)
	if len(f) == 0 {
		return true
	}
	// `cd` decides nothing on its own; what follows it does, and that is a
	// separate part.
	if f[0] == "cd" {
		return true
	}
	if f[0] == "git" {
		// git's own options come before the subcommand: `git -C dir status`
		// read as the subcommand `-C` and fell through as a change (#2163).
		f = append([]string{"git"}, dropGitGlobals(f[1:])...)
	}
	switch f[0] {
	case "ls", "cat", "pwd", "echo", "which", "whoami", "date", "env", "printenv",
		"head", "tail", "less", "more", "stat", "file", "find", "tree", "df", "du",
		"ps", "top", "htop", "id", "uname", "hostname", "clear", "history",
		// Waiting is not doing: a poll loop around a read is still a read,
		// and `sleep` left one part of it unclassified, which made the whole
		// line count as a change.
		"sleep", "wait", "true", "false", "printf":
		return true
	case "git":
		if len(f) > 1 {
			switch f[1] {
			case "stash", "worktree", "tag", "remote":
				// Read or write depending on the argument: `git stash list`
				// looks, `git stash push` changes; `git worktree list` looks,
				// `git worktree add` changes. Listed unconditionally before,
				// which read every one of them as a look.
				return len(f) > 2 && gitReadArg(f[2])
			case "status", "diff", "log", "show", "branch",
				"blame", "reflog", "describe", "rev-parse", "ls-files",
				// The rest of git's read side. `ls-files` was here and
				// `ls-tree` was not, so half of the same class fell through.
				"ls-tree", "grep", "cat-file", "show-ref", "for-each-ref",
				"shortlog", "whatchanged", "rev-list", "merge-base", "config":
				return true
			}
		}
	}
	return false
}

func hasFlag(fields []string, flag string) bool {
	for _, f := range fields[1:] {
		if f == flag || strings.HasPrefix(f, flag+"=") {
			return true
		}
	}
	return false
}

// investigationCommand is the wider question the fix miner asks: could this
// command have been the remedy at all? Reading and searching cannot be, and
// neither can running the suite — that is how a fix is checked, not what fixes
// anything. Kept apart from InspectionCommand deliberately: that one decides
// whether "you have run this before" is worth saying, and a `go test` an agent
// runs in every session is worth saying nothing about while still being a
// perfectly ordinary thing to do.
//
// Reading the pairs a real store mined, the commands stored as the remedy for
// `undefined: X` were overwhelmingly a grep for X — the rule that keeps a pair
// (the command names what the error named) is satisfied by construction there.
func investigationCommand(cmd string) bool {
	for _, part := range commandParts(cmd) {
		if !investigationOne(part) {
			return false
		}
	}
	return true
}

func investigationOne(cmd string) bool {
	if inspectionOne(cmd) {
		return true
	}
	f := dropShellKeywords(strings.Fields(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cmd), "$ "))))
	if len(f) == 0 {
		return true
	}
	if f[0] == "git" {
		f = append([]string{"git"}, dropGitGlobals(f[1:])...)
	}
	switch f[0] {
	case "grep", "rg", "ag", "ack", "awk", "wc", "diff", "jq", "yq", "column",
		"sort", "uniq", "basename", "dirname", "realpath", "readlink", "gofmt":
		return true
	case "gh":
		// Going to look at CI after a failure is the same move as grepping
		// for the symbol: it is where an agent goes next, not what it did
		// about it.
		if len(f) > 2 {
			switch f[2] {
			case "checks", "view", "list", "status", "diff":
				return true
			}
		}
	case "sed":
		// `sed -n '10,20p' f` prints; `sed -i …` edits.
		return !hasFlag(f, "-i") && !hasFlag(f, "--in-place")
	case "go":
		if len(f) > 1 {
			switch f[1] {
			case "test", "vet", "list", "doc", "version", "env":
				return true
			}
		}
	}
	return false
}

// dropGitGlobals removes the options git accepts before its subcommand, so the
// subcommand is where the classifier looks.
func dropGitGlobals(rest []string) []string {
	for len(rest) > 0 {
		switch {
		case rest[0] == "-C" || rest[0] == "-c":
			if len(rest) < 2 {
				return nil
			}
			rest = rest[2:]
		case strings.HasPrefix(rest[0], "--git-dir=") || strings.HasPrefix(rest[0], "--work-tree="),
			rest[0] == "--no-pager", rest[0] == "--paginate", rest[0] == "--no-replace-objects":
			rest = rest[1:]
		default:
			return rest
		}
	}
	return nil
}

// dropShellKeywords removes the words a shell puts in front of the command
// itself. `until gh pr checks …; do sleep 20; done` was classified as the
// command `until`.
func dropShellKeywords(f []string) []string {
	for len(f) > 0 {
		switch f[0] {
		case "until", "while", "if", "then", "else", "elif", "do", "done", "fi", "time", "!":
			f = f[1:]
		default:
			return f
		}
	}
	return nil
}

// gitReadArg is the argument that makes an argument-dependent git subcommand a
// read: `list` and `show` for stash and worktree, the list flags for tag and
// remote.
func gitReadArg(a string) bool {
	switch a {
	case "list", "show", "-l", "--list", "-v", "--verbose":
		return true
	}
	return false
}
