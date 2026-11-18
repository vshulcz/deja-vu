package index

import (
	"path/filepath"
	"testing"
)

// DefaultDir honors DEJA_INDEX_DIR first, then an absolute XDG_CACHE_HOME
// (relative values are invalid per the XDG spec and ignored), then ~/.cache.
func TestDefaultDirXDGCacheHome(t *testing.T) {
	home := t.TempDir()
	xdgCache := t.TempDir()
	override := filepath.Join(t.TempDir(), "custom-index")
	setHome(t, home)

	t.Setenv("DEJA_INDEX_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	wantHome := filepath.Join(home, ".cache", "deja", "index.db")
	if got := DefaultDir(); got != wantHome {
		t.Fatalf("default DefaultDir() = %q, want %q", got, wantHome)
	}

	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	if got := DefaultDir(); got != wantHome {
		t.Fatalf("relative XDG_CACHE_HOME DefaultDir() = %q, want %q", got, wantHome)
	}

	t.Setenv("XDG_CACHE_HOME", xdgCache)
	wantXDG := filepath.Join(xdgCache, "deja", "index.db")
	if got := DefaultDir(); got != wantXDG {
		t.Fatalf("XDG_CACHE_HOME DefaultDir() = %q, want %q", got, wantXDG)
	}

	t.Setenv("DEJA_INDEX_DIR", override)
	if got := DefaultDir(); got != override {
		t.Fatalf("DEJA_INDEX_DIR DefaultDir() = %q, want %q", got, override)
	}
}
