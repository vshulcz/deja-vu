package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// A hook or plugin deja wrote is a copy of what this binary generates, frozen
// at install time. Upgrade the binary and those copies stay as they were:
// when a generator changes — a wrong event name, a timeout in the wrong unit —
// the fix ships and never reaches anyone, because nobody re-runs an installer
// that already succeeded. That is how qwen and kimi sat with dead wiring here
// for weeks.
//
// So deja records what it wired and with which version, and refreshes those
// same targets after an upgrade. Only targets it installed itself, never a
// new one: this repairs, it does not spread.
type wiringState struct {
	Version string   `json:"version"`
	Targets []string `json:"targets"`
	// Home is the home directory the targets were written under. The repair
	// derives every path from the environment it runs in, so a process whose
	// HOME points elsewhere while the record is still visible — sudo with a
	// preserved XDG_CONFIG_HOME, a container, an su session — wrote a fresh
	// set of configs into a home nobody installed into, left the real ones
	// pointing at the old binary, and marked the record repaired (#885).
	Home string `json:"home,omitempty"`
	// Created are the config paths that did not exist before deja wrote them.
	// #840 established that a file which turns out to be entirely deja's is
	// deleted on the way out rather than left empty; that test is byte-emptiness,
	// which the structured writers never reach — they leave `{"mcpServers":{}}`.
	// Knowing which files deja made is what lets the same rule apply to them
	// without ever removing a config the reader already had (#2583).
	Created []string `json:"created,omitempty"`
	// Blocks are the containers deja added to a config that had none —
	// "<path>#mcpServers". Removing only deja's entry left the reader with an
	// empty block they never wrote (#2604); knowing deja added it is what
	// allows taking it back without touching one they wrote themselves.
	Blocks []string `json:"blocks,omitempty"`
	// Exe is the binary path the configs were written with. A move without a
	// version change is ordinary — a relink, a reinstall of the same release,
	// a `go install` over a manual download — and left every config pointing
	// at a path that no longer exists (#773).
	Exe string `json:"exe,omitempty"`
}

func wiringStatePath() string {
	return filepath.Join(xdgConfigHome(), "deja", "wiring.json")
}

// xdgConfigHome is the base for the files that are deja's own — the wiring
// record and the sync timer's unit — not for the ones that mirror a harness's
// path logic.
//
// The XDG spec: "All paths set in these environment variables must be
// absolute. If an implementation encounters a relative path in any of these
// variables it should consider the path invalid and ignore it." deja honoured
// a relative value instead, so `XDG_CONFIG_HOME=relcfg deja install` wrote
// wiring.json into whatever directory the command ran from, where no later run
// would look for it (#1693).
func xdgConfigHome() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(base) {
		return base
	}
	return filepath.Join(homeDir(), ".config")
}

func readWiringState() wiringState {
	var st wiringState
	b, err := os.ReadFile(wiringStatePath())
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

// recordWiring remembers the targets an install wrote. Uninstalled ones drop
// out, so a user who removes a harness is not re-wired behind their back.
func recordWiring(targets []string, uninstall bool) {
	st := readWiringState()
	have := map[string]bool{}
	for _, t := range st.Targets {
		have[t] = true
	}
	for _, t := range targets {
		if t == "statusline" {
			continue
		}
		have[t] = !uninstall
	}
	var kept []string
	for t, on := range have {
		if on {
			kept = append(kept, t)
		}
	}
	sort.Strings(kept)
	exe, _ := os.Executable()
	exe, _ = filepath.Abs(exe)
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Uninstalling everything on a machine that was never wired would otherwise
	// create the record on the way out — a file left behind by the command that
	// removes things (#676).
	if len(kept) == 0 {
		if _, err := os.Stat(wiringStatePath()); os.IsNotExist(err) {
			return
		}
	}
	created := append([]string(nil), st.Created...)
	seen := map[string]bool{}
	for _, p := range created {
		seen[p] = true
	}
	for _, p := range createdByThisRun {
		if !seen[p] {
			created = append(created, p)
			seen[p] = true
		}
	}
	// A path deja no longer wired is not one it will be asked about again.
	if len(kept) == 0 {
		created = nil
	}
	sort.Strings(created)
	blocks := append([]string(nil), st.Blocks...)
	for _, b := range blocksAddedThisRun {
		if !slices.Contains(blocks, b) {
			blocks = append(blocks, b)
		}
	}
	blocks = slices.DeleteFunc(blocks, func(b string) bool { return blocksForgottenThisRun[b] })
	if len(kept) == 0 {
		blocks = nil
	}
	sort.Strings(blocks)
	st = wiringState{Version: version, Targets: kept, Created: created, Blocks: blocks, Exe: exe, Home: homeDir()}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	path := wiringStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(b, '\n'), 0o644)
}

