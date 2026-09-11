package index

// The states a reader has to handle are written by an upgrade, not by a test, so
// a test needs a way to put a store into one. These three are the smallest way
// to do that without a test reaching into the manifest's encoding itself.

// CurrentVersionForTest is the content version this build writes.
func CurrentVersionForTest() int { return version }

// SetManifestVersionForTest rewrites the stored content version, which is what
// an upgrade leaves behind: the store is this build's layout, and its derived
// content is one version old.
func SetManifestVersionForTest(dir string, v int) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	m.Version = v
	// The cache keys on the file's mtime and size, and a rewrite of the same
	// manifest can land inside one filesystem timestamp tick — so clear it
	// rather than trusting the stat to differ.
	if err := writeManifest(dir, m); err != nil {
		return err
	}
	clearManifestCacheForTest()
	return nil
}

// SetManifestFormatForTest rewrites the stored on-disk format, for the state a
// reader must decline: a layout it cannot be sure of. Zero is what a store
// written before the field existed carries.
func SetManifestFormatForTest(dir string, f int) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	m.Format = f
	if err := writeManifest(dir, m); err != nil {
		return err
	}
	clearManifestCacheForTest()
	return nil
}

// clearManifestCacheForTest drops the cached manifest so the next read sees what
// was just written.
func clearManifestCacheForTest() {
	manifestCache.mu.Lock()
	manifestCache.ok = false
	manifestCache.mu.Unlock()
}
