package index

import (
	"errors"
	"slices"
	"time"
)

func isHoldNoiseRole(role string) bool { return role == roleSummary }

func isEndedNoiseRole(role string) bool {
	return role == roleSummary || role == roleEdit || role == roleWrote || role == roleFiles
}

func isEndedNoise(r Record) bool {
	return afterForkNoise(r) || r.Role == roleEdit || r.Role == roleWrote || r.Role == roleFiles || r.Time.IsZero()
}

func isWholeHoldNoise(r Record) bool {
	return afterForkNoise(r) || r.Role == roleEdit || r.Role == roleWrote || r.Role == roleFiles
}

func (lr *lineageReader) matchAtOffset(key string, off int64, target Record) (bool, error) {
	if rec, ok := lr.fullByOff[off]; ok {
		return rec.Role == target.Role && rec.Time.UnixNano() == target.Time.UnixNano() && rec.Text == target.Text, nil
	}
	st, err := lr.stubAt(key, off)
	if err != nil {
		return false, err
	}
	if st.role != target.Role || st.at.UnixNano() != target.Time.UnixNano() || st.at.IsZero() != target.Time.IsZero() {
		return false, nil
	}
	rec, err := lr.fullRecordAt(key, off)
	if err != nil {
		return false, err
	}
	return rec.Text == target.Text, nil
}

func (lr *lineageReader) probeHoldCandidates(aKey string, aOffs []int64, target Record, targetIdx int) (matched, fallback bool, err error) {
	candidates := [9]int{
		targetIdx, targetIdx - 1, targetIdx + 1, targetIdx - 2, targetIdx + 2,
		len(aOffs) - 1, len(aOffs) - 2, len(aOffs) - 3, len(aOffs) - 4,
	}
	var seen [9]int
	seenN := 0
	for _, idx := range candidates {
		if idx < 0 || idx >= len(aOffs) || slices.Contains(seen[:seenN], idx) {
			continue
		}
		seen[seenN] = idx
		seenN++
		ok, perr := lr.matchAtOffset(aKey, aOffs[idx], target)
		if perr != nil {
			if errors.Is(perr, errOffsetsMismatch) {
				lr.roOK = false
				return false, true, nil
			}
			return false, false, perr
		}
		if ok {
			return true, false, nil
		}
	}
	return false, false, nil
}

func (lr *lineageReader) heldFastCheck(aKey, oKey string, last Record, lastIdx int) (decided, isHeld bool, startIdx int) {
	if !lr.roOK {
		return false, false, 0
	}
	aOffs, aOK := lr.offsetsFor(aKey)
	if !aOK || len(aOffs) == 0 {
		return aOK, false, 0
	}
	matched, fallback, err := lr.probeHoldCandidates(aKey, aOffs, last, lastIdx)
	if err != nil || matched {
		return true, err == nil && matched, 0
	}
	if fallback || len(aOffs) < lineagePrefixMinRecords {
		return false, false, 0
	}
	oOffs, oOK := lr.offsetsFor(oKey)
	if !oOK || len(oOffs) < lineagePrefixMinRecords {
		return false, false, 0
	}
	pref := lr.sharedPrefix(aKey, aOffs, oKey, oOffs)
	if lr.roOK && pref.monotonic && last.Time.After(pref.bound) {
		return false, false, pref.length
	}
	return false, false, 0
}

func (lr *lineageReader) held(asker, other SessionMeta) bool {
	last, lastIdx, ok := lr.lastMatchWithIndex(other.Key(), isHoldNoiseRole, afterForkNoise)
	if !ok {
		return false
	}
	aKey := asker.Key()
	decided, isHeld, startIdx := lr.heldFastCheck(aKey, other.Key(), last, lastIdx)
	if decided {
		return isHeld
	}
	stubs, ok := lr.sessionRoleStubs(aKey, startIdx, map[string]bool{last.Role: true})
	if !ok {
		return false
	}
	wantNano := last.Time.UnixNano()
	for _, st := range stubs {
		if st.at.UnixNano() != wantNano {
			continue
		}
		if txt, txtOK := lr.stubText(aKey, st); txtOK && txt == last.Text {
			return true
		}
	}
	return false
}

func (lr *lineageReader) endedBy(src SessionMeta, at time.Time) bool {
	if at.IsZero() {
		return true
	}
	rec, ok := lr.lastMatch(src.Key(), isEndedNoiseRole, isEndedNoise)
	if !ok {
		return false
	}
	return !rec.Time.After(at)
}

