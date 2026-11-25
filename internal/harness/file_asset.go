package harness

import (
	"bytes"
	"os"
	"path/filepath"
)

var _ Spec = (*FileAssetSpec)(nil)

// FileAssetSpec manages a whole-file harness asset (such as SKILL.md or custom
// instructions) with optional user-edit protection and retired-file cleanup.
type FileAssetSpec struct {
	HarnessName       string
	Path              string
	Content           []byte
	Force             bool
	GuardUserEdits    bool
	OwnedMarker       string
	PruneEmptyParents int
	CheckEdited       func(path string, existing []byte) bool
	CheckWired        func(path string, existing []byte) bool
	DropRetired       func(path string, uninstall bool) error
	Ops               *Ops

	mem *memoryState
}

// Name returns the harness name.
func (s *FileAssetSpec) Name() string {
	return s.HarnessName
}

// TargetPath returns the managed asset file path.
func (s *FileAssetSpec) TargetPath() string {
	return s.Path
}

// Detect reports whether the managed file exists on disk.
func (s *FileAssetSpec) Detect() (DetectResult, error) {
	return detectPath(s.Path)
}

// Probe reads the asset file and reports whether deja owns or matches it.
func (s *FileAssetSpec) Probe() (ProbeResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	det, err := detectPath(s.Path)
	if err != nil {
		return ProbeResult{Path: s.Path}, err
	}
	res := ProbeResult{Path: s.Path, Present: det.Present}
	if !det.Present {
		return res, nil
	}
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return ProbeResult{Path: s.Path, Present: true}, ops.formatErr(s.Path, err)
	}
	res.Wired = s.matchesContent(old)
	return res, nil
}

// Wire writes or updates the asset file unless guarded as user-edited.
func (s *FileAssetSpec) Wire() (WireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	if s.DropRetired != nil {
		if err := s.DropRetired(s.Path, false); err != nil {
			return WireResult{}, err
		}
	}
	old, err := ops.readConfig(s.Path)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	if !s.Force && len(old) > 0 && s.isUserEdited(old) {
		return WireResult{Path: s.Path, Action: ActionSkipped, EditedByUser: true}, nil
	}
	if len(old) > 0 && bytes.Equal(normalizeLF(old), normalizeLF(s.Content)) {
		return WireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	ops.noteCreatedDirs(filepath.Dir(s.Path))
	action, err := ops.writeConfig(s.Path, old, s.Content)
	if err != nil {
		return WireResult{}, ops.formatErr(s.Path, err)
	}
	return WireResult{Path: s.Path, Action: action}, nil
}

// Unwire deletes the managed asset file and prunes empty parent directories.
func (s *FileAssetSpec) Unwire() (UnwireResult, error) {
	ops := resolveOps(s.Ops, &s.mem)
	if s.DropRetired != nil {
		if err := s.DropRetired(s.Path, true); err != nil {
			return UnwireResult{}, err
		}
	}
	det, err := detectPath(s.Path)
	if err != nil {
		return UnwireResult{}, err
	}
	if !det.Present {
		return UnwireResult{Path: s.Path, Action: ActionUnchanged}, nil
	}
	if err := ops.removeFile(s.Path); err != nil && !os.IsNotExist(err) {
		return UnwireResult{}, ops.formatErr(s.Path, err)
	}
	ops.pruneDir(filepath.Dir(s.Path), s.PruneEmptyParents)
	return UnwireResult{Path: s.Path, Action: ActionRemoved}, nil
}

func (s *FileAssetSpec) matchesContent(existing []byte) bool {
	if s.CheckWired != nil {
		return s.CheckWired(s.Path, existing)
	}
	if s.OwnedMarker != "" {
		return len(bytes.TrimSpace(existing)) > 0 && bytes.Contains(existing, []byte(s.OwnedMarker))
	}
	return len(existing) > 0 && bytes.Equal(normalizeLF(existing), normalizeLF(s.Content))
}

func (s *FileAssetSpec) isUserEdited(existing []byte) bool {
	if s.CheckEdited != nil {
		return s.CheckEdited(s.Path, existing)
	}
	if !s.GuardUserEdits {
		return false
	}
	return !bytes.Equal(normalizeLF(existing), normalizeLF(s.Content))
}
