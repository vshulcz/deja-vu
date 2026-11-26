package vault

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

const (
	archiveMagic        = "DJV1"
	archiveChunkSize    = 1 << 20 // 1 MiB
	maxArchiveFiles     = 1 << 20
	maxArchiveFileBytes = int64(512 << 20)
)

var contentSidecars = []string{
	".injections.jsonl",
	".dejavu",
	".envblock",
	".hookseen",
	".live",
	".forks",
	".embeddings",
	".embeddings.bin",
	".vectors.bin",
	".compactions.gob",
	".firstrecall",
	".tui-history",
}

// Init enables encryption at rest, generates a random 32-byte DEK wrapped by
// passphrase, and encrypts existing personal notes and snapshot digests in place.
func Init(indexDir, passphrase string, autoLock time.Duration) (string, error) {
	if Enabled() {
		return "", ErrAlreadyInitialized
	}
	if len(passphrase) < 8 {
		return "", errors.New("passphrase must be at least 8 characters")
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return "", err
	}
	defer clear(dek)

	iters := KDFIterations
	wrapped, err := wrapDEK(dek, passphrase, salt, iters)
	if err != nil {
		return "", err
	}
	cfg := Config{
		Version:         1,
		KDF:             "pbkdf2-sha256",
		Salt:            toB64(salt),
		Iterations:      iters,
		WrappedDEK:      wrapped,
		KeyID:           keyIDFor(dek),
		KeyBackend:      activeKeyring.Name(),
		Locked:          false,
		AutoLockSeconds: int64(autoLock.Seconds()),
		UnlockedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	ClearCache()
	if err := activeKeyring.Set(cfg.KeyID, dek); err != nil {
		cfg.KeyBackend = "passphrase"
	}
	if err := saveConfig(cfg); err != nil {
		_ = activeKeyring.Delete(cfg.KeyID)
		return "", err
	}
	dir := effectiveIndexDir(indexDir)
	if dir != "" {
		wipePromptCache(dir)
	}
	if err := encryptExistingNotes(dek); err != nil {
		return "", err
	}
	if err := encryptExistingSnapshots(dir, dek); err != nil {
		return "", err
	}
	if err := encryptExistingSidecars(dir, dek); err != nil {
		return "", err
	}
	if err := runMigrators(dir, dek); err != nil {
		return "", err
	}
	return cfg.KeyBackend, nil
}

// Lock seals the index directory and content-bearing sibling sidecars into
// ArchivePath(indexDir), securely wipes the plaintext files, and evicts the key.
func Lock(indexDir, passphrase string) error {
	ClearCache()
	if !Enabled() {
		return ErrNotInitialized
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	dir := effectiveIndexDir(indexDir)
	var dek []byte
	if !cfg.Locked {
		dek, err = CheckUnlocked(dir)
		if err == nil && len(dek) == 32 {
			defer clear(dek)
		}
	}
	if len(dek) != 32 && passphrase != "" {
		dek, err = unwrapDEK(cfg, passphrase)
		if err != nil {
			return err
		}
		defer clear(dek)
	}
	if dir != "" && hasPlaintextStore(dir) {
		if len(dek) != 32 {
			return errors.New("passphrase or unlocked key required to seal plaintext index — pass DEJA_VAULT_PASSPHRASE or unlock first")
		}
		if err := SealTree(dir, dek); err != nil {
			return err
		}
	}
	cfg.Locked = true
	if err := saveConfig(cfg); err != nil {
		return err
	}
	_ = activeKeyring.Delete(cfg.KeyID)
	ClearCache()
	return nil
}

// Unlock validates passphrase, restores any sealed archive back into indexDir,
// stores the DEK in the OS keyring when available, and clears the locked flag.
func Unlock(indexDir, passphrase string) error {
	ClearCache()
	if !Enabled() {
		return ErrNotInitialized
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if passphrase == "" {
		passphrase = os.Getenv(EnvVaultPassphrase)
	}
	if passphrase == "" {
		return errors.New("passphrase required")
	}
	dek, err := unwrapDEK(cfg, passphrase)
	if err != nil {
		return err
	}
	defer clear(dek)

	dir := effectiveIndexDir(indexDir)
	if dir != "" {
		if _, err := os.Stat(ArchivePath(dir)); err == nil {
			if err := OpenTree(dir, dek); err != nil {
				return err
			}
		}
	}
	if err := activeKeyring.Set(cfg.KeyID, dek); err != nil && cfg.KeyBackend != "passphrase" {
		cfg.KeyBackend = "passphrase"
	} else if err == nil {
		cfg.KeyBackend = activeKeyring.Name()
	}
	cfg.Locked = false
	cfg.UnlockedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveConfig(cfg); err != nil {
		return err
	}
	if err := encryptExistingNotes(dek); err != nil {
		return err
	}
	if err := encryptExistingSnapshots(dir, dek); err != nil {
		return err
	}
	if err := encryptExistingSidecars(dir, dek); err != nil {
		return err
	}
	return runMigrators(dir, dek)
}

func hasPlaintextStore(indexDir string) bool {
	base := strings.TrimSuffix(indexDir, string(filepath.Separator))
	if _, err := os.Stat(base); err == nil {
		return true
	}
	for _, suf := range contentSidecars {
		if _, err := os.Stat(base + suf); err == nil {
			return true
		}
	}
	cacheDir, singleFile := promptCacheTargets(base)
	if cacheDir != "" {
		if entries, err := os.ReadDir(cacheDir); err == nil && len(entries) > 0 {
			return true
		}
	}
	if singleFile != "" {
		if _, err := os.Stat(singleFile); err == nil {
			return true
		}
	}
	return false
}

// SealTree tars and encrypts indexDir + contentSidecars into ArchivePath(indexDir),
// then overwrites with zeros and deletes the plaintext files and parked leftovers.
func SealTree(indexDir string, dek []byte) error {
	base := strings.TrimSuffix(indexDir, string(filepath.Separator))
	archPath := ArchivePath(base)
	if err := os.MkdirAll(filepath.Dir(archPath), 0o700); err != nil {
		return err
	}
	if !hasPlaintextStore(base) {
		return nil
	}
	tmpPath := archPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cw, err := newChunkWriter(f, dek)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	tw := tar.NewWriter(cw)
	if err := writeTreeToTar(tw, base); err != nil {
		_ = tw.Close()
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := cw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, archPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	wipePlaintextStore(base)
	return nil
}

// OpenTree decrypts ArchivePath(indexDir) into indexDir + sibling sidecars.
func OpenTree(indexDir string, dek []byte) error {
	base := strings.TrimSuffix(indexDir, string(filepath.Separator))
	archPath := ArchivePath(base)
	f, err := os.Open(archPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	cr, err := newChunkReader(f, dek)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(base), ".vault-unpack-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	stageRoot, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { _ = stageRoot.Close() }()

	tr := tar.NewReader(cr)
	entries := 0
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		entries++
		if entries > maxArchiveFiles {
			return errors.New("vault archive exceeds file limit")
		}
		if strings.Contains(hdr.Name, "..") {
			return fmt.Errorf("unsafe path in vault archive: %q", hdr.Name)
		}
		rel := filepath.Clean(filepath.FromSlash(hdr.Name))
		if rel == "." {
			continue
		}
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("unsafe path in vault archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if mkErr := stageRoot.MkdirAll(rel, 0o700); mkErr != nil {
				return mkErr
			}
		case tar.TypeReg:
			if parent := filepath.Dir(rel); parent != "." {
				if mkErr := stageRoot.MkdirAll(parent, 0o700); mkErr != nil {
					return mkErr
				}
			}
			if hdr.Size < 0 || hdr.Size > maxArchiveFileBytes {
				return fmt.Errorf("vault archive entry %q exceeds size limit (%d)", hdr.Name, hdr.Size)
			}
			out, openErr := stageRoot.OpenFile(rel, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if openErr != nil {
				return openErr
			}
			if _, copyErr := io.CopyN(out, tr, hdr.Size); copyErr != nil {
				_ = out.Close()
				return copyErr
			}
			if closeErr := out.Close(); closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported tar entry type %d in vault archive", hdr.Typeflag)
		}
	}
	if verifyErr := cr.VerifyEOF(); verifyErr != nil {
		return verifyErr
	}
	_ = stageRoot.Close()

	stagedIndex := filepath.Join(stage, "index")
	if fi, err := os.Stat(stagedIndex); err == nil && fi.IsDir() {
		wipeTree(base)
		if err := os.Rename(stagedIndex, base); err != nil {
			return err
		}
	}
	for _, suf := range contentSidecars {
		src := filepath.Join(stage, "sidecars", suf)
		if _, err := os.Stat(src); err == nil {
			dst := base + suf
			shredFile(dst)
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
	}
	if err := restorePromptCache(stage, base); err != nil {
		return err
	}
	_ = f.Close()
	return os.Remove(archPath)
}

func writeTreeToTar(tw *tar.Writer, base string) error {
	if _, statErr := os.Stat(base); statErr == nil {
		walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			rel, relErr := filepath.Rel(base, path)
			if relErr != nil {
				return relErr
			}
			name := "index"
			if rel != "." {
				name = filepath.ToSlash(filepath.Join("index", rel))
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			if d.IsDir() {
				return tw.WriteHeader(&tar.Header{
					Typeflag: tar.TypeDir,
					Name:     name + "/",
					Mode:     0o700,
					ModTime:  info.ModTime(),
				})
			}
			return addFileToTar(tw, path, name, info)
		})
		if walkErr != nil {
			return walkErr
		}
	}
	for _, suf := range contentSidecars {
		p := base + suf
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			continue
		}
		if err := addFileToTar(tw, p, filepath.ToSlash(filepath.Join("sidecars", suf)), info); err != nil {
			return err
		}
	}
	return addPromptCacheToTar(tw, base)
}

func addFileToTar(tw *tar.Writer, srcPath, tarName string, info fs.FileInfo) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     tarName,
		Size:     info.Size(),
		Mode:     0o600,
		ModTime:  info.ModTime(),
	}
	if hdrErr := tw.WriteHeader(hdr); hdrErr != nil {
		return hdrErr
	}
	_, err = io.CopyN(tw, f, info.Size())
	return err
}

func wipePlaintextStore(base string) {
	wipeTree(base)
	for _, suffix := range []string{".old", ".tmp", ".part"} {
		wipeTree(base + suffix)
	}
	if matches, err := filepath.Glob(base + ".old.*"); err == nil {
		for _, m := range matches {
			wipeTree(m)
		}
	}
	for _, suf := range contentSidecars {
		shredFile(base + suf)
	}
	wipePromptCache(base)
}

func wipePromptCache(base string) {
	cacheDir, singleFile := promptCacheTargets(base)
	if cacheDir != "" {
		wipeTree(cacheDir)
	}
	if singleFile != "" {
		shredFile(singleFile)
	}
}

func promptCacheRoot() string {
	if base := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(base) {
		return filepath.Clean(base)
	}
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); filepath.IsAbs(base) {
			return filepath.Clean(base)
		}
		if h := homeDir(); filepath.IsAbs(h) {
			return filepath.Join(h, "AppData", "Local")
		}
		return ""
	}
	if h := homeDir(); filepath.IsAbs(h) {
		return filepath.Join(h, ".cache")
	}
	return ""
}

