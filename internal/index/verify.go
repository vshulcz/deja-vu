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
	for _, part := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(cmd, "&&", ";"), "||", ";"), ";") {
		part = strings.TrimSpace(part)
		if part == "" || InspectionCommand(part) {
			continue
		}
		if !verifyProgram(part) {
			return false
		}
		checks = true
	}
	return checks
}

// verifyProgram is one part of a chain: the program it runs, after the
// environment a caller sets in front of it — `SVC_FIXTURES=./fixtures make
// test` is a suite, and the assignment is not the program.
func verifyProgram(part string) bool {
	f := verifyProgramFields(part)
	if len(f) == 0 {
		return false
	}
	prog := strings.ToLower(filepath.Base(f[0]))
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
			case "test", "build", "lint", "typecheck", "check", "tsc", "vitest", "jest":
				return true
			}
		}
		return false
	case "python", "python3":
		for _, a := range f[1:] {
			if a == "pytest" || a == "unittest" || a == "mypy" {
				return true
			}
		}
		return false
	}
	return false
}

// verifyProgramFields is the command's own words, with the environment a caller
// set in front of it dropped. A quoted value holds spaces, so one assignment is
// more than one field: reading only the first field of
// `GOFLAGS_EXTRA="-tags golden" make test` left `golden"` as the program.
func verifyProgramFields(part string) []string {
	f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(part), "$ "))
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
