package vault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

const (
	// DomainIndexFile isolates AES-GCM subkeys for whole-file index artifacts.
	DomainIndexFile = "deja-vault-index-file-v1"
	// DomainRecordLog isolates AES-GCM subkeys for live records.bin payloads.
	DomainRecordLog = "deja-vault-record-log-v1"
	// DomainSidecar isolates AES-GCM subkeys for live sibling sidecar files.
	DomainSidecar = "deja-vault-sidecar-v1"
	// DomainEmbed isolates AES-GCM subkeys for streaming embedding sidecars.
	DomainEmbed = "deja-vault-embed-v1"
	// DomainSpill isolates AES-GCM subkeys for temporary spill shard streams.
	DomainSpill = "deja-vault-spill-v1"

	fileMagic       = "DJVF1"
	fileNonceSize   = 12
	fileTagSize     = 16
	keyringCacheTTL = 5 * time.Second

	maxSealedBytesPayload = 512 << 20

	// RecordOverhead is the per-record byte overhead added by 12B nonce + 16B GCM tag.
	RecordOverhead = fileNonceSize + fileTagSize
)

var (
	wholeFileSidecars = []string{
		".dejavu",
		".envblock",
		".live",
		".tui-history",
	}
	lineEncryptedSidecars = []string{
		".hookseen",
		".forks",
	}
)

type (
	cfgCacheEntry struct {
		path    string
		modTime int64
		size    int64
		mode    os.FileMode
		cfg     Config
		valid   bool
	}
	pbCacheKey struct {
		keyID      string
		wrappedDEK string
		salt       string
		iterations int
	}
	pbCacheEntry struct {
		pass []byte
		dek  []byte
	}
	krCacheKey struct {
		cfgPath    string
		keyID      string
		wrappedDEK string
		backend    string
		backendEnv string
		krID       string
		execPtr    uintptr
	}
	aeadCacheKey struct {
		dekSum [sha256.Size]byte
		domain string
	}
)

var (
	cacheMu  sync.Mutex
	cfgCache cfgCacheEntry
	pbCache  = map[pbCacheKey]pbCacheEntry{}
	krEntry  struct {
		key       krCacheKey
		dek       []byte
		expiresAt time.Time
		valid     bool
	}
	aeadCache = map[aeadCacheKey]cipher.AEAD{}

	migratorMu sync.Mutex
	migrators  = map[string]func(indexDir string, dek []byte) error{}
)

// ClearCache wipes cached DEKs, config snapshots, and subkey ciphers from memory.
func ClearCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cfgCache = cfgCacheEntry{}
	for k, v := range pbCache {
		clear(v.pass)
		clear(v.dek)
		delete(pbCache, k)
	}
	clear(krEntry.dek)
	krEntry.dek = nil
	krEntry.valid = false
	clear(aeadCache)
}

func invalidateConfigCache() {
	cacheMu.Lock()
	cfgCache = cfgCacheEntry{}
	clear(krEntry.dek)
	krEntry.dek = nil
	krEntry.valid = false
	cacheMu.Unlock()
}

func loadConfigCached(p string) (Config, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return Config{}, false
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cfgCache.valid &&
		cfgCache.path == p &&
		cfgCache.modTime == fi.ModTime().UnixNano() &&
		cfgCache.size == fi.Size() &&
		cfgCache.mode == fi.Mode() {
		return cfgCache.cfg, true
	}
	return Config{}, false
}

func storeConfigCache(p string, fi os.FileInfo, cfg Config) {
	if fi == nil {
		return
	}
	cacheMu.Lock()
	cfgCache = cfgCacheEntry{
		path:    p,
		modTime: fi.ModTime().UnixNano(),
		size:    fi.Size(),
		mode:    fi.Mode(),
		cfg:     cfg,
		valid:   true,
	}
	cacheMu.Unlock()
}

func unwrapDEKCached(cfg Config, passphrase string) ([]byte, error) {
	key := pbCacheKey{
		keyID:      cfg.KeyID,
		wrappedDEK: cfg.WrappedDEK,
		salt:       cfg.Salt,
		iterations: cfg.Iterations,
	}
	cacheMu.Lock()
	if cached, ok := pbCache[key]; ok && subtle.ConstantTimeCompare(cached.pass, []byte(passphrase)) == 1 {
		out := append([]byte(nil), cached.dek...)
		cacheMu.Unlock()
		return out, nil
	}
	cacheMu.Unlock()

	dek, err := unwrapDEK(cfg, passphrase)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	if prev, ok := pbCache[key]; ok {
		clear(prev.pass)
		clear(prev.dek)
	}
	pbCache[key] = pbCacheEntry{
		pass: append([]byte(nil), passphrase...),
		dek:  append([]byte(nil), dek...),
	}
	cacheMu.Unlock()
	return dek, nil
}