func promptCacheTargets(indexDir string) (cacheDir, singleFile string) {
	base := strings.TrimSuffix(effectiveIndexDir(indexDir), string(filepath.Separator))
	cleanIndex := ""
	if base != "" {
		cleanIndex = filepath.Clean(base)
	}
	root := promptCacheRoot()
	inCacheRoot := false
	if root != "" && cleanIndex != "" {
		if rel, err := filepath.Rel(root, cleanIndex); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			inCacheRoot = true
		}
	}
	def := DefaultIndexDir()
	isDefault := os.Getenv("DEJA_INDEX_DIR") == "" && def != "" && cleanIndex == filepath.Clean(def)
	singleFile = strings.TrimSpace(os.Getenv("DEJA_PROMPT_CACHE_FILE"))
	if !inCacheRoot && !isDefault && singleFile == "" {
		return "", ""
	}
	if (inCacheRoot || isDefault) && root != "" {
		cacheDir = filepath.Join(root, "deja-vu")
	}
	return cacheDir, singleFile
}

func addPromptCacheToTar(tw *tar.Writer, base string) error {
	cacheDir, singleFile := promptCacheTargets(base)
	if cacheDir != "" {
		if info, err := os.Lstat(cacheDir); err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			if err := filepath.WalkDir(cacheDir, func(p string, d fs.DirEntry, werr error) error {
				if werr != nil {
					return werr
				}
				if d.Type()&os.ModeSymlink != 0 || d.IsDir() {
					return nil
				}
				entryInfo, entryErr := d.Info()
				if entryErr != nil {
					return entryErr
				}
				if entryInfo.Mode()&os.ModeSymlink != 0 {
					return nil
				}
				rel, relErr := filepath.Rel(cacheDir, p)
				if relErr != nil {
					return relErr
				}
				return addFileToTar(tw, p, filepath.ToSlash(filepath.Join("prompt-cache", rel)), entryInfo)
			}); err != nil {
				return err
			}
		}
	}
	if singleFile != "" {
		if info, err := os.Lstat(singleFile); err == nil && info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
			if err := addFileToTar(tw, singleFile, "prompt-cache-file", info); err != nil {
				return err
			}
		}
	}
	return nil
}

