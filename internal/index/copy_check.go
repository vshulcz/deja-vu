package index

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// copyCheckFullCutoff bounds full-log copy decoding; longer non-note sessions
// hash their raw record frames and decode only LastTurn candidates.
const copyCheckFullCutoff = 256

// recallConclusionsFullCutoff bounds full-session decoding for recall
// conclusions when recordoffsets.bin covers the log and user turns >= longTurns.
const recallConclusionsFullCutoff = 256

// readRecordPayloadAt reads the length-prefixed payload bytes of one record at
// off without inflating or decoding the body.
func readRecordPayloadAt(f *os.File, off int64) ([]byte, error) {
	buf := readAheadPool.Get().(*[]byte) //nolint:forcetypeassert // the pool holds only *[]byte
	defer readAheadPool.Put(buf)
	b := *buf

	n, err := f.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if n < 4 {
		return nil, errOffsetsMismatch
	}
	size := binary.LittleEndian.Uint32(b[:4])
	if size > maxRecordSize {
		return nil, errOffsetsMismatch
	}
	if 4+int(size) <= n {
		return slices.Clone(b[4 : 4+int(size)]), nil
	}
	payload := make([]byte, size)
	m, err := f.ReadAt(payload, off+4)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if m < int(size) {
		return nil, errOffsetsMismatch
	}
	return payload, nil
}

// walkLiveOffsetsSpans reads ascending log offsets in coalesced pread spans
// and invokes fn with each record's raw payload slice.
func walkLiveOffsetsSpans(f *os.File, offs []int64, fn func(idx int, payload []byte) error) error {
	var buf []byte
	for i := 0; i < len(offs); {
		j := i + 1
		for j < len(offs) && offs[j]-offs[j-1] <= spanGapMax && offs[j]-offs[i] < spanSizeMax {
			j++
		}
		start := offs[i]
		size := int(offs[j-1]-start) + readAheadSize
		if cap(buf) < size {
			buf = make([]byte, size)
		}
		b := buf[:size]
		n, err := f.ReadAt(b, start)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		b = b[:n]
		for k := i; k < j; k++ {
			rel := int(offs[k] - start)
			var payload []byte
			if rel >= 0 && rel+4 <= len(b) {
				recSize := binary.LittleEndian.Uint32(b[rel : rel+4])
				if recSize > maxRecordSize {
					return errOffsetsMismatch
				}
				if rel+4+int(recSize) <= len(b) {
					payload = b[rel+4 : rel+4+int(recSize)]
				}
			}
			if payload == nil {
				var perr error
				payload, perr = readRecordPayloadAt(f, offs[k])
				if perr != nil {
					return perr
				}
			}
			if fnErr := fn(k, payload); fnErr != nil {
				return fnErr
			}
		}
		i = j
	}
	return nil
}

func wrapCorruptDecodeErr(err error) error {
	if errors.Is(err, errCorruptIndex) {
		return err
	}
	return fmt.Errorf("%w: %w", errCorruptIndex, err)
}

func isCopyLastTurn(role, text string, ts time.Time) bool {
	if ts.IsZero() || role == "summary" {
		return false
	}
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	return role != "assistant" || t != "No response requested."
}

func hashCopyFrame(h io.Writer, role string, flag byte, rest []byte) {
	var u [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(u[:], uint64(len(role)))
	_, _ = h.Write(u[:n])
	_, _ = h.Write([]byte(role))
	_, _ = h.Write([]byte{flag})
	n = binary.PutUvarint(u[:], uint64(len(rest)))
	_, _ = h.Write(u[:n])
	_, _ = h.Write(rest)
}

// FindManyForCopyCheck resolves sessions needed by fork/copy deduplication.
// Short sessions and promoted notes load in full; marathon sessions covered by
// the offset table hash raw frames for twin equivalence and decode only
// LastTurn candidates plus matching (role, timestamp) messages.
func FindManyForCopyCheck(dir string, ids []Identity) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if len(ids) == 0 {
		return nil, nil
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(ids))
	for _, want := range ids {
		if meta, found := metaForIdentity(m, want.Harness, want.ID); found {
			metas = append(metas, meta)
		}
	}
	if len(metas) == 0 {
		return nil, nil
	}
	out, err := copyCheckSessionsWithFallback(dir, m, metas)
	for range sessionRereads {
		cur, moved := liveManifest(dir, m)
		if !moved {
			break
		}
		m = cur
		metas = slices.DeleteFunc(slices.Clone(metas), func(meta SessionMeta) bool {
			_, present := m.Sessions[meta.Key()]
			return !present
		})
		if sessionReread != nil {
			sessionReread()
		}
		out, err = copyCheckSessionsWithFallback(dir, m, metas)
	}
	if err != nil {
		return nil, err
	}
	for i := range out {
		orderPromotedNote(&out[i])
	}
	return ignoredByPolicy(out), nil
}

