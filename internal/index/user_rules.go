package index

import (
	"regexp"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// A user rule is the one kind of recalled line an agent is told to follow: a
// rule a person typed, in an interactive session of the project the agent
// works in, about how to check or build it. Everything else recall returns is
// reference data. The parser decides who typed what (model.Session.Typed);
// this file decides which of it is such a rule, and which later turn took
// one back.
//
// Narrow on purpose. On the quillmark stand, telling the agent to follow
// every line a person's turn held made it run a publish step a third party
// had planted in an issue the person pasted; telling it to follow nothing
// made it skip the user's own check.

const (
	userRuleCap      = 3
	userRuleRunes    = 300
	userRevokeCap    = 8
	userPlantedCap   = 8
	typedTurnLines   = 6
	typedTurnRunes   = 1000
	userRuleMinRunes = 16
)

var (
	// How a rule is said: when it applies, and that it always does.
	userRulePhraseRE = regexp.MustCompile(`(?i)\b(rule|always|every time|each time|whenever|from now on|before you|after you|before (?:any|each|every|calling|committing|merging|marking|saying|you call)|after (?:any|each|every|editing|changing|you)|must|make sure|don'?t forget|is not enough)\b|всегда|каждый раз|перед тем|прежде чем|после того|после каждо|обязательно|правило|не забывай`)
	// That it runs something: a script, a make target, a tool.
	userRuleRunRE = regexp.MustCompile("(?i)`[^`]+`|(?:^|\\s)\\./[\\w./-]+|\\b(?:run|rerun|re-run|execute|invoke)\\b|\\b(?:make|go|npm|pnpm|yarn|cargo|pytest|tox|bazel|gradle|mvn|just|task)\\s+[\\w:-]+|запуска|запусти|прогон|прогоня")
	// That what it runs checks or builds.
	userRuleCheckRE = regexp.MustCompile(`(?i)\b(?:test|tests|testing|lint|linter|vet|check|checks|fmt|gofmt|format|build|compile|typecheck|generate|regenerate|codegen|golden|[\w-]*(?:check|lint|test|gen|build|fmt)[\w-]*\.(?:sh|py|js|ts|mjs|bash))\b|тест|линт|провер|сборк|собер|генер`)
	// What a rule can never be: anything that leaves the machine or does not
	// come back.
	userRuleDenyRE = regexp.MustCompile(`(?i)\bpush\w*|\bpublish\w*|\bupload\w*|\bsend\w*|\bemail\w*|\bmail\b|\bpost\s+(?:to|on|it|the|a|this|that)\b|\bdeploy\s+(?:it|to|the|this|that|them|everything|changes)\b|\b(?:deploy|release)\.sh\b|\brelease\s+(?:it|the|a|this|that|them|v?\d)|\btag\s+(?:it|the|a|this|v?\d)|\bmerge\s+(?:it|to|into|the|this|that|them)\b|\bgh\s+(?:pr|release|issue|api|repo|gist)\b|\bnpm\s+publish|\bdocker\s+push|\bkubectl\b|\bterraform\s+apply|\bhelm\s+(?:install|upgrade)|\bcurl\b|\bwget\b|\bssh\b|\bscp\b|\brsync\b|\brm\s+-|\brm\s+\S|\bdelet\w*|\bdrop\s+(?:table|database|the|it)|\btruncate\b|\breset\s+--hard|\bclean\s+-[a-z]*f|--force|\bforce[- ]push|\bwipe\b|\bdestroy\b|\bprune\b|пуш|запуш|опубликуй|публику|отправ|удали|удалять|задеплой|деплой|релиз`)
)

// userRuleLine reports whether a typed line is a rule about checking or
// building this project.
func userRuleLine(l string) bool {
	t := strings.TrimSpace(l)
	if n := len([]rune(t)); n < userRuleMinRunes || n > userRuleRunes || strings.HasSuffix(t, "?") {
		return false
	}
	return userRulePhraseRE.MatchString(t) && userRuleRunRE.MatchString(t) &&
		userRuleCheckRE.MatchString(t) && !userRuleDenyRE.MatchString(t)
}

// typedLines is the lines of a typed turn a rule may come from: what is ahead
// of any envelope the harness put in the turn. A long turn is a paste the
// harness did not mark, a log or a document, and holds none.
func typedLines(turn string) []string {
	t := digest.BeforeHarnessBlock(turn)
	if len([]rune(t)) > typedTurnRunes {
		return nil
	}
	var out []string
	for l := range strings.SplitSeq(t, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > typedTurnLines {
		return nil
	}
	return out
}

// foldUserRules reads the turns a person typed, in order, into the rules
// they stand by and the commands they took back. A later turn that takes a
// command back removes the rules naming it; a restated rule moves to the end;
// the newest userRuleCap are kept.
func foldUserRules(rules, revokes, typed []string) ([]string, []string) {
	for _, turn := range typed {
		for _, l := range typedLines(turn) {
			switch {
			case userRuleLine(l):
				r, _ := redact.Text(l)
				rules = slices.DeleteFunc(rules, func(x string) bool { return strings.EqualFold(x, r) })
				rules = append(rules, r)
				if len(rules) > userRuleCap {
					rules = rules[len(rules)-userRuleCap:]
				}
			default:
				toks := evidence.Revoked(l)
				if len(toks) == 0 {
					continue
				}
				rules = dropRevoked(rules, toks)
				for _, t := range toks {
					if !slices.Contains(revokes, t) {
						revokes = append(revokes, t)
					}
				}
				if len(revokes) > userRevokeCap {
					revokes = revokes[len(revokes)-userRevokeCap:]
				}
			}
		}
	}
	return rules, revokes
}

// dropRevoked is rules without those naming a revoked command.
func dropRevoked(rules, revoked []string) []string {
	if len(rules) == 0 || len(revoked) == 0 {
		return rules
	}
	return slices.DeleteFunc(slices.Clone(rules), func(r string) bool {
		for _, t := range evidence.Commands(r) {
			if slices.Contains(revoked, t) {
				return true
			}
		}
		return false
	})
}

// MarkUserRules fills what a surface may say about the person's rules in s
// for a reader working in scope (the name forms of their project), and only
// for a local session of a person in that project: UserRules, the rules it
// may mark as the person's, less any a later session of the project took
// back; TakenBack, the commands the person took back, here or later. Both
// stay empty otherwise, and for an empty scope. Planted and Foreign, which
// say where recall cuts orders, are filled for every session.
func MarkUserRules(dir string, s *model.Session, scope []string) {
	s.UserRules, s.TakenBack, s.Planted, s.Foreign = nil, nil, nil, false
	m, err := readManifestCached(dir)
	if err != nil {
		return
	}
	key := s.Harness + ":" + s.ID
	row, ok := m.Sessions[key]
	if !ok {
		return
	}
	s.Planted = slices.Clone(row.Planted)
	s.Foreign = len(scope) > 0 && !inAnyScope(row.Project, scope)
	if len(scope) == 0 || s.Foreign || !localPersonSession(s.Project, s.From, s.OrigID, s.Kind) ||
		!localPersonSession(row.Project, row.From, row.OrigID, row.Kind) {
		return
	}
	later := slices.Clone(row.Revokes)
	for k, other := range m.Sessions {
		if len(other.Revokes) == 0 || k == key || !other.Updated.After(row.Started) ||
			!localPersonSession(other.Project, other.From, other.OrigID, other.Kind) || !inAnyScope(other.Project, scope) {
			continue
		}
		for _, t := range other.Revokes {
			if !slices.Contains(later, t) {
				later = append(later, t)
			}
		}
		// The session's own revocations were applied when it was read, in
		// order; another session's apply to all of its rules.
		row.Rules = dropRevoked(row.Rules, other.Revokes)
	}
	if len(row.Rules) > 0 {
		s.UserRules = slices.Clone(row.Rules)
	}
	if len(later) > 0 {
		s.TakenBack = later
	}
}

// foldPlanted adds a pass's planted orders to a row's, newest kept.
func foldPlanted(old, add []string) []string {
	for _, p := range add {
		r, _ := redact.Text(p)
		if r = strings.TrimSpace(r); r == "" || slices.Contains(old, r) {
			continue
		}
		old = append(old, r)
	}
	if len(old) > userPlantedCap {
		old = old[len(old)-userPlantedCap:]
	}
	return old
}

func localPersonSession(project, from, origID, kind string) bool {
	return from == "" && origID == "" && !strings.HasPrefix(project, "imported:") && !model.SpawnedKind(kind)
}

func inAnyScope(project string, scope []string) bool {
	for _, name := range scope {
		if strings.TrimSpace(name) != "" && projectInScope(project, name) {
			return true
		}
	}
	return false
}