func keyringIdentity(kr Keyring) string {
	if kr == nil {
		return ""
	}
	rv := reflect.ValueOf(kr)
	k := rv.Kind()
	if k == reflect.Pointer || k == reflect.Map || k == reflect.Slice || k == reflect.Chan || k == reflect.UnsafePointer {
		return fmt.Sprintf("%T:%x", kr, rv.Pointer())
	}
	return fmt.Sprintf("%T:%s", kr, kr.Name())
}

func execFnPointer() uintptr {
	if execKeyringFn == nil {
		return 0
	}
	return reflect.ValueOf(execKeyringFn).Pointer()
}

func keyringDEKCached(cfg Config) ([]byte, error) {
	k := krCacheKey{
		cfgPath:    ConfigPath(),
		keyID:      cfg.KeyID,
		wrappedDEK: cfg.WrappedDEK,
		backend:    cfg.KeyBackend,
		backendEnv: strings.TrimSpace(os.Getenv("DEJA_VAULT_BACKEND")),
		krID:       keyringIdentity(activeKeyring),
		execPtr:    execFnPointer(),
	}
	now := time.Now()
	cacheMu.Lock()
	if krEntry.valid && krEntry.key == k && now.Before(krEntry.expiresAt) {
		out := append([]byte(nil), krEntry.dek...)
		cacheMu.Unlock()
		return out, nil
	}
	cacheMu.Unlock()

	dek, err := activeKeyring.Get(cfg.KeyID)
	if err != nil || len(dek) != 32 || !keyIDMatches(dek, cfg.KeyID) {
		clear(dek)
		return nil, ErrKeyUnavailable
	}
	cacheMu.Lock()
	clear(krEntry.dek)
	krEntry.key = k
	krEntry.dek = append([]byte(nil), dek...)
	krEntry.expiresAt = now.Add(keyringCacheTTL)
	krEntry.valid = true
	cacheMu.Unlock()
	return dek, nil
}

func aeadFor(dek []byte, domain string) (cipher.AEAD, error) {
	if len(dek) != 32 {
		return nil, errors.New("invalid vault DEK length")
	}
	if domain == "" {
		return nil, errors.New("missing vault encryption domain")
	}
	key := aeadCacheKey{
		dekSum: sha256.Sum256(dek),
		domain: domain,
	}
	cacheMu.Lock()
	if gcm, ok := aeadCache[key]; ok {
		cacheMu.Unlock()
		return gcm, nil
	}
	cacheMu.Unlock()

	sub, err := deriveSubkey(dek, domain)
	if err != nil {
		return nil, err
	}
	defer clear(sub)
	block, err := aes.NewCipher(sub)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	aeadCache[key] = gcm
	cacheMu.Unlock()
	return gcm, nil
}

// IsSealedBytes reports whether data begins with the DJVF1 whole-file envelope prefix.
func IsSealedBytes(data []byte) bool {
	return bytes.HasPrefix(data, []byte(fileMagic))
}

// IsStreamHeader reports whether head begins with the DJV1 chunked stream prefix.
func IsStreamHeader(head []byte) bool {
	return len(head) >= len(archiveMagic) && string(head[:len(archiveMagic)]) == archiveMagic
}

// SealBytes encrypts data into a DJVF1 whole-file AES-256-GCM envelope bound to domain.
func SealBytes(dek []byte, domain string, data []byte) ([]byte, error) {
	n := len(data)
	if n > maxSealedBytesPayload {
		return nil, errors.New("vault payload exceeds maximum whole-file size")
	}
	gcm, err := aeadFor(dek, domain)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, fileNonceSize)
	if _, rErr := rand.Read(nonce); rErr != nil {
		return nil, rErr
	}
	aad := []byte(fileMagic + ":" + domain)
	out := make([]byte, len(fileMagic), len(fileMagic)+fileNonceSize+n+fileTagSize)
	copy(out, fileMagic)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, data, aad), nil
}

// OpenBytes decrypts and authenticates a DJVF1 whole-file envelope bound to domain.
func OpenBytes(dek []byte, domain string, data []byte) ([]byte, error) {
	if !IsSealedBytes(data) {
		return nil, errors.New("not a vault-sealed file")
	}
	minLen := len(fileMagic) + fileNonceSize + fileTagSize
	if len(data) < minLen {
		return nil, errors.New("vault-sealed file truncated")
	}
	gcm, err := aeadFor(dek, domain)
	if err != nil {
		return nil, err
	}
	aad := []byte(fileMagic + ":" + domain)
	nonce := data[len(fileMagic) : len(fileMagic)+fileNonceSize]
	ct := data[len(fileMagic)+fileNonceSize:]
	return gcm.Open(nil, nonce, ct, aad)
}

