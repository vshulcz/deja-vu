package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/vault"
)

var (
	initFlags   = []string{"--encrypted", "--passphrase-stdin", "--auto-lock"}
	lockFlags   = []string{"--passphrase-stdin"}
	unlockFlags = []string{"--passphrase-stdin"}
)

func cmdInit(dir string, args []string) error {
	args = splitKnownValueFlags(args, "--auto-lock")
	encrypted := false
	passStdin := false
	var autoLock time.Duration
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--encrypted":
			encrypted = true
		case "--passphrase-stdin":
			passStdin = true
		case "--auto-lock":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return errors.New("init: --auto-lock needs a duration (for example 30m)")
			}
			i++
			d, err := parseDur(args[i])
			if err != nil || d < 0 {
				return fmt.Errorf("init: invalid --auto-lock duration %q", args[i])
			}
			autoLock = d
		default:
			if strings.HasPrefix(args[i], "-") {
				return unknownFlag("init", args[i], initFlags)
			}
			return fmt.Errorf("init: unexpected argument %q", args[i])
		}
	}
	if !encrypted {
		return errors.New("init: pass --encrypted to initialize encrypted storage")
	}
	pass, err := readVaultPassphrase(passStdin, true)
	if err != nil {
		return err
	}
	backend, err := vault.Init(dir, pass, autoLock)
	if err != nil {
		return err
	}
	// Seal the derived index now. Init only encrypts notes and snapshot
	// digests in place; without this the session index (past prompts) sits in
	// plaintext until someone remembers to run `deja lock`.
	if err := vault.Lock(dir, pass); err != nil {
		return fmt.Errorf("init: sealed store setup failed: %w", err)
	}
	fmt.Fprintf(os.Stdout, "deja: initialized encrypted memory (backend: %s, sealed: yes, config: %s)\n", backend, vault.ConfigPath())
	fmt.Fprintln(os.Stdout, "deja: memory store is locked — run `deja unlock` before use")
	return nil
}

func cmdLock(dir string, args []string) error {
	passStdin := false
	for _, a := range args {
		switch a {
		case "--passphrase-stdin":
			passStdin = true
		default:
			if strings.HasPrefix(a, "-") {
				return unknownFlag("lock", a, lockFlags)
			}
			return fmt.Errorf("lock: unexpected argument %q", a)
		}
	}
	var pass string
	if passStdin {
		var err error
		pass, err = readVaultPassphrase(true, true)
		if err != nil {
			return err
		}
	} else {
		pass = os.Getenv(vault.EnvVaultPassphrase)
	}
	if err := vault.Lock(dir, pass); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "deja: memory store locked")
	return nil
}

func cmdUnlock(dir string, args []string) error {
	passStdin := false
	for _, a := range args {
		switch a {
		case "--passphrase-stdin":
			passStdin = true
		default:
			if strings.HasPrefix(a, "-") {
				return unknownFlag("unlock", a, unlockFlags)
			}
			return fmt.Errorf("unlock: unexpected argument %q", a)
		}
	}
	pass, err := readVaultPassphrase(passStdin, true)
	if err != nil {
		return err
	}
	if err := vault.Unlock(dir, pass); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "deja: memory store unlocked")
	return nil
}

func readVaultPassphrase(fromStdin, required bool) (string, error) {
	if !fromStdin {
		if p := os.Getenv(vault.EnvVaultPassphrase); p != "" {
			return p, nil
		}
		if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
			// Piped non-TTY without --passphrase-stdin stays explicit unless env is set.
			if !required {
				return "", nil
			}
			return "", errors.New("passphrase required — pass --passphrase-stdin or set " + vault.EnvVaultPassphrase)
		}
		fmt.Fprint(os.Stderr, "Passphrase: ")
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	pass := strings.TrimRight(line, "\r\n")
	if required && pass == "" {
		return "", errors.New("empty passphrase")
	}
	return pass, nil
}
