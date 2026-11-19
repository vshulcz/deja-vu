package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/peers"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Naming the host every time is what makes a three-machine setup six commands
// and a thing to remember. One exchange is enough for deja to know a machine.
func TestSyncRemembersAHostItHasTalkedTo(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = fakeSSHOK()

	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), []string{"ssh", "mini"}); err != nil {
		t.Fatal(err)
	}
	list := peers.Load()
	if len(list) != 1 || list[0].Host != "mini" {
		t.Fatalf("the host was not remembered: %+v", list)
	}
	if list[0].LastPush.IsZero() {
		t.Error("a push was not recorded")
	}
}

// A failed exchange is recorded too, or a peer that has been broken for a week
// looks exactly like one with nothing to send.
func TestSyncRecordsAFailedExchange(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(_ string, _ ...string) (string, error) {
		return "no route to host", errors.New("exit status 255")
	}
	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), []string{"ssh", "mini"}); err == nil {
		t.Fatal("an unreachable host was reported as success")
	}
	list := peers.Load()
	if len(list) != 1 {
		t.Fatalf("the host was not remembered: %+v", list)
	}
	if list[0].LastError == "" {
		t.Error("the failure was not recorded")
	}
	if !list[0].Last().IsZero() {
		t.Error("a failed exchange was recorded as memory having moved")
	}
}

func TestBareSyncSaysWhatToDoWithNoPeers(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	// No peers is a state, not a failure: exit 0, and the line says how to
	// add one.
	var err error
	said := captureStderr(t, func() { err = runSync(os.Getenv("DEJA_INDEX_DIR"), nil) })
	if err != nil {
		t.Fatalf("a sync with no peers failed: %v", err)
	}
	if !strings.Contains(said, "deja sync ssh") {
		t.Errorf("the line does not say how to add one: %q", said)
	}
}

// The point of the bare form: every machine, both ways, without being told.
func TestBareSyncExchangesWithEveryPeerBothWays(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	for _, h := range []string{"mini", "laptop"} {
		if err := peers.Record(h, false, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	old := sshRunner
	defer func() { sshRunner = old }()
	var hosts []string
	var pulls, pushes int
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && len(args) > 1 {
			hosts = append(hosts, sshHostArg(args))
			switch {
			case strings.Contains(args[len(args)-1], "sync export"):
				pulls++
				return "deja: exported 0 records", nil
			case strings.Contains(args[len(args)-1], "sync import"):
				pushes++
				return "deja: imported 0 records", nil
			}
		}
		if args[len(args)-1] == "mktemp -d" || (len(args) > 1 && args[len(args)-1] == "mktemp -d") {
			return "/tmp/remote-out", nil
		}
		return "", nil
	}
	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mini", "laptop"} {
		found := false
		for _, h := range hosts {
			if h == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was never contacted: %v", want, hosts)
		}
	}
	if pulls == 0 {
		t.Error("nothing was pulled, so this machine only ever sends")
	}
}

// One unreachable laptop must not stop the server from getting what the
// desktop did.
func TestBareSyncKeepsGoingPastAnUnreachableMachine(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	for _, h := range []string{"broken", "mini"} {
		if err := peers.Record(h, false, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	old := sshRunner
	defer func() { sshRunner = old }()
	reached := map[string]bool{}
	ok := fakeSSHOK()
	sshRunner = func(name string, args ...string) (string, error) {
		if len(args) > 0 {
			reached[sshHostArg(args)] = true
			if sshHostArg(args) == "broken" {
				return "no route to host", errors.New("exit status 255")
			}
		}
		return ok(name, args...)
	}
	if err := runSync(os.Getenv("DEJA_INDEX_DIR"), nil); err != nil {
		t.Fatalf("one broken machine failed the whole sync: %v", err)
	}
	if !reached["mini"] {
		t.Error("mini was skipped because broken came first")
	}
}

func TestPeerLineNamesWhatIsWrong(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		p    peers.Peer
		want []string
	}{
		{"a healthy peer",
			peers.Peer{Host: "mini", LastPush: now.Add(-2 * time.Hour), LastPull: now.Add(-2 * time.Hour)},
			[]string{"2 hours ago"}},
		{"one that takes but never sends",
			peers.Peer{Host: "mini", LastPush: now.Add(-time.Hour)},
			[]string{"nothing received yet"}},
		{"one this machine never sent to",
			peers.Peer{Host: "mini", LastPull: now.Add(-time.Hour)},
			[]string{"nothing sent yet"}},
		{"one that has been failing",
			peers.Peer{Host: "mini", LastPush: now.Add(-8 * 24 * time.Hour), LastPull: now.Add(-8 * 24 * time.Hour), LastError: "no route to host"},
			[]string{"8 days ago", "no route to host"}},
		{"one never reached at all",
			peers.Peer{Host: "mini"},
			[]string{"never exchanged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := peerLine(tc.p, 0, now)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("peer line %q does not say %q", got, want)
				}
			}
		})
	}
	// The count answers "did anything ever actually arrive from there".
	if got := peerLine(peers.Peer{Host: "mini", LastPull: now}, 41, now); !strings.Contains(got, "41 sessions from there") {
		t.Errorf("the line does not say how much came from there: %q", got)
	}
}

func TestDoctorReportsTheMachinesItSyncsWith(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_PEERS_FILE", os.Getenv("DEJA_INDEX_DIR")+"-peers.json")
	var buf bytes.Buffer
	doctorPeers(&buf, os.Getenv("DEJA_INDEX_DIR"), time.Now())
	if !strings.Contains(buf.String(), "deja sync ssh") {
		t.Errorf("with no peers, doctor does not say how to add one:\n%s", buf.String())
	}

	if err := peers.Record("mini", false, time.Now().Add(-6*24*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorPeers(&buf, os.Getenv("DEJA_INDEX_DIR"), time.Now())
	out := buf.String()
	for _, want := range []string{"Sync:", "mini", "6 days ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// fakeSSHOK is a remote that answers every step of a push and a pull.
func fakeSSHOK() func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		if name != "ssh" || len(args) < 2 {
			return "", nil
		}
		switch {
		case args[len(args)-1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case strings.Contains(args[len(args)-1], "sync export"):
			return "deja: exported 0 records", nil
		case strings.Contains(args[len(args)-1], "sync import"):
			return "deja: imported 0 records", nil
		}
		return "", nil
	}
}

// The Sync format section described the watermark as it was before #982 — per
// source path — while it has been per peer and source since, and folded by
// peers.Identity since #1878. The rules that decide whether two exports are for
// the same machine were written down in Go comments and nowhere a reader would
// look (#1915).
//
// A prose check rather than a key check: this section has no keys, and what it
// has to get right is which of the two names a watermark is filed under.
func TestTheSyncSectionDescribesTheWatermarkItHas(t *testing.T) {
	doc, err := os.ReadFile("../../docs/ARCHITECTURE.md")
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(doc, []byte("## Sync format"))
	if i < 0 {
		t.Fatal("docs/ARCHITECTURE.md no longer has a Sync format section")
	}
	section := string(doc)[i:]
	if end := strings.Index(section[3:], "\n## "); end > 0 {
		section = section[:end+3]
	}
	prose := strings.Join(strings.Fields(section), " ")

	for _, want := range []struct{ what, phrase string }{
		{"the watermark is per peer as well as per source", "per peer"},
		{"the peers file is named", "peers.json"},
		{"one machine, whatever case its name is typed in", "case"},
		{"a pull learns what the machine calls itself", "learns"},
	} {
		if !strings.Contains(prose, want.phrase) {
			t.Errorf("the Sync format section does not say %s (looked for %q)", want.what, want.phrase)
		}
	}
	if strings.Contains(prose, "The export watermark is per source path (falling back") {
		t.Error("the section still describes the pre-#982 watermark")
	}
}

// The last write path still handing back a syscall: `open …/deja-sync-3e5cfa…
// .jsonl: permission denied` names a file deja was about to create, with a
// name nobody chose (#893).
func TestSyncExportIntoAnUnwritableDirectorySaysWhatToFix(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "export")
	if err := os.MkdirAll(out, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(out, 0o700) })

	_, err := captureRun(t, "sync", "export", "--full", out)
	if err == nil {
		t.Fatal("an export into an unwritable directory succeeded")
	}
	got := err.Error()
	if !strings.Contains(got, "cannot write the export into "+out) {
		t.Errorf("does not name the directory: %q", got)
	}
	if strings.Contains(got, "deja-sync-") {
		t.Errorf("names a file deja was about to create: %q", got)
	}

	// And the refusal left the watermarks alone: everything is still owed.
	if err = os.Chmod(out, 0o700); err != nil {
		t.Fatal(err)
	}
	n, err := index.Export(os.Getenv("DEJA_INDEX_DIR"), out)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("the failed export moved the watermark")
	}
}

