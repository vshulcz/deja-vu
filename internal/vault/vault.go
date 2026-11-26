// Package vault encrypts derived memory stores and personal notes at rest
// with AES-256-GCM and a key wrapped by passphrase or held in the OS keyring.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

const (
	// EnvVaultFile overrides the vault metadata file location.
	EnvVaultFile = "DEJA_VAULT_FILE"
	// EnvVaultPassphrase supplies the passphrase non-interactively.
	EnvVaultPassphrase = "DEJA_VAULT_PASSPHRASE" //nolint:gosec // environment variable name, not a credential
	// EnvVaultKey supplies a hex-encoded 32-byte raw DEK directly.
	EnvVaultKey = "DEJA_VAULT_KEY"

	// NoteLinePrefix identifies AEAD-encrypted lines in personal notes.jsonl
	// and snapshot digest fields in .injections.jsonl.
	NoteLinePrefix = "enc:v1:"

	notesDomain     = "deja-vault-notes-v1"
	snapshotsDomain = "deja-vault-snapshots-v1"
	archiveDomain   = "deja-vault-archive-v1"
	wrapDomain      = "deja-vault-wrap-v1"
)

var (
	// ErrLocked reports that encryption is enabled and the vault is locked.
	ErrLocked = errors.New("memory store is locked — run `deja unlock`")
	// ErrKeyUnavailable reports that the vault is unlocked in config but its
	// decryption key could not be resolved from env or the OS credential store.
	ErrKeyUnavailable = errors.New("encryption key is not in credential store — run `deja unlock`")
	// ErrAlreadyInitialized reports that an encrypted vault config already exists.
	ErrAlreadyInitialized = errors.New("encrypted store already initialized")
	// ErrNotInitialized reports that no encrypted vault config exists yet.
	ErrNotInitialized = errors.New("encrypted store is not initialized — run `deja init --encrypted`")

	// KDFIterations controls PBKDF2-SHA256 cost when deriving KEKs.
	KDFIterations = 600_000
)

// Config persists encrypted-store metadata (never the raw DEK or passphrase).
type Config struct {
	Version         int    `json:"version"`
	KDF             string `json:"kdf"`
	Salt            string `json:"salt"`
	Iterations      int    `json:"iterations"`
	WrappedDEK      string `json:"wrapped_dek"`
	KeyID           string `json:"key_id"`
	KeyBackend      string `json:"key_backend"`
	Locked          bool   `json:"locked"`
	AutoLockSeconds int64  `json:"auto_lock_seconds,omitempty"`
	UnlockedAt      string `json:"unlocked_at,omitempty"`
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func configHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && filepath.IsAbs(dir) {
			return dir
		}
	}
	h := homeDir()
	if !filepath.IsAbs(h) {
		return ""
	}
	return filepath.Join(h, ".config")
}

// ConfigPath returns the vault configuration path.
func ConfigPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvVaultFile)); p != "" {
		return p
	}
	base := configHome()
	if !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, "deja", "vault.json")
}

// NotesPath returns the personal notes.jsonl path matching sources.NotesFile.
func NotesPath() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_NOTES_FILE")); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "deja", "notes.jsonl")
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "deja", "notes.jsonl")
		}
		return filepath.Join(homeDir(), "AppData", "Roaming", "deja", "notes.jsonl")
	}
	return filepath.Join(homeDir(), ".local", "share", "deja", "notes.jsonl")
}

// DefaultIndexDir returns the default derived index path matching index.DefaultDir.
func DefaultIndexDir() string {
	if v := strings.TrimSpace(os.Getenv("DEJA_INDEX_DIR")); v != "" {
		return v
	}
	if base := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(base) {
		return filepath.Join(base, "deja", "index.db")
	}
	h := homeDir()
	if !filepath.IsAbs(h) {
		return ""
	}
	return filepath.Join(h, ".cache", "deja", "index.db")
}

func effectiveIndexDir(indexDir string) string {
	if indexDir != "" {
		return indexDir
	}
	return DefaultIndexDir()
}

// ArchivePath returns the sealed archive path alongside indexDir.
func ArchivePath(indexDir string) string {
	return strings.TrimSuffix(indexDir, string(filepath.Separator)) + ".vault"
}

