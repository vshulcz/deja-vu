package index

import (
	"path/filepath"
	"strings"
)

// VerifyCommand reports a command that checks the work: a build, a suite, a
// linter, a type check. The rest of what a session runs — a kill, a copy, a
// commit, a curl — passed just as truly and answers a question nobody asked at
// the moment a file is opened.
//
// Positive rather than an exclusion list, because the exclusion has no end: on
// a real store the commands that pass in two sessions are mostly incidental,
// and one wrong line at every action is worse than no line at all. A chain is
// allowed where every part of it either checks something or only looks (`cd`,
// `echo`, `git status`), and at least one part checks.
func VerifyCommand(cmd string) bool {
	checks := false
	for part := range strings.SplitSeq(strings.ReplaceAll(strings.ReplaceAll(cmd, "&&", ";"), "||", ";"), ";") {
		part = strings.TrimSpace(part)
		if part == "" || verifySetupSegment(part) {
			continue
		}
		if hasEnvWrapperCommand(part) {
			if !verifyProgram(part) {
				return false
			}
			checks = true
			continue
		}
		if InspectionCommand(part) {
			continue
		}
		if !verifyProgram(part) {
			return false
		}
		checks = true
	}
	return checks
}

func hasEnvWrapperCommand(part string) bool {
	f := verifyProgramFields(part)
	if len(f) == 0 || strings.ToLower(filepath.Base(f[0])) != "env" {
		return false
	}
	return len(skipEnvCommandPrefix(f)) > 0
}

func skipEnvCommandPrefix(f []string) []string {
	i := 1
	for i < len(f) {
		tok := f[i]
		if tok == "--" {
			i++
			break
		}
		if tok == "-u" || tok == "--unset" || tok == "-C" || tok == "--chdir" {
			i += 2
			continue
		}
		if strings.HasPrefix(tok, "-") {
			i++
			continue
		}
		if verifyEnvAssignment(tok) {
			rest := stripEnvAssignments(f[i:])
			i = len(f) - len(rest)
			continue
		}
		break
	}
	return f[min(i, len(f)):]
}

// verifySetupSegment matches environment activation segments that prepare a
// verify command without counting as verification on their own.
func verifySetupSegment(part string) bool {
	f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(part), "$ "))
	if len(f) < 2 {
		return false
	}
	switch f[0] {
	case "export":
		return len(stripEnvAssignments(f[1:])) == 0
	case "source", ".":
		target := strings.Trim(f[1], "\"'")
		return len(f) == 2 && strings.HasPrefix(filepath.Base(target), "activate")
	}
	return false
}

// verifyProgram is one part of a chain: the program it runs, after the
// environment a caller sets in front of it — `SVC_FIXTURES=./fixtures make
// test` is a suite, and the assignment is not the program.
func verifyProgram(part string) bool {
	f, fromRunner := unwrapVerifyCommand(verifyProgramFields(part))
	if len(f) == 0 {
		return false
	}
	prog := strings.ToLower(filepath.Base(f[0]))
	if fromRunner {
		if idx := strings.IndexByte(prog, '@'); idx > 0 {
			prog = prog[:idx]
		}
	}
	if base := strings.TrimSuffix(prog, ".sh"); base == "verify" || strings.HasPrefix(base, "verify-") || strings.HasPrefix(base, "verify_") {
		return true
	}
	if isPythonVerifyProg(prog) {
		for _, a := range f[1:] {
			switch a {
			case "pytest", "unittest", "mypy", "ruff", "flake8", "pyright", "tox", "nox":
				return true
			}
		}
		return false
	}
	switch prog {
	case "make", "just", "task", "mage", "bazel", "buck", "ninja", "cmake", "ctest",
		"cargo", "gradle", "gradlew", "mvn", "dotnet", "swift", "zig", "rustc",
		"pytest", "tox", "nox", "rspec", "rake", "mix", "jest", "vitest",
		"tsc", "eslint", "biome", "ruff", "mypy", "pyright", "flake8",
		"golangci-lint", "staticcheck", "govulncheck", "shellcheck", "actionlint",
		"clang-tidy", "cppcheck", "phpunit":
		return true
	case "go":
		if len(f) < 2 {
			return false
		}
		switch f[1] {
		case "test", "build", "vet":
			return true
		}
		return false
	case "npm", "pnpm", "yarn", "bun", "deno":
		// A package manager runs anything; what it is running decides.
		for _, a := range f[1:] {
			switch a {
			case "test", "build", "lint", "typecheck", "check", "tsc", "vitest", "jest", "biome", "verify":
				return true
			}
		}
		return false
	}
	return false
}