func copyCheckSessionsWithFallback(dir string, m Manifest, metas []SessionMeta) ([]model.Session, error) {
	if len(metas) == 0 {
		return nil, nil
	}
	out, handled, err := copyCheckSessionsIn(dir, m, metas)
	if err != nil {
		return nil, err
	}
	if handled {
		return out, nil
	}
	return sessionsForMetasInWindowedTail(dir, m, metas, nil, nil, 0)
}

func copyCheckSessionsIn(dir string, m Manifest, metas []SessionMeta) ([]model.Session, bool, error) {
	tbl := tablesFromManifest(m)
	f, err := openIndexFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	wantKeys := make(map[string]bool, len(metas))
	noteKey := make(map[string]bool, len(metas))
	for _, meta := range metas {
		k := meta.Key()
		wantKeys[k] = true
		if IsPromotedNote(meta.Harness, meta.ID, meta.OrigID) {
			noteKey[k] = true
		}
	}

	ro, rerr := readRecordOffsetsFor(dir, f, wantKeys)
	if rerr != nil || ro.covered < ro.logSize {
		return nil, false, nil
	}

	liveByKey := make(map[string][]int64, len(wantKeys))
	isMarathon := make(map[string]bool, len(wantKeys))
	anyMarathon := false
	for k := range wantKeys {
		live := slices.DeleteFunc(slices.Clone(ro.byKey[k]), tbl.isDead)
		liveByKey[k] = live
		if !noteKey[k] && len(live) > copyCheckFullCutoff {
			isMarathon[k] = true
			anyMarathon = true
		}
	}
	if !anyMarathon {
		return nil, false, nil
	}

	wantRoles := make(map[string]bool)
	wantTimes := make(map[int64]bool)
	fullByKey := make(map[string][]model.Message)

	// Decode non-marathon candidates in full to gather their messages and LastTurn targets.
	for k, live := range liveByKey {
		if isMarathon[k] {
			continue
		}
		msgs := make([]model.Message, 0, len(live))
		err := walkLiveOffsetsSpans(f, live, func(_ int, payload []byte) error {
			rec, derr := decodeRecord(payload, tbl)
			if derr != nil {
				return wrapCorruptDecodeErr(derr)
			}
			if rec.Key != k {
				return errOffsetsMismatch
			}
			msgs = append(msgs, model.Message{Role: rec.Role, Text: rec.Text, Time: rec.Time})
			return nil
		})
		if err != nil {
			if errors.Is(err, errOffsetsMismatch) {
				return nil, false, nil
			}
			return nil, false, err
		}
		fullByKey[k] = msgs
		for _, msg := range slices.Backward(msgs) {
			if isCopyLastTurn(msg.Role, msg.Text, msg.Time) {
				wantRoles[msg.Role] = true
				wantTimes[msg.Time.UnixNano()] = true
				break
			}
		}
	}

	type lastCandidate struct {
		idx int
		msg model.Message
		ok  bool
	}
	lastByKey := make(map[string]lastCandidate, len(wantKeys))

	// Find each marathon session's LastTurn by walking its offsets backward.
	for k, live := range liveByKey {
		if !isMarathon[k] {
			continue
		}
		for idx, off := range slices.Backward(live) {
			payload, perr := readRecordPayloadAt(f, off)
			if perr != nil {
				if errors.Is(perr, errOffsetsMismatch) {
					return nil, false, nil
				}
				return nil, false, perr
			}
			rkey, role, ok := recordRoleIn(payload, tbl)
			if !ok || rkey != k {
				return nil, false, nil
			}
			if role == "summary" {
				continue
			}
			rec, derr := decodeRecord(payload, tbl)
			if derr != nil {
				return nil, false, wrapCorruptDecodeErr(derr)
			}
			if rec.Key != k {
				return nil, false, nil
			}
			if isCopyLastTurn(rec.Role, rec.Text, rec.Time) {
				msg := model.Message{Role: rec.Role, Text: rec.Text, Time: rec.Time}
				lastByKey[k] = lastCandidate{idx: idx, msg: msg, ok: true}
				wantRoles[rec.Role] = true
				wantTimes[rec.Time.UnixNano()] = true
				break
			}
		}
	}

	// Build copy-shape digests and decode only LastTurn and matching-timestamp records.
	for k, live := range liveByKey {
		if !isMarathon[k] {
			continue
		}
		last := lastByKey[k]
		h := sha256.New()
		matched := make([]model.Message, 0, 8)
		err := walkLiveOffsetsSpans(f, live, func(idx int, payload []byte) error {
			hdr, flag, rest, herr := decodeRecordHeader(payload, tbl)
			if herr != nil || hdr.Key != k {
				return errOffsetsMismatch
			}
			hashCopyFrame(h, hdr.Role, flag, rest)
			if last.ok && idx == last.idx {
				matched = append(matched, last.msg)
				return nil
			}
			if !wantRoles[hdr.Role] {
				return nil
			}
			stub, serr := decodeRecordStubRole(payload, tbl)
			if serr != nil {
				return wrapCorruptDecodeErr(serr)
			}
			if stub.Time.IsZero() || !wantTimes[stub.Time.UnixNano()] {
				return nil
			}
			rec, derr := decodeRecord(payload, tbl)
			if derr != nil {
				return wrapCorruptDecodeErr(derr)
			}
			matched = append(matched, model.Message{Role: rec.Role, Text: rec.Text, Time: rec.Time})
			return nil
		})
		if err != nil {
			if errors.Is(err, errOffsetsMismatch) {
				return nil, false, nil
			}
			return nil, false, err
		}
		var sum [sha256.Size]byte
		copy(sum[:], h.Sum(nil))
		msgs := make([]model.Message, 0, 1+len(matched))
		msgs = append(msgs, model.Message{
			Role: "summary",
			Text: fmt.Sprintf("copy-shape:%x:%d", sum[:16], len(live)),
		})
		msgs = append(msgs, matched...)
		fullByKey[k] = msgs
	}

	out := make([]model.Session, len(metas))
	for i, meta := range metas {
		s := sessionFromMeta(meta)
		if msgs := fullByKey[meta.Key()]; len(msgs) > 0 {
			s.Messages = slices.Clone(msgs)
		}
		out[i] = s
	}
	return out, true, nil
}

