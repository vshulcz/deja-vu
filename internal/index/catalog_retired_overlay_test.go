package index

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// #6168: an append commits its manifest and then retires the overlays it
// folded in. A reader that listed buckets/ before the retirement opened an
// overlay that was gone, and recall failed with ENOENT until the next query.

// catalogIndexWithOverlay builds a small index and adds one overlay, a copy of
// a real bucket, as an append leaves one. It returns the index and the overlay.
func catalogIndexWithOverlay(t *testing.T) (string, string) {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "catalog-overlay")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sessions := []model.Session{{ID: "one", Harness: "claude", Project: "p", Updated: base,
		Messages: []model.Message{{Role: "user", Text: "amberwolf paperbox canonical fixture answer"}}}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	var bucket string
	for _, de := range entries {
		if strings.HasSuffix(de.Name(), ".bin") {
			bucket = filepath.Join(dir, "buckets", de.Name())
			break
		}
	}
	if bucket == "" {
		t.Fatal("the index has no bucket to copy")
	}
	body, err := os.ReadFile(bucket)
	if err != nil {
		t.Fatal(err)
	}
	overlay := overlayPath(bucket, 1)
	if err := os.WriteFile(overlay, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, overlay
}

func TestTokenCatalogRereadsWhenAnAppendRetiresAnOverlay(t *testing.T) {
	dir, overlay := catalogIndexWithOverlay(t)
	listings := 0
	catalogListed = func() {
		listings++
		// The append's cleanup, after the reader listed the overlay and
		// before it opened it.
		if listings == 1 {
			if err := os.Remove(overlay); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { catalogListed = nil })
	catalog, dirs, err := tokenCatalogDirs(dir)
	if err != nil {
		t.Fatalf("tokenCatalogDirs with an overlay retired mid-read: %v", err)
	}
	if listings != 2 {
		t.Fatalf("listings = %d, want 2: one read, and one from a fresh listing", listings)
	}
	if !catalog["amberwolf"] {
		t.Fatalf("catalog lacks a token of the index after the reread: %d tokens", len(catalog))
	}
	for _, d := range dirs {
		if d.path == overlay {
			t.Fatalf("the reread kept the retired overlay %s", overlay)
		}
	}
}

func TestTokenCatalogStillReportsAnOverlayThatStaysMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a dangling symlink needs privileges on Windows")
	}
	dir, overlay := catalogIndexWithOverlay(t)
	// Listed by every read and opened by none: a lost overlay, not a cleanup.
	if err := os.Remove(overlay); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere.ov"), overlay); err != nil {
		t.Fatal(err)
	}
	listings := 0
	catalogListed = func() { listings++ }
	t.Cleanup(func() { catalogListed = nil })
	if _, _, err := tokenCatalogDirs(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("tokenCatalogDirs with a lost overlay: err = %v, want ENOENT", err)
	}
	if listings != 2 {
		t.Fatalf("listings = %d, want 2: the retry runs once and no more", listings)
	}
}

func TestTokenCatalogDoesNotRetryADamagedOverlay(t *testing.T) {
	dir, overlay := catalogIndexWithOverlay(t)
	if err := os.WriteFile(overlay, []byte("not a bucket"), 0o600); err != nil {
		t.Fatal(err)
	}
	listings := 0
	catalogListed = func() { listings++ }
	t.Cleanup(func() { catalogListed = nil })
	if _, _, err := tokenCatalogDirs(dir); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("tokenCatalogDirs with a damaged overlay: err = %v, want a read error", err)
	}
	if listings != 1 {
		t.Fatalf("listings = %d, want 1: only a missing file is read again", listings)
	}
}