// The export watermark is saved into the index, so an index that cannot be
// written fails the same call — and was reported against the destination,
// which was writable and already held the batch (#1046).
func TestSyncExportNamesTheIndexWhenTheIndexIsWhatIsDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Block the one file the manifest is written through, leaving the index
	// directory and the destination as writable as they were.
	idx := os.Getenv("DEJA_INDEX_DIR")
	blocked := filepath.Join(idx, "manifest.gob.tmp")
	if err := os.WriteFile(blocked, []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(blocked) })

	out := filepath.Join(tmp, "export")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := captureRun(t, "sync", "export", "--full", out)
	if err == nil {
		t.Fatal("an export with an unwritable index succeeded")
	}
	got := err.Error()
	if strings.Contains(got, "cannot write the export into "+out) {
		t.Errorf("blames the destination, which is writable: %q", got)
	}
	if !strings.Contains(got, "cannot write the index at "+idx) {
		t.Errorf("does not name the index: %q", got)
	}
	// The batch is on disk; saying nothing about it reads as "nothing left".
	entries, rerr := os.ReadDir(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was written into the destination, so the message is about the wrong half")
	}
	if !strings.Contains(got, "are written into "+out) {
		t.Errorf("does not say the batch is already there: %q", got)
	}
}

// The same store one step earlier: an index deja cannot rebuild stops the
// export before a record is read, and handed back the syscall for a temporary
// directory nobody chose (#1046).
func TestSyncExportNamesTheIndexWhenTheRebuildIsDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	idx := filepath.Join(tmp, "store", "idx")
	t.Setenv("DEJA_INDEX_DIR", idx)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// A transcript rewritten under the index, and no room to write the new one:
	// the rebuild swaps directories inside the parent. A file that only appeared
	// or grew is appended inside the index directory since #3500, which the
	// parent's mode still allows — a rewind is what forces the swap.
	rewound := `{"type":"user","message":{"role":"user","content":"the topping lift block is seized"},"timestamp":"2026-07-02T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rewound+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(idx)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	out := filepath.Join(tmp, "export")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "sync", "export", "--full", out)
	if err == nil {
		t.Fatal("an export with an unbuildable index succeeded")
	}
	got := err.Error()
	if !strings.Contains(got, "cannot write the index at "+idx) {
		t.Errorf("does not name the index: %q", got)
	}
	if strings.Contains(got, ".tmp") || strings.Contains(got, "mkdir ") {
		t.Errorf("hands back the syscall for an internal path: %q", got)
	}
}

// The CLI wrapper has its own family of sentences naming the export directory,
// and they printed the path raw while the index package's five did not (#1857).
func TestTheExportSentencesAreSafeToPrint(t *testing.T) {
	tmp := hermeticEnv(t)
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

	// A parent that cannot be created because a file sits where it would go:
	// MkdirAll fails, and the sentence names the path.
	blocker := filepath.Join(tmp, "gone\x1b[31mHACK\rrewound") //nolint:gocritic // one name with control bytes, no separator
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Skipf("the filesystem refused the name: %v", err)
	}
	odd := filepath.Join(blocker, "batch")
	err := runSync(dir, []string{"export", odd})
	if err == nil {
		t.Fatal("exporting into a missing parent was accepted")
	}
	if strings.ContainsAny(err.Error(), "\x1b\r") {
		t.Errorf("the sentence carries an escape or a rewind: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "batch") {
		t.Errorf("the sentence no longer names the directory: %q", err.Error())
	}
}

// The ssh push settles watermarks only after the remote acknowledges the batch
// (#215), and namespaces them per peer (#982). `index.Export` does neither — it
// advances the unnamed watermark as it writes. Nothing on the ssh path may
// reach for it: `exportBatches` did, kept alive by one test line for a year
// after its caller went away (#1891), and wiring it back would have undone both
// rules with nothing on any surface saying so.
func TestTheSSHPathTakesNoUnnamedWatermark(t *testing.T) {
	src, err := os.ReadFile("sync_ssh.go")
	if err != nil {
		t.Fatal(err)
	}
	// index.Export( exactly — not ExportFull, which ignores watermarks by
	// design, nor ExportDeferred, which is the acknowledged-delivery form.
	unnamed := regexp.MustCompile(`index\.Export\(`)
	if m := unnamed.FindString(string(src)); m != "" {
		t.Errorf("the ssh path exports under the unnamed watermark: %s", m)
	}
}

// A dropped flag fell back to the incremental path, which on a second run has
// nothing left to send: `--ful` exported zero records into an empty directory
// while the reader believed they had carried their whole memory across (#745).
func TestSyncExportRejectsUnknownFlags(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry budget"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, flag := range []string{"--ful", "--fully", "-full", "--json"} {
		_, err := captureRun(t, "sync", "export", filepath.Join(tmp, "out-"+flag), flag)
		if err == nil {
			t.Errorf("%s was accepted", flag)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: %v", flag, err)
		}
	}

	// The real flag still works, and so does a plain export.
	if _, err := captureRun(t, "sync", "export", filepath.Join(tmp, "ok-full"), "--full"); err != nil {
		t.Fatalf("--full: %v", err)
	}
	if _, err := captureRun(t, "sync", "export", filepath.Join(tmp, "ok-plain")); err != nil {
		t.Fatalf("plain: %v", err)
	}
	// A stray argument to import is a mistake too — it takes one directory.
	if _, err := captureRun(t, "sync", "import", filepath.Join(tmp, "ok-full"), "extra"); err == nil ||
		!strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("import with extra arg: %v", err)
	}
	if _, err := captureRun(t, "sync", "import", filepath.Join(tmp, "ok-full")); err != nil {
		t.Errorf("import: %v", err)
	}
}

// hostileHostsFile writes a peers file holding a host with an escape byte and
// one long enough to fill a screen, and points deja at it.
func hostileHostsFile(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peers.json")
	body, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": "build\x1b[31mALERT\x1b[0m\rrewound", "last_push": time.Now().Add(-48 * time.Hour).Format(time.RFC3339)},
		{"host": strings.Repeat("w", 300), "last_push": time.Now().Add(-72 * time.Hour).Format(time.RFC3339)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_PEERS_FILE", path)
}

// A host name is printed on the screen someone reads when sync is misbehaving;
// an escape byte in it rewrites that screen (#1808).
func TestTheDoctorDoesNotHandTheTerminalAPeerName(t *testing.T) {
	hermeticEnv(t)
	hostileHostsFile(t)
	var b strings.Builder
	doctorPeers(&b, t.TempDir(), time.Now())
	out := b.String()
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("the doctor's Sync section carried an escape or a rewind:\n%q", out)
	}
	if !strings.Contains(out, "ALERT") {
		t.Errorf("the host deja is talking to is gone from the line:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) > 400 {
			t.Errorf("one peer line is %d bytes:\n%s", len(line), line[:120])
		}
	}
}

// The name is bounded on screen and nowhere else: ssh is still handed the host
// as written, and `deja sync forget` still matches on it.
func TestForgetStillMatchesTheHostAsWritten(t *testing.T) {
	hermeticEnv(t)
	hostileHostsFile(t)
	long := strings.Repeat("w", 300)
	out, err := captureRunStderr(t, "sync", "forget", long)
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	for _, p := range peersLoadForTest() {
		if p.Host == long {
			t.Errorf("forget did not match the host it was given in full")
		}
	}
}

func peersLoadForTest() []peers.Peer { return peers.Load() }

// The other machine's deja writes on this screen, and it is a machine this one
// does not control: its output reached the terminal raw.
func TestRemoteOutputCannotRewriteTheLocalScreen(t *testing.T) {
	hostile := "deja: exported 3 records\x1b[31m\rHACKED" + strings.Repeat(" padding", 400)
	got := remoteOutputForEcho(hostile)
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("remote output carried an escape or a rewind: %q", got[:60])
	}
	if len(got) > remoteEchoMax+8 {
		t.Errorf("remote output printed %d bytes", len(got))
	}
	if !strings.Contains(got, "exported 3 records") {
		t.Errorf("the report itself was lost: %q", got[:60])
	}
	if short := remoteOutputForEcho("deja: exported 3 records"); short != "deja: exported 3 records" {
		t.Errorf("an ordinary report was altered: %q", short)
	}
}

// An imported record keeps the sender's offset. The lines that prove the
// memory arrived formatted it as it came, so a batch from +14 was dated a day
// ahead of what `last` said on the same machine seconds later (#1050).
func TestSyncImportProofDatesInTheReadersZone(t *testing.T) {
	stamp := time.Date(2026, 8, 6, 14, 45, 0, 0, time.UTC)
	local := stamp.Local().Format("Jan 2")
	var sender *time.Location
	for _, offset := range []int{14 * 3600, -11 * 3600} {
		if zone := time.FixedZone("sender", offset); stamp.In(zone).Format("Jan 2") != local {
			sender = zone
			break
		}
	}
	if sender == nil {
		t.Skip("no sender zone here dates that instant differently from the reader")
	}
	sent := stamp.In(sender).Format("Jan 2")

	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := `{"harness":"deja","session_id":"deja-2026-08-06-boat","project":"boat","role":"user",` +
		`"text":"the forward hatch gasket has gone hard","time":"` + stamp.In(sender).Format(time.RFC3339Nano) + `"}`
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-aaaa-1.jsonl"), []byte(rec+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "sync", "import", batch)
	if err != nil {
		t.Fatal(err)
	}
	var proof string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "imported:boat") {
			proof = line
		}
	}
	if proof == "" {
		t.Fatalf("no line proving the import arrived:\n%s", out)
	}
	if strings.Contains(proof, sent) {
		t.Errorf("dated in the sender's zone (%s), not the reader's (%s): %q", sent, local, proof)
	}
	if !strings.Contains(proof, local) {
		t.Errorf("does not carry the reader's date %s: %q", local, proof)
	}
}

// A batch of nothing but tool output imported 0 records under
// DEJA_INDEX_TOOL_OUTPUT=0, which read as an empty or already-synced batch
// (#5425). The line names the switch that kept them out.
func TestSyncImportSaysASwitchKeptRecordsOut(t *testing.T) {
	hermeticEnv(t)
	for _, k := range []string{"DEJA_INDEX_COMMANDS", "DEJA_INDEX_PATHS", "DEJA_INDEX_EDITS", "DEJA_INDEX_WRITES", "DEJA_INCLUDE_SUBAGENTS"} {
		t.Setenv(k, "")
	}
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	batch := t.TempDir()
	line := `{"harness":"claude","session_id":"s1","project":"work/app","role":"tool-output","text":"zonkoerror: overflow","time":"2026-08-04T12:00:00Z","origin":"devbox"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	out := captureStdout(t, func() { err = runSync(index.DefaultDir(), []string{"import", batch}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 record left out — switched off on this machine (DEJA_INDEX_TOOL_OUTPUT=0); to take them, unset it and import the batch again") {
		t.Errorf("the switched-off record is not reported:\n%s", out)
	}

	// Control: with the switch on the same record lands and no line is printed.
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	out = captureStdout(t, func() { err = runSync(index.DefaultDir(), []string{"import", batch}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "switched off") || !strings.Contains(out, "imported 1 record") {
		t.Errorf("with the switch on:\n%s", out)
	}
}

// A pull has moved the peer's watermark past what a switch left out, so the
// line on that path names the full pull that sends it again.
func TestSwitchedOffLineNamesTheFullPullForAHost(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	batch := t.TempDir()
	line := `{"harness":"claude","session_id":"s1","project":"work/app","role":"command","text":"go test ./...","time":"2026-08-04T12:00:00Z","origin":"devbox"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(index.DefaultDir(), batch); err != nil {
		t.Fatal(err)
	}
	if got := switchedOffLine("devbox"); !strings.Contains(got, "deja sync ssh devbox --pull --full") {
		t.Errorf("the pull's line: %q", got)
	}
}

// A batch emptied by the harness exclude or the ignore rule imported 0 records
// with no word of why (#5435).
func TestSyncImportSaysAHarnessOrIgnoreRuleKeptRecordsOut(t *testing.T) {
	home := hermeticEnv(t)
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode")
	pol := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)
	if err := os.WriteFile(pol, []byte(`{"ignore":["*/scratch/*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	lines := `{"harness":"opencode","session_id":"o1","project":"work/app","role":"user","text":"zorbleflux one","time":"2026-08-04T12:00:00Z","origin":"mini"}` + "\n" +
		`{"harness":"claude","session_id":"c1","project":"/srv/scratch/ci","role":"user","text":"zorbleflux two","time":"2026-08-04T12:01:00Z","origin":"mini"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	out := captureStdout(t, func() { err = runSync(index.DefaultDir(), []string{"import", batch}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1 record left out — this machine does not read their harness (excluded: opencode)",
		"1 record left out — your ignore rule covers their project",
		"imported 0 records",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// A sync record carries the exporting machine's UTC offset. Formatting it as
// it arrived dated a UTC+14 session "Aug 7" on the import receipt while `last`
// and search on the same machine put it under Aug 6 — three days for one
// record, seconds apart (#1047).
func TestImportReceiptDatesArrivalsInTheReadersZone(t *testing.T) {
	tmp := hermeticEnv(t)
	// time.Local is resolved at process start, so TZ= in the environment does
	// not move it; the reader's zone is set on the package clock instead.
	savedLocal := time.Local
	time.Local = time.FixedZone("test-11", -11*60*60)
	t.Cleanup(func() { time.Local = savedLocal })

	// 2026-08-06 14:09 UTC: Aug 7 for the sender at +14, Aug 6 for the reader.
	dir := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"harness":"claude","session_id":"s_now","project":"proj/p","role":"user",` +
		`"text":"the connection pool exhausted under load","time":"2026-08-07T04:09:20+14:00"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "deja-sync-aaaa-1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	receipt, err := captureRunStderr(t, "sync", "import", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt, "the connection pool exhausted under load") {
		t.Fatalf("receipt did not list the imported session:\n%s", receipt)
	}
	if strings.Contains(receipt, "Aug 7") {
		t.Errorf("receipt dates the arrival in the sender's zone:\n%s", receipt)
	}
	if !strings.Contains(receipt, "Aug 6") {
		t.Errorf("receipt does not date the arrival in the reader's zone:\n%s", receipt)
	}

	// The day the receipt names is the day the rest of the machine lists it
	// under — that is the whole point.
	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Aug 6") {
		t.Fatalf("last disagrees about the day:\n%s", out)
	}
}

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

// A transfer that stopped leaves a directory of whole batches and one cut off
// mid-line. Both halves of the answer matter: what did arrive is in, and what
// did not is named — with a non-zero exit, so a script that chains on the
// import does not read the loss as success.
func TestAnInterruptedTransferImportsWhatArrivedAndNamesWhatDidNot(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	batch := t.TempDir()
	rec := func(id, text string) string {
		return fmt.Sprintf(`{"harness":"claude","session_id":%q,"project":"work/app","role":"user",`+
			`"text":%q,"time":"2026-08-04T12:00:00Z","origin":"laptop"}`, id, text)
	}
	whole := rec("a0", "the first batch arrived whole") + "\n" + rec("a1", "and so did this one") + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-laptop-1.jsonl"), []byte(whole), 0o600); err != nil {
		t.Fatal(err)
	}
	torn := rec("b0", "this one was cut") + "\n" + `{"harness":"claude","session_id":"b1","project":"work/app","role":"user","text":"cut off mid-tra`
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-laptop-2.jsonl"), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}

	// What arrived is printed; what did not comes back as the error, which is
	// what carries the exit code — a script chaining on the import must not
	// read the loss as success.
	out, err := captureRun(t, "sync", "import", batch)
	if !strings.Contains(out, "imported 2 records") {
		t.Errorf("what arrived was not reported:\n%s", out)
	}
	if err == nil {
		t.Fatalf("an import that lost a file exited zero:\n%s", out)
	}
	if !strings.Contains(err.Error(), "deja-sync-laptop-2.jsonl") || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("the file that did not arrive was not named: %v", err)
	}

	// And the store agrees with the report: the whole batch is in, the cut one
	// is not.
	if err = index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	listed, err := captureRun(t, "last", "10")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, "the first batch arrived whole") {
		t.Errorf("the whole batch is not in the store:\n%s", listed)
	}
	if strings.Contains(listed, "this one was cut") {
		t.Errorf("a record from the truncated file was imported:\n%s", listed)
	}
}

// The first line after a sync, and the line on the card people post. #737 put
// pluralS in for exactly this and neither surface used it: one imported
// session printed "imported 1 records — 1 sessions from another machine" and
// "1 sessions indexed" (#1052).
func TestSyncAndStatsCountOneSessionInTheSingular(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	batch := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"harness":"claude","session_id":"one-1","project":"p","role":"user","text":"only one record here","time":"2026-01-02T06:04:05+03:00"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-one.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "sync", "import", batch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "imported 1 record —") {
		t.Errorf("the import line counts one record as many:\n%s", out)
	}
	if !strings.Contains(out, "1 session from another machine") {
		t.Errorf("the import line counts one session as many:\n%s", out)
	}

	if got := statsHeadline(stats.Report{TotalSessions: 1}); got != "1 session indexed" {
		t.Errorf("the card headline reads %q", got)
	}
	if got := statsHeadline(stats.Report{TotalSessions: 2}); got != "2 sessions indexed" {
		t.Errorf("the plural case broke: %q", got)
	}
}

// The own-copy line counted one record with a plural verb: "1 record were
// already here", the same slip #1052 fixed on the import and stats lines.
func TestTheOwnCopyLineAgreesWithItsCount(t *testing.T) {
	if got := ownCopyLine(1); !strings.Contains(got, "1 record was already here") {
		t.Errorf("one record: %q", got)
	}
	if got := ownCopyLine(2); !strings.Contains(got, "2 records were already here") {
		t.Errorf("two records: %q", got)
	}
}

// A sync that says nothing for minutes reads as a hang, and the report at the
// end is what a later performance question is answered from (#3801).
func TestSyncPhasesSayWhatIsRunningAndWhereTheTimeWent(t *testing.T) {
	buf := &lockedBuffer{}
	p := newSyncPhases(buf)
	p.beat = 20 * time.Millisecond
	p.floor = time.Millisecond

	p.start("exporting records")
	p.note("12 MiB in 3 batches")
	// Long enough for the heartbeat to fire at least once.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "still exporting") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	p.start("importing on mini")
	time.Sleep(3 * time.Millisecond)
	// The report is about the run, so a fast run gets none of it: the threshold
	// is the heartbeat, which this stand has shortened.
	p.summary("pushed to mini")

	got := buf.String()
	for _, want := range []string{
		"deja: exporting records…",
		"deja:   12 MiB in 3 batches",
		"still exporting records…",
		"deja: importing on mini…",
		"pushed to mini in ",
		"exporting records ",
		"importing on mini ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The phases are reported in the order they ran, which is what makes the
	// line readable as a timeline.
	if i, j := strings.Index(got, "— exporting records"), strings.Index(got, "importing on mini "); i < 0 || j < i {
		t.Errorf("the report is not in phase order:\n%s", got)
	}
	// Nothing keeps printing after the run ends.
	before := len(buf.String())
	time.Sleep(60 * time.Millisecond)
	if got := buf.String(); len(got) != before {
		t.Errorf("a heartbeat outlived the phase: %q", got[before:])
	}
}

// A quick exchange gets no report at all: two lines about two seconds is noise
// on the one path that runs from launchd every hour.
func TestAQuickSyncPrintsNoReport(t *testing.T) {
	buf := &lockedBuffer{}
	p := newSyncPhases(buf)
	p.start("exporting records")
	p.summary("pushed to mini")
	if strings.Contains(buf.String(), "pushed to mini in") {
		t.Errorf("a two-millisecond sync reported its timings: %q", buf.String())
	}
}

// ssh hands over whatever arrived in one read, which splits mid-line as often
// as not, so the remote's narration has to be reassembled before it is echoed.
func TestRemoteOutputIsEchoedLineByLine(t *testing.T) {
	var got []string
	w := newLineWriter(func(line string) { got = append(got, line) })
	for _, chunk := range []string{"deja: imp", "orted 5 records\r\ndeja: 2 ses", "sions\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "deja: imported 5 records" || got[1] != "deja: 2 sessions" {
		t.Fatalf("lines = %q", got)
	}
	// An unterminated tail is held, not printed as a fragment...
	if _, err := w.Write([]byte("deja: still going")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("a fragment was echoed: %q", got)
	}
	// ...unless it grows past the bound, since a remote that writes one
	// enormous line must not grow this buffer without limit.
	if _, err := w.Write(bytes.Repeat([]byte("x"), remoteStreamHoldMax+1)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("the held line was never flushed: %d lines", len(got))
	}
}

// The remote is another machine, so what it prints is bounded and sanitised
// the way every other remote sentence is — and a line already shown as it
// arrived must not be printed again at the end.
func TestStreamedRemoteLinesAreBoundedAndCounted(t *testing.T) {
	var counted bool
	got := captureStdout(t, func() {
		stop := streamRemoteLines("mini")
		sink := currentLineSink()
		if sink == nil {
			t.Error("nothing is streaming")
			return
		}
		sink("deja: imported 5 records\x1b[31m")
		sink("   ")
		for range remoteStreamLineMax + 10 {
			sink("chatter")
		}
		counted = stop()
	})
	if !counted {
		t.Error("the streamed lines were not counted, so the caller will print them twice")
	}
	if currentLineSink() != nil {
		t.Error("the sink outlived the phase")
	}
	if !strings.Contains(got, "mini: deja: imported 5 records") {
		t.Errorf("the remote's line did not arrive: %q", got)
	}
	if strings.Contains(got, "\x1b[31m") {
		t.Errorf("an escape from the remote reached the terminal: %q", got)
	}
	if n := strings.Count(got, "chatter"); n > remoteStreamLineMax {
		t.Errorf("a talkative remote printed %d lines", n)
	}
	if strings.Contains(got, "mini: \n") {
		t.Errorf("a blank remote line was echoed: %q", got)
	}
}

// lockedBuffer is a buffer this test can read while the heartbeat goroutine
// writes to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Watermarks are per peer, and a pull left the remote's export unnamed — so it
// shared one watermark with every `deja sync export` run by hand there. Take a
// backup on the server, then pull from it, and the pull receives almost
// nothing: the backup already settled those records. Measured on a real index,
// the second unnamed export sent 2 records where the first sent 246822.
func TestSyncPullNamesThisMachineToTheRemote(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "laptop")
	old := sshRunner
	defer func() { sshRunner = old }()
	var exportCmd string
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[len(args)-1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[len(args)-1], "sync export"):
			exportCmd = args[len(args)-1]
			return "deja: exported 0 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err != nil {
		t.Fatal(err)
	}
	// The whole remote command is quoted again for `sh -lc`, so the name
	// arrives escaped; what matters is that the flag and the name are in it.
	if !strings.Contains(exportCmd, "--peer") || !strings.Contains(exportCmd, "laptop") {
		t.Errorf("the pull did not tell the remote who it is: %s", exportCmd)
	}
}

// A machine name is not guaranteed to be a bare word, and it is spliced into a
// command the remote shell runs.
func TestSyncPullQuotesAnAwkwardMachineName(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "lap; rm -rf ~")
	old := sshRunner
	defer func() { sshRunner = old }()
	var exportCmd string
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[len(args)-1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[len(args)-1], "sync export"):
			exportCmd = args[len(args)-1]
			return "deja: exported 0 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err != nil {
		t.Fatal(err)
	}
	// Unwrap the one level `sh -lc '…'` added: what is left is the command the
	// remote shell sees, and the semicolon in the name must still be inside a
	// quoted word there.
	inner := strings.ReplaceAll(exportCmd, `'"'"'`, "'")
	inner = strings.TrimSuffix(strings.TrimPrefix(inner, "sh -lc '"), "'")
	if !strings.Contains(inner, `'lap; rm -rf ~'`) {
		t.Errorf("the machine name reached the remote shell unquoted: %s", inner)
	}
}

// The flag is refused by a deja too old to know it, which is what stops a typo
// from exporting nothing. Both machines are not upgraded at the same moment,
// so a pull falls back to the shared watermark rather than failing.
func TestSyncPullFallsBackWhenTheRemoteIsOlder(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "laptop")
	old := sshRunner
	defer func() { sshRunner = old }()
	var commands []string
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[len(args)-1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[len(args)-1], "sync export"):
			commands = append(commands, args[len(args)-1])
			if strings.Contains(args[len(args)-1], "--peer") {
				return `deja: sync export: unknown flag "--peer" — only --full is accepted`, errors.New("exit status 1")
			}
			return "deja: exported 0 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err != nil {
		t.Fatalf("a pull from an older remote failed instead of falling back: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("ran %d remote exports, want the named one and the fallback: %v", len(commands), commands)
	}
	if strings.Contains(commands[1], "--peer") {
		t.Errorf("the fallback still carried the flag: %s", commands[1])
	}
}

// A remote error that is not about the flag must not be retried: running the
// export twice would advance the remote's watermark a second time.
func TestSyncPullDoesNotRetryOtherFailures(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "laptop")
	old := sshRunner
	defer func() { sshRunner = old }()
	var calls int
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[len(args)-1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[len(args)-1], "sync export"):
			calls++
			return "deja: no sessions indexed yet", errors.New("exit status 1")
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err == nil {
		t.Fatal("a failing remote export was reported as success")
	}
	if calls != 1 {
		t.Errorf("the remote export ran %d times, want once", calls)
	}
}

