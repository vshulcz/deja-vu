package hookengine

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// Key prefixes and limits shared across hook dedupe ledgers.
const (
	SessionStartKeyPrefix    = "start:"
	OnceDigestPrefix         = "once:"
	AgyDigestPrefix          = "agy:"
	AgyCompactPrefix         = "agy-compact:"
	SpawnReaderPrefix        = "task:"
	DigestSeenPrefix         = "digest:"
	OnceDigestMarker         = "once-digest"
	CompactionMarksKept      = 1000
	DefaultLedgerRotateBytes = 1 << 20

	ledgerTailLines = 4000
)

var ledgerLockRegistry sync.Map

func ledgerMutexFor(lockPath string) *sync.Mutex {
	key := filepath.Clean(lockPath)
	if v, ok := ledgerLockRegistry.Load(key); ok {
		if mu, ok := v.(*sync.Mutex); ok {
			return mu
		}
	}
	actual, _ := ledgerLockRegistry.LoadOrStore(key, &sync.Mutex{})
	if mu, ok := actual.(*sync.Mutex); ok {
		return mu
	}
	return &sync.Mutex{}
}

// SeenKey maps whitespace and control characters to underscores so an arbitrary
// session identifier stays one whitespace-separated field in .hookseen.
func SeenKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, s)
}

// IsSeenField reports whether s is non-empty and safe to store verbatim as a
// single whitespace-separated field in .hookseen.
func IsSeenField(s string) bool {
	return s != "" && SeenKey(s) == s
}

// IsPrefixedKey reports whether key carries a reserved deja ledger prefix.
func IsPrefixedKey(key string) bool {
	for _, p := range []string{SessionStartKeyPrefix, OnceDigestPrefix, AgyDigestPrefix, AgyCompactPrefix} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// OnceDigestKey returns the ledger key used for one-shot session markers.
func OnceDigestKey(sessionID string) string {
	return OnceDigestPrefix + sessionID
}

// AgyDigestKey returns the ledger key storing per-conversation Antigravity
// prompt dedupe markers.
func AgyDigestKey(conversationID string) string {
	return AgyDigestPrefix + conversationID
}

// AgyCompactKey returns the ledger key storing Antigravity checkpoint markers.
func AgyCompactKey(conversationID string) string {
	return AgyCompactPrefix + conversationID
}

// ShortHash returns a deterministic FNV-1a 64-bit hex fingerprint for dedupe
// tokens.
func ShortHash(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 16)
}

// BlockFingerprint hashes whitespace-normalized recall text for session-level
// duplicate suppression.
func BlockFingerprint(body string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(body), " ")))
	return hex.EncodeToString(sum[:8])
}

// AskKey returns the order-independent question dedupe token for search terms.
func AskKey(terms []string) string {
	sorted := slices.Clone(terms)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, " ")))
	return "ask:" + hex.EncodeToString(sum[:8])
}

// SeenLines is a snapshot of lines read from a .hookseen dedupe ledger.
type SeenLines []string

// Injected returns the set of tokens recorded against sid.
func (sl SeenLines) Injected(sid string) map[string]bool {
	out := map[string]bool{}
	if sid == "" {
		return out
	}
	key := SeenKey(sid)
	for _, line := range sl {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == key {
			out[parts[1]] = true
		}
	}
	return out
}

// Recent returns up to window most recently recorded tokens for sid.
func (sl SeenLines) Recent(sid string, window int) map[string]bool {
	out := map[string]bool{}
	if sid == "" || window <= 0 {
		return out
	}
	key := SeenKey(sid)
	kept := 0
	for i := len(sl) - 1; i >= 0 && kept < window; i-- {
		parts := strings.Fields(sl[i])
		if len(parts) < 2 || parts[0] != key {
			continue
		}
		kept++
		out[parts[1]] = true
	}
	return out
}

// RecentInProject returns up to window most recently recorded tokens for
// project across sessions.
func (sl SeenLines) RecentInProject(project string, window int) map[string]bool {
	out := map[string]bool{}
	if project == "" || window <= 0 {
		return out
	}
	kept := 0
	for i := len(sl) - 1; i >= 0 && kept < window; i-- {
		parts := strings.Fields(sl[i])
		if len(parts) < 4 || parts[3] != project {
			continue
		}
		kept++
		out[parts[1]] = true
	}
	return out
}

// Asked reports whether sid asked key within window relative to now.
func (sl SeenLines) Asked(sid, key string, window time.Duration) bool {
	return sl.AskedWithin(sid, key, time.Now().UTC(), window)
}

// AskedWithin reports whether sid asked key within window relative to ref.
func (sl SeenLines) AskedWithin(sid, key string, ref time.Time, window time.Duration) bool {
	if sid == "" || key == "" || window <= 0 {
		return false
	}
	want := SeenKey(sid)
	cutoff := ref.UTC().Add(-window)
	for _, s := range slices.Backward(sl) {
		parts := strings.Fields(s)
		if len(parts) < 3 || parts[0] != want || parts[1] != key {
			continue
		}
		t, err := time.Parse(time.RFC3339, parts[2])
		if err != nil {
			return false
		}
		return t.After(cutoff)
	}
	return false
}