// WriteSealedFile atomically writes data to path, encrypting with DJVF1 when vault is active.
func WriteSealedFile(indexDir, path, domain string, data []byte, perm os.FileMode) error {
	dek, err := CheckUnlocked(indexDir)
	if err != nil {
		return err
	}
	out := data
	if len(dek) == 32 {
		defer clear(dek)
		out, err = SealBytes(dek, domain, data)
		if err != nil {
			return err
		}
	}
	return atomicfile.Write(path, out, perm)
}

// ReadSealedFile reads path, failing closed when the vault is locked and transparently
// authenticating/decrypting DJVF1 files when unlocked.
func ReadSealedFile(indexDir, path, domain string) ([]byte, error) {
	dek, err := CheckUnlocked(indexDir)
	if err != nil {
		return nil, err
	}
	if len(dek) == 32 {
		defer clear(dek)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if IsSealedBytes(raw) {
		if len(dek) != 32 {
			return nil, errors.New("encrypted file requires vault key")
		}
		return OpenBytes(dek, domain, raw)
	}
	return raw, nil
}

// NewStreamWriter wraps w in an authenticated DJV1 streaming AEAD writer bound to domain.
func NewStreamWriter(w io.Writer, dek []byte, domain string) (io.WriteCloser, error) {
	return newChunkWriterDomain(w, dek, domain)
}

type verifiedStreamReader struct {
	cr *chunkReader
}

func (s *verifiedStreamReader) Read(p []byte) (int, error) {
	n, err := s.cr.Read(p)
	if errors.Is(err, io.EOF) {
		if verifyErr := s.cr.VerifyEOF(); verifyErr != nil {
			return n, verifyErr
		}
	}
	return n, err
}

// NewStreamReader authenticates and decrypts a DJV1 stream bound to domain, failing closed on truncation or trailing garbage.
func NewStreamReader(r io.Reader, dek []byte, domain string) (io.Reader, error) {
	cr, err := newChunkReaderDomain(r, dek, domain)
	if err != nil {
		return nil, err
	}
	return &verifiedStreamReader{cr: cr}, nil
}

// RecordCipher encrypts and authenticates individual records inside records.bin.
type RecordCipher struct {
	aead cipher.AEAD
}

// ActiveRecordCipher validates that the store is usable and returns a RecordCipher when encryption is enabled.
func ActiveRecordCipher(indexDir string) (*RecordCipher, bool, error) {
	dek, err := CheckUnlocked(indexDir)
	if err != nil {
		return nil, false, err
	}
	if len(dek) == 0 {
		return nil, false, nil
	}
	defer clear(dek)
	rc, err := NewRecordCipher(dek)
	if err != nil {
		return nil, false, err
	}
	return rc, true, nil
}

// NewRecordCipher returns a RecordCipher backed by dek.
func NewRecordCipher(dek []byte) (*RecordCipher, error) {
	aead, err := aeadFor(dek, DomainRecordLog)
	if err != nil {
		return nil, err
	}
	return &RecordCipher{aead: aead}, nil
}

// Seal encrypts payload with a fresh 12-byte nonce and binds baseFlag into AAD.
func (rc *RecordCipher) Seal(baseFlag byte, payload []byte) ([]byte, error) {
	nonce := make([]byte, fileNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	aad := [5]byte{'D', 'J', 'R', '1', baseFlag}
	out := make([]byte, fileNonceSize, fileNonceSize+len(payload)+fileTagSize)
	copy(out, nonce)
	return rc.aead.Seal(out, nonce, payload, aad[:]), nil
}

// Open decrypts sealed and verifies its GCM tag against baseFlag.
func (rc *RecordCipher) Open(baseFlag byte, sealed []byte) ([]byte, error) {
	if len(sealed) < RecordOverhead {
		return nil, errors.New("sealed record truncated")
	}
	aad := [5]byte{'D', 'J', 'R', '1', baseFlag}
	return rc.aead.Open(nil, sealed[:fileNonceSize], sealed[fileNonceSize:], aad[:])
}

// SealSidecarLine encrypts plain under domain when vault is active, or returns plain unchanged when disabled.
func SealSidecarLine(indexDir, domain string, plain []byte) (string, error) {
	dek, err := CheckUnlocked(indexDir)
	if err != nil {
		return "", err
	}
	if len(dek) == 0 {
		return string(plain), nil
	}
	defer clear(dek)
	return SealLine(dek, domain, plain)
}

// OpenSidecarLine verifies unlock and decrypts NoteLinePrefix lines under domain.
func OpenSidecarLine(indexDir, domain, line string) ([]byte, bool, error) {
	dek, err := CheckUnlocked(indexDir)
	if err != nil {
		return nil, false, err
	}
	if len(dek) == 32 {
		defer clear(dek)
	}
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, NoteLinePrefix) {
		return []byte(line), false, nil
	}
	if len(dek) != 32 {
		return nil, false, errors.New("encrypted sidecar line requires vault key")
	}
	plain, err := OpenLine(dek, domain, trimmed)
	if err != nil {
		return nil, false, err
	}
	return plain, true, nil
}

// RegisterMigrator registers an idempotent store/sidecar encryption callback invoked by Init and Unlock.
func RegisterMigrator(name string, fn func(indexDir string, dek []byte) error) {
	migratorMu.Lock()
	migrators[name] = fn
	migratorMu.Unlock()
}

func unregisterMigratorForTest(name string) {
	migratorMu.Lock()
	delete(migrators, name)
	migratorMu.Unlock()
}

func runMigrators(indexDir string, dek []byte) error {
	migratorMu.Lock()
	names := make([]string, 0, len(migrators))
	for name := range migrators {
		names = append(names, name)
	}
	sort.Strings(names)
	fns := make([]func(string, []byte) error, len(names))
	for i, name := range names {
		fns[i] = migrators[name]
	}
	migratorMu.Unlock()

	for _, fn := range fns {
		if err := fn(indexDir, dek); err != nil {
			return err
		}
	}
	return nil
}

func encryptExistingSidecars(indexDir string, dek []byte) error {
	if indexDir == "" || len(dek) != 32 {
		return nil
	}
	base := strings.TrimSuffix(indexDir, string(os.PathSeparator))
	for _, suf := range wholeFileSidecars {
		p := base + suf
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil || IsSealedBytes(raw) {
			continue
		}
		sealed, err := SealBytes(dek, DomainSidecar, raw)
		if err != nil {
			return err
		}
		perm := (fi.Mode().Perm() & 0o660) | 0o600
		if err := atomicfile.Write(p, sealed, perm); err != nil {
			return err
		}
	}
	for _, suf := range lineEncryptedSidecars {
		p := base + suf
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(raw), "\n")
		changed := false
		var out strings.Builder
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, NoteLinePrefix) {
				out.WriteString(trimmed)
				out.WriteByte('\n')
				continue
			}
			encLine, err := SealLine(dek, DomainSidecar, []byte(line))
			if err != nil {
				return err
			}
			out.WriteString(encLine)
			out.WriteByte('\n')
			changed = true
		}
		if changed {
			perm := (fi.Mode().Perm() & 0o660) | 0o600
			if err := atomicfile.Write(p, []byte(out.String()), perm); err != nil {
				return err
			}
		}
	}
	return nil
}