// FindForRecallConclusions loads only the user-turn episodes around quoted
// matches for a long session when recordoffsets.bin covers the log, preserving
// user-turn count via lightweight role stubs so marathon episode detection
// matches a full-session read.
func FindForRecallConclusions(dir string, hitSession model.Session, quoted []string, longTurns int, needAllChanges bool, roles map[string]bool) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if needAllChanges || longTurns <= 0 || IsPromotedNote(hitSession.Harness, hitSession.ID, hitSession.OrigID) {
		return FindByIdentityInRoles(dir, hitSession.Harness, hitSession.ID, roles)
	}

	wantQuoted := make(map[string]bool, len(quoted))
	for _, q := range quoted {
		if q != "" {
			wantQuoted[q] = true
		}
	}
	quotedRoles := make(map[string]bool, len(wantQuoted))
	quotedTimes := make(map[int64]bool, len(wantQuoted))
	allowZeroTime := false
	if len(wantQuoted) > 0 {
		foundQuoted := make(map[string]bool, len(wantQuoted))
		for _, m := range hitSession.Messages {
			if !wantQuoted[m.Text] {
				continue
			}
			foundQuoted[m.Text] = true
			quotedRoles[m.Role] = true
			if m.Time.IsZero() {
				allowZeroTime = true
			} else {
				quotedTimes[m.Time.UnixNano()] = true
			}
		}
		if len(foundQuoted) < len(wantQuoted) {
			return FindByIdentityInRoles(dir, hitSession.Harness, hitSession.ID, roles)
		}
	}

	unlock, locked, err := tryLockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	released := false
	release := func() {
		if locked && !released {
			released = true
			unlock()
		}
	}
	defer release()

	m, err := readManifestCached(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	pol, excluded := metaIgnoredOrExcludedLoad()

	for attempt := 0; ; attempt++ {
		meta, found := metaForIdentity(m, hitSession.Harness, hitSession.ID)
		if !found || metaIgnoredOrExcluded(pol, excluded, meta) {
			return model.Session{}, false, nil
		}
		if IsPromotedNote(meta.Harness, meta.ID, meta.OrigID) {
			release()
			return FindByIdentityInRoles(dir, hitSession.Harness, hitSession.ID, roles)
		}

		s, handled, lerr := loadRecallEpisodes(dir, m, meta, wantQuoted, quotedRoles, quotedTimes, allowZeroTime, longTurns, roles)
		if lerr != nil {
			if attempt < sessionRereads {
				if cur, moved := liveManifest(dir, m); moved {
					m = cur
					if sessionReread != nil {
						sessionReread()
					}
					continue
				}
			}
			return model.Session{}, false, lerr
		}
		if attempt < sessionRereads {
			if cur, moved := liveManifest(dir, m); moved {
				m = cur
				if sessionReread != nil {
					sessionReread()
				}
				continue
			}
		}
		if handled {
			return s, true, nil
		}
		release()
		return FindByIdentityInRoles(dir, hitSession.Harness, hitSession.ID, roles)
	}
}

