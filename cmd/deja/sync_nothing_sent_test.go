package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/peers"
)

// "Last exchange just now" has to mean memory moved. A push with nothing to
// send never opens a connection, so it is not an exchange — and stamping it
// made doctor say a machine that does not exist was reached a moment ago, in
// the same line that reported the failure (#1780).
func TestNothingToSendIsNotAnExchange(t *testing.T) {
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(t.TempDir(), "peers.json"))

	if err := recordExchange("quiet.example", false, time.Now(), errNothingToSend); err != nil {
		t.Fatal(err)
	}
	list := peers.Load()
	if len(list) != 1 {
		t.Fatalf("peers = %v", list)
	}
	if !list[0].LastPush.IsZero() {
		t.Errorf("a push that sent nothing was stamped %v", list[0].LastPush)
	}
	if list[0].LastError != "" {
		t.Errorf("nothing to send is not a failure: %q", list[0].LastError)
	}

	// A real exchange still stamps, and a real failure still records.
	if err := recordExchange("quiet.example", false, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if peers.Load()[0].LastPush.IsZero() {
		t.Error("a push that sent something was not stamped")
	}
	if err := recordExchange("broken.example", false, time.Now(), errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	for _, p := range peers.Load() {
		if p.Host == "broken.example" && p.LastError == "" {
			t.Error("a failure was not recorded")
		}
	}
}

// peers.Forget was written and tested and nothing reached it: a typo'd hostname
// stayed in the list and every bare `deja sync` retried it, at the cost of the
// connect timeout each time (#1780).
func TestSyncForgetRemovesAMachine(t *testing.T) {
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(t.TempDir(), "peers.json"))
	if err := peers.Record("mini", false, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if err := runSync(t.TempDir(), []string{"forget", "mini"}); err != nil {
		t.Fatalf("forgetting a known machine: %v", err)
	}
	if list := peers.Load(); len(list) != 0 {
		t.Errorf("peers = %v", list)
	}
	err := runSync(t.TempDir(), []string{"forget", "mini"})
	if err == nil {
		t.Fatal("forgetting a machine deja does not know said nothing")
	}
	if !strings.Contains(err.Error(), "does not know") {
		t.Errorf("the refusal reads %q", err)
	}
}
