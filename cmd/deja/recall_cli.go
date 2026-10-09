package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/usage"
)

var recallFlags = []string{"--project", "--harness", "--limit"}

// runRecall prints what the MCP recall tool answers: the framed page, budgeted
// the same way, this project first. `deja search --json` was the only answer
// an agent could ask for on the command line, and it ran to 1-2 MB, which a
// harness truncates to nothing; a skill that drives deja through the shell had
// nothing it could read (#4781).
func runRecall(dir string, args []string, stdout io.Writer) error {
	limit := 0
	project, harness := "", ""
	var terms []string
	value := func(i int, flag string) (string, error) {
		if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
			return "", fmt.Errorf("recall: %s needs a value", flag)
		}
		return args[i+1], nil
	}
	// --flag=value splits into the two-argument form, and after -- every
	// word is the query, so a query may start with a dash.
	var norm []string
	for i, a := range args {
		if a == "--" {
			terms = append(terms, args[i+1:]...)
			break
		}
		if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "--") {
			norm = append(norm, k, v)
			continue
		}
		norm = append(norm, a)
	}
	args = norm
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--limit":
			v, err := value(i, a)
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return fmt.Errorf("recall: --limit wants a positive number, got %q", v)
			}
			limit, i = n, i+1
		case "--project":
			v, err := value(i, a)
			if err != nil {
				return err
			}
			project, i = v, i+1
		case "--harness":
			v, err := value(i, a)
			if err != nil {
				return err
			}
			harness, i = v, i+1
		default:
			if strings.HasPrefix(a, "--") {
				return unknownFlag("recall", a, recallFlags)
			}
			terms = append(terms, a)
		}
	}
	q := strings.TrimSpace(strings.Join(terms, " "))
	if q == "" {
		return fmt.Errorf("recall: what to recall? deja recall <words>")
	}
	if err := checkHarness(&harness); err != nil {
		return err
	}
	if line := buildingNowForAgent(dir); line != "" {
		_, err := fmt.Fprintln(stdout, frameRecall(line))
		return err
	}
	text, sessions, raw, ids, projects, err := recallTextResultIn(dir, q, harness, project, limit, 0, recallMCPBudget-recallFrameOverhead)
	if err != nil {
		return err
	}
	text = frameRecall(text)
	usage.RecordServedFromInto(dir, usage.KindRecall, text, "", sessions, raw, ids, projects, policy.Load().Describe(policy.ActivationMCP))
	_, err = fmt.Fprintln(stdout, text)
	return err
}