// DiagnosticStatus describes encryption state for diagnostics such as deja doctor.
type DiagnosticStatus struct {
	Enabled          bool   `json:"enabled"`
	Locked           bool   `json:"locked"`
	KeyBackend       string `json:"key_backend,omitempty"`
	KeyringAvailable bool   `json:"keyring_available"`
	AutoLockSeconds  int64  `json:"auto_lock_seconds,omitempty"`
	EncryptedStore   bool   `json:"encrypted_store"`
	Error            string `json:"error,omitempty"`
}

// Status inspects vault configuration and index encryption state without modifying disk state.
func Status(indexDir string) DiagnosticStatus {
	st := DiagnosticStatus{KeyringAvailable: KeyringAvailable()}
	if !Enabled() {
		return st
	}
	st.Enabled = true
	cfg, err := LoadConfig()
	if err != nil {
		st.Locked = true
		st.Error = err.Error()
		return st
	}
	dir := effectiveIndexDir(indexDir)
	st.KeyBackend = cfg.KeyBackend
	st.AutoLockSeconds = cfg.AutoLockSeconds
	st.Locked = cfg.Locked || (cfg.AutoLockSeconds > 0 && idleExpired(dir, cfg))
	if dir != "" {
		base := strings.TrimSuffix(dir, string(os.PathSeparator))
		if _, err := os.Stat(ArchivePath(base)); err == nil {
			st.EncryptedStore = true
		} else if b, rerr := os.ReadFile(filepath.Join(base, "manifest.gob")); rerr == nil && IsSealedBytes(b) {
			st.EncryptedStore = true
		}
	}
	return st
}
