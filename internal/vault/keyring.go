package vault

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/proc"
)

// Keyring stores and retrieves raw 32-byte DEKs in an OS credential store.
type Keyring interface {
	Name() string
	Set(keyID string, dek []byte) error
	Get(keyID string) ([]byte, error)
	Delete(keyID string) error
}

var (
	activeKeyring Keyring = osKeyring{}
	execKeyringFn         = defaultExecKeyring
)

// SetKeyringForTest swaps the process keyring backend for tests and clears any
// cached key state, returning a restore closure.
func SetKeyringForTest(kr Keyring) func() {
	prev := activeKeyring
	activeKeyring = kr
	ClearCache()
	return func() {
		activeKeyring = prev
		ClearCache()
	}
}

func defaultExecKeyring(bin string, args []string, stdin string, extraEnv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := proc.CommandContext(ctx, bin, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.CombinedOutput()
}

type osKeyring struct{}

func keyringBackendName(goos string) string {
	switch goos {
	case "darwin":
		return "keychain"
	case "linux":
		return "secret-service"
	case "windows":
		return "dpapi"
	default:
		return "passphrase"
	}
}

func (osKeyring) Name() string {
	return keyringBackendName(runtime.GOOS)
}

const keyringService = "deja-vu-vault"

func validKeyID(keyID string) bool {
	if len(keyID) != 16 {
		return false
	}
	for i := range len(keyID) {
		c := keyID[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func keyringCommand(goos, op, keyID, secret string) (bin string, args []string, stdin string, extraEnv []string, err error) {
	switch goos {
	case "darwin":
		switch op {
		case "set":
			return "/usr/bin/security", []string{"add-generic-password", "-U", "-s", keyringService, "-a", keyID, "-w", secret}, "", nil, nil
		case "get":
			return "/usr/bin/security", []string{"find-generic-password", "-s", keyringService, "-a", keyID, "-w"}, "", nil, nil
		case "delete":
			return "/usr/bin/security", []string{"delete-generic-password", "-s", keyringService, "-a", keyID}, "", nil, nil
		}
	case "linux":
		var extraEnv []string
		if strings.TrimSpace(os.Getenv("DBUS_SESSION_BUS_ADDRESS")) == "" {
			if sock := linuxDBusSocket(); sock != "" {
				extraEnv = append(extraEnv, "DBUS_SESSION_BUS_ADDRESS=unix:path="+sock)
			}
		}
		switch op {
		case "set":
			return "secret-tool", []string{"store", "--label=deja-vu vault", "service", keyringService, "account", keyID}, secret, extraEnv, nil
		case "get":
			return "secret-tool", []string{"lookup", "service", keyringService, "account", keyID}, "", extraEnv, nil
		case "delete":
			return "secret-tool", []string{"clear", "service", keyringService, "account", keyID}, "", extraEnv, nil
		}
	case "windows":
		path, perr := dpapiPath(keyID)
		if perr != nil {
			return "", nil, "", nil, perr
		}
		if op == "set" {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return "", nil, "", nil, err
			}
			script := `$sec = ConvertTo-SecureString -AsPlainText -Force ([Console]::In.ReadLine()); ` +
				`$enc = ConvertFrom-SecureString $sec; [IO.File]::WriteAllText($env:DEJA_DPAPI_PATH, $enc)`
			return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", script}, secret + "\n", []string{"DEJA_DPAPI_PATH=" + path}, nil
		}
		if op == "get" {
			script := `$enc = [IO.File]::ReadAllText($env:DEJA_DPAPI_PATH); ` +
				`$sec = ConvertTo-SecureString $enc; ` +
				`[Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))`
			return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", script}, "", []string{"DEJA_DPAPI_PATH=" + path}, nil
		}
		return "", nil, "", []string{"DEJA_DPAPI_PATH=" + path}, nil
	}
	return "", nil, "", nil, errors.New("OS keyring unsupported on this platform")
}

func usePassphraseOnly() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("DEJA_VAULT_BACKEND")), "passphrase")
}

func linuxDBusSocket() string {
	var cands []string
	if dir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); dir != "" {
		cands = append(cands, filepath.Join(dir, "bus"))
	}
	if uid := os.Getuid(); uid >= 0 {
		cands = append(cands, filepath.FromSlash("/run/user/"+strconv.Itoa(uid)+"/bus"))
	}
	for _, p := range cands {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return p
		}
	}
	return ""
}

func keyringAvailableFor(goos string, lookPath func(string) (string, error)) bool {
	switch goos {
	case "darwin":
		_, err := lookPath("/usr/bin/security")
		return err == nil
	case "linux":
		if _, err := lookPath("secret-tool"); err != nil {
			return false
		}
		return strings.TrimSpace(os.Getenv("DBUS_SESSION_BUS_ADDRESS")) != "" || linuxDBusSocket() != ""
	case "windows":
		return true
	default:
		return false
	}
}

// KeyringAvailable reports whether the platform credential backend appears usable.
func KeyringAvailable() bool {
	if usePassphraseOnly() {
		return false
	}
	if _, isOS := activeKeyring.(osKeyring); !isOS && activeKeyring != nil {
		return true
	}
	return keyringAvailableFor(runtime.GOOS, exec.LookPath)
}

func (osKeyring) Set(keyID string, dek []byte) error {
	if usePassphraseOnly() {
		return errors.New("passphrase backend forced")
	}
	if !validKeyID(keyID) || len(dek) != 32 {
		return errors.New("invalid vault key material")
	}
	bin, args, stdin, env, err := keyringCommand(runtime.GOOS, "set", keyID, hex.EncodeToString(dek))
	if err != nil {
		return err
	}
	if out, err := execKeyringFn(bin, args, stdin, env); err != nil {
		return fmt.Errorf("%s set: %w (%s)", activeKeyring.Name(), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (osKeyring) Get(keyID string) ([]byte, error) {
	if usePassphraseOnly() {
		return nil, errors.New("passphrase backend forced")
	}
	if !validKeyID(keyID) {
		return nil, errors.New("invalid vault key id")
	}
	bin, args, stdin, env, err := keyringCommand(runtime.GOOS, "get", keyID, "")
	if err != nil {
		return nil, err
	}
	out, err := execKeyringFn(bin, args, stdin, env)
	if err != nil {
		return nil, err
	}
	dek, err := hex.DecodeString(strings.TrimSpace(string(bytes.TrimSpace(out))))
	if err != nil || len(dek) != 32 {
		return nil, errors.New("corrupt credential store entry")
	}
	return dek, nil
}

func deleteOSKeyring(goos, keyID string) error {
	if usePassphraseOnly() {
		return nil
	}
	if !validKeyID(keyID) {
		return nil
	}
	if goos == "windows" {
		path, err := dpapiPath(keyID)
		if err != nil {
			return err
		}
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
		return nil
	}
	bin, args, stdin, env, err := keyringCommand(goos, "delete", keyID, "")
	if err != nil {
		return err
	}
	_, err = execKeyringFn(bin, args, stdin, env)
	return err
}

func (osKeyring) Delete(keyID string) error {
	return deleteOSKeyring(runtime.GOOS, keyID)
}

func dpapiPath(keyID string) (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = dir
	}
	return filepath.Join(base, "deja", "vault-"+keyID+".dpapi"), nil
}