func (lr *lineageReader) startedAt(meta SessionMeta) time.Time {
	if !meta.Started.IsZero() {
		return meta.Started
	}
	lr.ensureOpen()
	if lr.file == nil {
		return time.Time{}
	}
	if lr.roOK {
		if offs, ok := lr.offsetsFor(meta.Key()); ok {
			for _, off := range offs {
				rec, err := lr.readStubAt(meta.Key(), off)
				if err != nil {
					if errors.Is(err, errOffsetsMismatch) {
						lr.roOK = false
						break
					}
					return time.Time{}
				}
				if !rec.Time.IsZero() {
					return rec.Time
				}
			}
			if lr.roOK {
				return time.Time{}
			}
		}
	}
	recs, ok := lr.fallbackRecords(meta.Key())
	if !ok {
		return time.Time{}
	}
	for _, r := range recs {
		if !r.Time.IsZero() {
			return r.Time
		}
	}
	return time.Time{}
}

func (lr *lineageReader) wholeHolds(wholes []map[string]bool, other SessionMeta) bool {
	rec, ok := lr.lastMatch(other.Key(), isEndedNoiseRole, isWholeHoldNoise)
	if !ok {
		return false
	}
	k := turnKey(rec.Role, rec.Time.UnixMilli())
	for _, w := range wholes {
		if w[k] {
			return true
		}
	}
	return false
}

func (lr *lineageReader) matchSharedStubs(longKey string, longStubs []recordStub, shortKey string, shortByRT map[roleTimeKey][]recordStub, best time.Time) time.Time {
	var cands []int
	for i, st := range longStubs {
		if !st.at.After(best) {
			continue
		}
		if len(shortByRT[roleTimeKey{role: st.role, nano: st.at.UnixNano()}]) > 0 {
			cands = append(cands, i)
		}
	}
	if len(cands) == 0 {
		return best
	}
	slices.SortFunc(cands, func(i, j int) int {
		ti, tj := longStubs[i].at, longStubs[j].at
		if ti.After(tj) {
			return -1
		}
		if tj.After(ti) {
			return 1
		}
		return 0
	})
	for _, idx := range cands {
		lSt := longStubs[idx]
		lTxt, ok := lr.stubText(longKey, lSt)
		if !ok {
			continue
		}
		for _, sSt := range shortByRT[roleTimeKey{role: lSt.role, nano: lSt.at.UnixNano()}] {
			if sTxt, ok := lr.stubText(shortKey, sSt); ok && sTxt == lTxt {
				return lSt.at
			}
		}
	}
	return best
}

func (lr *lineageReader) sharedPrefixAndBounds(aKey, oKey string) (best time.Time, startIdx int, shortKey, longKey string) {
	shortKey, longKey = aKey, oKey
	if !lr.roOK {
		return best, 0, shortKey, longKey
	}
	aOffs, aOK := lr.offsetsFor(aKey)
	oOffs, oOK := lr.offsetsFor(oKey)
	if !aOK || !oOK {
		return best, 0, shortKey, longKey
	}
	if len(oOffs) < len(aOffs) {
		shortKey, longKey = oKey, aKey
	}
	if len(aOffs) >= lineagePrefixMinRecords && len(oOffs) >= lineagePrefixMinRecords {
		if pref := lr.sharedPrefix(aKey, aOffs, oKey, oOffs); pref.ok {
			best = pref.bound
			if pref.monotonic {
				startIdx = pref.length
			}
		}
	}
	if !lr.roOK {
		return time.Time{}, 0, aKey, oKey
	}
	return best, startIdx, shortKey, longKey
}

func (lr *lineageReader) shared(asker, other SessionMeta) time.Time {
	var zero time.Time
	if asker.Harness != other.Harness {
		return zero
	}
	aKey, oKey := asker.Key(), other.Key()
	best, startIdx, shortKey, longKey := lr.sharedPrefixAndBounds(aKey, oKey)
	shortStubs, ok := lr.sessionRoleStubs(shortKey, startIdx, nil)
	if !ok {
		return zero
	}
	shortByRT := make(map[roleTimeKey][]recordStub, len(shortStubs))
	wantRoles := map[string]bool{}
	for _, st := range shortStubs {
		if !st.at.After(best) {
			continue
		}
		rt := roleTimeKey{role: st.role, nano: st.at.UnixNano()}
		shortByRT[rt] = append(shortByRT[rt], st)
		wantRoles[st.role] = true
	}
	if len(wantRoles) == 0 {
		return best
	}
	longStubs, ok := lr.sessionRoleStubs(longKey, startIdx, wantRoles)
	if !ok {
		return zero
	}
	return lr.matchSharedStubs(longKey, longStubs, shortKey, shortByRT, best)
}

func (lr *lineageReader) wholeShared(turns map[string]bool, other SessionMeta) time.Time {
	var at time.Time
	oStubs, ok := lr.sessionRoleStubs(other.Key(), 0, nil)
	if !ok {
		return at
	}
	for _, st := range oStubs {
		if !st.at.IsZero() && turns[turnKey(st.role, st.at.UnixMilli())] && st.at.After(at) {
			at = st.at
		}
	}
	return at
}
