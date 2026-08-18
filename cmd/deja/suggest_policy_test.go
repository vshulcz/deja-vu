package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// recentImportedStore is importedStore with dates and topics a suggestion can
// actually be made from: the phrase picker looks at the last two months only,
// and takes a phrase whose two words each recur across sessions.
func recentImportedStore(t *testing.T, n int) string {
	t.Helper()
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	topics := []string{
		"kafka consumer rebalance keeps flapping",
		"vault rotation broke staging deploy",
		"scheduler retries twice every timeout",
		"cache eviction wiped session store",
	}
	var batch []byte
	for i := 0; i < n; i++ {
		b, err := json.Marshal(index.SyncRecord{
			Harness: "claude", SessionID: "peer" + string(rune('1'+i)), Project: "tmp/projx",
			Role: "user", Text: topics[i%len(topics)], Time: now.AddDate(0, 0, -i-1),
		})
		if err != nil {
			t.Fatal(err)
		}
		batch = append(append(batch, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The suggestion after a first index is "a phrase from your own history",
// printed to argue that the memory is worth having. On a machine whose history
// arrived by sync, it was lifting that phrase out of sessions the trust policy
// withholds from every other surface.
func TestSuggestHonoursThePolicy(t *testing.T) {
	dir := recentImportedStore(t, 8)
	if got := suggestFirstQuery(dir); got == "" {
		t.Fatal("no suggestion without a policy, so the test measures nothing")
	}
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false},"auto":{"local":true,"imported":false}}}`)
	if got := suggestFirstQuery(dir); got != "" {
		t.Errorf("suggested %q, a phrase from sessions the policy withholds", got)
	}
}

// A rule that allows the sessions leaves the suggestion in place: the screen
// is there to show someone their own history back.
func TestSuggestStillSpeaksWhenAllowed(t *testing.T) {
	dir := recentImportedStore(t, 8)
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":true}}}`)
	if got := suggestFirstQuery(dir); got == "" {
		t.Error("no suggestion though the policy allows these sessions")
	}
}
