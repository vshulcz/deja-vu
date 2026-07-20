package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func runRemember(dir string, args []string) error {
	var text, project string
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" {
			if i+1 >= len(args) {
				return fmt.Errorf("remember: --project needs value")
			}
			project = args[i+1]
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
	if err := sources.AppendNote(project, text, time.Now()); err != nil {
		return err
	}
	if err := index.EnsureForSearch(dir, search.Options{All: true}, false, os.Stderr); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "deja: remembered under %s\n", strings.TrimSpace(project))
	return nil
}
