package index

import (
	"path/filepath"
	"strings"
)

// shellOnlySigs are the signatures of text that only the shell's complaint
// about the command line carries: `(eval):1: == not found` after an `echo
// ===`, `zsh: parse error near ')'`, a program the shell could not find that
// this command does not run. Any command can trip one, so two failures that
// share it are not the same failure; on a real replay one such zsh error,
// seen in 125 sessions, gave every hint that had nothing to do with its
// failure. Empty when the command is unknown: then nothing can be compared.
func shellOnlySigs(text, command string, sigs map[uint64]bool) map[uint64]bool {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	shell := map[uint64]bool{}
	other := map[uint64]bool{}
	for raw := range strings.SplitSeq(text, "\n") {
		line, ok := FrictionLine(raw)
		if !ok {
			continue
		}
		h := frictionHash(line)
		if !sigs[h] {
			continue
		}
		if shellComplaint(raw, line, command) {
			shell[h] = true
		} else {
			other[h] = true
		}
	}
	for h := range other {
		delete(shell, h)
	}
	return shell
}

// shellComplaint reports a line the shell printed about the command line
// rather than an error a program printed. A missing program the command runs
// is that program's failure, and the same for every command that runs it.
func shellComplaint(raw, line, command string) bool {
	if !shellSpoke(trimEnvelopeLabel(strings.TrimSpace(raw))) {
		return false
	}
	if prog := missingProgram(line); prog != "" && runsProgram(command, prog) {
		return false
	}
	return true
}

// shellSpoke reports a line that opens with a shell's name or position
// marker: `zsh:1:`, `(eval):2:`, `bash: line 3:`, `zsh: `.
func shellSpoke(l string) bool {
	if shellErrorPrefix(l) {
		return true
	}
	name, _, ok := strings.Cut(l, ": ")
	if !ok {
		return false
	}
	switch strings.ToLower(name) {
	case "zsh", "bash", "sh", "dash", "ksh", "fish", "(eval)", "(anon)", "-bash", "-zsh":
		return true
	}
	return false
}

// runsProgram reports whether command names prog as a word: `timeout 30 go
// vet` runs timeout though the work is go's.
func runsProgram(command, prog string) bool {
	for _, w := range strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '|' || r == '&' || r == '(' || r == ')' || r == '`'
	}) {
		if filepath.Base(strings.Trim(w, `'"`)) == prog {
			return true
		}
	}
	return false
}

// sameFailingCommand reports whether command is the one p's error came from:
// the pair's failed command (or, for a pair that kept none, its remedy) is
// this one or this one corrected. Leading `cd` parts are dropped from both,
// so the checkout a command ran in decides nothing.
func sameFailingCommand(p FixPair, command string) bool {
	ref := p.Failed
	if ref == "" {
		ref = p.Command
	}
	a, b := withoutLeadingCd(ref), withoutLeadingCd(command)
	if a == "" || b == "" {
		return false
	}
	return a == b || repairedVariant(a, b)
}

func withoutLeadingCd(cmd string) string {
	parts := commandParts(withoutExitStatus(strings.TrimPrefix(strings.TrimSpace(cmd), "$ ")))
	for len(parts) > 0 && commandProgram(commandTokens(parts[0])) == "cd" {
		parts = parts[1:]
	}
	return strings.Join(parts, " && ")
}

// without is sigs less drop.
func without(sigs, drop map[uint64]bool) map[uint64]bool {
	out := make(map[uint64]bool, len(sigs))
	for h := range sigs {
		if !drop[h] {
			out[h] = true
		}
	}
	return out
}
