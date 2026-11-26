// Package harness defines declarative specs for detecting, probing, wiring,
// and unwiring AI coding harness integrations (JSON/JSONC MCP, TOML MCP, JSON
// hooks, YAML sections, marked blocks, and managed skill or asset files).
package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Action values returned by Wire and Unwire operations.
const (
	ActionWrote     = "wrote"
	ActionUpdated   = "updated"
	ActionUnchanged = "unchanged"
	ActionRemoved   = "removed"
	ActionSkipped   = "skipped"
)

// DetectResult reports whether a harness target exists on disk.
type DetectResult struct {
	Path    string
	Present bool
}

// ProbeResult reports the parsed wiring state of a harness target.
type ProbeResult struct {
	Path        string
	Present     bool
	Wired       bool
	Disabled    bool
	DisabledFor []string
	DejaKeys    []string
}

// WireResult reports the outcome of wiring a harness target.
type WireResult struct {
	Path              string
	Action            string
	AdoptedKey        string
	LeftKeys          []string
	PreservedDisabled bool
	DisabledFor       []string
	EditedByUser      bool
	PrevEntry         any
	Note              string
	BeforeRoot        map[string]any
	AfterRoot         map[string]any
}

// UnwireResult reports the outcome of removing deja wiring from a target.
type UnwireResult struct {
	Path        string
	Action      string
	RemovedKeys []string
	LeftKeys    []string
	Note        string
	BeforeRoot  map[string]any
	AfterRoot   map[string]any
}

// Spec is the uniform lifecycle contract implemented by every harness adapter.
type Spec interface {
	Name() string
	TargetPath() string
	Detect() (DetectResult, error)
	Probe() (ProbeResult, error)
	Wire() (WireResult, error)
	Unwire() (UnwireResult, error)
}

// Ops injects external side effects (backups, read/write tracking, persistent
// wiring state, binary matching). Zero values fall back to safe local defaults.
type Ops struct {
	ReadFile          func(path string) ([]byte, error)
	WriteFile         func(path string, old, next []byte) (string, error)
	RemoveFile        func(path string) error
	BeforeWrite       func(path string, old []byte) error
	PruneDir          func(dir string)
	NoteCreatedDirs   func(dir string)
	RecordBlockAdded  func(path, blockPath string)
	WasBlockAdded     func(path, blockPath string) bool
	ForgetBlockAdded  func(path, blockPath string)
	RecordAdoptedKey  func(path, blockPath, key string)
	AdoptedKeys       func(path, blockPath string) []string
	ForgetAdoptedKeys func(path, blockPath string)
	IsDejaCommand     func(cmd string, args []string) bool
	IsDejaEntry       func(entry map[string]any) bool
	IsDejaServer      func(entry map[string]any) bool
	MarshalJSON       func(old []byte, root map[string]any) ([]byte, error)
	EditJSONC         func(old []byte, blockPath, key string, entry map[string]any, uninstall bool, dropFrom int) ([]byte, error)
	FormatError       func(path string, err error) error
}

type memoryState struct {
	mu      sync.Mutex
	blocks  map[string]bool
	adopted map[string][]string
}

func (m *memoryState) recordBlock(path, blockPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.blocks == nil {
		m.blocks = map[string]bool{}
	}
	m.blocks[path+"\x00"+blockPath] = true
}

func (m *memoryState) hasBlock(path, blockPath string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blocks[path+"\x00"+blockPath]
}

func (m *memoryState) forgetBlock(path, blockPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blocks, path+"\x00"+blockPath)
}

func (m *memoryState) recordAdopted(path, blockPath, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.adopted == nil {
		m.adopted = map[string][]string{}
	}
	k := path + "\x00" + blockPath
	if slices.Contains(m.adopted[k], key) {
		return
	}
	m.adopted[k] = append(m.adopted[k], key)
}