// The flag on this side: a named peer keeps its own watermark, so a second
// export for a different name still carries the whole history.
func TestExportPeerKeepsItsOwnWatermark(t *testing.T) {
	tmp := setupLocalIndex(t)
	dir := os.Getenv("DEJA_INDEX_DIR")

	first := filepath.Join(tmp, "backup")
	if err := runSync(dir, []string{"export", first}); err != nil {
		t.Fatal(err)
	}
	if n := batchRecordCount(t, first); n == 0 {
		t.Fatal("the first export sent nothing")
	}

	// Same records, different peer: the backup above must not have settled
	// them for this one.
	named := filepath.Join(tmp, "toMini")
	if err := runSync(dir, []string{"export", named, "--peer", "mini"}); err != nil {
		t.Fatal(err)
	}
	if n := batchRecordCount(t, named); n == 0 {
		t.Error("a hand-taken backup starved the export for a named peer")
	}

	// And the same peer twice sends nothing the second time, which is the
	// point of a watermark.
	again := filepath.Join(tmp, "toMiniAgain")
	if err := runSync(dir, []string{"export", again, "--peer", "mini"}); err != nil {
		t.Fatal(err)
	}
	if n := batchRecordCount(t, again); n != 0 {
		t.Errorf("the same peer received %d records twice", n)
	}
}

