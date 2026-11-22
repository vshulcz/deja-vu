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
	var out []string
	queue := []string{strings.TrimSpace(cmd)}
	for len(queue) > 0 {
		cur := strings.TrimSpace(queue[0])
		queue = queue[1:]
		if cur == "" {
			continue
		}
		parts := splitShellSeparators(cur)
		if len(parts) > 1 {
			queue = append(parts, queue...)
			continue
		}
		if inner, ok := unwrapShellGroup(cur); ok {
			queue = append([]string{inner}, queue...)
			continue
		}
		out = append(out, cur)
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

func splitShellSeparators(cmd string) []string {
	return splitShellSeparatorsDepth(cmd, true)
}

func splitShellSeparatorsDepth(cmd string, respectGroups bool) []string {
	var out []string
	var inQuote byte
	escaped := false
	parenDepth := 0
	braceDepth := 0
	start := 0
	flush := func(end int) {
		if seg := strings.TrimSpace(cmd[start:end]); seg != "" {
			out = append(out, seg)
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inQuote != '\'' {
			escaped = true
			continue
		}
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			inQuote = c
		case '(':
			if respectGroups {
				parenDepth++
			}
		case ')':
			if respectGroups && parenDepth > 0 {
				parenDepth--
			}
		case '{':
			if respectGroups && shellBraceStartsGroup(cmd, start, i) {
				braceDepth++
			}
		case '}':
			if respectGroups && braceDepth > 0 {
				braceDepth--
			}
		case '\n', ';':
			if parenDepth == 0 && braceDepth == 0 {
				flush(i)
				start = i + 1
			}
		case '&':
			if parenDepth == 0 && braceDepth == 0 && i+1 < len(cmd) && cmd[i+1] == '&' {
				flush(i)
				i++
				start = i + 1
			}
		case '|':
			if parenDepth == 0 && braceDepth == 0 {
				flush(i)
				if i+1 < len(cmd) && cmd[i+1] == '|' {
					i++
				}
				start = i + 1
			}
		}
	}
	if respectGroups && (parenDepth != 0 || braceDepth != 0) {
		return splitShellSeparatorsDepth(cmd, false)
	}
	flush(len(cmd))
	return out
}

func shellBraceStartsGroup(cmd string, start, i int) bool {
	if i+1 >= len(cmd) {
		return false
	}
	if cmd[i+1] != ' ' && cmd[i+1] != '\t' && cmd[i+1] != '\n' {
		return false
	}
	allSpace := true
	for j := start; j < i; j++ {
		if cmd[j] != ' ' && cmd[j] != '\t' {
			allSpace = false
			break
		}
	}
	if allSpace {
		return true
	}
	if i > 0 {
		switch cmd[i-1] {
		case ' ', '\t', '\n', ';', '(', '|', '&':
			return true
		}
	}
	return false
}

func unwrapShellGroup(cmd string) (string, bool) {
	if len(cmd) < 2 {
		return "", false
	}
	open := cmd[0]
	var closeCh byte
	switch open {
	case '(':
		closeCh = ')'
	case '{':
		// Brace groups `{ cmd; }` require whitespace/newline after `{`; `{a,b}` is brace expansion.
		if cmd[1] != ' ' && cmd[1] != '\t' && cmd[1] != '\n' {
			return "", false
		}
		closeCh = '}'
	default:
		return "", false
	}
	if cmd[len(cmd)-1] != closeCh {
		return "", false
	}
	var inQuote byte
	escaped := false
	depth := 0
	for i := range len(cmd) {
		c := cmd[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inQuote != '\'' {
			escaped = true
			continue
		}
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			inQuote = c
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				if i != len(cmd)-1 {
					return "", false
				}
				inner := strings.TrimSpace(cmd[1 : len(cmd)-1])
				return inner, inner != ""
			}
		}
	}
	return "", false
}

