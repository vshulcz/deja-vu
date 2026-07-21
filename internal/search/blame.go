package search

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

type BlameTarget struct {
	FullPath string
	Base     string
	Stem     string
}

type BlameOptions struct {
	Harness string
	Project string
	Since   time.Duration
	All     bool
}

type BlameHit struct {
	Session  model.Session `json:"session"`
	Title    string        `json:"title"`
	Count    int           `json:"count"`
	Snippets []string      `json:"snippets"`
	Score    float64       `json:"score"`
	Tier     string        `json:"tier"`
}

func ResolveBlamePath(name string) (BlameTarget, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return BlameTarget{}, fmt.Errorf("path required")
	}
	full, err := filepath.Abs(name)
	if err != nil {
		return BlameTarget{}, err
	}
	full = filepath.Clean(full)
	base := filepath.Base(full)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return BlameTarget{}, fmt.Errorf("path must name a file")
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}
	return BlameTarget{FullPath: full, Base: base, Stem: stem}, nil
}

func Blame(ss []model.Session, target BlameTarget, o BlameOptions) []BlameHit {
	cut := time.Time{}
	if o.Since > 0 {
		cut = time.Now().Add(-o.Since)
	}
	base := strings.ToLower(filepath.ToSlash(target.Base))
	forms := blameForms(target.FullPath)
	hits := make([]BlameHit, 0)
	for _, session := range mergeSessions(ss) {
		if o.Harness != "" && session.Harness != o.Harness {
			continue
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(session.Project), strings.ToLower(o.Project)) {
			continue
		}
		if !cut.IsZero() && session.Updated.Before(cut) {
			continue
		}
		hit := BlameHit{Session: session, Title: sessionTitle(session), Tier: TierExact}
		specificity := 0.0
		for _, message := range session.Messages {
			count, level := mentionScore(message.Text, base, forms)
			if count == 0 {
				continue
			}
			hit.Count += count
			if level > specificity {
				specificity = level
			}
			if len(hit.Snippets) < 2 {
				hit.Snippets = append(hit.Snippets, snippet(message.Text, target.Base, nil))
			}
		}
		if hit.Count == 0 {
			continue
		}
		score := float64(hit.Count) * (1 + specificity)
		if projectContainsFile(session.Project, target.FullPath) {
			score *= 1.35
		}
		hit.Score = score * freshnessDecay(session.Updated, time.Now())
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Session.Updated.Equal(hits[j].Session.Updated) {
			return hits[i].Session.Updated.After(hits[j].Session.Updated)
		}
		return hits[i].Session.ID < hits[j].Session.ID
	})
	if !o.All && len(hits) > 10 {
		hits = hits[:10]
	}
	return hits
}

func blameForms(full string) []string {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(full)))
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	forms := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		form := strings.Join(parts[i:], "/")
		forms = append(forms, "/"+form, form)
	}
	return forms
}

func mentionScore(text, base string, forms []string) (int, float64) {
	low := strings.ToLower(filepath.ToSlash(text))
	count := 0
	level := 1.0
	for _, form := range forms {
		if pathFormCount(low, form) > 0 {
			candidate := 1.0 + float64(len(strings.Split(form, "/")))/4
			if candidate > level {
				level = candidate
			}
		}
	}
	for pos := 0; ; {
		i := strings.Index(low[pos:], base)
		if i < 0 {
			break
		}
		i += pos
		if pathComponentOrWord(low, i, i+len(base)) {
			count++
		}
		pos = i + len(base)
	}
	return count, level
}

func pathFormCount(s, form string) int {
	count := 0
	for pos := 0; ; {
		i := strings.Index(s[pos:], form)
		if i < 0 {
			return count
		}
		i += pos
		if boundary(s, i, true) && boundary(s, i+len(form), false) {
			count++
		}
		pos = i + len(form)
	}
}

func pathComponentOrWord(s string, start, end int) bool {
	if start > 0 && end < len(s) && s[start-1] == '/' && s[end] == '/' {
		return true
	}
	return boundary(s, start, true) && boundary(s, end, false)
}

func boundary(s string, at int, before bool) bool {
	if at == 0 || at == len(s) {
		return true
	}
	if before {
		r, _ := utf8.DecodeLastRuneInString(s[:at])
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	}
	r, _ := utf8.DecodeRuneInString(s[at:])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
}

func projectContainsFile(project, full string) bool {
	if project == "" || !filepath.IsAbs(project) {
		return false
	}
	root := filepath.Clean(project)
	rel, err := filepath.Rel(root, full)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func sessionTitle(s model.Session) string {
	if s.Title != "" {
		return s.Title
	}
	for _, message := range s.Messages {
		if message.Role == "user" {
			text := strings.Join(strings.Fields(message.Text), " ")
			runes := []rune(text)
			if len(runes) > 60 {
				return string(runes[:60]) + "..."
			}
			return text
		}
	}
	return ""
}

func PrintBlame(w io.Writer, hits []BlameHit, jsonOutput bool) {
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
	}
	if jsonOutput {
		_ = json.NewEncoder(w).Encode(hits)
		return
	}
	color := colorOK(w)
	for _, hit := range hits {
		date := "-"
		if !hit.Session.Updated.IsZero() {
			date = hit.Session.Updated.Format("2006-01-02")
		}
		if color {
			sep := cDim + " · " + cReset
			fmt.Fprintf(w, "%s%s%s %s%s%s%s", harnessTag(hit.Session.Harness, true), sep, date, cBold+short(hit.Session.ID)+cReset, sep, hit.Session.Project, "")
			if hit.Title != "" {
				fmt.Fprintf(w, "%s%s", sep, cBold+hit.Title+cReset)
			}
		} else {
			fmt.Fprintf(w, "%s · %s · %s · %s", date, hit.Session.Harness, short(hit.Session.ID), hit.Session.Project)
			if hit.Title != "" {
				fmt.Fprintf(w, " · %s", hit.Title)
			}
		}
		fmt.Fprintln(w)
		for _, text := range hit.Snippets {
			if color {
				fmt.Fprintf(w, "  %s%s%s\n", cDim, text, cReset)
			} else {
				fmt.Fprintf(w, "  %s\n", text)
			}
		}
	}
}