func restorePromptCache(stage, base string) error {
	cacheDir, singleFile := promptCacheTargets(base)
	if cacheDir != "" {
		wipeTree(cacheDir)
		srcDir := filepath.Join(stage, "prompt-cache")
		if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
			if err := copyTreeSecure(srcDir, cacheDir); err != nil {
				return err
			}
		}
	}
	if singleFile != "" {
		shredFile(singleFile)
		srcFile := filepath.Join(stage, "prompt-cache-file")
		if info, err := os.Stat(srcFile); err == nil && !info.IsDir() {
			stageRoot, err := os.OpenRoot(stage)
			if err != nil {
				return err
			}
			b, readErr := stageRoot.ReadFile("prompt-cache-file")
			_ = stageRoot.Close()
			if readErr != nil {
				return readErr
			}
			if err := atomicfile.Write(singleFile, b, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyTreeSecure(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return err
	}
	srcRoot, err := os.OpenRoot(srcDir)
	if err != nil {
		return err
	}
	defer func() { _ = srcRoot.Close() }()
	dstRoot, err := os.OpenRoot(dstDir)
	if err != nil {
		return err
	}
	defer func() { _ = dstRoot.Close() }()
	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if mkErr := dstRoot.Mkdir(rel, 0o700); mkErr != nil && !os.IsExist(mkErr) {
				return mkErr
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := srcRoot.ReadFile(rel)
		if err != nil {
			return err
		}
		return dstRoot.WriteFile(rel, data, 0o600)
	})
}

func wipeTree(root string) {
	info, err := os.Lstat(root)
	if err != nil {
		return
	}
	if !info.IsDir() {
		shredFile(root)
		return
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr == nil && !d.IsDir() {
			shredFile(path)
		}
		return nil
	})
	_ = os.RemoveAll(root)
}

