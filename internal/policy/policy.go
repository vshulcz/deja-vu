// Package policy decides what memory activates where. Recall crossing a
// machine boundary was the launch thread's top concern; instead of one env
// var, a small explicit table: per activation (search, mcp, auto), which
// origins (local, imported, imported:<group>) may inject. Defaults allow
// everything, matching prior behavior; DEJA_AUTORECALL_LOCAL_ONLY=1 stays as
// an alias for denying imported memory on the auto path.
package policy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Activations a policy can restrict.
const (
	ActivationSearch = "search"
	ActivationMCP    = "mcp"
	ActivationAuto   = "auto"
)

// Policy maps activation → origin rules. A missing activation allows every
// origin. Origin keys: "local", "imported" (anything that arrived by sync) and
// "imported:<group>", where group is the first path component of the project
// the session had on the machine it came from. A sync record names its machine
// only as the sender calls itself, which nothing verifies, so this is a project
// grouping and not a peer name — which is how it was described until a rule
// written for a machine turned out to be accepted, displayed and never matched
// (#955). Most specific wins.
type Policy struct {
	Activations map[string]map[string]bool `json:"activations,omitempty"`
	// Ignore lists the directories deja does not recall from: an agent
	// runtime's scratch tree, a CI checkout, an evaluation harness. Without it
	// the only way to keep such sessions out was `deja forget`, which removes
	// what exists and says nothing about what the same directory writes
	// tomorrow (#2050). Empty means the default in ignore.go.
	Ignore []string `json:"ignore,omitempty"`
	// AutoIndex false stops every automatic index update: searches, recall
	// and hooks answer from the last finished index, and only `deja index`
	// writes it. For a busy machine that refreshes from cron (#5385).
	AutoIndex *bool `json:"auto_index,omitempty"`
	// SearchOnly true builds the index for conversation search alone and
	// skips mining fixes, co-occurrence, commands, command failures and
	// session facts (#5395).
	SearchOnly bool `json:"search_only,omitempty"`
	// Write maps write surfaces ("cli", "mcp", "*") to allow/deny (#5823).
	Write map[string]bool `json:"write,omitempty"`
	// RequireClients restricts memory access to explicitly enrolled Clients (#5823).
	RequireClients bool `json:"require_clients,omitempty"`
	// Clients maps normalized client names to per-client read/write/project rules (#5823).
	Clients map[string]ClientRule `json:"clients,omitempty"`
	// ClientTokens maps normalized client names to 64-char hex SHA-256 digests (#5823).
	ClientTokens map[string]string `json:"client_tokens,omitempty"`
}

// ClientRule configures read/write grants and allowed project names for one client.
type ClientRule struct {
	Read     *bool    `json:"read,omitempty"`
	Write    *bool    `json:"write,omitempty"`
	Projects []string `json:"projects,omitempty"`
}

// SearchOnlyOn reports "search_only": true in the policy file.
func SearchOnlyOn() bool { return Load().SearchOnly }

// AutoIndexOff reports "auto_index": false in the policy file.
func AutoIndexOff() bool {
	p := Load()
	return p.AutoIndex != nil && !*p.AutoIndex
}