// exeIsTemporary reports whether the running binary is one the wiring must not
// adopt. A variable so a test can drive both answers: the test binary itself
// always lives in a temp directory, so the check has to be exercised rather
// than inherited (#2684).
var exeIsTemporary = func(p string) bool {
	if underTestBinary() {
		return false
	}
	return underTempDir(p)
}

// underTempDir reports whether a path sits under this machine's temp directory,
// or under the two that are temp everywhere regardless of what TMPDIR says.
func underTempDir(p string) bool {
	if p == "" {
		return false
	}
	p = filepath.Clean(p)
	// Both forms of each root: /tmp resolves to /private/tmp on macOS and
	// /var/folders to /private/var/folders, and the path being judged may be
	// written either way — it need not exist, so it cannot be resolved itself.
	var roots []string
	for _, root := range []string{os.TempDir(), "/tmp", "/private/tmp"} {
		if root == "" || root == "/" {
			continue
		}
		root = filepath.Clean(root)
		roots = append(roots, root)
		if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
			roots = append(roots, resolved)
		}
	}
	for _, root := range roots {
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// refreshWiringAfterUpgrade rewrites the recorded targets when the binary that
// wrote them is not the one running now. It returns the targets it changed, so
// a caller with somewhere to print can say so; the hook paths call it and stay
// quiet, because a session start is not the place for maintenance chatter.
func refreshWiringAfterUpgrade() []string {
	// Each run answers for itself: the list is process state, and a second call
	// in one process — the test binary, a long-lived host — must not inherit
	// the first one's failures.
	stuckWiring = nil
	st := readWiringState()
	if len(st.Targets) == 0 || version == "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	exe, _ = filepath.Abs(exe)
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Either the version or the path: a binary that moved writes the same
	// version into configs that now name a file which is not there (#773).
	if st.Version == version && (st.Exe == "" || st.Exe == exe) {
		return nil
	}
	// But not a binary that will be gone tomorrow. The repair exists because
	// deja moves — a release replaces it, a package manager relocates it — and
	// it cannot tell that from one run of a build somewhere else: a `go run`, a
	// colleague's checkout, a CI job, a scratch build in a temp directory. Each
	// of those rewrote every config on the machine to a path that stops
	// existing, and the reader found out when recall went quiet (#2684).
	if exeIsTemporary(exe) {
		return nil
	}
	// Only the home the targets were written under: this repairs, it does not
	// spread (#885).
	if st.Home != "" && st.Home != homeDir() {
		return nil
	}
	var changed []string
	failed := false
	for _, target := range st.Targets {
		res, err := installTarget(target, exe, false)
		if err != nil {
			// A harness the user has since removed is not an error worth
			// surfacing: the next install run will drop it from the record.
			// One whose config cannot be written is: the record is left
			// unstamped on purpose (#2212), so every later start repeats the
			// repair and the same line, and nothing said which target was
			// stuck or that anything had failed (#2594).
			failed = true
			stuckWiring = append(stuckWiring, target)
			continue
		}
		cr, cerr := refreshCommandFile(target, exe)
		if cerr != nil {
			failed = true
			stuckWiring = append(stuckWiring, target)
			continue
		}
		// The command counts as a change of its own. It is text the reader can
		// have edited, and a rewrite that replaces it has to be announced even
		// when the wiring beside it was already right (#886).
		if wiringChanged(res) || wiringChanged(cr) {
			changed = append(changed, target)
		}
	}
	// Only when every target took the new path. Stamping the record after a
	// refusal claimed a rewire that did not happen: the version then matched,
	// so no later start tried again, and doctor's stale-wiring check reads the
	// recorded path — which named the binary that exists while the config on
	// disk still named the old one, so it said nothing either (#2212).
	if failed {
		return changed
	}
	recordWiring(st.Targets, false)
	return changed
}

// wiringCreated reports that deja created this config rather than finding it.
func wiringCreated(path string) bool {
	for _, p := range readWiringState().Created {
		if p == path {
			return true
		}
	}
	for _, p := range createdByThisRun {
		if p == path {
			return true
		}
	}
	return false
}

// refreshCommandFile rewrites the /deja command for a target that already has
// one. The command holds the same absolute path the wiring does, and was
// written by the install command rather than by installTarget, so a move left
// it running a binary that is gone while everything around it was repaired
// (#2693).
//
// Only a file that is already there: a machine installed with --no-guidance
// has none, and the repair is not the place to hand it text it declined.
func refreshCommandFile(target, exe string) (installResult, error) {
	harness := guidanceHarness(target)
	if !commandFileWritten(harness) {
		return installResult{}, nil
	}
	return installCommandFile(harness, exe, false)
}

// wiringChanged reports whether a write did something worth telling the reader
// about.
func wiringChanged(r installResult) bool {
	return r.Action != "" && r.Action != "unchanged"
}

// commandFileWritten reports whether this harness has a /deja command on disk.
// Goose keeps the command in config.yaml and the workflow in a recipe beside
// it, so the recipe is what answers for goose.
func commandFileWritten(harness string) bool {
	if harness == "goose" {
		// Both halves. The writer re-adds the slash_commands entry and creates
		// config.yaml if it has to, so going by the recipe alone would put back
		// a command someone took out of the config by hand.
		if _, err := os.Stat(gooseRecipePath()); err != nil {
			return false
		}
		return gooseSlashCommandPresent()
	}
	path := commandFilePath(harness)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// gooseSlashCommandPresent reports whether config.yaml still lists deja's
// slash command, in any of the three quotings removeGooseSlashCommand knows.
func gooseSlashCommandPresent() bool {
	b, err := os.ReadFile(filepath.Join(gooseConfigDir(), "config.yaml"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch strings.TrimSpace(line) {
		case `- command: "deja"`, "- command: deja", "- command: 'deja'":
			return true
		}
	}
	return false
}

// stuckWiring is what this process could not rewire. Read by the session start,
// which is the only surface a person sees on an ordinary day.
var stuckWiring []string

// blocksAddedThisRun and blocksForgottenThisRun carry what this process added to
// or took back from a config, until recordWiring folds them into the record.
var (
	blocksAddedThisRun     []string
	blocksForgottenThisRun = map[string]bool{}
)

func blockKey(path, name string) string { return path + "#" + name }

// noteBlockAdded records that deja, not the reader, put this container there.
func noteBlockAdded(path, name string) {
	blocksAddedThisRun = append(blocksAddedThisRun, blockKey(path, name))
}

// blockWasAdded reports whether deja added it, in this run or an earlier one.
func blockWasAdded(path, name string) bool {
	key := blockKey(path, name)
	if slices.Contains(blocksAddedThisRun, key) {
		return true
	}
	return slices.Contains(readWiringState().Blocks, key)
}

func forgetBlockAdded(path, name string) { blocksForgottenThisRun[blockKey(path, name)] = true }
