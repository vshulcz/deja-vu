package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deja wrote the session-start recall to `.goosehints` in goose's config
// directory, and goose 1.48 does not read that file. Measured by pointing goose
// at a stub endpoint and reading the request it sent: a project-local
// `.goosehints`, a project `AGENTS.md` and `~/.config/goose/AGENTS.md` all
// arrive; the config-directory `.goosehints`, a `~/.goosehints` and an
// ancestor's do not. So the block existed, was refreshed every session, and
// never reached the model.
func TestGooseRecallGoesWhereGooseReadsIt(t *testing.T) {
	cfg := gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "goose", "AGENTS.md")); err != nil {
		t.Errorf("nothing at the path goose reads: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "goose", ".goosehints")); err == nil {
		t.Error("the file goose does not read was written anyway")
	}
}

// An install that upgrades over an older one has to clear the file deja used to
// write. A stale copy of somebody's history, in a file nothing reads, is the
// worst of both — invisible and wrong.
func TestGooseRecallClearsTheFileItUsedToWrite(t *testing.T) {
	cfg := gooseHomeForTest(t)
	retired := filepath.Join(cfg, "goose", ".goosehints")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	// What an older deja actually wrote there: the recall frame, whole. The
	// fixture used to be a line of plain prose — which is what the reader's own
	// hints look like — and the code deleted the file on sight, so anyone
	// keeping global hints there lost them on every turn (#3196).
	if err := os.WriteFile(retired, []byte(frameRecall("recall from an older deja\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Errorf("the retired file survived the install: %v", err)
	}
}

// The same file, holding what the reader wrote. deja stopped writing here when
// the block moved to AGENTS.md, so there is nothing of deja's to take out and
// nothing to remove — on install, on uninstall, and on every hook-goose turn,
// which is where it was seen (#3196).
func TestGooseLeavesTheReadersOwnRetiredHints(t *testing.T) {
	cfg := gooseHomeForTest(t)
	retired := filepath.Join(cfg, "goose", ".goosehints")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "my own hints, written by the user\nalways run the linter first\n"
	if err := os.WriteFile(retired, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { _, err := installGooseAuto("/bin/deja", false); return err },
		dropRetiredGooseHints,
		func() error { _, err := installGooseAuto("/bin/deja", true); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(retired)
		if err != nil {
			t.Fatalf("deja deleted hints it did not write: %v", err)
		}
		if string(b) != mine {
			t.Fatalf("the reader's hints changed:\n%s", b)
		}
	}

	// And a file holding both: deja's block comes out, theirs stays.
	both := mine + "\n" + gooseRecallStart + "\nold recall\n" + gooseRecallEnd + "\n"
	if err := os.WriteFile(retired, []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(retired)
	if err != nil {
		t.Fatalf("the file went with deja's block: %v", err)
	}
	if !strings.Contains(string(b), "always run the linter first") {
		t.Fatalf("the reader's half went with deja's:\n%s", b)
	}
	if strings.Contains(string(b), gooseRecallStart) {
		t.Fatalf("deja's block stayed:\n%s", b)
	}
}

// The file is the reader's. deja edits its own block and leaves the rest, and
// uninstall takes the block out rather than the file — removing it would delete
// whatever they had written there.
func TestGooseRecallLeavesTheRestOfAgentsAlone(t *testing.T) {
	cfg := gooseHomeForTest(t)
	path := filepath.Join(cfg, "goose", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# my own notes\n\nalways use pgx, never database/sql\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "always use pgx") {
		t.Errorf("deja overwrote the reader's file:\n%s", b)
	}
	if !strings.Contains(string(b), gooseRecallStart) {
		t.Errorf("no recall block was added:\n%s", b)
	}

	if _, err := installGooseAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("uninstall deleted the reader's file: %v", err)
	}
	if !strings.Contains(string(after), "always use pgx") {
		t.Errorf("uninstall took the reader's lines with it:\n%s", after)
	}
	if strings.Contains(string(after), gooseRecallStart) {
		t.Errorf("deja's block survived uninstall:\n%s", after)
	}
}

// A second refresh replaces the block rather than stacking another one, or a
// long-running install would grow the file a session at a time.
func TestGooseRecallReplacesItsOwnBlock(t *testing.T) {
	gooseHomeForTest(t)
	doc := gooseRecallBlock("", "first")
	doc = gooseRecallBlock(doc, "second")
	if n := strings.Count(doc, gooseRecallStart); n != 1 {
		t.Errorf("%d blocks after a refresh:\n%s", n, doc)
	}
	if strings.Contains(doc, "first") || !strings.Contains(doc, "second") {
		t.Errorf("the block was not replaced:\n%s", doc)
	}
}

func gooseHomeForTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	return cfg
}