// Path is where the policy file lives.
func Path() string {
	if p := os.Getenv("DEJA_POLICY_FILE"); p != "" {
		return p
	}
	// A relative XDG_CONFIG_HOME is ignored rather than followed, as the spec
	// says; ConfigHome is the one place that decides it.
	base := sources.ConfigHome()
	// And nowhere, rather than somewhere relative: Home() answers "" when
	// there is no home directory, so this was `.config/deja/policy.json` —
	// deja read whatever a checkout happened to have there as the reader's own
	// trust policy, which is the wrong direction for the file that decides
	// what recall may hand over (#2785).
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
	// An entry from a repository's committed .deja/memory.jsonl is "repo:"
	// and the project, so one repository can be denied on its own and "repo"
	// denies them all.
	if strings.HasPrefix(project, model.RepoOriginPrefix) {
		return project
	}
	if peer, ok := strings.CutPrefix(project, model.ImportedPrefix); ok {
		if i := strings.IndexByte(peer, '/'); i > 0 {
			return model.ImportedPrefix + peer[:i]
		}
		return "imported"
	}
	if model.IsRepoProject(project) {
		return model.OriginRepo
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
	if model.IsImportedProject(origin) {
		if v, ok := rules["imported"]; ok {
			return v
		}
	}
	if strings.HasPrefix(origin, model.RepoOriginPrefix) {
		if v, ok := rules[model.OriginRepo]; ok {
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
	return origin == "local" || origin == "repo" || strings.HasPrefix(origin, model.RepoOriginPrefix) || origin == "imported" || model.IsImportedProject(origin)
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
			case "activations", "ignore", "auto_index", "search_only", "write", "require_clients", "clients", "client_tokens":
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
			if consultedOrigin(origin) {
				continue
			}
			unknown = append(unknown, activation+"."+origin)
		}
	}
	for surface := range p.Write {
		switch surface {
		case "cli", "mcp", "*":
		default:
			unknown = append(unknown, "write."+surface)
		}
	}
	for client, digest := range p.ClientTokens {
		if !isSHA256Hex(digest) {
			unknown = append(unknown, "client_tokens."+client)
		}
	}
	sort.Strings(unknown)
	return true, unknown, nil
}

// NormalizeClient normalizes an enrolled client identifier for lookups.
func NormalizeClient(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func isSHA256Hex(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 64 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (p Policy) findClientRule(client string) (ClientRule, bool) {
	c := NormalizeClient(client)
	if r, ok := p.Clients[c]; ok {
		return r, true
	}
	for k, r := range p.Clients {
		if NormalizeClient(k) == c {
			return r, true
		}
	}
	return ClientRule{}, false
}

func (p Policy) expectedClientToken(client string) (string, bool) {
	c := NormalizeClient(client)
	if d, ok := p.ClientTokens[c]; ok {
		return d, true
	}
	for k, d := range p.ClientTokens {
		if NormalizeClient(k) == c {
			return d, true
		}
	}
	return "", false
}

// AuthenticateClient checks enrollment and constant-time SHA-256 client token digest.
// Every enrolled client name that has a token must present it; there is no
// built-in exemption for the "cli" name, which MCP callers could otherwise
// self-assert to skip token validation.
func (p Policy) AuthenticateClient(client, presentedToken string) (bool, string) {
	c := NormalizeClient(client)
	_, enrolled := p.findClientRule(c)
	if p.RequireClients && (!enrolled || c == "") {
		return false, "client not enrolled"
	}
	wantHex, hasToken := p.expectedClientToken(c)
	if hasToken || (p.RequireClients && len(p.ClientTokens) > 0) {
		if !hasToken || presentedToken == "" {
			return false, "client token required"
		}
		sum := sha256.Sum256([]byte(presentedToken))
		gotHex := hex.EncodeToString(sum[:])
		wantNorm := strings.ToLower(strings.TrimSpace(wantHex))
		if subtle.ConstantTimeCompare([]byte(gotHex), []byte(wantNorm)) != 1 {
			return false, "invalid client token"
		}
	}
	return true, ""
}

// AllowsClientProject reports whether client may access project.
func (p Policy) AllowsClientProject(client, project string) bool {
	if project == "" {
		return true
	}
	rule, ok := p.findClientRule(client)
	if !ok || len(rule.Projects) == 0 {
		return true
	}
	for _, pat := range rule.Projects {
		if pat == "*" || pat == project {
			return true
		}
	}
	return false
}

// AllowsClientRead reports whether client is authenticated and permitted to read project on surface.
func (p Policy) AllowsClientRead(surface, client, presentedToken, project string) (bool, string) {
	_ = surface
	if ok, reason := p.AuthenticateClient(client, presentedToken); !ok {
		return false, reason
	}
	if rule, ok := p.findClientRule(client); ok && rule.Read != nil && !*rule.Read {
		return false, "client read denied"
	}
	if !p.AllowsClientProject(client, project) {
		return false, "project outside client scope"
	}
	return true, ""
}

// AllowsClientWrite reports whether client is permitted to write notes on surface and project.
func (p Policy) AllowsClientWrite(surface, client, presentedToken, project string) (bool, string) {
	if ok, reason := p.AuthenticateClient(client, presentedToken); !ok {
		return false, reason
	}
	if v, ok := p.Write[surface]; ok && !v {
		return false, "write denied on " + surface
	}
	if v, ok := p.Write["*"]; ok && !v {
		return false, "write denied"
	}
	rule, enrolled := p.findClientRule(client)
	if p.RequireClients && (!enrolled || rule.Write == nil || !*rule.Write) {
		return false, "client write not granted"
	}
	if enrolled && rule.Write != nil && !*rule.Write {
		return false, "client write denied"
	}
	if !p.AllowsClientProject(client, project) {
		return false, "project outside client scope"
	}
	return true, ""
}