func TestExportRejectsAPeerWithNoName(t *testing.T) {
	tmp := setupLocalIndex(t)
	err := runSync(os.Getenv("DEJA_INDEX_DIR"), []string{"export", filepath.Join(tmp, "out"), "--peer"})
	if err == nil {
		t.Fatal("--peer with no name was accepted")
	}
	if !strings.Contains(err.Error(), "--peer") {
		t.Errorf("the error does not name the flag: %v", err)
	}
}

func batchRecordCount(t *testing.T, dir string) int {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
	}
	return n
}

var _ = index.MachineName

// A machine holding only work that arrived by sync used to report "exported 0
// records" while its own `stats` showed hundreds of messages, which reads as
// data loss rather than as a rule (#2962). It now names what it is holding and
// the flag that sends it.
func TestSyncExportSaysWhatItHoldsBack(t *testing.T) {
	home := hermeticEnv(t)
	dir := filepath.Join(home, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	batch := filepath.Join(home, "from-elsewhere")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec := `{"harness":"claude","session_id":"far1","project":"app","role":"user",` +
		`"text":"relayneedle the loader stalls on boot","time":"` + at + `","origin":"container-1"}`
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(rec+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := index.Import(dir, batch); err != nil || n != 1 {
		t.Fatalf("import n=%d err=%v", n, err)
	}

	out := filepath.Join(home, "outgoing")
	stdout := captureStdout(t, func() {
		if err := runSync(dir, []string{"export", out, "--full"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stdout, "arrived from other machines") {
		t.Errorf("an empty export said nothing about what it holds:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--include-imported") {
		t.Errorf("the reader is not told how to send it:\n%s", stdout)
	}

	// And the flag sends it, under the name of the machine the work happened
	// on rather than this one.
	relayOut := filepath.Join(home, "relayed")
	if err := runSync(dir, []string{"export", relayOut, "--include-imported"}); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(relayOut, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("nothing was written: %v", err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var sr struct {
		Origin  string `json:"origin"`
		Project string `json:"project"`
		Text    string `json:"text"`
	}
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(string(body)), "\n")[0])
	if err := json.Unmarshal([]byte(line), &sr); err != nil {
		t.Fatal(err)
	}
	if sr.Origin != "container-1" {
		t.Errorf("relayed record signed %q, want the machine the work happened on", sr.Origin)
	}
	if sr.Project != "app" {
		t.Errorf("project = %q, want the original name", sr.Project)
	}
	if !strings.Contains(sr.Text, "relayneedle") {
		t.Errorf("the work itself did not travel: %q", sr.Text)
	}
}

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

// sshCapture is the first thing a pull does, so a remote whose mktemp fails is
// the most likely of the three failures — and it built its error from the raw
// host and the raw output (#1833).
func TestAFailingSSHCaptureCannotWriteOnThisTerminal(t *testing.T) {
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
	sshRunner = func(_ string, _ ...string) (string, error) {
		return "mktemp: no space\x1b[31m\rHACKED " + strings.Repeat("padding ", 300), errors.New("exit status 1")
	}
	err := syncSSHHost(dir, "peer\x1b[31mX", true, false, false)
	if err == nil {
		t.Fatal("the fixture did not fail, so there is no error to inspect")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\r") {
		t.Errorf("the error carries an escape or a rewind: %q", msg[:min(len(msg), 100)])
	}
	if len(msg) > remoteEchoMax+200 {
		t.Errorf("the error is %d bytes of remote output", len(msg))
	}
	// The control: what the remote said is still in it.
	if !strings.Contains(msg, "no space") {
		t.Errorf("the remote's own report is gone: %q", msg[:min(len(msg), 120)])
	}
}

// ssh writes host-key notices and server banners to stderr — text the machine
// at the other end controls — and the runner folds stderr into its error, so it
// reaches this terminal too (#1833).
func TestTheRunnersStderrIsBoundedToo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture runs a POSIX shell")
	}
	// The redirection wraps the whole group: applying it to the last command
	// only sends the banner to stdout, where the runner drops it, and the test
	// then passes against unfixed code.
	hostile := `{ printf 'banner\033[31m\rHACKED '; i=0; while [ $i -lt 400 ]; do printf 'padding '; i=$((i+1)); done; } >&2; exit 1`
	_, err := sshRunner("sh", "-c", hostile)
	if err == nil {
		t.Fatal("the fixture did not fail")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\r") {
		t.Errorf("the runner's error carries an escape or a rewind: %q", msg[:min(len(msg), 90)])
	}
	if len(msg) > remoteEchoMax+200 {
		t.Errorf("the runner's error is %d bytes", len(msg))
	}
	if !strings.Contains(msg, "banner") {
		t.Errorf("what the other end said is gone: %q", msg[:min(len(msg), 120)])
	}
}

// Export writes a file per source transcript, so a machine with tens of
// thousands of records hands scp thousands of paths. Windows refuses a command
// line over 32,767 characters outright — and it refuses it after the export has
// run, which is the worst moment to find out (#2002).
func TestScpSplitsPathsUnderTheCommandLineBound(t *testing.T) {
	paths := make([]string, 0, 23336)
	for i := range 23336 {
		paths = append(paths, fmt.Sprintf(`C:\Users\someone\AppData\Local\Temp\deja-sync-9f3a12bc-17761031960%05d.jsonl`, i))
	}
	budget := sshArgsBudget([]string{"-o", "BatchMode=yes"}, "root@example.com", "/tmp/deja-sync-XXXX")
	chunks := scpChunks(paths, budget)
	if len(chunks) < 2 {
		t.Fatalf("23336 paths went out in %d command line(s)", len(chunks))
	}
	seen := 0
	for i, chunk := range chunks {
		seen += len(chunk)
		if n := len(strings.Join(chunk, " ")); n > budget {
			t.Errorf("chunk %d is %d characters, the budget is %d", i, n, budget)
		}
		if len(chunk) == 0 {
			t.Errorf("chunk %d is empty", i)
		}
	}
	if seen != len(paths) {
		t.Errorf("sent %d paths of %d — the rest would never reach the peer", seen, len(paths))
	}
}

// One path longer than the whole budget still goes: refusing to send it is
// worse than letting the platform complain about that one file.
func TestAPathBiggerThanTheBudgetStillGoes(t *testing.T) {
	long := strings.Repeat("x", 40000) + ".jsonl"
	chunks := scpChunks([]string{long, "b.jsonl"}, 1000)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want the long path on its own and the short one after", len(chunks))
	}
	if chunks[0][0] != long {
		t.Error("the long path was dropped")
	}
}

// The budget counts what the flags and the destination already spend, so a long
// host or a long remote directory cannot push the line over on its own.
func TestTheBudgetLeavesRoomForFlagsAndDestination(t *testing.T) {
	plain := sshArgsBudget(nil, "h", "/tmp/x")
	loaded := sshArgsBudget([]string{"-o", strings.Repeat("K", 400)}, strings.Repeat("h", 200), strings.Repeat("/d", 100))
	if loaded >= plain {
		t.Errorf("a heavier fixed part left as much room for paths: %d vs %d", loaded, plain)
	}
	if loaded < 512 {
		t.Errorf("budget collapsed to %d; one path at a time is the floor", loaded)
	}
}

// The watermark is per machine, not per destination: the second peer someone
// hands memory to gets an empty folder and the same "exported 0 records" that
// means "you are up to date" at the first one (#982).
func TestExportToASecondDestinationSaysWhyItIsEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	first := filepath.Join(tmp, "to-laptop")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "sync", "export", first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exported 1 record\n") {
		t.Fatalf("the first export did not carry the session:\n%s", out)
	}
	// Same destination again is the up-to-date case and must stay quiet about
	// --full: there is nothing to send there.
	out, err = captureRun(t, "sync", "export", first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--full") {
		t.Errorf("a destination that already holds the batch was told to resend:\n%s", out)
	}

	second := filepath.Join(tmp, "to-desktop")
	if err = os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "sync", "export", second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--full") {
		t.Errorf("an empty second destination said nothing about how to fill it:\n%s", out)
	}
	entries, err := os.ReadDir(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the incremental export wrote something after all: %v", entries)
	}

	// And --full fills it, after which the folder is up to date like any other.
	if _, err = captureRun(t, "sync", "export", second, "--full"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "sync", "export", second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--full") {
		t.Errorf("a filled destination kept being told to resend:\n%s", out)
	}
}

