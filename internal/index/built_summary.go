package index

// BuiltSummary is what a store holds, read from the manifest alone: how many
// sessions, from how many harnesses, and how many questions were asked in
// more than one session — the same hashes FindAskedTwice reads, counted
// rather than picked. allow is the trust gate FindAskedTwice takes; nil
// counts every session.
func BuiltSummary(dir string, allow func(project string) bool) (sessions, harnesses, repeated int) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0, 0, 0
	}
	byHash := map[uint64]int{}
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
			byHash[h]++
		}
	}
	for _, n := range byHash {
		if n > 1 {
			repeated++
		}
	}
	return sessions, len(seenHarness), repeated
}
