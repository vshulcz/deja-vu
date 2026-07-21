package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// runPromote distills a session into a curated note: quoted evidence with
// provenance and a lifecycle state, stored in the notes source so it indexes
// like everything else but outranks the raw transcript it came from.
func runPromote(dir string, args []string, stdout io.Writer) error {
	prefix := ""
	state := "accepted"
	noteText := ""
	exportPath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--state":
			if i+1 >= len(args) {
				return fmt.Errorf("promote: --state needs a value")
			}
			i++
			state = strings.ToLower(args[i])
		case "--note":
			if i+1 >= len(args) {
				return fmt.Errorf("promote: --note needs text")
			}
			i++
			noteText = args[i]
		case "--to":
			if i+1 >= len(args) {
				return fmt.Errorf("promote: --to needs a path")
			}
			i++
			exportPath = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("promote: unknown flag %q", args[i])
			}
			prefix = args[i]
		}
	}
	if prefix == "" {
		return fmt.Errorf("promote needs a session id prefix (see `deja last`)")
	}
	if !sources.NoteStates[state] {
		return fmt.Errorf("promote: state must be accepted, rejected, superseded or stale")
	}
	s, ok, err := findByPrefix(dir, prefix)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q", prefix)
	}
	if s.Harness == "deja" {
		return fmt.Errorf("%q is already a note — promote the source session instead", prefix)
	}
	text := strings.TrimSpace(noteText)
	if text == "" {
		text = distillSession(s)
	}
	src := s.Harness + ":" + s.ID
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = firstLine(text)
	}
	if err := sources.AppendPromoted(s.Project, title, text, src, state, time.Now()); err != nil {
		return err
	}
	if exportPath != "" {
		if err := exportPromoted(exportPath, title, text, src, state, s.Updated); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "promoted %s as %s: %s\n", src, state, title)
	if exportPath != "" {
		fmt.Fprintf(stdout, "exported to %s\n", exportPath)
	}
	fmt.Fprintln(stdout, "the note now outranks the raw transcript in recall; corrections append with `deja promote", prefix, "--state <state>`")
	return nil
}

// distillSession quotes the session instead of summarizing it: the first user
// ask and the last assistant word, each trimmed — receipts, not generation.
func distillSession(s model.Session) string {
	var ask, answer string
	for _, m := range s.Messages {
		t := strings.TrimSpace(m.Text)
		if t == "" {
			continue
		}
		if ask == "" && m.Role == "user" {
			ask = t
		}
		if m.Role == "assistant" {
			answer = t
		}
	}
	parts := make([]string, 0, 2)
	if ask != "" {
		parts = append(parts, "asked: "+trimRunes(ask, 300))
	}
	if answer != "" {
		parts = append(parts, "outcome: "+trimRunes(answer, 300))
	}
	if len(parts) == 0 {
		return "promoted session (no text messages)"
	}
	return strings.Join(parts, " · ")
}

func trimRunes(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return trimRunes(s, 80)
}

// exportPromoted appends a Markdown block to a repo-visible notes file.
// Append-only like the store: a correction adds a new block below the old.
func exportPromoted(path, title, text, src, state string, updated time.Time) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	day := updated.UTC().Format("2006-01-02")
	if updated.IsZero() {
		day = time.Now().UTC().Format("2006-01-02")
	}
	_, err = fmt.Fprintf(f, "\n## %s\n\n- state: %s\n- source: %s (%s)\n\n%s\n", title, state, src, day, text)
	return err
}