func shredFile(path string) {
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(path)
		return
	}
	if size := info.Size(); size > 0 {
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			zeros := make([]byte, min(size, 64<<10))
			for left := size; left > 0; {
				n := min(left, int64(len(zeros)))
				if _, werr := f.Write(zeros[:n]); werr != nil {
					break
				}
				left -= n
			}
			_ = f.Sync()
			_ = f.Close()
		}
	}
	_ = os.Remove(path)
}

func encryptExistingNotes(dek []byte) error {
	path := NotesPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte(NoteLinePrefix)) {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		enc, err := SealNoteLine(dek, line)
		if err != nil {
			return err
		}
		out.WriteString(enc)
		out.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return atomicfile.Write(path, out.Bytes(), 0o600)
}

func encryptExistingSnapshots(indexDir string, dek []byte) error {
	if indexDir == "" {
		return nil
	}
	path := strings.TrimSuffix(indexDir, string(filepath.Separator)) + ".injections.jsonl"
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		delete(m, "terms")
		if d, _ := m["digest"].(string); d != "" && !strings.HasPrefix(d, NoteLinePrefix) {
			enc, err := SealSnapshot(dek, d)
			if err != nil {
				return err
			}
			m["digest"] = enc
		}
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(m); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return atomicfile.Write(path, out.Bytes(), 0o600)
}

type chunkWriter struct {
	w        io.Writer
	aead     cipher.AEAD
	streamID [8]byte
	seq      uint32
	buf      []byte
	closed   bool
}

func newChunkWriter(w io.Writer, dek []byte) (*chunkWriter, error) {
	return newChunkWriterDomain(w, dek, archiveDomain)
}

func newChunkWriterDomain(w io.Writer, dek []byte, domain string) (*chunkWriter, error) {
	aead, err := aeadFor(dek, domain)
	if err != nil {
		return nil, err
	}
	var sid [8]byte
	if _, err := rand.Read(sid[:]); err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(archiveMagic)); err != nil {
		return nil, err
	}
	if _, err := w.Write(sid[:]); err != nil {
		return nil, err
	}
	return &chunkWriter{w: w, aead: aead, streamID: sid, buf: make([]byte, 0, archiveChunkSize)}, nil
}

