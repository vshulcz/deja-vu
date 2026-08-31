package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/peers"
	"github.com/vshulcz/deja-vu/internal/search"
)

// sshRunner is swapped in tests. It returns stdout only: ssh writes host-key
// notices and server banners to stderr, and folding those into the result
// made `mktemp -d` unparseable on any host with a banner configured.
var sshRunner = func(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		// The comment above says what stderr holds — host-key notices and
		// server banners — which is text the machine at the other end writes.
		// It reaches this terminal through the error, so it takes the same
		// bound as the remote's stdout (#1833).
		return stdout.String(), fmt.Errorf("%w: %s", err, remoteOutputForEcho(strings.TrimSpace(stderr.String())))
	}
	return stdout.String(), err
}

// sshConnectTimeout is how long deja waits for a machine to answer. A laptop
// that is asleep neither answers nor refuses, and ssh's own default left one
// host waiting 446 seconds with nothing on screen — while `deja sync` walks
// every machine it knows (#1772).
const sshConnectTimeout = "10"

// sshOpts are the options every ssh and scp call carries: a connect timeout,
// and BatchMode so a host that wants a password fails instead of blocking on a
// prompt nothing is reading.
func sshOpts() []string {
	return []string{"-o", "ConnectTimeout=" + sshConnectTimeout, "-o", "BatchMode=yes"}
}

func runSyncSSH(dir string, args []string) error {
	host := ""
	pull, full := false, false
	both := false
	for _, a := range args {
		switch a {
		case "--pull":
			pull = true
		case "--both":
			both = true
		case "--full":
			full = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("sync ssh: unknown flag %q", a)
			}
			if host != "" {
				return fmt.Errorf("sync ssh takes one host")
			}
			host = a
		}
	}
	if host == "" {
		return fmt.Errorf("sync ssh needs a host (an ssh alias or user@host)")
	}
	return syncSSHHost(dir, host, pull, full, both)
}

// syncSSHHost runs one exchange and records it, so the next `deja sync` knows
// this host without being told again.
func syncSSHHost(dir, host string, pull, full, both bool) error {
	// One machine, one spelling, for the whole run: the watermark is keyed by
	// this string, and a second spelling of a known host pushed it everything
	// it already had (#1867). ssh matches a host without regard to case, so
	// this connects to the same machine either way.
	host = peers.Canonical(host)
	if both {
		if err := syncOneWay(dir, host, false, full); err != nil {
			return err
		}
		return syncOneWay(dir, host, true, full)
	}
	return syncOneWay(dir, host, pull, full)
}

// errNothingToSend says the push had nothing to send, so no connection was
// opened. It is not a failure and it is not an exchange: stamping it made
// doctor report a machine deja never contacted as reached a moment ago (#1780).
var errNothingToSend = errors.New("nothing new to push")

// recordExchange writes what happened to the peer list, which is where doctor
// and the bare `deja sync` read a machine's history from.
func recordExchange(host string, pull bool, when time.Time, err error) error {
	if errors.Is(err, errNothingToSend) {
		// Remember the machine — it is one deja knows about now — without
		// claiming an exchange or a failure.
		return peers.Record(host, pull, time.Time{}, nil)
	}
	return peers.Record(host, pull, when, err)
}

func syncOneWay(dir, host string, pull, full bool) error {
	var err error
	if pull {
		err = syncSSHPull(dir, host, full)
	} else {
		err = syncSSHPush(dir, host, full)
	}
	// Recorded either way: a peer that has been failing for a week is exactly
	// what the report exists to show, and it is invisible if only successes
	// are written down.
	if rerr := recordExchange(host, pull, time.Now(), err); rerr != nil && err == nil {
		fmt.Fprintf(os.Stderr, "deja: synced with %s, but could not record it: %v\n", hostForEcho(host), rerr)
	}
	if errors.Is(err, errNothingToSend) {
		// Nothing to send is a quiet success for the caller: a machine with no
		// new work is not a machine that could not be reached.
		return nil
	}
	return err
}

// runSyncAll exchanges with every machine this one already syncs with. The
// host had to be typed every time, which with three machines is six commands
// and a thing to remember rather than a thing that happens. Records do not
// travel through a third machine — an export never forwards what arrived by
// sync — so every pair has to meet directly, and that is what this does.
func runSyncAll(dir string, full bool) error {
	list, why := peers.Snapshot()
	if len(list) == 0 {
		// Load reports a malformed file as no peers, so without this the
		// sentence below tells someone whose file is broken to name their
		// first machine — which they already did (#1840).
		if why != "" {
			return fmt.Errorf("%s could not be read — %s", peers.Path(), remoteOutputForEcho(why))
		}
		return fmt.Errorf("no machines to sync with yet — name one once with `deja sync ssh <host>` and deja will remember it")
	}
	var failed int
	for _, p := range list {
		fmt.Fprintf(os.Stdout, "deja: %s\n", hostForEcho(p.Host))
		if err := syncSSHHost(dir, p.Host, false, full, true); err != nil {
			// One unreachable laptop must not stop the server from getting
			// what the desktop did.
			fmt.Fprintf(os.Stderr, "deja: %s: %s\n", hostForEcho(p.Host), safeForStatusline(err.Error(), 200))
			failed++
		}
	}
	if failed == len(list) {
		return fmt.Errorf("none of the %d machines could be reached", len(list))
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d of %d machines could not be reached\n", failed, len(list))
	}
	return nil
}