func commandFields(cmd string) (fields []string, hadEnvOnly bool) {
	f := strings.Fields(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cmd), "$ ")))
	f = dropShellKeywords(f)
	if len(f) == 0 {
		return nil, false
	}
	stripped := f
	for len(stripped) > 0 && strings.Contains(stripped[0], "=") && !strings.HasPrefix(stripped[0], "=") {
		stripped = stripped[1:]
	}
	if len(stripped) == 0 {
		return nil, true
	}
	stripped = dropShellKeywords(stripped)
	if len(stripped) == 0 {
		return nil, true
	}
	if stripped[0] == "git" {
		// git's own options come before the subcommand: `git -C dir status`
		// read as the subcommand `-C` and fell through as a change (#2163).
		stripped = append([]string{"git"}, dropGitGlobals(stripped[1:])...)
	}
	return stripped, false
}

func inspectionOne(cmd string) bool {
	f, envOnly := commandFields(cmd)
	if len(f) == 0 {
		return !envOnly
	}
	// `cd` decides nothing on its own; what follows it does, and that is a
	// separate part.
	if f[0] == "cd" {
		return true
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
	f, envOnly := commandFields(cmd)
	if len(f) == 0 {
		return !envOnly
	}
	switch f[0] {
	case "grep", "rg", "ag", "ack", "awk", "wc", "diff", "jq", "yq", "column",
		"sort", "uniq", "basename", "dirname", "realpath", "readlink", "gofmt":
		return true
	case "gh":
		// Going to look at CI after a failure is the same move as grepping
		// for the symbol: it is where an agent goes next, not what it did
		// about it.
		if len(f) > 1 {
			switch f[1] {
			case "search":
				return true
			case "api":
				return ghAPIReadOnly(f[2:])
			}
		}
		if len(f) > 2 {
			switch f[2] {
			case "checks", "view", "list", "status", "diff", "watch":
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

// ViewerCommand reports whether every segment of cmd is a file, search, diff,
// or listing viewer whose stdout carries searched or inspected content rather
// than a build or test failure of the command itself.
func ViewerCommand(cmd string) bool {
	for _, part := range commandParts(cmd) {
		if !viewerOne(part) {
			return false
		}
	}
	return true
}

func viewerOne(cmd string) bool {
	f, envOnly := commandFields(cmd)
	if len(f) == 0 {
		return !envOnly
	}
	switch f[0] {
	case "cd", "ls", "cat", "head", "tail", "less", "more", "find", "tree",
		"grep", "rg", "ag", "ack", "awk", "wc", "diff", "jq", "yq", "column",
		"sort", "uniq":
		return true
	case "sed":
		return !hasFlag(f, "-i") && !hasFlag(f, "--in-place")
	case "git":
		if len(f) > 1 {
			switch f[1] {
			case "status", "diff", "log", "show", "blame", "reflog", "ls-files", "ls-tree", "grep", "cat-file", "shortlog":
				return true
			}
		}
	case "gh":
		if len(f) > 1 {
			switch f[1] {
			case "search":
				return true
			case "api":
				return ghAPIReadOnly(f[2:])
			}
		}
		if len(f) > 2 {
			switch f[2] {
			case "view", "list", "status", "diff":
				return true
			}
		}
	}
	return false
}

func ghAPIReadOnly(args []string) bool {
	method := ""
	hasPayloadFlag := false
	for i := 0; i < len(args); i++ {
		a := strings.Trim(args[i], "\"'")
		switch {
		case a == "-x" && i+1 < len(args):
			method = strings.Trim(args[i+1], "\"'")
			i++
		case a == "--method" && i+1 < len(args):
			method = strings.Trim(args[i+1], "\"'")
			i++
		case strings.HasPrefix(a, "-x") && len(a) > 2:
			method = strings.TrimPrefix(a, "-x")
		case strings.HasPrefix(a, "--method="):
			method = strings.TrimPrefix(a, "--method=")
		case a == "-f" || a == "--raw-field" || a == "--field" || a == "--input" ||
			strings.HasPrefix(a, "-f=") || strings.HasPrefix(a, "--field=") ||
			strings.HasPrefix(a, "--raw-field=") || strings.HasPrefix(a, "--input="):
			// gh api defaults to POST when fields or --input are supplied without -X GET.
			hasPayloadFlag = true
		}
	}
	if method != "" {
		return method == "get"
	}
	return !hasPayloadFlag
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
