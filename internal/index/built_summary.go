package index

// BuiltSummary is what a store holds, read from the manifest alone: how many
// sessions, from how many harnesses, and how many questions were asked in
// more than one session — the same hashes FindAskedTwice reads, counted
// rather than picked, one conversation written twice counted once. allow is the trust gate FindAskedTwice takes; nil
// counts every session.
func BuiltSummary(dir string, allow func(project string) bool) (sessions, harnesses, repeated int) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0, 0, 0
	}
	byHash := map[uint64][]SessionMeta{}
	seenHarness := map[string]bool{}
	for _, meta := range m.Sessions {
		if allow != nil && !allow(meta.Project) {
			continue
		}
		sessions++
		if meta.Harness != "" {
			seenHarness[meta.Harness] = true
		}
		for _, h := range meta.Asked {
			byHash[h] = append(byHash[h], meta)
		}
	}
	// A resumed or forked conversation carries the same asked hashes under a
	// second key; the brief drops those copies (#3711) and so does this count.
	for h, metas := range byHash {
		if len(metas) > 1 && len(oneConversationEach(metas, h)) > 1 {
			repeated++
		}
	}
	return sessions, len(seenHarness), repeated
}
