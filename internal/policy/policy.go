// Package policy decides what memory activates where. Recall crossing a
// machine boundary was the launch thread's top concern; instead of one env
// var, a small explicit table: per activation (search, mcp, auto), which
// origins (local, imported, imported:<peer>) may inject. Defaults allow
// everything, matching prior behavior; DEJA_AUTORECALL_LOCAL_ONLY=1 stays as
// an alias for denying imported memory on the auto path.
package policy

import (
	"encoding/json"
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
// origin. Origin keys: "local", "imported" (any peer), "imported:<peer>"
// (most specific wins).
type Policy struct {
	Activations map[string]map[string]bool `json:"activations,omitempty"`
}

func Path() string {
	if p := os.Getenv("DEJA_POLICY_FILE"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(sources.Home(), ".config")
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

// Origin classifies a session's project name.
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
// this path. Most specific rule wins: imported:<peer> over imported over the
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

// Describe names the active rule set for receipts and `deja log`, so the
// audit trail explains itself. The default policy reads "local+imported".
func (p Policy) Describe(activation string) string {
	rules := p.Activations[activation]
	if rules == nil {
		return "local+imported"
	}
	if v, ok := rules["imported"]; ok && !v {
		return "local-only"
	}
	denied := make([]string, 0, len(rules))
	for origin, allowed := range rules {
		if !allowed {
			denied = append(denied, origin)
		}
	}
	if len(denied) == 0 {
		return "local+imported"
	}
	sort.Strings(denied)
	return "deny " + strings.Join(denied, ",")
}

// Filter drops the items whose project the policy blocks on this path.
// projectOf maps an element to its session project name.
func Filter[T any](p Policy, activation string, items []T, projectOf func(T) string) []T {
	out := items[:0]
	for _, it := range items {
		if p.Allows(activation, projectOf(it)) {
			out = append(out, it)
		}
	}
	return out
}
