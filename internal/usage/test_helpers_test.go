package usage

import "time"

// RecordDigestTerms is RecordDigest plus the query terms, for déjà vu audits,
// and the ids of the sessions the moment surfaced.
//
// The ids were missing, which meant a déjà vu moment — the user returning to
// ground they had covered before — left no trace on the session it was about.
// It is the only signal deja has that the *user* came back, as opposed to an
// agent pulling something, and it could not reach ranking at all.
func RecordDigestTerms(indexDir, kind, digest string, sessions int, raw int64, terms []string, ids ...string) {
	RecordDigestInto(indexDir, kind, digest, "", sessions, raw, terms, ids...)
}

// SnapshotOnly stores the digest text without writing a counting event, for
// callers that already recorded one with extra fields.
func SnapshotOnly(indexDir, kind, digest string, sessions int) {
	snapshotWrite(indexDir, kind, digest, sessions, "")
}

// InjectedToday returns session-start context bytes injected since local midnight.
func InjectedToday(indexDir string) int {
	_, _, injected := TodayWithInjections(indexDir)
	return injected
}

// TodayWithInjections returns today's agent-memory events, served bytes, and
// the subset of those bytes injected by session-start hooks.
func TodayWithInjections(indexDir string) (recalls, bytes, injected int) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, e := range read(Path(indexDir)) {
		if e.Time.Before(midnight) || ahead(e.Time, now) {
			continue
		}
		switch {
		case servedKind(e.Kind):
			recalls++
			bytes += e.Bytes
		case injectedKind(e.Kind):
			recalls++
			bytes += e.Bytes
			injected += e.Bytes
		}
	}
	return recalls, bytes, injected
}

// TodayRaw sums the source-transcript volume behind today's served digests.
func TodayRaw(indexDir string) int64 {
	return StatusCounters(indexDir).RawToday
}

// Today sums today's agent-memory events and their served bytes.
func Today(indexDir string) (recalls int, bytes int) {
	recalls, bytes, _ = TodayWithInjections(indexDir)
	return recalls, bytes
}
