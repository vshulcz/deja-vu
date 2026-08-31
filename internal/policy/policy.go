// Package policy decides what memory activates where. Recall crossing a
// machine boundary was the launch thread's top concern; instead of one env
// var, a small explicit table: per activation (search, mcp, auto), which
// origins (local, imported, imported:<group>) may inject. Defaults allow
// everything, matching prior behavior; DEJA_AUTORECALL_LOCAL_ONLY=1 stays as
// an alias for denying imported memory on the auto path.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	ActivationSearch = "search"
	ActivationMCP    = "mcp"
	ActivationAuto   = "auto"
)

// Policy maps activation → origin rules. A missing activation allows every
// origin. Origin keys: "local", "imported" (anything that arrived by sync) and
// "imported:<group>", where group is the first path component of the project
// the session had on the machine it came from — a sync batch carries no machine
// identity, so this is a project grouping and not a peer name, which is how it
// was described until a rule written for a machine turned out to be accepted,
// displayed and never matched (#955). Most specific wins.
type Policy struct {
	Activations map[string]map[string]bool `json:"activations,omitempty"`
	// Ignore lists the directories deja does not recall from: an agent
	// runtime's scratch tree, a CI checkout, an evaluation harness. Without it
	// the only way to keep such sessions out was `deja forget`, which removes
	// what exists and says nothing about what the same directory writes
	// tomorrow (#2050). Empty means the default in ignore.go.
	Ignore []string `json:"ignore,omitempty"`
}

func Path() string {
	if p := os.Getenv("DEJA_POLICY_FILE"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(sources.Home(), ".config")
	}
	// Nowhere, rather than somewhere relative. Home() answers "" when there is
	// no home directory, so this was `.config/deja/policy.json` — and deja read
	// whatever a checkout happened to have there as the reader's own trust
	// policy, which is the wrong direction for the file that decides what
	// recall may hand over (#2785). The spec says the same of XDG_CONFIG_HOME:
	// a relative value is to be ignored.
	if !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, "deja", "policy.json")
}

