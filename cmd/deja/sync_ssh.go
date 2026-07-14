package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// sshRunner is swapped in tests.
var sshRunner = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func runSyncSSH(args []string) error {
	host := ""
	pull, full := false, false
	for _, a := range args {
		switch a {
		case "--pull":
			pull = true
		case "--full":
			full = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("sync ssh: unknown flag %s", a)
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
	if pull {
		return syncSSHPull(host, full)
	}
	return syncSSHPush(host, full)
}

func syncSSHPush(host string, full bool) error {
	if err := index.EnsureForSearch(index.DefaultDir(), search.Options{All: true}, false, os.Stderr); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "deja-sync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	n, err := exportBatches(tmp, full)
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
	if out != "" {
		fmt.Fprintf(os.Stdout, "%s: %s\n", host, out)
	}
	return nil
}

func syncSSHPull(host string, full bool) error {
	rtmp, err := sshCapture(host, "mktemp -d")
	if err != nil {
		return err
	}
	exportCmd := "sync export"
	if full {
		exportCmd += " --full"
	}
	remote := fmt.Sprintf(`d=$(command -v deja || echo "$HOME/.local/bin/deja"); "$d" %s %s`, exportCmd, shellQuote(rtmp))
	out, err := sshRunner("ssh", host, "sh -lc "+shellQuote(remote))
	out = strings.TrimSpace(out)
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
		return fmt.Errorf("scp: %v: %s", err, strings.TrimSpace(out))
	}
	cleanup()
	n, err := index.Import(index.DefaultDir(), ltmp)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "deja: imported %d records\n", n)
	return nil
}

func exportBatches(dir string, full bool) (int, error) {
	if full {
		return index.ExportFull(index.DefaultDir(), dir)
	}
	return index.Export(index.DefaultDir(), dir)
}

func sshCapture(host, cmd string) (string, error) {
	out, err := sshRunner("ssh", host, cmd)
	s := strings.TrimSpace(out)
	if err != nil {
		return "", fmt.Errorf("ssh %s: %v: %s", host, err, s)
	}
	if s == "" || strings.ContainsAny(s, "'\"\n") {
		return "", fmt.Errorf("ssh %s: unexpected output %q", host, s)
	}
	return s, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
