package index

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// EachAssistantTurnSince calls fn with every assistant turn written after
// since, in sessions updated after since. Only those sessions' records are
// read, assistant role only, so the cost follows the week's work rather than
// the size of the store. allow gates the speaking session's project; ignored
// sessions are skipped the way TopFriction skips them.
func EachAssistantTurnSince(dir string, since time.Time, allow func(project string) bool, fn func(meta SessionMeta, text string, at time.Time)) error {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return err
	}
	pol := policy.Load()
	excluded := excludedHarnesses()
	keys := map[string]bool{}
	metas := map[string]SessionMeta{}
	for key, meta := range m.Sessions {
		if !meta.Updated.After(since) {
			continue
		}
		if allow != nil && !allow(meta.Project) {
			continue
		}
		if metaIgnoredOrExcluded(pol, excluded, meta) {
			continue
		}
		keys[key] = true
		metas[key] = meta
	}
	if len(keys) == 0 {
		return nil
	}
	return eachRecordForKeysInRoles(filepath.Join(dir, "records.bin"), tablesFromManifest(m), keys,
		map[string]bool{"assistant": true}, func(r Record) {
			if r.Time.IsZero() || !r.Time.After(since) {
				return
			}
			fn(metas[r.Key], r.Text, r.Time)
		})
}

// RecentSpokenAssistantTurnsForTerms returns up to maxRecords recent spoken
// assistant turns (newest first) whose indexed tokens include any of terms and
// whose session key is in allowedKeys. It reads only the bucket posting list
// and pread offsets in records.bin rather than scanning whole transcripts.
func RecentSpokenAssistantTurnsForTerms(dir string, terms []string, allowedKeys map[string]bool, maxRecords int) ([]Record, error) {
	if len(terms) == 0 || len(allowedKeys) == 0 || maxRecords <= 0 {
		return nil, nil
	}
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	pol := policy.Load()
	excluded := excludedHarnesses()
	allowedOrds := make(map[uint32]bool, len(allowedKeys))
	for key := range allowedKeys {
		meta, ok := m.Sessions[key]
		if !ok || metaIgnoredOrExcluded(pol, excluded, meta) {
			continue
		}
		allowedOrds[meta.Ord] = true
	}
	if len(allowedOrds) == 0 {
		return nil, nil
	}
	seenOff := make(map[int64]bool, maxRecords)
	var offs []int64
	for _, raw := range terms {
		var tokBuf [1 + tokenHitCap]byte
		var tok string
		eachWord(strings.TrimSpace(raw), &tokBuf, func(b []byte) {
			if tok == "" {
				tok = string(b)
			}
		})
		if len(tok) < 5 {
			continue
		}
		posts, pErr := postingsFor(dir, tok)
		if pErr != nil || len(posts) == 0 {
			continue
		}
		for i := len(posts) - 1; i >= 0 && len(offs) < maxRecords*2; i-- {
			p := posts[i]
			if p.Tool || !allowedOrds[p.Sid] || seenOff[p.Off] {
				continue
			}
			seenOff[p.Off] = true
			offs = append(offs, p.Off)
		}
	}
	if len(offs) == 0 {
		return nil, nil
	}
	slices.Sort(offs)
	if len(offs) > maxRecords {
		offs = offs[len(offs)-maxRecords:]
	}
	f, tbl, err := openRecordLog(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := make([]Record, 0, len(offs))
	if err := eachOffsetRecordAtInRoles(f, offs, tbl, map[string]bool{"assistant": true}, func(_ int64, r Record) {
		if r.Key != "" && allowedKeys[r.Key] {
			out = append(out, r)
		}
	}); err != nil {
		return nil, err
	}
	slices.Reverse(out)
	return out, nil
}