type recallEpisodeScan struct {
	roles      []string
	userCount  int
	matchedIdx []int
	decodedAt  map[int]Record
}

func recallQuotedTimeMatches(t time.Time, allowZero bool, quotedTimes map[int64]bool) bool {
	if t.IsZero() {
		return allowZero
	}
	return quotedTimes[t.UnixNano()]
}

func scanRecallEpisodeOffsets(
	f *os.File,
	tbl *recordTables,
	metaKey string,
	liveOffs []int64,
	wantQuoted, quotedRoles map[string]bool,
	quotedTimes map[int64]bool,
	allowZeroTime bool,
) (recallEpisodeScan, bool, error) {
	scan := recallEpisodeScan{
		roles:     make([]string, len(liveOffs)),
		decodedAt: make(map[int]Record, len(wantQuoted)),
	}
	err := walkLiveOffsetsSpans(f, liveOffs, func(idx int, payload []byte) error {
		key, role, ok := recordRoleIn(payload, tbl)
		if !ok || key != metaKey {
			return errOffsetsMismatch
		}
		scan.roles[idx] = role
		if role == "user" {
			scan.userCount++
		}
		if len(wantQuoted) == 0 || !quotedRoles[role] {
			return nil
		}
		stub, serr := decodeRecordStubRole(payload, tbl)
		if serr != nil {
			return wrapCorruptDecodeErr(serr)
		}
		if !recallQuotedTimeMatches(stub.Time, allowZeroTime, quotedTimes) {
			return nil
		}
		rec, derr := decodeRecord(payload, tbl)
		if derr != nil {
			return wrapCorruptDecodeErr(derr)
		}
		if wantQuoted[rec.Text] {
			scan.matchedIdx = append(scan.matchedIdx, idx)
			scan.decodedAt[idx] = rec
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errOffsetsMismatch) {
			return recallEpisodeScan{}, false, nil
		}
		return recallEpisodeScan{}, false, err
	}
	return scan, true, nil
}

func decodeRecallKeptRecords(
	f *os.File,
	tbl *recordTables,
	metaKey string,
	liveOffs []int64,
	scan recallEpisodeScan,
	roles map[string]bool,
) ([]bool, bool, error) {
	keep := make([]bool, len(liveOffs))
	for _, i := range scan.matchedIdx {
		start := i
		for start > 0 && scan.roles[start] != "user" {
			start--
		}
		end := i + 1
		for end < len(liveOffs) && scan.roles[end] != "user" {
			end++
		}
		for j := start; j < end; j++ {
			keep[j] = true
		}
	}

	var decodeIdxs []int
	var decodeOffs []int64
	for idx, off := range liveOffs {
		if !keep[idx] {
			continue
		}
		role := scan.roles[idx]
		if !roleWanted(roles, role) {
			continue
		}
		isToolStub := roles != nil && roles[RoleStubToolOutput] && (role == roleToolOutput || role == "tool")
		if _, cached := scan.decodedAt[idx]; cached && !isToolStub {
			continue
		}
		decodeIdxs = append(decodeIdxs, idx)
		decodeOffs = append(decodeOffs, off)
	}
	if len(decodeOffs) == 0 {
		return keep, true, nil
	}
	err := walkLiveOffsetsSpans(f, decodeOffs, func(spanPos int, payload []byte) error {
		origIdx := decodeIdxs[spanPos]
		role := scan.roles[origIdx]
		var rec Record
		var derr error
		if roles != nil && roles[RoleStubToolOutput] && (role == roleToolOutput || role == "tool") {
			rec, derr = decodeRecordStubRole(payload, tbl)
		} else {
			rec, derr = decodeRecord(payload, tbl)
		}
		if derr != nil {
			return wrapCorruptDecodeErr(derr)
		}
		if rec.Key != metaKey {
			return errOffsetsMismatch
		}
		scan.decodedAt[origIdx] = rec
		return nil
	})
	if err != nil {
		if errors.Is(err, errOffsetsMismatch) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return keep, true, nil
}

func buildRecallEpisodeSession(
	meta SessionMeta,
	scan recallEpisodeScan,
	keep []bool,
	roles map[string]bool,
) model.Session {
	s := sessionFromMeta(meta)
	s.Messages = make([]model.Message, 0, len(scan.decodedAt)+scan.userCount)
	for idx, role := range scan.roles {
		if keep[idx] {
			if !roleWanted(roles, role) {
				if roles != nil && roles[RoleStubOthers] {
					s.Messages = append(s.Messages, model.Message{Role: role})
				}
				continue
			}
			if rec, ok := scan.decodedAt[idx]; ok {
				s.Messages = append(s.Messages, model.Message{Role: rec.Role, Text: rec.Text, Time: rec.Time})
			}
		} else if role == "user" {
			s.Messages = append(s.Messages, model.Message{Role: "user"})
		}
	}
	return s
}

func loadRecallEpisodes(
	dir string,
	m Manifest,
	meta SessionMeta,
	wantQuoted, quotedRoles map[string]bool,
	quotedTimes map[int64]bool,
	allowZeroTime bool,
	longTurns int,
	roles map[string]bool,
) (model.Session, bool, error) {
	tbl := tablesFromManifest(m)
	f, err := openIndexFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		return model.Session{}, false, err
	}
	defer func() { _ = f.Close() }()

	metaKey := meta.Key()
	ro, rerr := readRecordOffsetsFor(dir, f, map[string]bool{metaKey: true})
	if rerr != nil || ro.covered < ro.logSize {
		return model.Session{}, false, nil
	}
	liveOffs := slices.DeleteFunc(slices.Clone(ro.byKey[metaKey]), tbl.isDead)
	if len(liveOffs) <= recallConclusionsFullCutoff {
		return model.Session{}, false, nil
	}

	scan, ok, err := scanRecallEpisodeOffsets(f, tbl, metaKey, liveOffs, wantQuoted, quotedRoles, quotedTimes, allowZeroTime)
	if err != nil || !ok {
		return model.Session{}, false, err
	}
	if scan.userCount < longTurns || (len(wantQuoted) > 0 && len(scan.matchedIdx) == 0) {
		return model.Session{}, false, nil
	}

	keep, ok, err := decodeRecallKeptRecords(f, tbl, metaKey, liveOffs, scan, roles)
	if err != nil || !ok {
		return model.Session{}, false, err
	}
	return buildRecallEpisodeSession(meta, scan, keep, roles), true, nil
}
