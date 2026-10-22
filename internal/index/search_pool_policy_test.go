package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The pool was drawn before the trust policy ran. Sessions it withholds
// took pool slots from ones the page could show, and the ones left unread
// were counted as matches, so the total named sessions no page would ever
// serve (#5488). Under Options.Activation the pool leaves them out and says
// how many.
func TestCommonWordPoolLeavesOutWhatThePolicyWithholds(t *testing.T) {
	tmp := t.TempDir()
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"mcp":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	dir := filepath.Join(tmp, "index.db")
	now := time.Now()
	imported, local := search.PoolSize(15)+200, search.PoolSize(15)+300
	ss := make([]model.Session, 0, imported+local)
	// The imported sessions are newer and say the word more often, so a pool
	// drawn without the policy is made of them.
	for i := range imported + local {
		project, at, says := "shop", now.Add(-time.Duration(30+i)*time.Hour), 1
		if i < imported {
			project, at, says = "imported:peer/shop", now.Add(-time.Duration(i)*time.Minute), 4
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: project, Started: at, Updated: at,
			Messages: []model.Message{{Role: "user", Text: strings.Repeat("deploy ", says) + fmt.Sprintf("service %d", i), Time: at}}})
	}
	writeTestStore(t, dir, ss...)

	// The control: without the activation the pool is drawn as before, and
	// the imported sessions fill it.
	res, err := SearchDetailed(dir, query.Options{Query: "deploy", Window: 15})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil || countImported(res.Sessions) == 0 {
		t.Fatalf("control: the pool held no imported session (%d read)", len(res.Sessions))
	}

	res, err = SearchDetailed(dir, query.Options{Query: "deploy", Window: 15, Activation: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unread == nil {
		t.Fatalf("read all %d sessions", len(res.Sessions))
	}
	if n := countImported(res.Sessions); n > 0 {
		t.Errorf("the pool read %d sessions the policy withholds on mcp", n)
	}
	if got := len(res.Sessions) + res.Unread.Sessions; got != local {
		t.Errorf("read and unread add up to %d, want the %d the policy allows", got, local)
	}
	if res.Unread.Withheld != imported {
		t.Errorf("withheld %d, want %d", res.Unread.Withheld, imported)
	}
}

func countImported(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		if strings.HasPrefix(s.Project, "imported:") {
			n++
		}
	}
	return n
}
