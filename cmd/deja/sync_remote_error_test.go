package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/peers"
)

// failingRemote points sshRunner at a peer that writes hostile output and then
// fails, and returns the error the sync produced.
func failingRemote(t *testing.T, out string) error {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "peers.json"))
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"v1","cwd":"/proj","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"the retry budget question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	old := sshRunner
	t.Cleanup(func() { sshRunner = old })
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && len(args) > 0 && args[len(args)-1] == "mktemp -d" {
			return "/tmp/remote", nil
		}
		if name == "ssh" {
			return out, errors.New("exit status 1")
		}
		return "", nil
	}
	err := syncSSHHost(dir, "peer1", false, false, false)
	if err == nil {
		t.Fatal("the fixture did not fail, so there is no error to inspect")
	}
	return err
}

// #1809 bounded the peer's output where it is printed. The failure paths build
// it into an error instead, and main prints an error exactly as it is — so a
// peer that fails could still recolour and rewind this terminal, and the text
// landed in peers.json as last_error (#1819).
func TestAFailingRemoteCannotWriteOnThisTerminal(t *testing.T) {
	hostile := "deja: import failed\x1b[31m\rHACKED " + strings.Repeat("padding ", 200)
	err := failingRemote(t, hostile)

	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\r") {
		t.Errorf("the error carries an escape or a rewind: %q", msg[:min(len(msg), 90)])
	}
	if len(msg) > remoteEchoMax+200 {
		t.Errorf("the error is %d bytes of remote output", len(msg))
	}
	// The control: what the remote actually said is still in it, so the bound
	// is not simply throwing the diagnosis away.
	if !strings.Contains(msg, "import failed") {
		t.Errorf("the remote's own report is gone: %q", msg[:min(len(msg), 120)])
	}

	if rerr := recordExchange("peer1", false, time.Now(), err); rerr != nil {
		t.Fatal(rerr)
	}
	list := peers.Load()
	if len(list) == 0 {
		t.Fatal("the peer was not recorded")
	}
	if strings.ContainsAny(list[0].LastError, "\x1b\r") {
		t.Errorf("peers.json holds an escape or a rewind: %q", list[0].LastError)
	}
}

// scp fails against a machine this one does not control either, and what it
// prints is the remote's as much as deja's own output is.
func TestAFailingScpCannotWriteOnThisTerminalEither(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "peers.json"))
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"v1","cwd":"/proj","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"the retry budget question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	old := sshRunner
	t.Cleanup(func() { sshRunner = old })
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "scp" {
			return "scp: permission denied\x1b[31m\rHACKED", errors.New("exit status 1")
		}
		if name == "ssh" && len(args) > 0 && args[len(args)-1] == "mktemp -d" {
			return "/tmp/remote", nil
		}
		return "", nil
	}
	err := syncSSHHost(dir, "peer1", false, false, false)
	if err == nil {
		t.Skip("this fixture never reaches the scp path")
	}
	if strings.ContainsAny(err.Error(), "\x1b\r") {
		t.Errorf("an scp failure carried an escape or a rewind: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "denied") && !strings.Contains(err.Error(), "nothing new") {
		t.Errorf("what the remote said is gone: %q", err.Error())
	}
}
