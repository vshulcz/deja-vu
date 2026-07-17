package search

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	RecallOff        = "off"
	RecallSafe       = "safe"
	RecallAggressive = "aggressive"
)

type AutoRecallOptions struct {
	Mode         string
	ProjectNames []string
	Now          time.Time
}

type AutoRecallResult struct {
	Text     string
	Sessions int
}

func AutoRecallDigest(ss []model.Session, budget int) string {
	if budget <= 0 {
		budget = 2000
	}
	var b strings.Builder
	for _, s := range ss {
		if b.Len() >= budget {
			break
		}
		section := autoRecallSession(s)
		if section == "" {
			continue
		}
		if b.Len()+len(section) > budget {
			cut := budget - b.Len()
			for cut > 0 && !utf8.RuneStart(section[cut]) {
				cut--
			}
			section = section[:cut]
		}
		b.WriteString(section)
	}
	return strings.TrimSpace(b.String())
}

// BuildAutoRecall applies the session-start recall policy while constructing
// the digest. Unknown modes use the safe policy.
func BuildAutoRecall(ss []model.Session, o AutoRecallOptions) AutoRecallResult {
	mode := strings.ToLower(strings.TrimSpace(o.Mode))
	if mode == RecallOff {
		return AutoRecallResult{}
	}
	if mode != RecallAggressive {
		mode = RecallSafe
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	candidates := append([]model.Session(nil), ss...)
	sort.SliceStable(candidates, func(i, j int) bool {
		iRecent := !candidates[i].Updated.Before(o.Now.AddDate(0, 0, -90))
		jRecent := !candidates[j].Updated.Before(o.Now.AddDate(0, 0, -90))
		if iRecent != jRecent {
			return iRecent
		}
		return candidates[i].Updated.After(candidates[j].Updated)
	})

	budget := 4096
	maxSessions := 6
	if mode == RecallSafe {
		budget = 2048
		maxSessions = 3
	}
	var b strings.Builder
	var fingerprints []map[string]bool
	for _, s := range candidates {
		if mode == RecallSafe && !projectMatches(s.Project, o.ProjectNames) {
			continue
		}
		section := autoRecallSession(s)
		if section == "" || (mode == RecallSafe && relevanceWords(s) < 3) {
			continue
		}
		fingerprint := sessionWordSet(s)
		if mode == RecallSafe && nearDuplicate(fingerprint, fingerprints) {
			continue
		}
		if b.Len()+len(section) > budget {
			cut := budget - b.Len()
			for cut > 0 && !utf8.RuneStart(section[cut]) {
				cut--
			}
			section = section[:cut]
		}
		if section == "" {
			break
		}
		b.WriteString(section)
		fingerprints = append(fingerprints, fingerprint)
		if b.Len() >= budget || len(fingerprints) >= maxSessions {
			break
		}
	}
	return AutoRecallResult{Text: strings.TrimSpace(b.String()), Sessions: len(fingerprints)}
}

func projectMatches(project string, names []string) bool {
	project = strings.ToLower(filepathClean(project))
	for _, name := range names {
		name = strings.ToLower(filepathClean(name))
		if name != "" && (project == name || strings.HasSuffix(project, "/"+name)) {
			return true
		}
	}
	return false
}

func filepathClean(s string) string {
	return strings.Trim(strings.ReplaceAll(s, "\\", "/"), "/")
}

func relevanceWords(s model.Session) int {
	return len(sessionWordSet(s))
}

func sessionWordSet(s model.Session) map[string]bool {
	text := s.Title
	for _, m := range s.Messages {
		text += " " + m.Text
	}
	return wordSet(text)
}

func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(word) >= 3 {
			set[word] = true
		}
	}
	return set
}

func nearDuplicate(candidate map[string]bool, prior []map[string]bool) bool {
	for _, other := range prior {
		intersection := 0
		for word := range candidate {
			if other[word] {
				intersection++
			}
		}
		union := len(candidate) + len(other) - intersection
		if union > 0 && float64(intersection)/float64(union) >= 0.8 {
			return true
		}
	}
	return false
}

func autoRecallSession(s model.Session) string {
	var problem string
	var conclusions []string
	for _, m := range s.Messages {
		text := contextText(m.Text, false)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case "user":
			if problem == "" && !noiseMessage(m.Text) {
				problem = firstLine(text, 160)
			}
		case "assistant":
			if len(conclusions) < 2 {
				conclusions = append(conclusions, firstLine(text, 220))
			}
		}
	}
	if problem == "" && len(conclusions) == 0 {
		return ""
	}
	date := ""
	if !s.Updated.IsZero() {
		date = " · " + s.Updated.Format("2006-01-02")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- **%s** `%s`%s\n", s.Project, short(s.ID), date)
	if problem != "" {
		fmt.Fprintf(&b, "  - User: %s\n", problem)
	}
	for _, c := range conclusions {
		fmt.Fprintf(&b, "  - Assistant: %s\n", c)
	}
	return b.String()
}

func noiseMessage(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "Caveat:", "<system-reminder"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func firstLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