func isPythonVerifyProg(prog string) bool {
	if prog == "python" || prog == "python3" {
		return true
	}
	rest, ok := strings.CutPrefix(prog, "python3.")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var (
	runnerValueFlags = map[string]bool{
		"-p": true, "--python": true,
		"--with": true, "--with-editable": true, "--with-requirements": true,
		"--from": true, "--directory": true, "--project": true, "--package": true,
		"--group": true, "--only-group": true, "--extra": true,
		"--python-platform": true, "--index": true, "--index-url": true,
		"--extra-index-url": true, "--env-file": true, "--cwd": true, "-C": true,
	}
	jsRunnerValueFlags = map[string]bool{
		"-p": true, "--package": true,
	}
)

func unwrapVerifyCommand(f []string) ([]string, bool) {
	fromRunner := false
	for len(f) > 0 {
		f = stripEnvAssignments(f)
		if len(f) == 0 {
			return nil, fromRunner
		}
		prog := strings.ToLower(filepath.Base(f[0]))
		switch prog {
		case "env":
			f = skipEnvCommandPrefix(f)
		case "timeout", "gtimeout":
			i := 1
			for i < len(f) {
				tok := f[i]
				if tok == "--" {
					i++
					break
				}
				if tok == "-s" || tok == "--signal" || tok == "-k" || tok == "--kill-after" {
					i += 2
					continue
				}
				if strings.HasPrefix(tok, "-") {
					i++
					continue
				}
				break
			}
			// Consume the mandatory DURATION token before the wrapped command.
			if i >= len(f) || strings.HasPrefix(f[i], "-") {
				return f, fromRunner
			}
			i++
			f = f[min(i, len(f)):]
		case "uv", "poetry", "pipenv", "pipx":
			i := 1
			for i < len(f) {
				tok := f[i]
				if tok == "--directory" || tok == "--project" || tok == "-C" {
					i += 2
					continue
				}
				if strings.HasPrefix(tok, "-") {
					i++
					continue
				}
				break
			}
			if i >= len(f) || f[i] != "run" {
				return f, fromRunner
			}
			i = skipWrapperFlags(f, i+1, runnerValueFlags)
			f = f[min(i, len(f)):]
		case "uvx":
			fromRunner = true
			i := skipWrapperFlags(f, 1, runnerValueFlags)
			f = f[min(i, len(f)):]
		case "npx", "bunx", "pnpx":
			fromRunner = true
			i := skipWrapperFlags(f, 1, jsRunnerValueFlags)
			f = f[min(i, len(f)):]
		default:
			return f, fromRunner
		}
	}
	return f, fromRunner
}

func skipWrapperFlags(f []string, i int, valueFlags map[string]bool) int {
	for i < len(f) {
		tok := f[i]
		if tok == "--" {
			return i + 1
		}
		if valueFlags[tok] {
			i += 2
			continue
		}
		if strings.HasPrefix(tok, "-") {
			i++
			continue
		}
		break
	}
	return min(i, len(f))
}

// verifyProgramFields is the command's own words, with the environment a caller
// set in front of it dropped. A quoted value holds spaces, so one assignment is
// more than one field: reading only the first field of
// `GOFLAGS_EXTRA="-tags golden" make test` left `golden"` as the program.
func verifyProgramFields(part string) []string {
	f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(part), "$ "))
	return stripEnvAssignments(f)
}

func stripEnvAssignments(f []string) []string {
	for len(f) > 0 && verifyEnvAssignment(f[0]) {
		open := verifyUnbalancedQuote(f[0])
		f = f[1:]
		for open != 0 && len(f) > 0 {
			if strings.ContainsRune(f[0], open) {
				open = 0
			}
			f = f[1:]
		}
	}
	return f
}

// verifyEnvAssignment reports a leading NAME=… , which is environment rather than a
// program. A flag that happens to carry an `=` is not one.
func verifyEnvAssignment(field string) bool {
	i := strings.Index(field, "=")
	if i <= 0 {
		return false
	}
	for j, r := range field[:i] {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && j > 0:
		default:
			return false
		}
	}
	return true
}

// verifyUnbalancedQuote is the quote character an assignment opened and did not
// close, or 0.
func verifyUnbalancedQuote(field string) rune {
	for _, q := range []rune{'"', '\''} {
		if n := strings.Count(field, string(q)); n%2 == 1 {
			return q
		}
	}
	return 0
}