// On a watermarked sync most runs send nothing, so the run that sends nothing
// is the one people see most — and it asked them to review an export it had
// not written (#1035).
func TestANoOpExportDoesNotWarnAboutAFileItDidNotWrite(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "to-laptop")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	// The run that writes a batch keeps the warning: that file is real.
	first, err := captureRunStderr(t, "sync", "export", out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "review the export before moving it") {
		t.Errorf("an export that wrote a batch dropped the redaction warning:\n%s", first)
	}

	before, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	second, err := captureRunStderr(t, "sync", "export", out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second, "review the export before moving it") {
		t.Errorf("a no-op export still points at a file it did not write:\n%s", second)
	}
	after, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("the no-op export wrote %d files, want %d", len(after), len(before))
	}
}

// The line the first sync against a new machine prints said "exported 1
// records", while the local path one file away counts properly (#2290).
func TestTheSSHExportLineCountsInSingularToo(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{
		{1, "exported 1 record\n"},
		{0, "exported 0 records\n"},
		{4, "exported 4 records\n"},
	} {
		got := sshExportedLine(c.n)
		if got != "deja: "+c.want {
			t.Errorf("%d: %q, want %q", c.n, got, "deja: "+c.want)
		}
		if c.n == 1 && strings.Contains(got, "1 records") {
			t.Errorf("one record is still plural: %q", got)
		}
	}

	// The pull line had the same shape and says it the same way now.
	if got := sshCountLine("imported", 1); got != "deja: imported 1 record\n" {
		t.Errorf("the pull line reads %q", got)
	}
	if got := sshCountLine("imported", 2); got != "deja: imported 2 records\n" {
		t.Errorf("the pull line reads %q", got)
	}
}

