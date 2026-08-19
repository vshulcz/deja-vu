package main

import (
	"bytes"
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
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), err
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
	if both {
		if err := syncOneWay(dir, host, false, full); err != nil {
			return err
		}
		return syncOneWay(dir, host, true, full)
	}
	return syncOneWay(dir, host, pull, full)
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
	if rerr := peers.Record(host, pull, time.Now(), err); rerr != nil && err == nil {
		fmt.Fprintf(os.Stderr, "deja: synced with %s, but could not record it: %v\n", host, rerr)
	}
	return err
}

// runSyncAll exchanges with every machine this one already syncs with. The
// host had to be typed every time, which with three machines is six commands
// and a thing to remember rather than a thing that happens. Records do not
// travel through a third machine — an export never forwards what arrived by
// sync — so every pair has to meet directly, and that is what this does.
func runSyncAll(dir string, full bool) error {
	list := peers.Load()
	if len(list) == 0 {
		return fmt.Errorf("no machines to sync with yet — name one once with `deja sync ssh <host>` and deja will remember it")
	}
	var failed int
	for _, p := range list {
		fmt.Fprintf(os.Stdout, "deja: %s\n", p.Host)
		if err := syncSSHHost(dir, p.Host, false, full, true); err != nil {
			// One unreachable laptop must not stop the server from getting
			// what the desktop did.
			fmt.Fprintf(os.Stderr, "deja: %s: %v\n", p.Host, err)
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
		n, commit, err = index.ExportDeferred(dir, tmp, host)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "deja: exported %d records\n", n)
	if n == 0 {
		fmt.Fprintln(os.Stdout, "deja: nothing new to push")
		return nil
	}
	batches, err := filepath.Glob(filepath.Join(tmp, "*.jsonl"))
	if err != nil || len(batches) == 0 {
		return fmt.Errorf("no batches produced")
	}
	rtmp, err := sshCapture(host, "mktemp -d")
	if err != nil {
		return err
	}
	scpArgs := append([]string{"-q"}, batches...)
	scpArgs = append(scpArgs, host+":"+rtmp+"/")
	if out, err := sshRunner("scp", scpArgs...); err != nil {
		return fmt.Errorf("scp: %v: %s", err, strings.TrimSpace(out))
	}
	remote := fmt.Sprintf(`d=$(command -v deja || echo "$HOME/.local/bin/deja"); "$d" sync import %s; rc=$?; rm -rf %s; exit $rc`,
		shellQuote(rtmp), shellQuote(rtmp))
	out, err := sshRunner("ssh", host, "sh -lc "+shellQuote(remote))
	out = strings.TrimSpace(out)
	if err != nil {
		return fmt.Errorf("remote import: %v: %s", err, out)
	}
	if err := commit(); err != nil {
		return fmt.Errorf("delivered, but recording watermarks failed (next push may resend; harmless — import dedupes): %w", err)
	}
	if out != "" {
		fmt.Fprintf(os.Stdout, "%s: %s\n", host, out)
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
	out, err := sshRunner("ssh", host, "sh -lc "+shellQuote(remoteCmd(pullCmd)))
	out = strings.TrimSpace(out)
	// A deja too old to know --peer refuses the flag rather than ignoring it,
	// which is the behaviour that stops a typo from exporting nothing (#745).
	// Upgrading both machines at once is not something a person does, so fall
	// back to the shared watermark rather than failing the pull.
	if err != nil && strings.Contains(out, "unknown flag") && pullCmd != exportCmd {
		out, err = sshRunner("ssh", host, "sh -lc "+shellQuote(remoteCmd(exportCmd)))
		out = strings.TrimSpace(out)
	}
	if err != nil {
		return fmt.Errorf("remote export: %v: %s", err, out)
	}
	if out != "" {
		fmt.Fprintf(os.Stdout, "%s: %s\n", host, out)
	}
	cleanup := func() {
		_, _ = sshRunner("ssh", host, "sh -lc "+shellQuote("rm -rf "+shellQuote(rtmp)))
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
	if out, err := sshRunner("scp", "-q", host+":"+rtmp+"/*.jsonl", ltmp+"/"); err != nil {
		cleanup()
		return fmt.Errorf("scp: %v: %s — the remote already advanced its watermark for this batch; recover it with `deja sync ssh %s --pull --full`", err, strings.TrimSpace(out), host)
	}
	cleanup()
	n, err := index.Import(dir, ltmp)
	if err != nil {
		return fmt.Errorf("%w — the remote already advanced its watermark for this batch; recover it with `deja sync ssh %s --pull --full`", err, host)
	}
	fmt.Fprintf(os.Stdout, "deja: imported %d records\n", n)
	return nil
}

func exportBatches(dir, out string, full bool) (int, error) {
	if full {
		return index.ExportFull(dir, out)
	}
	return index.Export(dir, out)
}

func sshCapture(host, cmd string) (string, error) {
	out, err := sshRunner("ssh", host, cmd)
	s := strings.TrimSpace(out)
	if err != nil {
		return "", fmt.Errorf("ssh %s: %v: %s", host, err, s)
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
		return "", fmt.Errorf("ssh %s: unexpected output %q", host, s)
	}
	if strings.ContainsAny(s, " \t*?$;`&|<>()") {
		return "", fmt.Errorf("ssh %s: remote temp path %q contains characters scp cannot carry — set TMPDIR on %s to a plain path", host, s, host)
	}
	return s, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
