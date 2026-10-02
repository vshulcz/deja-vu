package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Reasonix takes everything deja wires as one plugin package: a directory
// with reasonix-plugin.json (apiVersion reasonix.io/plugin/v2) declaring the
// MCP server, the skill, the /deja command and — for reasonix-auto — the code
// extension. Only a package installed through the plugin flow can start a
// runtime; project config can never declare one (docs/PLUGIN_PACKAGES.md).
//
// So install writes the package into deja's own directory and hands it to
// `reasonix plugin install <dir> --name deja --replace --yes`. That copies it
// to <home>/plugins/deja and records it in <home>/plugin-packages.json, the
// layout the same document names. With no reasonix on PATH, deja writes that
// layout itself, in the format Reasonix's SaveState writes it. `--yes` is the
// whole of the approval: installing a runtime is the authorization, and
// Reasonix asks nothing further, so the result says what was trusted.
//
// Uninstall is done by hand in both cases, because `reasonix plugin remove`
// leaves an empty state file behind on a machine that had none.
//
// config.toml is never written. Reasonix re-renders it on `reasonix config`
// and drops what it does not know, and nothing here needs it.

const reasonixPluginName = "deja"

// reasonixPluginSourceDir is the package as deja generates it — the plugin's
// recorded source, so Reasonix's own Update re-copies deja's current files.
func reasonixPluginSourceDir() string {
	return filepath.Join(xdgConfigHome(), "deja", "reasonix-plugin")
}

func reasonixInstalledRoot() string {
	return filepath.Join(sources.ReasonixHome(), "plugins", reasonixPluginName)
}

func reasonixInstalledManifest() string {
	return filepath.Join(reasonixInstalledRoot(), "reasonix-plugin.json")
}

func reasonixStatePath() string {
	return filepath.Join(sources.ReasonixHome(), "plugin-packages.json")
}

func reasonixSkillPath() string {
	return filepath.Join(reasonixInstalledRoot(), "skills", "deja-history", "SKILL.md")
}

func reasonixCommandPath() string {
	return filepath.Join(reasonixInstalledRoot(), "commands", "deja.md")
}

// reasonixIntercepts are the points the extension is allowed to rule on. The
// sidecar subscribes to the ones it finds here, so this list is the ceiling.
var reasonixIntercepts = []string{
	"input.receive", "tool.after", "compaction.prepare",
	"session.start", "session.load", "session.rotate",
}

func reasonixPackageVersion() string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" || v == "dev" {
		return "0.0.0-dev"
	}
	return v
}