// Load reads the policy file and folds in the env alias. Any read or parse
// failure means the default policy — recall must not break because a config
// file is malformed; doctor is the place to complain.
func Load() Policy {
	var p Policy
	if b, err := os.ReadFile(Path()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	if os.Getenv("DEJA_AUTORECALL_LOCAL_ONLY") == "1" {
		if p.Activations == nil {
			p.Activations = map[string]map[string]bool{}
		}
		if p.Activations[ActivationAuto] == nil {
			p.Activations[ActivationAuto] = map[string]bool{"local": true}
		}
		p.Activations[ActivationAuto]["imported"] = false
	}
	return p
}

// Origin classifies a session's project name. Everything local is "local";
// anything that arrived by sync is "imported", and additionally
// "imported:<group>" when its source project had a path, so a group of another
// machine's projects can be addressed on its own (#955).
func Origin(project string) string {
	if peer, ok := strings.CutPrefix(project, "imported:"); ok {
		if i := strings.IndexByte(peer, '/'); i > 0 {
			return "imported:" + peer[:i]
		}
		return "imported"
	}
	return "local"
}

// Allows reports whether memory from the session's origin may activate on
// this path. Most specific rule wins: imported:<group> over imported over the
// activation default (allow).
func (p Policy) Allows(activation, project string) bool {
	rules := p.Activations[activation]
	if rules == nil {
		return true
	}
	origin := Origin(project)
	if v, ok := rules[origin]; ok {
		return v
	}
	if strings.HasPrefix(origin, "imported:") {
		if v, ok := rules["imported"]; ok {
			return v
		}
	}
	if v, ok := rules["*"]; ok {
		return v
	}
	return true
}

// AllowsEgress reports whether a project's content may leave the machine.
//
// Every activation is a read path on this box; sending text to an embedding
// endpoint is not, and no rule describes it. Borrowing the loosest of the three
// let a machine refuse to show a session to its own agent and ship the same text
// to a third party in the same breath (#1311) — `search` is usually the most
// permissive rule there is, because a person's own terminal is theirs. So egress
// needs agreement: content the owner withholds on any path stays here.
func (p Policy) AllowsEgress(project string) bool {
	for _, a := range []string{ActivationSearch, ActivationMCP, ActivationAuto} {
		if !p.Allows(a, project) {
			return false
		}
	}
	return true
}

// consultedOrigin reports whether a rule key is one Origin can produce. Rules
// are keyed by origin; a project name looks like a rule and is not one.
func consultedOrigin(origin string) bool {
	return origin == "local" || origin == "imported" || strings.HasPrefix(origin, "imported:")
}

// Describe names the active rule set for receipts and `deja log`, so the
// audit trail explains itself. The default policy reads "local+imported".
func (p Policy) Describe(activation string) string {
	rules := p.Activations[activation]
	if rules == nil {
		return "local+imported"
	}
	localOff := false
	if v, ok := rules["local"]; ok && !v {
		localOff = true
	}
	importedOff := false
	if v, ok := rules["imported"]; ok && !v {
		importedOff = true
	}
	// A rule that denies both origins was described as "local-only", which is
	// the name of a different rule — and doctor printed it beside a count that
	// contradicted it: "local-only — withholds 1 of 1 indexed session" (#995).
	switch {
	case localOff && importedOff:
		return "nothing activates"
	case importedOff:
		return "local-only"
	}
	// A rule that denies only `local` keeps the generic "deny <origin>" form:
	// it is one denial among the others below, and two tests pin it (#941).
	denied := make([]string, 0, len(rules))
	for origin, allowed := range rules {
		// A key deja never consults restricts nothing, and naming it here put
		// two claims on one screen: "deny tmp/other" from this line and "this
		// rule does nothing" from Diagnose's, three lines down (#941).
		if !allowed && consultedOrigin(origin) {
			denied = append(denied, origin)
		}
	}
	if len(denied) == 0 {
		return "local+imported"
	}
	sort.Strings(denied)
	// Twenty group rules made one 1000-character line on the screen people
	// read to find out what is allowed; the file itself is named beside it
	// and holds the full set (#1023).
	const shown = 4
	if len(denied) > shown {
		return fmt.Sprintf("deny %s +%d more", strings.Join(denied[:shown], ","), len(denied)-shown)
	}
	return "deny " + strings.Join(denied, ",")
}

// Filter drops the items whose project the policy blocks on this path.
// projectOf maps an element to its session project name.
// The result is a new slice: filtering in place rewrote the caller's own
// input — handoff read the pre-filter list to learn what had been withheld and
// saw the kept sessions duplicated over it (#1013).
func Filter[T any](p Policy, activation string, items []T, projectOf func(T) string) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if p.Allows(activation, projectOf(it)) {
			out = append(out, it)
		}
	}
	return out
}

// Diagnose reports what a reader needs to know about the policy file: whether
// it exists, whether it parses, and which keys it uses that mean nothing.
//
// Load deliberately falls back to the permissive default on any error, so a
// malformed file changes nothing and says nothing — the wrong outcome for the
// one mechanism separating local memory from imported (#661).
func Diagnose() (exists bool, unknown []string, err error) {
	path := Path()
	if path == "" {
		return false, nil, nil
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return false, nil, nil
		}
		return true, nil, rerr
	}
	var p Policy
	if jerr := json.Unmarshal(b, &p); jerr != nil {
		return true, nil, jerr
	}
	// The whole shape, before the keys inside it. A file written from memory or
	// from another tool's config — `{"rules":[{"project":…,"auto":"deny"}]}` —
	// parses into an empty policy, denies nothing, and read exactly like a rule
	// that works, while the checks below only ever looked inside `activations`
	// (#2504).
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) == nil {
		for key := range top {
			switch key {
			case "activations", "ignore":
			default:
				unknown = append(unknown, key)
			}
		}
	}
	// A file that parses but names an activation or origin deja never consults
	// is silently doing nothing, which reads exactly like a rule that works.
	for activation, rules := range p.Activations {
		switch activation {
		case ActivationSearch, ActivationMCP, ActivationAuto:
		default:
			unknown = append(unknown, "activation "+activation)
			continue
		}
		for origin := range rules {
			if origin == "local" || origin == "imported" || strings.HasPrefix(origin, "imported:") {
				continue
			}
			unknown = append(unknown, activation+"."+origin)
		}
	}
	sort.Strings(unknown)
	return true, unknown, nil
}
