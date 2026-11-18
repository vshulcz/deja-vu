package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

const (
	trailDefaultLimit = 20
	trailMaxLimit     = 200
)

type trailOptions struct {
	path        string
	project     string
	allProjects bool
	harness     string
	limit       int
	asJSON      bool
}

type trailEntry struct {
	ID      string    `json:"id"`
	Harness string    `json:"harness"`
	Project string    `json:"project,omitempty"`
	Title   string    `json:"title,omitempty"`
	Started time.Time `json:"started,omitzero"`
	Updated time.Time `json:"updated,omitzero"`
}

type trailReport struct {
	SchemaVersion int          `json:"schema_version"`
	Path          string       `json:"path"`
	Total         int          `json:"total"`
	Entries       []trailEntry `json:"entries"`
}

func parseTrailArgs(args []string) (trailOptions, error) {
	o := trailOptions{limit: trailDefaultLimit}
	afterDashDash := false
	args = splitKnownValueFlags(args, "--project", "--harness", "--limit")
	for i := 0; i < len(args); i++ {
		a := args[i]
		if afterDashDash {
			if err := setTrailPath(&o, a); err != nil {
				return trailOptions{}, err
			}
			continue
		}
		switch a {
		case "--":
			afterDashDash = true
		case "--json":
			o.asJSON = true
		case "--all-projects":
			o.allProjects = true
		case "--project":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return trailOptions{}, errors.New("trail: --project needs value")
			}
			i++
			o.project = args[i]
		case "--harness":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return trailOptions{}, errors.New("trail: --harness needs value")
			}
			i++
			o.harness = args[i]
		case "--limit":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return trailOptions{}, errors.New("trail: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 || n > trailMaxLimit {
				return trailOptions{}, fmt.Errorf("trail: --limit must be 1..%d, got %q", trailMaxLimit, args[i])
			}
			o.limit = n
		default:
			if strings.HasPrefix(a, "-") {
				return trailOptions{}, unknownFlag("trail", a, trailFlags)
			}
			if err := setTrailPath(&o, a); err != nil {
				return trailOptions{}, err
			}
		}
	}
	if strings.TrimSpace(o.path) == "" {
		return trailOptions{}, errors.New("trail: path required — deja trail <path>")
	}
	return o, nil
}

func setTrailPath(o *trailOptions, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("trail: path cannot be empty")
	}
	if o.path != "" {
		return fmt.Errorf("trail takes one path — got %q and %q", o.path, raw)
	}
	o.path = raw
	return nil
}

func runTrail(dir string, args []string, stdout io.Writer) error {
	o, err := parseTrailArgs(args)
	if err != nil {
		return err
	}
	if herr := checkHarness(&o.harness); herr != nil {
		return herr
	}
	target, err := search.ResolveBlamePath(o.path)
	if err != nil {
		return err
	}
	var scope []string
	if strings.TrimSpace(o.project) != "" {
		scope = howScope(howCwd(), o.project, false)
	} else if !o.allProjects {
		scope = blameScope(howCwd(), target, "", false)
	}

	q := query.Options{
		Query:    target.Stem,
		Harness:  o.harness,
		Projects: scope,
		Sort:     query.SortDate,
	}
	if ensureErr := index.EnsureForSearch(dir, q, false, os.Stderr); ensureErr != nil {
		return ensureErr
	}
	found, err := index.SearchWithRecovery(dir, q, os.Stderr)
	if err != nil {
		return err
	}

	candidates := withFileTouchers(dir, found, target)
	var scoped []model.Session
	for _, s := range candidates {
		if o.harness != "" && s.Harness != o.harness {
			continue
		}
		if len(scope) > 0 && !howProjectMatches(s.Project, scope) {
			continue
		}
		scoped = append(scoped, s)
	}

	hits := search.Blame(scoped, target, search.BlameOptions{
		Harness: o.harness,
		All:     true,
	})
	matched := make([]model.Session, 0, len(hits))
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		k := h.Session.Key()
		if seen[k] {
			continue
		}
		seen[k] = true
		matched = append(matched, h.Session)
	}

	visible, _ := policyFilterSessionsCounted(policy.ActivationSearch, matched)
	slices.SortFunc(visible, compareTrailChrono)

	total := len(visible)
	if total > o.limit {
		visible = visible[total-o.limit:]
	}

	entries := make([]trailEntry, 0, len(visible))
	for _, s := range visible {
		entries = append(entries, trailEntry{
			ID:      s.ID,
			Harness: s.Harness,
			Project: s.Project,
			Title:   s.Title,
			Started: s.Started,
			Updated: s.Updated,
		})
	}

	if o.asJSON {
		rep := trailReport{
			SchemaVersion: jsonout.Version,
			Path:          o.path,
			Total:         total,
			Entries:       entries,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	return printTrailReport(stdout, o.path, total, entries)
}

func compareTrailChrono(a, b model.Session) int {
	ta := trailSessionTime(a)
	tb := trailSessionTime(b)
	if !ta.Equal(tb) {
		if ta.Before(tb) {
			return -1
		}
		return 1
	}
	if !a.Updated.Equal(b.Updated) {
		if a.Updated.Before(b.Updated) {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Key(), b.Key())
}

func trailSessionTime(s model.Session) time.Time {
	if !s.Started.IsZero() {
		return s.Started
	}
	return s.Updated
}

func printTrailReport(w io.Writer, targetPath string, total int, entries []trailEntry) error {
	if len(entries) == 0 {
		_, err := fmt.Fprintf(w, "trail: no sessions mention or touch %s\n", targetPath)
		return err
	}
	if _, err := fmt.Fprintf(w, "trail for %s (%d of %d, oldest first):\n", targetPath, len(entries), total); err != nil {
		return err
	}
	for _, e := range entries {
		when := e.Started
		if when.IsZero() {
			when = e.Updated
		}
		stamp := "-"
		if !when.IsZero() {
			stamp = when.Local().Format("2006-01-02 15:04")
		}
		title := strings.TrimSpace(e.Title)
		if title == "" {
			title = "(untitled)"
		}
		if _, err := fmt.Fprintf(w, "  %s  %-10s  %s  %s\n", stamp, e.Harness, e.ID, title); err != nil {
			return err
		}
	}
	return nil
}
