package index

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// SecretFinding is one kind of credential in one session, with where to look
// for it and never what it was.
//
// The value is not here because it is not in the index: redaction runs at
// ingest, so every one of these was read back out of a `[redacted:<kind>]`
// marker. What deja can say is that a session in a project on a date pasted
// something of a recognisable shape, and which file still holds it.
type SecretFinding struct {
	Kind    string    `json:"kind"`
	Harness string    `json:"harness"`
	ID      string    `json:"id"`
	Project string    `json:"project,omitempty"`
	Path    string    `json:"path,omitempty"`
	When    time.Time `json:"when"`
	Count   int       `json:"count"`
}

// SecretScan is what one pass over the store found.
type SecretScan struct {
	// Findings are the shapes worth naming to a person, newest first.
	Findings []SecretFinding
	// Counted is every other rule, by kind: a number at the bottom of the
	// screen rather than a list. Measured on a 2,719-session store, the
	// entropy rule alone was 1,916 of 3,929 markers and its largest visible
	// class was a WireGuard interface dump — public keys, none of it secret
	// (#3638). A list like that is wallpaper, and wallpaper is what makes a
	// person stop reading the four lines that matter (#536).
	Counted map[string]int
	// Sessions is how many sessions the findings come from, and Withheld how
	// many the ignore rule kept out of the scan.
	Sessions int
	Withheld int
}

// namedSecretKinds are the rules that name a provider or a protocol shape, and
// so are precise enough to put in front of someone.
//
// What is deliberately not here: `entropy`, `credential` and `quoted-secret`.
// Those fire on the value side of an assignment — `key=`, `token=`,
// `password:` — which is as often a variable reference as a secret. On this
// machine's store they were 3,465 markers against 82 findings below, and
// naming them would bury the list. They need a pass of their own before a
// person is told they are credentials.
//
// `password` is here, though its name reads like one of those: the rule behind
// it is the netrc shape, `machine <host> login <user> password <value>`, which
// is a file format rather than a guess about an assignment.
var namedSecretKinds = map[string]bool{
	"aws-access-key":    true,
	"aws-secret":        true,
	"aws-key":           true,
	"openai-key":        true,
	"anthropic-key":     true,
	"google-api-key":    true,
	"github-token":      true,
	"gitlab-token":      true,
	"groq-key":          true,
	"xai-key":           true,
	"npm-token":         true,
	"slack-token":       true,
	"huggingface-token": true,
	"stripe-key":        true,
	"provider-token":    true,
	"private-key":       true,
	"jwt":               true,
	"bearer-token":      true,
	"cookie":            true,
	"url-credentials":   true,
	"command-password":  true,
	"password":          true,
}

// SecretKindNamed reports whether a redaction rule is one `deja secrets` lists
// by name rather than counts.
func SecretKindNamed(kind string) bool { return namedSecretKinds[kind] }

// ScanSecrets reads every record once and reports the credential shapes the
// store's own redaction markers record.
//
// It is a records pass rather than a manifest read because the manifest counts
// redactions per file and per rule separately, and the answer needs them
// joined: a file with four markers and a store with four rules says nothing
// about which session pasted which. Measured at about 5 s on a 325k-record
// store, which is a cost a command someone typed can pay.
func ScanSecrets(dir string) (SecretScan, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return SecretScan{}, err
	}
	pol := policy.Load()
	out := SecretScan{Counted: map[string]int{}}
	// Keyed by session and kind: a session that pasted the same key into six
	// turns is one finding with a count, not six lines.
	type key struct{ session, kind string }
	seen := map[key]*SecretFinding{}
	withheld := map[string]bool{}
	err = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if !strings.Contains(r.Text, redact.Marker) {
			return
		}
		meta, ok := m.Sessions[r.Key]
		if !ok {
			return
		}
		// The same rule every other answer follows: a project the user
		// excluded stays excluded, and the screen says how many that was
		// rather than pretending the store is clean (#2630).
		if pol.Ignored(meta.Path, meta.Project) {
			withheld[r.Key] = true
			return
		}
		for _, kind := range markerKinds(r.Text) {
			if !namedSecretKinds[kind] {
				out.Counted[kind]++
				continue
			}
			k := key{r.Key, kind}
			f := seen[k]
			if f == nil {
				f = &SecretFinding{
					Kind:    kind,
					Harness: meta.Harness,
					ID:      meta.ID,
					Project: meta.Project,
					When:    meta.Updated,
				}
				// A database store has one path for every session in it, so
				// naming it as the file to look in would be a wrong answer
				// four sessions out of five. The harness is the location
				// there, and the screen says so.
				if p := meta.Path; p != "" && strings.EqualFold(filepath.Ext(p), ".jsonl") {
					f.Path = p
				}
				seen[k] = f
			}
			f.Count++
		}
	})
	if err != nil {
		return SecretScan{}, err
	}
	sessions := map[string]bool{}
	for k, f := range seen {
		out.Findings = append(out.Findings, *f)
		sessions[k.session] = true
	}
	out.Sessions = len(sessions)
	out.Withheld = len(withheld)
	sort.Slice(out.Findings, func(i, j int) bool {
		a, b := out.Findings[i], out.Findings[j]
		if !a.When.Equal(b.When) {
			return a.When.After(b.When)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	return out, nil
}

// markerKinds lists the rule names inside a record's redaction markers, one
// per marker, so a turn holding two keys counts twice.
func markerKinds(text string) []string {
	var out []string
	for i := 0; ; {
		at := strings.Index(text[i:], redact.Marker)
		if at < 0 {
			return out
		}
		i += at + len(redact.Marker)
		end := strings.IndexByte(text[i:], ']')
		if end < 0 {
			return out
		}
		kind := text[i : i+end]
		i += end + 1
		// Only a rule the redactor can write. Text can hold something that
		// merely looks like a marker — deja's own documentation of the format,
		// a test fixture, a message quoting an earlier answer — and taking
		// those at their word invented rules called `<kind>` and `…` with 17
		// and 3 hits on this store (#536).
		if !redact.IsKind(kind) {
			continue
		}
		out = append(out, kind)
	}
}