func (m *memoryState) getAdopted(path, blockPath string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.adopted[path+"\x00"+blockPath]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

func (m *memoryState) forgetAdopted(path, blockPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.adopted, path+"\x00"+blockPath)
}

type resolvedOps struct {
	ops *Ops
	mem *memoryState
}

func resolveOps(ops *Ops, mem **memoryState) resolvedOps {
	if *mem == nil {
		*mem = &memoryState{}
	}
	return resolvedOps{ops: ops, mem: *mem}
}

func (r resolvedOps) formatErr(path string, err error) error {
	if err == nil {
		return nil
	}
	if r.ops != nil && r.ops.FormatError != nil {
		return r.ops.FormatError(path, err)
	}
	return fmt.Errorf("%s: %w", path, err)
}

func (r resolvedOps) readConfig(path string) ([]byte, error) {
	if r.ops != nil && r.ops.ReadFile != nil {
		return r.ops.ReadFile(path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return bytes.TrimPrefix(b, utf8BOM), nil
}

func (r resolvedOps) writeConfig(path string, old, next []byte) (string, error) {
	if r.ops != nil && r.ops.WriteFile != nil {
		return r.ops.WriteFile(path, old, next)
	}
	normalized := matchLineEndings(old, next)
	if bytes.Equal(old, normalized) {
		return ActionUnchanged, nil
	}
	out := normalized
	if fileStartsWithBOM(path) {
		out = withBOM(out)
	}
	if r.ops != nil && r.ops.BeforeWrite != nil {
		if err := r.ops.BeforeWrite(path, old); err != nil {
			return "", err
		}
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := writeFileAtomic(path, out, mode); err != nil {
		return "", err
	}
	if len(old) == 0 {
		return ActionWrote, nil
	}
	return ActionUpdated, nil
}

func (r resolvedOps) removeFile(path string) error {
	if r.ops != nil && r.ops.RemoveFile != nil {
		return r.ops.RemoveFile(path)
	}
	return os.Remove(path)
}

func (r resolvedOps) noteCreatedDirs(dir string) {
	if r.ops != nil && r.ops.NoteCreatedDirs != nil {
		r.ops.NoteCreatedDirs(dir)
	}
}

func (r resolvedOps) pruneDir(dir string, levels int) {
	if r.ops != nil && r.ops.PruneDir != nil {
		r.ops.PruneDir(dir)
		return
	}
	cur := dir
	for range levels {
		if cur == "" || cur == "." || cur == string(filepath.Separator) {
			return
		}
		if err := os.Remove(cur); err != nil {
			return
		}
		cur = filepath.Dir(cur)
	}
}

func (r resolvedOps) recordBlockAdded(path, blockPath string) {
	if r.ops != nil && r.ops.RecordBlockAdded != nil {
		r.ops.RecordBlockAdded(path, blockPath)
		return
	}
	r.mem.recordBlock(path, blockPath)
}

func (r resolvedOps) wasBlockAdded(path, blockPath string) bool {
	if r.ops != nil && r.ops.WasBlockAdded != nil {
		return r.ops.WasBlockAdded(path, blockPath)
	}
	return r.mem.hasBlock(path, blockPath)
}

func (r resolvedOps) forgetBlockAdded(path, blockPath string) {
	if r.ops != nil && r.ops.ForgetBlockAdded != nil {
		r.ops.ForgetBlockAdded(path, blockPath)
		return
	}
	r.mem.forgetBlock(path, blockPath)
}

func (r resolvedOps) recordAdoptedKey(path, blockPath, key string) {
	if r.ops != nil && r.ops.RecordAdoptedKey != nil {
		r.ops.RecordAdoptedKey(path, blockPath, key)
		return
	}
	r.mem.recordAdopted(path, blockPath, key)
}

func (r resolvedOps) adoptedKeys(path, blockPath string) []string {
	if r.ops != nil && r.ops.AdoptedKeys != nil {
		return r.ops.AdoptedKeys(path, blockPath)
	}
	return r.mem.getAdopted(path, blockPath)
}

func (r resolvedOps) forgetAdoptedKeys(path, blockPath string) {
	if r.ops != nil && r.ops.ForgetAdoptedKeys != nil {
		r.ops.ForgetAdoptedKeys(path, blockPath)
		return
	}
	r.mem.forgetAdopted(path, blockPath)
}

func (r resolvedOps) isDejaCommand(cmd string, args []string) bool {
	if r.ops != nil && r.ops.IsDejaCommand != nil {
		return r.ops.IsDejaCommand(cmd, args)
	}
	return defaultIsDejaCommand(cmd, args)
}

func (r resolvedOps) isDejaEntry(entry map[string]any) bool {
	if entry == nil {
		return false
	}
	if r.ops != nil && r.ops.IsDejaEntry != nil {
		return r.ops.IsDejaEntry(entry)
	}
	return defaultIsJSONDejaEntry(r, entry)
}

func (r resolvedOps) isDejaServer(entry map[string]any) bool {
	if entry == nil {
		return false
	}
	if r.ops != nil && r.ops.IsDejaServer != nil {
		return r.ops.IsDejaServer(entry)
	}
	return r.isDejaEntry(entry)
}

func (r resolvedOps) marshalJSON(old []byte, root map[string]any) ([]byte, error) {
	if r.ops != nil && r.ops.MarshalJSON != nil {
		return r.ops.MarshalJSON(old, root)
	}
	return marshalJSONLike(old, root)
}

func isDejaBinaryToken(tok string) bool {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return false
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(tok, `\`, `/`))), ".exe")
	return base == "deja" || base == "deja-mcp"
}

func defaultIsDejaCommand(cmd string, args []string) bool {
	hasMCP := false
	argHasDeja := false
	for _, a := range args {
		t := strings.TrimSpace(a)
		if t == "mcp" {
			hasMCP = true
		}
		if isDejaBinaryToken(t) {
			argHasDeja = true
		}
	}
	if isDejaBinaryToken(cmd) {
		base := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(strings.TrimSpace(cmd), `\`, `/`))), ".exe")
		if base == "deja-mcp" {
			return true
		}
		return hasMCP
	}
	return argHasDeja && hasMCP
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func fileStartsWithBOM(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var head [3]byte
	n, _ := f.Read(head[:])
	return n == len(utf8BOM) && bytes.Equal(head[:], utf8BOM)
}

func withBOM(b []byte) []byte {
	if len(b) == 0 || bytes.HasPrefix(b, utf8BOM) {
		return b
	}
	return slices.Concat(utf8BOM, b)
}

func matchLineEndings(old, next []byte) []byte {
	if !bytes.Contains(old, []byte("\r\n")) || bytes.Contains(next, []byte("\r\n")) {
		return next
	}
	return bytes.ReplaceAll(next, []byte("\n"), []byte("\r\n"))
}

func normalizeLF(b []byte) []byte {
	return bytes.ReplaceAll(bytes.TrimPrefix(b, utf8BOM), []byte("\r\n"), []byte("\n"))
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".deja-harness-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func detectPath(path string) (DetectResult, error) {
	_, err := os.Stat(path)
	if err == nil {
		return DetectResult{Path: path, Present: true}, nil
	}
	if os.IsNotExist(err) {
		for cur := filepath.Dir(path); cur != "" && cur != "." && filepath.Dir(cur) != cur; cur = filepath.Dir(cur) {
			fi, serr := os.Stat(cur)
			if serr == nil {
				if !fi.IsDir() {
					return DetectResult{Path: path}, fmt.Errorf("%s: not a directory", cur)
				}
				break
			}
			if !os.IsNotExist(serr) {
				return DetectResult{Path: path}, serr
			}
		}
		return DetectResult{Path: path, Present: false}, nil
	}
	return DetectResult{Path: path}, err
}