func setupLocalIndex(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-sshsync")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"ssh1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"sshneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "ssh1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	return tmp
}

func TestSyncSSHPullFullAndScpFailure(t *testing.T) {
	setupLocalIndex(t)
	old := sshRunner
	defer func() { sshRunner = old }()
	var exportCmd string
	var cleanup bool
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[len(args)-1] == "mktemp -d" {
			return "/tmp/remote-out", nil
		}
		if name == "ssh" && strings.Contains(args[len(args)-1], "sync export") {
			exportCmd = args[len(args)-1]
			return "deja: exported 1 records", nil
		}
		if name == "ssh" && strings.Contains(args[len(args)-1], "rm -rf") {
			cleanup = true
			return "", nil
		}
		if name == "scp" {
			return "permission denied", errors.New("exit status 1")
		}
		return "", nil
	}
	err := runSyncSSH(index.DefaultDir(), []string{"minihost", "--pull", "--full"})
	if err == nil || !strings.Contains(err.Error(), "scp: exit status 1: permission denied") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(exportCmd, "sync export --full") {
		t.Fatalf("export command = %q", exportCmd)
	}
	if !cleanup {
		t.Fatal("expected remote cleanup after scp failure")
	}
}

func TestSyncSSHPush(t *testing.T) {
	setupLocalIndex(t)
	var calls [][]string
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		if name == "ssh" && args[len(args)-1] == "mktemp -d" {
			return "/tmp/remote-batch\n", nil
		}
		if name == "ssh" {
			return "deja: imported 1 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"minihost"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want mktemp+scp+import: %v", len(calls), calls)
	}
	if calls[1][0] != "scp" || !strings.HasSuffix(calls[1][len(calls[1])-1], ":/tmp/remote-batch/") {
		t.Fatalf("bad scp call: %v", calls[1])
	}
	last := calls[2][len(calls[2])-1]
	if !strings.Contains(last, "sync import") || !strings.Contains(last, "/tmp/remote-batch") {
		t.Fatalf("bad remote import call: %v", calls[2])
	}
}