func syncSSHPush(dir, host string, full bool) error {
	if err := index.EnsureForSearch(dir, search.Options{All: true}, false, os.Stderr); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "deja-sync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// Watermarks advance only after the remote import succeeds: acknowledged
	// delivery, so a failed scp or remote error cannot silently drop records
	// from every later push.
	var commit func() error
	var n int
	if full {
		n, err = index.ExportFull(dir, tmp)
		commit = func() error { return nil }
	} else {
		// The connection takes the host as stored; the watermark takes the
		// folded name, so a push and a hand-run `sync export --peer` settle the
		// same machine rather than two (#1878).
		n, commit, err = index.ExportDeferred(dir, tmp, peers.Identity(host))
	}
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, sshExportedLine(n))
	if n == 0 {
		fmt.Fprintln(os.Stdout, "deja: nothing new to push")
		return errNothingToSend
	}
	batches, err := filepath.Glob(filepath.Join(tmp, "*.jsonl"))
	if err != nil || len(batches) == 0 {
		return fmt.Errorf("no batches produced")
	}
	rtmp, err := sshCapture(host, "mktemp -d")
	if err != nil {
		return err
	}
	// One scp per batch of paths, not one for all of them. Export writes a file
	// per source transcript, so a machine with tens of thousands of records
	// hands scp thousands of paths — and Windows refuses a command line over
	// 32,767 characters with "The filename or extension is too long", after the
	// export has already run (#2002).
	for _, chunk := range scpChunks(batches, sshArgsBudget(sshOpts(), host, rtmp)) {
		scpArgs := append(append(sshOpts(), "-q"), chunk...)
		scpArgs = append(scpArgs, host+":"+rtmp+"/")
		if out, err := sshRunner("scp", scpArgs...); err != nil {
			return fmt.Errorf("scp: %v: %s", err, remoteOutputForEcho(out))
		}
	}
	remote := fmt.Sprintf(`d=$(command -v deja || echo "$HOME/.local/bin/deja"); "$d" sync import %s; rc=$?; rm -rf %s; exit $rc`,
		shellQuote(rtmp), shellQuote(rtmp))
	out, err := sshRunner("ssh", append(sshOpts(), host, "sh -lc "+shellQuote(remote))...)
	out = strings.TrimSpace(out)
	if err != nil {
		return fmt.Errorf("remote import: %v: %s", err, remoteOutputForEcho(out))
	}
	if err := commit(); err != nil {
		return fmt.Errorf("delivered, but recording watermarks failed (next push may resend; harmless — import dedupes): %w", err)
	}
	if out != "" {
		fmt.Fprintf(os.Stdout, "%s: %s\n", hostForEcho(host), remoteOutputForEcho(out))
	}
	return nil
}

