package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
)

// `deja secrets` says which credentials an agent history is carrying.
//
// deja redacts on the way in, which means it has always known where the values
// were and has never said so. The transcripts themselves are the least guarded
// place in a developer's setup: plain files under a home directory, in five
// formats, synced by IDEs, swept into backups, and scanned by nothing (#536).
//
// The report is deliberately short, and grouped by session rather than by hit:
// one session pasting one database URL into ten turns is one thing to act on.
// Only the rules that name a provider or a protocol shape are listed; the
// assignment-shaped rules and the entropy catch-all are a count at the bottom,
// because on this machine's store they were 3,465 markers against 82 findings,
// and their largest visible class was a WireGuard dump of public keys. A list
// nobody can act on is what stops the lines that matter from being read.
//
// The value is never printed, and cannot be: it is not in the index.

// secretsDefaultLimit is how many sessions the screen shows before it says how
// many more there are. --limit raises it; --limit 0 prints all of them.
const secretsDefaultLimit = 10

type secretsJSON struct {
	Kind     string                `json:"kind"`
	Schema   int                   `json:"schema_version"`
	Findings []index.SecretFinding `json:"findings"`
	Counted  map[string]int        `json:"counted,omitempty"`
	Sessions int                   `json:"sessions"`
	Withheld int                   `json:"withheld,omitempty"`
}

const secretsJSONKind = "deja.secrets"

func runSecrets(dir string, args []string, stdout io.Writer) error {
	asJSON := false
	limit := secretsDefaultLimit
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--limit":
			if i+1 >= len(args) {
				return fmt.Errorf("secrets: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return fmt.Errorf("secrets: --limit wants a number, got %q", args[i])
			}
			limit = n
		default:
			return fmt.Errorf("secrets: unknown flag %q — it takes --limit n and --json", args[i])
		}
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	scan, err := index.ScanSecrets(dir)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		// Every finding, not the screen's page: a consumer asked for the data.
		// An empty list rather than null, so iterating it needs no special
		// case for a clean machine.
		findings := scan.Findings
		if findings == nil {
			findings = []index.SecretFinding{}
		}
		return enc.Encode(secretsJSON{
			Kind: secretsJSONKind, Schema: jsonout.Version,
			Findings: findings, Counted: scan.Counted,
			Sessions: scan.Sessions, Withheld: scan.Withheld,
		})
	}
	printSecrets(stdout, scan, limit)
	return nil
}

// secretsGroup is one session's findings, which is the unit a person acts on.
type secretsGroup struct {
	head  index.SecretFinding
	kinds []index.SecretFinding
}

func groupSecrets(findings []index.SecretFinding) []secretsGroup {
	var out []secretsGroup
	at := map[string]int{}
	for _, f := range findings {
		key := f.Harness + ":" + f.ID
		i, ok := at[key]
		if !ok {
			at[key] = len(out)
			out = append(out, secretsGroup{head: f})
			i = len(out) - 1
		}
		out[i].kinds = append(out[i].kinds, f)
	}
	for i := range out {
		sort.SliceStable(out[i].kinds, func(a, b int) bool {
			ka, kb := out[i].kinds[a], out[i].kinds[b]
			if ka.Count != kb.Count {
				return ka.Count > kb.Count
			}
			return ka.Kind < kb.Kind
		})
	}
	return out
}

func printSecrets(w io.Writer, scan index.SecretScan, limit int) {
	if len(scan.Findings) == 0 {
		fmt.Fprintln(w, "no provider keys, private keys or connection strings in your agent history")
		printSecretsTail(w, scan)
		return
	}
	groups := groupSecrets(scan.Findings)
	fmt.Fprintf(w, "%d credential%s in your agent history, in %d session%s\n",
		len(scan.Findings), pluralS(len(scan.Findings)), len(groups), pluralS(len(groups)))
	shown := groups
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, g := range shown {
		where := g.head.Harness
		if !g.head.When.IsZero() {
			where += " · " + g.head.When.Local().Format("2006-01-02")
		}
		if p := g.head.Project; p != "" && p != "-" {
			where += " · " + p
		}
		fmt.Fprintf(w, "\n  %s\n", where)
		fmt.Fprintf(w, "    %s\n", secretsKindList(g.kinds))
		// The file is the actionable half, and only a file-based store has
		// one. Printing a database path would send someone to a 3.5 GB file
		// holding fifteen hundred other sessions.
		if g.head.Path != "" {
			fmt.Fprintf(w, "    %s\n", reportPath(g.head.Path))
		}
	}
	if n := len(groups) - len(shown); n > 0 {
		fmt.Fprintf(w, "\n  %d more session%s — `deja secrets --limit 0` for all of them, `--json` for the whole list\n",
			n, pluralS(n))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "these values are in the source transcripts, not in deja's copy — deja redacted its own.")
	fmt.Fprintln(w, "rotate the live ones; the files above are yours to edit or delete.")
	// The rows with no file are the ones the closing line does not cover, and
	// saying nothing left them looking like rows deja simply knew less about.
	// A database store holds every session in one file, so editing it is not
	// the answer — the harness's own history screen is. Measured on this
	// machine's store, 12 of the 41 sessions the report lists are in one, and
	// they carry 36 of the 83 findings (#3823).
	if n := groupsWithoutAFile(shown); n > 0 {
		fmt.Fprintf(w, "%d of the sessions above keep their history in a database with every other session in it, so there is no file to edit — clear those in the harness itself.\n", n)
	}
	printSecretsTail(w, scan)
}

// groupsWithoutAFile counts the shown sessions whose store is a database, which
// is exactly the set the closing line about files does not speak for.
func groupsWithoutAFile(groups []secretsGroup) int {
	n := 0
	for _, g := range groups {
		if g.head.Path == "" {
			n++
		}
	}
	return n
}

// secretsKindList names a session's kinds, biggest first, with the number of
// turns each was pasted into.
func secretsKindList(kinds []index.SecretFinding) string {
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if k.Count > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", k.Kind, k.Count))
			continue
		}
		parts = append(parts, k.Kind)
	}
	return strings.Join(parts, ", ")
}

func printSecretsTail(w io.Writer, scan index.SecretScan) {
	if n := secretsCountedTotal(scan.Counted); n > 0 {
		// Counted, not listed, and never called credentials: these rules fire
		// on the shape of an assignment rather than on the shape of a key, so
		// most of them are digests, lockfile hashes and identifiers.
		fmt.Fprintf(w, "\n%d more redacted string%s came from the assignment and entropy rules (%s) — counted, not listed, because most are digests and identifiers\n",
			n, pluralS(n), secretsCountedSummary(scan.Counted))
	}
	if scan.Withheld > 0 {
		fmt.Fprintf(w, "the ignore rule kept %d session%s out of this scan\n", scan.Withheld, pluralS(scan.Withheld))
	}
}

func secretsCountedTotal(counted map[string]int) int {
	n := 0
	for _, c := range counted {
		n += c
	}
	return n
}

// secretsCountedSummary names the rules behind that number, biggest first, so
// the count can be checked rather than believed.
func secretsCountedSummary(counted map[string]int) string {
	kinds := make([]string, 0, len(counted))
	for k := range counted {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if counted[kinds[i]] != counted[kinds[j]] {
			return counted[kinds[i]] > counted[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	if len(kinds) > 3 {
		kinds = kinds[:3]
	}
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d", k, counted[k]))
	}
	return strings.Join(parts, ", ")
}