func TestSyncSSHPushNothingNew(t *testing.T) {
	setupLocalIndex(t)
	var calls int
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(_ string, _ ...string) (string, error) {
		calls++
		return "/tmp/x", nil
	}
	// First push exports the one record; run it with a runner that accepts it.
	full := sshRunner
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[len(args)-1] == "mktemp -d" {
			return "/tmp/remote-batch", nil
		}
		return "deja: imported 1 records", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"minihost"}); err != nil {
		t.Fatal(err)
	}
	// Second push has nothing new: no ssh/scp calls at all.
	sshRunner = full
	calls = 0
	if err := runSyncSSH(index.DefaultDir(), []string{"minihost"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("expected no remote calls on empty export, got %d", calls)
	}
}

func TestSyncSSHPull(t *testing.T) {
	setupLocalIndex(t)
	base := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[len(args)-1] == "mktemp -d" {
			return "/tmp/remote-out", nil
		}
		if name == "ssh" && strings.Contains(args[len(args)-1], "sync export") {
			return "deja: exported 1 records", nil
		}
		if name == "scp" {
			dest := strings.TrimSuffix(args[len(args)-1], "/")
			f, err := os.Create(filepath.Join(dest, "deja-sync-remote-1.jsonl"))
			if err != nil {
				return "", err
			}
			enc := json.NewEncoder(f)
			if err = enc.Encode(index.SyncRecord{Harness: "claude", SessionID: "remote-ssh", Project: "otherbox", Role: "user", Text: "pullneedle from remote", Time: base}); err != nil {
				_ = f.Close()
				return "", err
			}
			return "", f.Close()
		}
		return "", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"minihost", "--pull"}); err != nil {
		t.Fatal(err)
	}
	ss, err := indexSearch(os.Getenv("DEJA_INDEX_DIR"), search.Options{Query: "pullneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || !strings.HasPrefix(ss[0].Project, "imported:") {
		t.Fatalf("pulled session not searchable: %#v", ss)
	}
}

func TestSyncSSHBadArgs(t *testing.T) {
	if err := runSyncSSH(index.DefaultDir(), nil); err == nil {
		t.Fatal("expected error for missing host")
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"--evil"}); err == nil {
		t.Fatal("expected error for flag-looking host")
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"a", "b"}); err == nil {
		t.Fatal("expected error for two hosts")
	}
}