func syncSSHPull(dir, host string, full bool) error {
	rtmp, err := sshCapture(host, "mktemp -d")
	if err != nil {
		return err
	}
	exportCmd := "sync export"
	if full {
		exportCmd += " --full"
	}
	// Name this machine, so the remote settles what it sent us against us and
	// nobody else. Without it a pull shares one watermark with every hand-run
	// `deja sync export` there: whichever ran first settles the records, and
	// the other silently receives almost nothing.
	pullCmd := exportCmd
	if self := index.MachineName(); self != "" {
		pullCmd += " --peer " + shellQuote(self)
	}
	remoteCmd := func(c string) string {
		return fmt.Sprintf(`d=$(command -v deja || echo "$HOME/.local/bin/deja"); "$d" %s %s`, c, shellQuote(rtmp))
	}
	out, err := sshRunner("ssh", append(sshOpts(), host, "sh -lc "+shellQuote(remoteCmd(pullCmd)))...)
	out = strings.TrimSpace(out)
	// A deja too old to know --peer refuses the flag rather than ignoring it,
	// which is the behaviour that stops a typo from exporting nothing (#745).
	// Upgrading both machines at once is not something a person does, so fall
	// back to the shared watermark rather than failing the pull.
	if err != nil && strings.Contains(out, "unknown flag") && pullCmd != exportCmd {
		out, err = sshRunner("ssh", append(sshOpts(), host, "sh -lc "+shellQuote(remoteCmd(exportCmd)))...)
		out = strings.TrimSpace(out)
	}
	if err != nil {
		return fmt.Errorf("remote export: %v: %s", err, remoteOutputForEcho(out))
	}
	if out != "" {
		fmt.Fprintf(os.Stdout, "%s: %s\n", hostForEcho(host), remoteOutputForEcho(out))
	}
	cleanup := func() {
		_, _ = sshRunner("ssh", append(sshOpts(), host, "sh -lc "+shellQuote("rm -rf "+shellQuote(rtmp)))...)
	}
	if strings.Contains(out, "exported 0 records") {
		cleanup()
		fmt.Fprintln(os.Stdout, "deja: nothing new to pull")
		return nil
	}
	ltmp, err := os.MkdirTemp("", "deja-sync-")
	if err != nil {
		cleanup()
		return err
	}
	defer os.RemoveAll(ltmp)
	if out, err := sshRunner("scp", append(sshOpts(), "-q", host+":"+rtmp+"/*.jsonl", ltmp+"/")...); err != nil {
		cleanup()
		// The host stays as written here: the sentence hands over a command to
		// paste, and a bounded name would name no machine. Same tension as the
		// tombstone id in #1794.
		return fmt.Errorf("scp: %v: %s — the remote already advanced its watermark for this batch; recover it with `deja sync ssh %s --pull --full`",
			err, remoteOutputForEcho(out), pasteSafe(host))
	}
	cleanup()
	// Taken before the import so only what this exchange brings is attributed
	// to this host (#1887).
	before := importsByPeerName(dir)
	n, err := index.Import(dir, ltmp)
	if err != nil {
		return fmt.Errorf("%w — the remote already advanced its watermark for this batch; recover it with `deja sync ssh %s --pull --full`", err, pasteSafe(host))
	}
	learnPeerMachine(dir, host, before)
	fmt.Fprint(os.Stdout, sshCountLine("imported", n))
	return nil
}

func sshCapture(host, cmd string) (string, error) {
	out, err := sshRunner("ssh", append(sshOpts(), host, cmd)...)
	s := strings.TrimSpace(out)
	if err != nil {
		return "", fmt.Errorf("ssh %s: %v: %s", hostForEcho(host), err, remoteOutputForEcho(s))
	}
	// A remote that still prints something conversational on stdout (motd,
	// profile chatter) leaves the useful value on the last line.
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	// scp interprets the remote path through the remote shell on older
	// OpenSSH and through SFTP on newer ones, so neither quoting nor leaving
	// it bare is safe for a path with shell metacharacters. Reject it with a
	// message that points at the cause instead of failing obscurely later.
	if s == "" || strings.ContainsAny(s, "'\"\n") {
		return "", fmt.Errorf("ssh %s: unexpected output %q", hostForEcho(host), s)
	}
	if strings.ContainsAny(s, " \t*?$;`&|<>()") {
		return "", fmt.Errorf("ssh %s: remote temp path %q contains characters scp cannot carry — set TMPDIR on %s to a plain path", hostForEcho(host), s, hostForEcho(host))
	}
	return s, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// sshCountLine is a push or pull count, in the same words the local paths
// use: this file had its own format strings and said "exported 1 records" on
// the first sync anyone runs against a new machine, and the same for the
// pull (#2290).
func sshCountLine(verb string, n int) string {
	return fmt.Sprintf("deja: %s %d record%s\n", verb, n, pluralS(n))
}

func sshExportedLine(n int) string { return sshCountLine("exported", n) }

// scpCommandLineMax is the bound a single scp invocation's paths must stay
// under. Windows refuses a command line over 32,767 characters outright; the
// margin below it leaves room for the flags, the destination, and the quoting
// the shim adds. Unix allows far more, and one bound for both keeps the
// batching identical everywhere — the failure this exists for was reported
// after an export had already finished, which is the worst moment to find out.
const scpCommandLineMax = 24000

// sshArgsBudget is what is left for paths once the fixed part of the command
// line is counted.
func sshArgsBudget(opts []string, host, rtmp string) int {
	fixed := len("scp") + len(" -q ") + len(host) + len(rtmp) + 4
	for _, o := range opts {
		fixed += len(o) + 1
	}
	budget := scpCommandLineMax - fixed
	if budget < 512 {
		// A pathological set of options should still move one file at a time
		// rather than produce an empty chunk and loop forever.
		budget = 512
	}
	return budget
}

// scpChunks splits paths into groups whose command line stays inside budget.
// A single path longer than the budget still gets its own chunk: refusing to
// send it would be worse than letting the platform complain about that one.
func scpChunks(paths []string, budget int) [][]string {
	var out [][]string
	var cur []string
	n := 0
	for _, p := range paths {
		cost := len(p) + 1
		if len(cur) > 0 && n+cost > budget {
			out = append(out, cur)
			cur, n = nil, 0
		}
		cur = append(cur, p)
		n += cost
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