func (cw *chunkWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		room := archiveChunkSize - len(cw.buf)
		n := min(len(p), room)
		cw.buf = append(cw.buf, p[:n]...)
		p = p[n:]
		if len(cw.buf) == archiveChunkSize {
			if err := cw.flushChunk(false); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func (cw *chunkWriter) Close() error {
	if cw.closed {
		return nil
	}
	cw.closed = true
	return cw.flushChunk(true)
}

func (cw *chunkWriter) flushChunk(final bool) error {
	var nonce [12]byte
	copy(nonce[:8], cw.streamID[:])
	binary.BigEndian.PutUint32(nonce[8:], cw.seq)
	cw.seq++

	var flag byte
	if final {
		flag = 1
	}
	aad := [5]byte{'D', 'J', 'V', '1', flag}
	sealed := cw.aead.Seal(nil, nonce[:], cw.buf, aad[:])
	cw.buf = cw.buf[:0]

	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(sealed))) //nolint:gosec // bounded by archiveChunkSize+tag
	hdr[4] = flag
	if _, err := cw.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := cw.w.Write(sealed)
	return err
}

type chunkReader struct {
	r        io.Reader
	aead     cipher.AEAD
	streamID [8]byte
	seq      uint32
	pending  []byte
	sawFinal bool
}

func newChunkReader(r io.Reader, dek []byte) (*chunkReader, error) {
	return newChunkReaderDomain(r, dek, archiveDomain)
}

func newChunkReaderDomain(r io.Reader, dek []byte, domain string) (*chunkReader, error) {
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, fmt.Errorf("vault archive header: %w", err)
	}
	if string(head[:4]) != archiveMagic {
		return nil, errors.New("not a deja vault archive")
	}
	aead, err := aeadFor(dek, domain)
	if err != nil {
		return nil, err
	}
	cr := &chunkReader{r: r, aead: aead}
	copy(cr.streamID[:], head[4:])
	return cr, nil
}

func (cr *chunkReader) Read(p []byte) (int, error) {
	for len(cr.pending) == 0 {
		if cr.sawFinal {
			return 0, io.EOF
		}
		if err := cr.nextChunk(); err != nil {
			return 0, err
		}
	}
	n := copy(p, cr.pending)
	cr.pending = cr.pending[n:]
	return n, nil
}

func (cr *chunkReader) nextChunk() error {
	var hdr [5]byte
	if _, err := io.ReadFull(cr.r, hdr[:]); err != nil {
		return errors.New("vault archive truncated before final frame")
	}
	ctLen := binary.BigEndian.Uint32(hdr[:4])
	if ctLen > uint32(archiveChunkSize+cr.aead.Overhead()) || ctLen < uint32(cr.aead.Overhead()) { //nolint:gosec // constant small positive sum
		return errors.New("corrupt vault archive chunk length")
	}
	flag := hdr[4]
	if flag != 0 && flag != 1 {
		return errors.New("corrupt vault archive frame flag")
	}
	sealed := make([]byte, ctLen)
	if _, err := io.ReadFull(cr.r, sealed); err != nil {
		return errors.New("vault archive truncated")
	}
	var nonce [12]byte
	copy(nonce[:8], cr.streamID[:])
	binary.BigEndian.PutUint32(nonce[8:], cr.seq)
	cr.seq++
	aad := [5]byte{'D', 'J', 'V', '1', flag}
	plain, err := cr.aead.Open(nil, nonce[:], sealed, aad[:])
	if err != nil {
		return errors.New("vault archive authentication failed")
	}
	cr.pending = plain
	if flag == 1 {
		cr.sawFinal = true
	}
	return nil
}

// VerifyEOF drains the remaining final marker and confirms authentication finished.
func (cr *chunkReader) VerifyEOF() error {
	for !cr.sawFinal {
		if err := cr.nextChunk(); err != nil {
			return err
		}
		if len(cr.pending) > 0 {
			return errors.New("unexpected trailing data in vault archive")
		}
	}
	var extra [1]byte
	if _, err := cr.r.Read(extra[:]); !errors.Is(err, io.EOF) {
		return errors.New("trailing bytes past final vault frame")
	}
	return nil
}