func TestRunSyncExportImport(t *testing.T) {
	setupLocalIndex(t)
	out := filepath.Join(t.TempDir(), "export")
	if err := runSync(index.DefaultDir(), []string{"export", "--full", out}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected exported batch")
	}
	importDir := filepath.Join(t.TempDir(), "import.db")
	t.Setenv("DEJA_INDEX_DIR", importDir)
	if err = runSync(index.DefaultDir(), []string{"import", out}); err != nil {
		t.Fatal(err)
	}
	ss, err := indexSearch(importDir, search.Options{Query: "sshneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("imported sessions = %#v", ss)
	}
}

// ssh writes host-key notices and server banners to stderr; folding them into
// the result made `mktemp -d` unparseable and sync unusable against such a
// host.
func TestSyncSSHIgnoresStderrBanner(t *testing.T) {
	setupLocalIndex(t)
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && len(args) > 1 && strings.Contains(args[len(args)-1], "mktemp") {
			// stdout only — the banner went to stderr, which we now drop.
			return "/tmp/remote-batch\n", nil
		}
		return "deja: imported 1 records", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"bannerhost"}); err != nil {
		t.Fatalf("banner on stderr broke sync: %v", err)
	}
}

// A remote whose profile still prints chatter on stdout leaves the value on
// the last line.
func TestSyncSSHTakesLastLineOfRemoteOutput(t *testing.T) {
	setupLocalIndex(t)
	old := sshRunner
	defer func() { sshRunner = old }()
	var target string
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && len(args) > 1 && strings.Contains(args[len(args)-1], "mktemp") {
			return "Welcome to prod\n/tmp/remote-batch\n", nil
		}
		if name == "scp" {
			target = args[len(args)-1]
		}
		return "deja: imported 1 records", nil
	}
	if err := runSyncSSH(index.DefaultDir(), []string{"chattyhost"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(target, "/tmp/remote-batch/") {
		t.Fatalf("scp target took the wrong line: %q", target)
	}
}

// A remote temp path scp cannot carry must fail with a message naming the
// cause rather than word-splitting on the far side.
func TestSyncSSHRejectsUnusableRemotePath(t *testing.T) {
	setupLocalIndex(t)
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && len(args) > 1 && strings.Contains(args[len(args)-1], "mktemp") {
			return "/tmp/deja sync/tmp.AbCd\n", nil
		}
		return "deja: imported 1 records", nil
	}
	err := runSyncSSH(index.DefaultDir(), []string{"oddhost"})
	if err == nil || !strings.Contains(err.Error(), "TMPDIR") {
		t.Fatalf("want an actionable TMPDIR error, got %v", err)
	}
}

// A laptop that is asleep neither answers nor refuses, so ssh waits out the
// system default — measured at 446 s on one host, and `deja sync` walks every
// machine it knows (#1772).
func TestSSHCarriesAConnectTimeout(t *testing.T) {
	var got [][]string
	old := sshRunner
	sshRunner = func(name string, args ...string) (string, error) {
		got = append(got, append([]string{name}, args...))
		return "/tmp/deja-remote", nil
	}
	t.Cleanup(func() { sshRunner = old })

	if _, err := sshCapture("somehost", "mktemp -d"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("ssh was run %d times", len(got))
	}
	line := strings.Join(got[0], " ")
	if !strings.Contains(line, "ConnectTimeout=") {
		t.Errorf("ssh runs without a connect timeout: %s", line)
	}
	if !strings.Contains(line, "BatchMode=yes") {
		t.Errorf("ssh can stop on a password prompt nothing is reading: %s", line)
	}
	// The host and the command still arrive, in that order.
	if i, j := strings.Index(line, "somehost"), strings.Index(line, "mktemp -d"); i < 0 || j < 0 || i > j {
		t.Errorf("the call lost its shape: %s", line)
	}
}

// Every ssh and scp deja runs carries the options, including the cleanup call
// that removes the remote batch directory — a host that has gone away between
// the transfer and the cleanup would otherwise hold the sync open.
func TestEverySSHCallCarriesTheOptions(t *testing.T) {
	src, err := os.ReadFile("sync_ssh.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `sshRunner("ssh"`) && !strings.Contains(line, `sshRunner("scp"`) {
			continue
		}
		if !strings.Contains(line, "sshOpts()") && !strings.Contains(line, "scpArgs") {
			t.Errorf("sync_ssh.go:%d runs without the options: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// sshHostArg is what a test stub needs to read the host out of an ssh call now
// that every call carries options: the first argument that is neither an
// option nor an option's value.
func sshHostArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i]
	}
	return ""
}

// clearDejaEnv unsets every DEJA_* and XDG_* variable the test process
// inherited, so what a unit carries is only what the test sets.
func clearDejaEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "DEJA_") || strings.HasPrefix(name, "XDG_") {
			t.Setenv(name, "")
		}
	}
}

// launchd and systemd start the timer with their own environment, not the
// shell's, so `deja sync` from the timer imported into another index under
// other switches than the same command typed by hand (#5436).
func TestSyncTimerUnitsCarryTheShellsDejaSettings(t *testing.T) {
	home := syncTimerHome(t)
	clearDejaEnv(t)
	cache := filepath.Join(home, "elsewhere", `50%off "cache"`)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode")

	plist := syncAutoPlist("/usr/local/bin/deja")
	var doc struct{}
	if err := xml.Unmarshal([]byte(plist), &doc); err != nil {
		t.Fatalf("the plist does not parse: %v\n%s", err, plist)
	}
	for _, want := range []string{
		"<key>EnvironmentVariables</key>",
		"<key>DEJA_INDEX_TOOL_OUTPUT</key><string>0</string>",
		"<key>DEJA_EXCLUDE_HARNESSES</key><string>opencode</string>",
		"<key>XDG_CACHE_HOME</key><string>" + xmlEscape(cache) + "</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("the plist lacks %s:\n%s", want, plist)
		}
	}

	service := syncAutoService("/usr/local/bin/deja")
	for _, want := range []string{
		`Environment="DEJA_INDEX_TOOL_OUTPUT=0"`,
		`Environment="DEJA_EXCLUDE_HARNESSES=opencode"`,
		`Environment="XDG_CACHE_HOME=` + strings.NewReplacer(`\`, `\\`, "%", "%%", `"`, `\"`).Replace(cache) + `"`,
	} {
		if !strings.Contains(service, want) {
			t.Errorf("the service lacks %s:\n%s", want, service)
		}
	}
	if !strings.Contains(service, "ExecStart=\"/usr/local/bin/deja\" sync\n") {
		t.Errorf("ExecStart changed:\n%s", service)
	}
}

// Control: a default setup writes the units it always wrote.
func TestSyncTimerUnitsCarryNothingOnADefaultSetup(t *testing.T) {
	syncTimerHome(t)
	clearDejaEnv(t)
	if p := syncAutoPlist("/usr/local/bin/deja"); strings.Contains(p, "EnvironmentVariables") {
		t.Errorf("a default setup carries an environment:\n%s", p)
	}
	if s := syncAutoService("/usr/local/bin/deja"); strings.Contains(s, "Environment=") {
		t.Errorf("a default setup carries an environment:\n%s", s)
	}
}
