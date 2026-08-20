package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// rolesIndex lays down one session whose speech and whose work records say
// different things, so a result can be traced to which of the two carried it.
func rolesIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := []model.Session{{
		ID: "a", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "the deploy pipeline kept timing out on the staging cluster"},
			{Role: "assistant", Text: "raised the readiness probe budget and it settled"},
			{Role: roleCommand, Text: "kubectl rollout restart deployment/frobnicator"},
			{Role: roleFiles, Text: "charts/frobnicator/values.yaml"},
		},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The relevance tier never scans records, so it served every record of every
// session it ranked. Exact search hides file lists and commands on purpose — a
// path is a note of what a turn touched, not something said — and the two paths
// disagreed about the same session.
func TestRelevanceDoesNotServeWorkRecords(t *testing.T) {
	dir := rolesIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{Query: "frobnicator values rollout", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Sessions {
		for _, msg := range s.Messages {
			if msg.Role == roleCommand || msg.Role == roleFiles {
				t.Errorf("relevance served a %s record: %q", msg.Role, msg.Text)
			}
		}
	}
}

// And with an explicit role, the other side of the conversation is not an
// answer to it.
func TestRelevanceHonoursTheRequestedRole(t *testing.T) {
	dir := rolesIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{
		Query: "readiness probe budget settled", Role: "user", All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Sessions {
		for _, msg := range s.Messages {
			if msg.Role != "user" {
				t.Errorf("--role=user returned a %s message: %q", msg.Role, msg.Text)
			}
		}
	}
}

// The same rule reaches the auto-recall path, which loads sessions the same way
// and had the same hole: a hook that pastes a file list into the model's
// context is pasting the wrong thing.
func TestProjectRelevantDoesNotServeWorkRecords(t *testing.T) {
	dir := rolesIndex(t)
	ss, _, _, _, err := ProjectRelevant(dir, []string{"p"}, []string{"frobnicator", "values", "rollout"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		for _, msg := range s.Messages {
			if msg.Role == roleCommand || msg.Role == roleFiles {
				t.Errorf("auto-recall served a %s record: %q", msg.Role, msg.Text)
			}
		}
	}
}

// Nothing above should have cost the ordinary answer.
func TestSpeechStillComesBack(t *testing.T) {
	dir := rolesIndex(t)
	result, err := SearchDetailed(dir, search.Options{Query: "deploy pipeline timing out", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("the session that answers this in plain speech did not come back")
	}
}