// Enabled reports whether an encrypted vault configuration is present on disk.
func Enabled() bool {
	p := ConfigPath()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// IsLocked reports whether encryption is enabled and the store is locked. It
// never reports a lock that was not sealed: when an idle timeout cannot seal
// (no key to seal with), it reports false and the store stays unlocked, the
// same refusal CheckUnlocked returns.
func IsLocked(indexDir string) bool {
	if !Enabled() {
		return false
	}
	cfg, err := LoadConfig()
	if err != nil {
		return true
	}
	if cfg.Locked {
		return true
	}
	dir := effectiveIndexDir(indexDir)
	if idleExpired(dir, cfg) {
		return autoLock(dir, cfg) == nil
	}
	return false
}

// LoadConfig reads and validates vault.json.
func LoadConfig() (Config, error) {
	p := ConfigPath()
	if p == "" {
		return Config{}, errors.New("cannot determine vault config path")
	}
	if cfg, ok := loadConfigCached(p); ok {
		return cfg, nil
	}
	fi, statErr := os.Stat(p)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, ErrNotInitialized
		}
		return Config{}, fmt.Errorf("vault unavailable (failing closed): %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("vault config malformed (failing closed): %w", err)
	}
	if cfg.Version != 1 || cfg.WrappedDEK == "" || cfg.Salt == "" || cfg.KeyID == "" {
		return Config{}, errors.New("vault config incomplete (failing closed)")
	}
	if statErr == nil {
		storeConfigCache(p, fi, cfg)
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	invalidateConfigCache()
	p := ConfigPath()
	if p == "" {
		return errors.New("cannot determine vault config path")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(p, append(b, '\n'), 0o600); err != nil {
		return err
	}
	invalidateConfigCache()
	return nil
}

// CheckUnlocked verifies that the store is usable and returns the active 32-byte
// DEK when encryption is enabled. Returns (nil, nil) for unencrypted stores.
func CheckUnlocked(indexDir string) ([]byte, error) {
	if !Enabled() {
		return nil, nil
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Locked {
		ClearCache()
		return nil, ErrLocked
	}
	dir := effectiveIndexDir(indexDir)
	if idleExpired(dir, cfg) {
		if lockErr := autoLock(dir, cfg); lockErr != nil {
			return nil, lockErr
		}
		return nil, ErrLocked
	}
	return resolveDEK(cfg)
}

// resolveDEK returns the active 32-byte DEK from DEJA_VAULT_KEY, then
// DEJA_VAULT_PASSPHRASE, then the OS keyring. It never returns a partial key:
// a host with none of the three gets ErrKeyUnavailable.
func resolveDEK(cfg Config) ([]byte, error) {
	if rawHex := strings.TrimSpace(os.Getenv(EnvVaultKey)); rawHex != "" {
		dek, decErr := hex.DecodeString(rawHex)
		if decErr != nil || len(dek) != 32 || !keyIDMatches(dek, cfg.KeyID) {
			return nil, errors.New("DEJA_VAULT_KEY does not match vault")
		}
		return dek, nil
	}
	if pass := os.Getenv(EnvVaultPassphrase); pass != "" {
		dek, unwrapErr := unwrapDEKCached(cfg, pass)
		if unwrapErr != nil {
			return nil, unwrapErr
		}
		return dek, nil
	}
	return keyringDEKCached(cfg)
}

func idleExpired(indexDir string, cfg Config) bool {
	if cfg.AutoLockSeconds <= 0 {
		return false
	}
	var last time.Time
	if cfg.UnlockedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, cfg.UnlockedAt); err == nil {
			last = t
		}
	}
	dir := effectiveIndexDir(indexDir)
	if dir != "" {
		if fi, err := os.Stat(strings.TrimSuffix(dir, string(filepath.Separator)) + ".usage.jsonl"); err == nil {
			if fi.ModTime().After(last) {
				last = fi.ModTime()
			}
		}
	}
	if last.IsZero() {
		return true
	}
	return time.Since(last) > time.Duration(cfg.AutoLockSeconds)*time.Second
}

// autoLock seals the plaintext store before marking the vault locked. It
// reports success only after SealTree has run with a resolvable DEK, so a
// keyringless host with no DEJA_VAULT_KEY/DEJA_VAULT_PASSPHRASE fails loudly
// instead of reporting "locked" while leaving the plaintext index on disk.
func autoLock(indexDir string, cfg Config) error {
	ClearCache()
	dir := effectiveIndexDir(indexDir)
	if dir != "" && hasPlaintextStore(dir) {
		dek, err := resolveDEK(cfg)
		if err != nil {
			return fmt.Errorf("auto-lock refused: cannot seal plaintext memory store — %w", err)
		}
		sealErr := SealTree(dir, dek)
		clear(dek)
		if sealErr != nil {
			return fmt.Errorf("auto-lock refused: sealing memory store failed — %w", sealErr)
		}
	}
	cfg.Locked = true
	if err := saveConfig(cfg); err != nil {
		return err
	}
	_ = activeKeyring.Delete(cfg.KeyID)
	return nil
}

func keyIDFor(dek []byte) string {
	sum := sha256.Sum256(append([]byte("deja-key-id:"), dek...))
	return hex.EncodeToString(sum[:8])
}

func keyIDMatches(dek []byte, want string) bool {
	got := keyIDFor(dek)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func deriveKEK(passphrase string, salt []byte, iterations int) ([]byte, error) {
	if iterations < 1 {
		iterations = KDFIterations
	}
	return pbkdf2.Key(sha256.New, passphrase, salt, iterations, 32)
}

func deriveSubkey(dek []byte, domain string) ([]byte, error) {
	return hkdf.Key(sha256.New, dek, nil, domain, 32)
}

func sealAEAD(key, aad, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, aad), nil
}

func openAEAD(key, aad, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, errors.New("ciphertext truncated")
	}
	return gcm.Open(nil, sealed[:ns], sealed[ns:], aad)
}

