package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

const shareBudget = 6 * 1024

func runShare(args []string, w io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("share needs id-prefix")
	}
	s, ok, err := findByPrefix(args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q", args[0])
	}
	printSanitized(w, shareDigest(s, shareBudget))
	return nil
}

func shareDigest(s model.Session, budget int) string {
	if budget <= 0 {
		budget = shareBudget
	}
	var b strings.Builder
	date := "unknown"
	if !s.Updated.IsZero() {
		date = s.Updated.Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "# deja share: %s\n\n", s.ID)
	fmt.Fprintf(&b, "- Project: %s\n", s.Project)
	fmt.Fprintf(&b, "- Harness: %s\n", s.Harness)
	fmt.Fprintf(&b, "- Date: %s\n\n", date)
	appendSection := func(title string, messages []model.Message) {
		if len(messages) == 0 || b.Len() >= budget {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		for _, m := range messages {
			if b.Len() >= budget {
				break
			}
			text := shareMessageText(m.Text)
			if text == "" {
				continue
			}
			chunk := fmt.Sprintf("%s\n\n", text)
			if b.Len()+len(chunk) > budget {
				chunk = utf8SafeCut(chunk, budget-b.Len())
			}
			b.WriteString(chunk)
		}
	}
	var users, assistants []model.Message
	for _, m := range s.Messages {
		if noisyShareMessage(m.Text) {
			continue
		}
		switch m.Role {
		case "user":
			users = append(users, m)
		case "assistant":
			assistants = append(assistants, m)
		}
	}
	appendSection("User problem statement(s)", users)
	appendSection("Key assistant conclusions / code blocks", assistants)
	return strings.TrimSpace(b.String()) + "\n"
}

func printSanitized(w io.Writer, text string) {
	// Redact the whole document at once: multiline secrets (PEM private key
	// blocks) never match when scanned line-by-line.
	redacted, _ := redact.Text(text)
	fmt.Fprint(w, redacted)
	if !strings.HasSuffix(redacted, "\n") {
		fmt.Fprintln(w)
	}
}

func shareMessageText(s string) string {
	s = strings.TrimSpace(stripANSI(s))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	var keep []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || noisyShareMessage(line) || noiseLine(line) || !looksLikeProse(line) {
			continue
		}
		keep = append(keep, line)
		if len(keep) >= 16 {
			break
		}
	}
	return strings.Join(strings.Fields(strings.Join(keep, " ")), " ")
}

var (
	shareLineNumRE = regexp.MustCompile(`^\s*\d{1,6}\s`)            // "1 diff --git", numbered dumps
	shareGrepRE    = regexp.MustCompile(`^\S+\.[a-z]{1,5}:\d+[:)]`) // path/file.go:18: grep output
)

func looksLikeProse(line string) bool {
	// Short lines are kept: dumps are long. The prose gate exists to drop
	// pasted JSON/CLI walls, not three-word problem statements.
	if len(line) < 80 {
		return true
	}
	letters, total, wordish := 0, 0, 0
	for _, f := range strings.Fields(line) {
		hasLetter := false
		for _, r := range f {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80 {
				hasLetter = true
			}
		}
		if hasLetter && len(f) >= 2 {
			wordish++
		}
	}
	for _, r := range line {
		total++
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80 {
			letters++
		}
	}
	if total == 0 {
		return false
	}
	// enough real words, and letters are a real share of the characters
	return wordish >= 4 && letters*100/total >= 45
}

func noiseLine(line string) bool {
	return shareLineNumRE.MatchString(line) || shareGrepRE.MatchString(line)
}

func noisyShareMessage(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "Caveat:", "<system-reminder"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	if strings.Contains(t, "tool_use") || strings.Contains(t, "tool_result") {
		return true
	}
	return looksLikeDataDump(t)
}

// looksLikeDataDump flags pasted JSON, CLI output, or blobs with very long
// unbroken tokens — content that would make a shared digest unreadable.
func looksLikeDataDump(t string) bool {
	if len(t) > 400 {
		if (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && strings.Contains(t, "\":\"") {
			return true
		}
	}
	longestRun := 0
	run := 0
	for _, r := range t {
		if r == ' ' || r == '\n' || r == '\t' {
			run = 0
			continue
		}
		run++
		if run > longestRun {
			longestRun = run
		}
	}
	return longestRun > 200
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	inCSI := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inCSI {
			if c >= '@' && c <= '~' {
				inCSI = false
			}
			continue
		}
		if inEsc {
			inEsc = false
			if c == '[' {
				inCSI = true
			}
			continue
		}
		if c == 0x1b {
			inEsc = true
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func utf8SafeCut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