// Ledger manages locked transactions and atomic rewrites on a .hookseen file.
type Ledger struct {
	path        string
	lockPath    string
	rotateBytes int64
	now         func() time.Time
}

// NewLedger returns a Ledger managing the .hookseen sidecar beside indexDir.
func NewLedger(indexDir string) *Ledger {
	if strings.TrimSpace(indexDir) == "" {
		return &Ledger{
			rotateBytes: DefaultLedgerRotateBytes,
			now:         func() time.Time { return time.Now().UTC() },
		}
	}
	return &Ledger{
		path:        indexDir + ".hookseen",
		lockPath:    indexDir + ".hookseen.lock",
		rotateBytes: DefaultLedgerRotateBytes,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// Lock acquires the in-process mutex and cross-process advisory lock for the
// ledger, returning an unlocked SeenTx handle valid until release is called.
// Callers must invoke methods on the returned SeenTx while locked rather than
// calling lock-acquiring methods on Ledger, which would self-deadlock.
func (l *Ledger) Lock() (tx *SeenTx, release func(), contended bool) {
	if l == nil || l.path == "" {
		return &SeenTx{ledger: l}, func() {}, false
	}
	mu := ledgerMutexFor(l.lockPath)
	inProcContended := !mu.TryLock()
	if inProcContended {
		mu.Lock()
	}
	advRelease, ok, busy := atomicfile.TakeAdvisoryLock(l.lockPath, false)
	if !ok && busy {
		inProcContended = true
		advRelease, _, _ = atomicfile.TakeAdvisoryLock(l.lockPath, true)
	}
	var once sync.Once
	tx = &SeenTx{ledger: l}
	release = func() {
		once.Do(func() {
			advRelease()
			mu.Unlock()
		})
	}
	return tx, release, inProcContended
}

// Read returns a snapshot of the ledger lines, or nil when unreadable.
func (l *Ledger) Read() SeenLines {
	if l == nil || l.path == "" {
		return nil
	}
	b, err := os.ReadFile(l.path)
	if err != nil {
		return nil
	}
	return SeenLines(strings.Split(string(b), "\n"))
}

// RecordIDs locks the ledger and appends sid -> id tokens.
func (l *Ledger) RecordIDs(sid string, ids ...string) {
	tx, release, _ := l.Lock()
	defer release()
	tx.RecordIDs(sid, ids...)
}

// RecordForProject locks the ledger and appends sid -> id entries stamped with
// UTC timestamp and optional project name.
func (l *Ledger) RecordForProject(sid, project string, ids ...string) {
	tx, release, _ := l.Lock()
	defer release()
	tx.RecordForProject(sid, project, ids...)
}

// ForgetSessions locks the ledger and removes entries for the given session
// identifiers while preserving compaction delivery marks.
func (l *Ledger) ForgetSessions(sids ...string) {
	tx, release, _ := l.Lock()
	defer release()
	tx.ForgetSessions(sids...)
}

// ClaimOnce locks the ledger, checks whether token is already recorded under
// key, and if not records it and returns true.
func (l *Ledger) ClaimOnce(key, token string) bool {
	tx, release, _ := l.Lock()
	defer release()
	return tx.ClaimOnce(key, token)
}

// ForgetCheckpoint locks the ledger, claims the checkpoint marker once for
// conversationID, and forgets all prior conversation rows in one rewrite.
func (l *Ledger) ForgetCheckpoint(conversationID, marker string) bool {
	tx, release, _ := l.Lock()
	defer release()
	return tx.ForgetCheckpoint(conversationID, marker)
}

// SeenTx operates on the ledger inside an active Lock() transaction.
type SeenTx struct {
	ledger *Ledger
}

// Load reads the current ledger snapshot under the active transaction.
func (tx *SeenTx) Load() SeenLines {
	if tx == nil || tx.ledger == nil {
		return nil
	}
	return tx.ledger.Read()
}

// RecordIDs appends sid -> id tokens under the active transaction.
func (tx *SeenTx) RecordIDs(sid string, ids ...string) {
	tx.appendRecords(sid, "", "", ids)
}

// RecordForProject appends timestamped (and optionally project-scoped) entries
// for sid -> ids under the active transaction.
func (tx *SeenTx) RecordForProject(sid, project string, ids ...string) {
	if tx == nil || tx.ledger == nil {
		return
	}
	stamp := tx.ledger.now().UTC().Format(time.RFC3339)
	tx.appendRecords(sid, stamp, project, ids)
}

func (tx *SeenTx) appendRecords(sid, stamp, project string, ids []string) {
	if tx == nil || tx.ledger == nil || tx.ledger.path == "" || sid == "" {
		return
	}
	p := tx.ledger.path
	if tx.ledger.rotateBytes > 0 {
		if fi, err := os.Stat(p); err == nil && fi.Size() > tx.ledger.rotateBytes {
			RotateFile(p, sid)
		}
	}
	f, err := atomicfile.OpenSharedFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if atomicfile.EndsMidLine(f) {
		_, _ = f.WriteString("\n")
	}
	sKey := SeenKey(sid)
	var buf strings.Builder
	for _, id := range ids {
		if !IsSeenField(id) {
			continue
		}
		buf.WriteString(sKey)
		buf.WriteByte(' ')
		buf.WriteString(id)
		if stamp != "" {
			buf.WriteByte(' ')
			buf.WriteString(stamp)
			if project != "" {
				buf.WriteByte(' ')
				buf.WriteString(project)
			}
		}
		buf.WriteByte('\n')
	}
	if buf.Len() > 0 {
		_, _ = f.WriteString(buf.String())
	}
}

// ForgetSessions removes rows for each non-empty session identifier (plus its
// start:/once: companion keys when unprefixed) in a single atomic rewrite,
// preserving compaction delivery marks.
func (tx *SeenTx) ForgetSessions(sids ...string) {
	tx.rewriteOmitting(sids, nil)
}

// ClaimOnce checks under the active lock whether token is already recorded for
// key, recording it if absent.
func (tx *SeenTx) ClaimOnce(key, token string) bool {
	if tx == nil || tx.ledger == nil || tx.ledger.path == "" {
		return false
	}
	if strings.TrimSpace(key) == "" || !IsSeenField(token) {
		return false
	}
	if tx.Load().Injected(key)[token] {
		return false
	}
	tx.RecordIDs(key, token)
	return true
}

// ForgetCheckpoint records an idempotent compaction marker for conversationID
// and clears its prior prompt/digest/start rows in a single atomic rewrite.
func (tx *SeenTx) ForgetCheckpoint(conversationID, marker string) bool {
	if tx == nil || tx.ledger == nil || tx.ledger.path == "" {
		return false
	}
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(marker) == "" {
		return false
	}
	key := AgyCompactKey(conversationID)
	token := "checkpoint:" + ShortHash(marker)
	if tx.Load().Injected(key)[token] {
		return false
	}
	checkpointLine := SeenKey(key) + " " + token
	tx.rewriteOmitting([]string{conversationID, AgyDigestKey(conversationID)}, []string{checkpointLine})
	return true
}

func (tx *SeenTx) rewriteOmitting(sids, appendLines []string) {
	if tx == nil || tx.ledger == nil || tx.ledger.path == "" {
		return
	}
	keys := make(map[string]bool)
	for _, sid := range sids {
		if sid == "" {
			continue
		}
		key := SeenKey(sid)
		keys[key] = true
		if !IsPrefixedKey(key) {
			keys[SeenKey(SessionStartKeyPrefix+sid)] = true
			keys[SeenKey(OnceDigestKey(sid))] = true
		}
	}
	if len(keys) == 0 && len(appendLines) == 0 {
		return
	}
	p := tx.ledger.path
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) && len(appendLines) > 0 {
			_ = atomicfile.Write(p, []byte(strings.Join(appendLines, "\n")+"\n"), 0o600)
		}
		return
	}
	var kept []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if line == "" {
			continue
		}
		if parts := strings.Fields(line); len(parts) >= 2 && keys[parts[0]] && !strings.HasPrefix(parts[1], "compaction:") {
			continue
		}
		kept = append(kept, line)
	}
	kept = append(kept, appendLines...)
	out := ""
	if len(kept) > 0 {
		out = strings.Join(kept, "\n") + "\n"
	}
	_ = atomicfile.Write(p, []byte(out), 0o600)
}

// RotateFile shrinks the ledger at path to sid's lines, compaction delivery
// marks, and the recent tail.
func RotateFile(path, sid string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	start := 0
	if len(lines) > ledgerTailLines {
		start = len(lines) - ledgerTailLines
	}
	var keep, mine, packets []string
	prefix := SeenKey(sid) + " "
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		switch {
		case i >= start:
			keep = append(keep, ln)
		case strings.HasPrefix(ln, prefix):
			mine = append(mine, ln)
		case compactionMark(ln):
			packets = append(packets, ln)
		}
	}
	if len(mine) > ledgerTailLines {
		mine = mine[len(mine)-ledgerTailLines:]
	}
	if len(packets) > CompactionMarksKept {
		packets = packets[len(packets)-CompactionMarksKept:]
	}
	outLines := slices.Concat(packets, mine, keep)
	_ = atomicfile.Write(path, []byte(strings.Join(outLines, "\n")+"\n"), 0o600)
}

func compactionMark(line string) bool {
	parts := strings.Fields(line)
	return len(parts) >= 2 && strings.HasPrefix(parts[1], "compaction")
}