func wrapDEK(dek []byte, passphrase string, salt []byte, iterations int) (string, error) {
	kek, err := deriveKEK(passphrase, salt, iterations)
	if err != nil {
		return "", err
	}
	defer clear(kek)
	sealed, err := sealAEAD(kek, []byte(wrapDomain), dek)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func toB64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func unwrapDEK(cfg Config, passphrase string) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(cfg.Salt)
	if err != nil {
		return nil, fmt.Errorf("invalid vault salt: %w", err)
	}
	wrapped, err := base64.StdEncoding.DecodeString(cfg.WrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("invalid wrapped key: %w", err)
	}
	kek, err := deriveKEK(passphrase, salt, cfg.Iterations)
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	dek, err := openAEAD(kek, []byte(wrapDomain), wrapped)
	if err != nil {
		return nil, errors.New("invalid vault passphrase")
	}
	if len(dek) != 32 || !keyIDMatches(dek, cfg.KeyID) {
		clear(dek)
		return nil, errors.New("invalid vault passphrase")
	}
	return dek, nil
}

// SealLine encrypts plain under a domain-separated subkey and encodes it with NoteLinePrefix.
func SealLine(dek []byte, domain string, plain []byte) (string, error) {
	gcm, err := aeadFor(dek, domain)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, fileNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plain, []byte(domain))
	return NoteLinePrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// OpenLine decrypts a NoteLinePrefix-encoded ciphertext under the domain subkey.
func OpenLine(dek []byte, domain, encoded string) ([]byte, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(encoded), NoteLinePrefix)
	if !ok {
		return nil, errors.New("not a vault-sealed line")
	}
	raw, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return nil, err
	}
	gcm, err := aeadFor(dek, domain)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("ciphertext truncated")
	}
	return gcm.Open(nil, raw[:ns], raw[ns:], []byte(domain))
}

// SealNoteLine encrypts a personal notes.jsonl record line.
func SealNoteLine(dek, plain []byte) (string, error) {
	return SealLine(dek, notesDomain, plain)
}

// OpenNoteLine decrypts an encrypted personal notes.jsonl record line.
func OpenNoteLine(dek []byte, encoded string) ([]byte, error) {
	return OpenLine(dek, notesDomain, encoded)
}

// SealSnapshot encrypts an injection snapshot digest field.
func SealSnapshot(dek []byte, digest string) (string, error) {
	return SealLine(dek, snapshotsDomain, []byte(digest))
}

// OpenSnapshot decrypts an encrypted injection snapshot digest field.
func OpenSnapshot(dek []byte, encoded string) (string, error) {
	b, err := OpenLine(dek, snapshotsDomain, encoded)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
