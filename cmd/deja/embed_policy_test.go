package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// The policy behind #1311: the owner's own terminal is theirs, the channels
// they lock down are the ones an agent speaks on. Embedding borrowed the
// loosest of the three, so a machine refused to show a session to its own agent
// and shipped the same text to a third party.
const egressPolicy = `{"activations":{"search":{"local":true},"mcp":{"local":false},"auto":{"local":false}}}`

func TestEgressNeedsEveryPathToAgree(t *testing.T) {
	hermeticEnv(t)
	writePolicy(t, egressPolicy)
	pol := policy.Load()
	if !pol.Allows(policy.ActivationSearch, "myapp") {
		t.Fatal("the fixture does not reproduce the policy: search denies a local project")
	}
	if pol.AllowsEgress("myapp") {
		t.Error("content withheld from the agent on this machine is allowed off it")
	}
}

func TestEgressAllowsWhatEveryPathAllows(t *testing.T) {
	hermeticEnv(t)
	writePolicy(t, `{"activations":{"search":{"local":true},"mcp":{"local":true},"auto":{"local":true}}}`)
	if !policy.Load().AllowsEgress("myapp") {
		t.Error("a project every rule allows was held back from embedding")
	}
}

// The filter embed actually runs, over a real index, plus the count it reports:
// a sidecar that covers less than the index is worth a line, or semantic search
// quietly answers from less than the user thinks.
func TestEmbedKeepHonoursEveryActivation(t *testing.T) {
	storeWith(t, false)
	writePolicy(t, egressPolicy)
	dir := index.DefaultDir()
	keep, held, err := embedPolicyKeep(dir)
	if err != nil {
		t.Fatal(err)
	}
	if keep == nil {
		t.Fatal("no filter built for a readable index")
	}
	offs, err := index.ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	var recs []index.Record
	for _, o := range offs {
		recs = append(recs, o.Record)
	}
	if len(recs) == 0 {
		t.Fatal("the fixture indexed nothing, so the filter is never asked")
	}
	for _, r := range recs {
		if keep(r) {
			t.Errorf("record %q from a project the policy withholds was sent to the endpoint", r.Key)
		}
	}
	if held() != len(recs) {
		t.Errorf("reported %d records held back, kept %d out of %d", held(), len(recs)-held(), len(recs))
	}
}

// An unreadable manifest is the one moment nothing can be checked, and a nil
// filter means "embed everything". The gate fails closed instead.
func TestEmbedRefusesWhenItCannotTellWhatIsLocal(t *testing.T) {
	storeWith(t, false)
	writePolicy(t, egressPolicy)
	dir := index.DefaultDir()
	if err := os.Remove(filepath.Join(dir, "manifest.gob")); err != nil {
		t.Fatal(err)
	}
	keep, _, err := embedPolicyKeep(dir)
	if err == nil {
		t.Error("embed built a filter without knowing which sessions are local")
	}
	if keep != nil {
		t.Error("a filter was returned on the failure path, and nil there means embed everything")
	}
}
