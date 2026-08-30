package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// projectForEcho is how a project name is written back to whoever named it.
// The value is theirs and reaches a terminal or a model unchanged otherwise:
// an escape byte recoloured the confirmation and a carriage return rewound it,
// and a 5000-character name printed whole. The listing surfaces have bounded
// both since #1090; this is the same bound on the line that says a note was
// stored (#1792).
func projectForEcho(project string) string {
	out := neutralizeFrameMarkers(safeForStatusline(project, mcpResourceNameMax))
	// A name of nothing but control bytes bounds down to nothing, and
	// "remembered under " names no project at all. The note is stored either
	// way, so the line says what happened instead of trailing off.
	if out == "" && strings.TrimSpace(project) != "" {
		return "a name with no printable characters"
	}
	return out
}

func runRemember(dir string, args []string) error {
	var text, project string
	var tags []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" {
			if i+1 >= len(args) {
				return fmt.Errorf("remember: --project needs value")
			}
			project = args[i+1]
			i++
			continue
		}
		if args[i] == "--tag" {
			if i+1 >= len(args) {
				return fmt.Errorf("remember: --tag needs value")
			}
			tags = append(tags, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			return fmt.Errorf("remember: unknown flag %q", args[i])
		}
		if text != "" {
			return fmt.Errorf("remember: expected one text argument")
		}
		text = args[i]
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("remember: text required")
	}
	if strings.TrimSpace(project) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		project = sources.ClaudeProjectName(cwd)
	}
	now := time.Now()
	if err := sources.AppendNoteTagged(project, text, tags, now); err != nil {
		if errors.Is(err, sources.ErrNoteExists) {
			fmt.Fprintf(os.Stderr, "deja: already remembered under %s\n", projectForEcho(project))
			return nil
		}
		return notesWriteError(err)
	}
	if err := index.EnsureForSearch(dir, search.Options{All: true}, false, os.Stderr); err != nil {
		return err
	}
	// A day-note the reader forgot keeps its tombstone until unforget, so a
	// later `remember` on the same day and project writes into a note that
	// stays hidden — the silent success promote used to report on the same
	// path. Name the restore command rather than lose the line quietly.
	dayNote := "deja-" + now.Local().Format("2006-01-02") + "-" + strings.TrimSpace(project)
	if index.Tombstoned("deja:" + dayNote) {
		fmt.Fprint(os.Stderr, tombstoneHint("it is written", dayNote))
	}
	suffix := ""
	if norm := sources.NormalizeTags(tags); len(norm) > 0 {
		// Cleaned at the source now, but the echo is the line a user checks
		// what was filed against, and it is one line: bound it the way the
		// project name beside it is bounded (#1810).
		suffix = " " + safeForStatusline("#"+strings.Join(norm, " #"), mcpResourceNameMax)
	}
	fmt.Fprintf(os.Stdout, "deja: remembered under %s%s\n", projectForEcho(project), suffix)
	return nil
}