func reasonixManifest(exe string, auto bool) []byte {
	m := map[string]any{
		"apiVersion":  "reasonix.io/plugin/v2",
		"name":        reasonixPluginName,
		"version":     reasonixPackageVersion(),
		"description": "Recall your own past coding sessions before you ask (deja-vu)",
		"homepage":    "https://github.com/vshulcz/deja-vu",
		"skills":      []string{"skills"},
		"commands":    []string{"commands"},
		"mcpServers": map[string]any{
			"deja": map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp"}},
		},
	}
	if auto {
		m["runtime"] = map[string]any{
			"command":      exe,
			"args":         []string{"reasonix-ext"},
			"required":     false,
			"intercepts":   reasonixIntercepts,
			"capabilities": []string{"interceptors", "ui"},
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return append(b, '\n')
}

// reasonixPackageFiles is the whole package, by path inside it.
func reasonixPackageFiles(exe string, auto bool) map[string][]byte {
	return map[string][]byte{
		"reasonix-plugin.json":         reasonixManifest(exe, auto),
		"skills/deja-history/SKILL.md": []byte(skillFile(skillBody)),
		"commands/deja.md":             []byte(markdownCommand(exe)),
	}
}

func sortedPackagePaths(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// reasonixEntry is the plugin-packages.json record, in the field order
// Reasonix's InstalledPlugin struct marshals: a file deja writes has to read
// back byte for byte the way Reasonix would have written it.
type reasonixEntry struct {
	Name         string `json:"name"`
	Source       string `json:"source,omitempty"`
	Root         string `json:"root"`
	Version      string `json:"version,omitempty"`
	Description  string `json:"description,omitempty"`
	ManifestKind string `json:"manifestKind,omitempty"`
	Enabled      bool   `json:"enabled"`
}

func wantReasonixEntry() reasonixEntry {
	return reasonixEntry{
		Name: reasonixPluginName, Source: reasonixPluginSourceDir(),
		Root: "plugins/" + reasonixPluginName, Version: reasonixPackageVersion(),
		Description:  "Recall your own past coding sessions before you ask (deja-vu)",
		ManifestKind: "reasonix", Enabled: true,
	}
}

// reasonixState is plugin-packages.json with every other plugin's record kept
// as the bytes it was, so rewriting ours reorders nothing of theirs.
type reasonixState struct {
	Version int               `json:"version"`
	Plugins []json.RawMessage `json:"plugins"`
}

func readReasonixState(path string) (reasonixState, []byte, error) {
	old, err := readConfig(path)
	if err != nil {
		return reasonixState{}, nil, err
	}
	st := reasonixState{Version: 1}
	if len(bytes.TrimSpace(old)) == 0 {
		return st, old, nil
	}
	if err := json.Unmarshal(old, &st); err != nil {
		return reasonixState{}, nil, configParseError(path, err)
	}
	if st.Version == 0 {
		st.Version = 1
	}
	return st, old, nil
}

func reasonixEntryName(raw json.RawMessage) string {
	var e struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &e)
	return e.Name
}

// ours finds deja's record, and whether it is the one this install writes.
func (st reasonixState) ours() (index int, current bool) {
	want := wantReasonixEntry()
	for i, raw := range st.Plugins {
		if reasonixEntryName(raw) != reasonixPluginName {
			continue
		}
		var got reasonixEntry
		_ = json.Unmarshal(raw, &got)
		// The paths as Reasonix may spell them: root with forward slashes
		// (pluginpkg.RelativeRoot), source however the command line gave it.
		// Enabled is the reader's: `reasonix plugin disable deja` turns it
		// off, and an install that counted that as stale wrote it back on
		// (#4472).
		same := got.Name == want.Name && rxSamePath(got.Source, want.Source) &&
			filepath.ToSlash(got.Root) == want.Root && got.Version == want.Version &&
			got.Description == want.Description && got.ManifestKind == want.ManifestKind
		return i, same
	}
	return -1, false
}

func (st reasonixState) marshal() []byte {
	sort.SliceStable(st.Plugins, func(i, j int) bool {
		return reasonixEntryName(st.Plugins[i]) < reasonixEntryName(st.Plugins[j])
	})
	b, _ := json.MarshalIndent(st, "", "  ")
	return append(b, '\n')
}

// reasonixPackageIsOurs reports whether a plugins/deja already on disk is one
// deja wrote. A package someone else named deja is left where it is.
func reasonixPackageIsOurs(manifestPath string) bool {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return false
	}
	var m struct {
		MCPServers map[string]any `json:"mcpServers"`
		Runtime    struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"runtime"`
	}
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	if mcpEntryRunsDeja(m.MCPServers["deja"]) {
		return true
	}
	if isDejaBinaryToken(m.Runtime.Command) && len(m.Runtime.Args) > 0 && m.Runtime.Args[0] == "reasonix-ext" {
		return true
	}
	// The binary's name is not the only proof. On Windows the entry names
	// the build itself, since the launcher is unix-only, and a build called
	// anything but deja.exe — a test binary, a renamed download — reads as
	// someone else's. A package byte-identical to the copy deja keeps in its
	// own directory is the one deja handed Reasonix.
	own, err := os.ReadFile(filepath.Join(reasonixPluginSourceDir(), "reasonix-plugin.json"))
	return err == nil && bytes.Equal(own, b)
}

// reasonixInstalledRuntime reports whether deja's installed package carries
// the extension.
func reasonixInstalledRuntime() bool {
	if reasonixForeignPackage() {
		return false
	}
	b, err := os.ReadFile(reasonixInstalledManifest())
	if err != nil {
		return false
	}
	var m struct {
		Runtime struct {
			Args []string `json:"args"`
		} `json:"runtime"`
	}
	return json.Unmarshal(b, &m) == nil && len(m.Runtime.Args) > 0 && m.Runtime.Args[0] == "reasonix-ext"
}

// reasonixForeignPackage reports whether plugins/deja is someone else's: a
// link (`reasonix plugin install --link` leaves one, and deja never does), or
// a directory whose manifest does not run deja.
func reasonixForeignPackage() bool {
	root := reasonixInstalledRoot()
	fi, err := os.Lstat(root)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	manifest := reasonixInstalledManifest()
	return fileExists(manifest) && !reasonixPackageIsOurs(manifest)
}

// reasonixRecordIsOurs reports whether a plugin-packages.json record named
// deja is deja's: installed from deja's own source directory, or rooted at
// plugins/deja while that directory holds deja's package.
func reasonixRecordIsOurs(raw json.RawMessage, ourDir bool) bool {
	var e reasonixEntry
	if json.Unmarshal(raw, &e) != nil {
		return false
	}
	if rxSamePath(e.Source, reasonixPluginSourceDir()) {
		return true
	}
	return filepath.ToSlash(e.Root) == "plugins/"+reasonixPluginName && ourDir && reasonixPackageIsOurs(reasonixInstalledManifest())
}

func reasonixInstalledMatches(files map[string][]byte) bool {
	root := reasonixInstalledRoot()
	for _, rel := range sortedPackagePaths(files) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(b, files[rel]) {
			return false
		}
	}
	return true
}

// reasonixCLI is the reasonix binary deja hands the package to, or "". A
// variable so the installer tests can put a fake in its place.
var reasonixCLI = func() string {
	p, err := exec.LookPath("reasonix")
	if err != nil {
		return ""
	}
	return p
}

func installReasonix(exe string, uninstall, auto bool) (installResult, error) {
	if uninstall {
		return uninstallReasonix()
	}
	exe = hookExeFor(exe, false)
	manifest := reasonixInstalledManifest()
	// The plain target does not take out a runtime deja already installed:
	// `install --all` runs it beside the -auto target, and each would undo
	// the other. `deja uninstall reasonix-auto` is how the runtime goes.
	if !auto && reasonixInstalledRuntime() {
		auto = true
	}
	if reasonixForeignPackage() {
		return installResult{}, fmt.Errorf("%s is a Reasonix plugin named %q that deja did not write — left as it was", reasonixInstalledRoot(), reasonixPluginName)
	}
	files := reasonixPackageFiles(exe, auto)
	src := reasonixPluginSourceDir()
	if err := writePackageTree(src, files); err != nil {
		return installResult{}, err
	}
	statePath := reasonixStatePath()
	st, _, err := readReasonixState(statePath)
	if err != nil {
		return installResult{}, err
	}
	if i, _ := st.ours(); i >= 0 && !reasonixRecordIsOurs(st.Plugins[i], isRealDir(reasonixInstalledRoot())) {
		return installResult{}, fmt.Errorf("%s records a Reasonix plugin named %q that deja did not install — left as it was", statePath, reasonixPluginName)
	}
	off := st.off()
	if _, current := st.ours(); current && reasonixInstalledMatches(files) {
		return reasonixKeptOff(reasonixResult("unchanged", auto), off), nil
	}
	had := fileExists(manifest)
	if cli := reasonixCLI(); cli != "" {
		if err := installReasonixViaCLI(cli, src); err != nil {
			return installResult{}, err
		}
		// Reasonix's own installer records the plugin enabled; the switch
		// the reader had goes back on top of it.
		if off {
			if err := reasonixSwitchOff(); err != nil {
				return installResult{}, err
			}
		}
	} else if err := installReasonixByHand(files, st, off); err != nil {
		return installResult{}, err
	}
	if !reasonixInstalledMatches(files) {
		return installResult{}, fmt.Errorf("%s does not hold the package deja wrote after the install", reasonixInstalledRoot())
	}
	action := "created"
	if had {
		action = "updated"
	}
	return reasonixKeptOff(reasonixResult(action, auto), off), nil
}

// off reports whether deja's record is there and switched off.
func (st reasonixState) off() bool {
	i, _ := st.ours()
	if i < 0 {
		return false
	}
	var got reasonixEntry
	return json.Unmarshal(st.Plugins[i], &got) == nil && !got.Enabled
}

// reasonixKeptOff says so when install left the plugin the way the reader
// switched it.
func reasonixKeptOff(r installResult, off bool) installResult {
	if off {
		r.Note = "left deja's plugin switched off, the way it was — `reasonix plugin enable deja` turns it back on; " + r.Note
	}
	return r
}

// reasonixSwitchOff writes deja's record back as disabled.
func reasonixSwitchOff() error {
	path := reasonixStatePath()
	st, old, err := readReasonixState(path)
	if err != nil {
		return err
	}
	i, _ := st.ours()
	if i < 0 {
		return nil
	}
	// Through the struct, which marshals in Reasonix's own field order.
	var rec reasonixEntry
	if err := json.Unmarshal(st.Plugins[i], &rec); err != nil {
		return err
	}
	rec.Enabled = false
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	st.Plugins[i] = b
	_, err = writeIfChanged(path, old, st.marshal())
	return err
}

// reasonixResult names the package directory whole, the state file that
// records it and deja's source copy, since an install writes all three.
func reasonixResult(action string, auto bool) installResult {
	note := fmt.Sprintf("recorded in %s; source %s", shortHome(reasonixStatePath()), shortHome(reasonixPluginSourceDir()))
	if trust := reasonixTrustNote(auto); trust != "" {
		note += "; " + trust
	}
	return installResult{Path: reasonixInstalledRoot(), Action: action, Note: note, also: []string{reasonixStatePath()}}
}

// reasonixTrustNote says what reasonix-auto trusted. Reasonix shows a FULL
// TRUST block for a runtime in its own install preview; --yes skips that
// screen, so the line it would have shown is printed here instead.
func reasonixTrustNote(auto bool) string {
	if !auto {
		return ""
	}
	return "the extension runs with full trust, as every Reasonix runtime does; `reasonix plugin show deja` lists what it intercepts"
}

// installReasonixViaCLI runs Reasonix's own installer. What it creates is
// recorded first, the way every writer here records what it makes, so the
// uninstall can take back exactly that.
func installReasonixViaCLI(cli, src string) error {
	statePath := reasonixStatePath()
	if fileExists(statePath) {
		if _, err := backupOnceUnlessCreated(statePath); err != nil {
			return err
		}
	} else {
		createdByThisRun = append(createdByThisRun, statePath)
	}
	noteCreatedDirs(reasonixInstalledRoot())
	// Starting reasonix at all makes a key it signs model-settings receipts
	// with, and an empty directory for crash reports. On a home that had
	// neither, they are this install's doing too.
	key := reasonixReceiptKey()
	var missing []string
	for p := key; p != sources.ReasonixHome() && p != filepath.Dir(p); p = filepath.Dir(p) {
		if _, err := os.Lstat(p); err != nil {
			missing = append(missing, p)
		}
	}
	if _, err := os.Lstat(reasonixCrashDir()); err != nil {
		missing = append(missing, reasonixCrashDir())
	}
	defer func() {
		for i := len(missing) - 1; i >= 0; i-- {
			if _, err := os.Lstat(missing[i]); err != nil {
				continue
			}
			if missing[i] == key {
				createdByThisRun = append(createdByThisRun, key)
			} else {
				createdDirsByThisRun = append(createdDirsByThisRun, missing[i])
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, "plugin", "install", src, "--name", reasonixPluginName, "--replace", "--yes")
	cmd.Env = append(os.Environ(), "REASONIX_HOME="+sources.ReasonixHome())
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 600 {
			tail = "…" + tail[len(tail)-600:]
		}
		return fmt.Errorf("reasonix plugin install failed: %v: %s", err, tail)
	}
	return nil
}

// installReasonixByHand writes what `reasonix plugin install` writes: the
// package copied under plugins/deja and deja's record in the state file.
func installReasonixByHand(files map[string][]byte, st reasonixState, off bool) error {
	if err := writePackageTree(reasonixInstalledRoot(), files); err != nil {
		return err
	}
	want := wantReasonixEntry()
	want.Enabled = !off
	entry, _ := json.Marshal(want)
	if i, _ := st.ours(); i >= 0 {
		st.Plugins[i] = entry
	} else {
		st.Plugins = append(st.Plugins, entry)
	}
	path := reasonixStatePath()
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	_, err = writeIfChanged(path, old, st.marshal())
	return err
}

// writePackageTree writes the package files under dir. Both copies are
// deja's own directories, so there is no snapshot beside a file: a .bak left
// in the source would be copied into the plugin by the next install.
func writePackageTree(dir string, files map[string][]byte) error {
	noteCreatedDirs(dir)
	for _, rel := range sortedPackagePaths(files) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, files[rel]) {
			continue
		}
		noteCreatedDirs(filepath.Dir(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[rel], 0o644); err != nil {
			return err
		}
	}
	return nil
}
