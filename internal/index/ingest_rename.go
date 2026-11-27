package index

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type renamePlan struct {
	pairs       map[string]string
	reprojected map[string]bool
	regather    []string
}

func planRenames(old *Manifest, files map[string]FileState) renamePlan {
	pairs := detectRenamedFiles(old.Files, files)
	if len(pairs) == 0 {
		return renamePlan{}
	}
	reprojected, regather := applyRenamedFiles(old, pairs)
	return renamePlan{
		pairs:       pairs,
		reprojected: reprojected,
		regather:    regather,
	}
}

func sayRenamed(progress io.Writer, pairs map[string]string) {
	if progress != nil && len(pairs) > 0 {
		fmt.Fprintf(progress, "deja: %d transcript%s renamed — the index followed the new name\n", len(pairs), pluralS(len(pairs)))
	}
}

func harnessIn(reg []sources.Harness, p string) string {
	if k, ok := kindIn(reg, p); ok {
		return k.Name
	}
	return harnessForPath(p)
}

func appendableForRename(kind, p string) bool {
	return appendableKind(kind) || (kind == "" && strings.HasSuffix(p, ".jsonl"))
}

type renameKey struct {
	harness      string
	size         int64
	safeSize     int64
	prefixSample uint64
}

// detectRenamedFiles pairs a path that has gone with a path that has appeared
// holding the same bytes. Both sides carry a prefix fingerprint already — the
// scan computes one for regular non-DB transcripts it has not seen before — so
// the match is on content, not on a name or a timestamp: same size, same
// safe-size, same sample, same harness.
//
// Pass 1 pairs exact-size moves via an O(G + A) hash lookup with zero disk
// reads. Pass 2 checks remaining appendable candidates that may have grown
// after the move.
func detectRenamedFiles(oldFiles, files map[string]FileState) map[string]string {
	reg := sources.Registry()
	sameBuckets := map[renameKey][]string{}
	goneHarness := map[string]string{}
	var goneCount int
	for _, p := range sortedKeys(oldFiles) {
		if p == syncImportPath {
			continue
		}
		of := oldFiles[p]
		if of.PrefixSample == 0 || of.SafeSize == 0 {
			continue
		}
		if _, still := files[p]; still {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue // there after all, just not in this pass's set
		}
		h := harnessIn(reg, p)
		goneHarness[p] = h
		k := renameKey{harness: h, size: of.Size, safeSize: of.SafeSize, prefixSample: of.PrefixSample}
		sameBuckets[k] = append(sameBuckets[k], p)
		goneCount++
	}
	if goneCount == 0 {
		return nil
	}

	var added []string
	addedHarness := make(map[string]string)
	for _, np := range sortedKeys(files) {
		if _, seen := oldFiles[np]; seen {
			continue
		}
		nf := files[np]
		if nf.PrefixSample == 0 || nf.SafeSize == 0 {
			continue
		}
		added = append(added, np)
		addedHarness[np] = harnessIn(reg, np)
	}
	if len(added) == 0 {
		return nil
	}

	out := map[string]string{}
	taken := map[string]bool{}
	matchedNew := map[string]bool{}

	// Pass 1: exact same-size matches via O(1) lookup (0 prefix reads).
	for _, np := range added {
		nf := files[np]
		k := renameKey{harness: addedHarness[np], size: nf.Size, safeSize: nf.SafeSize, prefixSample: nf.PrefixSample}
		b := sameBuckets[k]
		for len(b) > 0 && taken[b[0]] {
			b = b[1:]
		}
		if len(b) == 0 {
			continue
		}
		op := b[0]
		sameBuckets[k] = b[1:]
		out[np] = op
		taken[op] = true
		matchedNew[np] = true
	}

	// Pass 2: remaining appendable files that may have grown since moving.
	remGoneByHarness := map[string][]string{}
	for _, b := range sameBuckets {
		for _, op := range b {
			if taken[op] {
				continue
			}
			h := goneHarness[op]
			remGoneByHarness[h] = append(remGoneByHarness[h], op)
		}
	}
	if len(remGoneByHarness) > 0 {
		for h, ops := range remGoneByHarness {
			sort.Strings(ops)
			remGoneByHarness[h] = ops
		}
		for _, np := range added {
			if matchedNew[np] {
				continue
			}
			h := addedHarness[np]
			if !appendableForRename(h, np) {
				continue
			}
			ops := remGoneByHarness[h]
			if len(ops) == 0 {
				continue
			}
			if op := matchGrownRename(np, files[np], ops, oldFiles, taken); op != "" {
				out[np] = op
				taken[op] = true
			}
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func matchGrownRename(np string, nf FileState, ops []string, oldFiles map[string]FileState, taken map[string]bool) string {
	var f *os.File
	var samples map[int64]uint64
	opened := false
	for _, op := range ops {
		if taken[op] {
			continue
		}
		of := oldFiles[op]
		if nf.Size <= of.Size || nf.SafeSize < of.SafeSize {
			continue
		}
		if !opened {
			opened = true
			var err error
			f, err = os.Open(np)
			if err != nil {
				return ""
			}
			defer func() { _ = f.Close() }()
			samples = map[int64]uint64{}
		}
		got, cached := samples[of.SafeSize]
		if !cached {
			got = filePrefixSampleFrom(f, of.SafeSize)
			renamePrefixReads.Add(1)
			samples[of.SafeSize] = got
		}
		if got == of.PrefixSample {
			return op
		}
	}
	return ""
}

func parseRenamedFiles(paths []string) map[string]map[string]model.Session {
	if len(paths) == 0 {
		return nil
	}
	parsed := make([]map[string]model.Session, len(paths))
	workers := min(len(paths), runtime.GOMAXPROCS(0))
	if workers < 1 {
		workers = 1
	}
	if workers == 1 {
		for i, np := range paths {
			if ss, _ := parseAppendedFile("", np, FileState{}, true); len(ss) > 0 {
				m := make(map[string]model.Session, len(ss))
				for _, s := range ss {
					m[s.Key()] = s
				}
				parsed[i] = m
			}
		}
	} else {
		ch := make(chan int, len(paths))
		for i := range paths {
			ch <- i
		}
		close(ch)
		var wg sync.WaitGroup
		wg.Add(workers)
		for range workers {
			go func() {
				defer wg.Done()
				for i := range ch {
					if ss, _ := parseAppendedFile("", paths[i], FileState{}, true); len(ss) > 0 {
						m := make(map[string]model.Session, len(ss))
						for _, s := range ss {
							m[s.Key()] = s
						}
						parsed[i] = m
					}
				}
			}()
		}
		wg.Wait()
	}
	out := make(map[string]map[string]model.Session, len(paths))
	for i, np := range paths {
		if len(parsed[i]) > 0 {
			out[np] = parsed[i]
		}
	}
	return out
}

// applyRenamedFiles moves a manifest from the old names to the new ones in a
// single O(M + N) sweep across Sessions, Empty, and RecordStrings. It returns
// sessions whose Project changed (for sidecar reprojection) and paths whose
// session identity or multi-copy ordering changed (requiring regather).
func applyRenamedFiles(m *Manifest, renamed map[string]string) (map[string]bool, []string) {
	newPaths := sortedKeys(renamed)
	oldToNew := make(map[string]string, len(renamed))
	var toParse []string
	for _, np := range newPaths {
		op := renamed[np]
		if _, ok := m.Files[op]; !ok {
			continue
		}
		oldToNew[op] = np
		if filepath.Dir(np) != filepath.Dir(op) || filepath.Base(np) != filepath.Base(op) {
			toParse = append(toParse, np)
		}
	}
	if len(oldToNew) == 0 {
		return nil, nil
	}

	movedByKey := parseRenamedFiles(toParse)

	for _, np := range newPaths {
		op := renamed[np]
		of, ok := m.Files[op]
		if !ok {
			continue
		}
		of.Path = np
		m.Files[np] = of
		delete(m.Files, op)
		if m.IngestFiles != nil {
			if fi, ok := m.IngestFiles[op]; ok {
				m.IngestFiles[np] = fi
				delete(m.IngestFiles, op)
			}
		}
	}

	reprojected := map[string]bool{}
	regatherMap := map[string]bool{}

	for key, meta := range m.Sessions {
		updated := false
		if np, ok := oldToNew[meta.Path]; ok {
			meta.Path = np
			updated = true
			if moved := movedByKey[np]; len(moved) > 0 {
				if s, ok := moved[key]; ok {
					if s.Project != "" && s.Project != meta.Project {
						meta.Project = s.Project
						reprojected[key] = true
					}
				} else {
					regatherMap[np] = true
				}
			}
		}
		if len(meta.Copies) > 0 {
			copiesChanged := false
			for _, c := range meta.Copies {
				if _, ok := oldToNew[c]; ok {
					copiesChanged = true
					break
				}
			}
			if copiesChanged {
				cp := make([]string, len(meta.Copies))
				for i, c := range meta.Copies {
					if np, ok := oldToNew[c]; ok {
						cp[i] = np
					} else {
						cp[i] = c
					}
				}
				meta.Copies = withCopies(meta.Path, cp)
				updated = true
			}
		}
		if updated {
			m.Sessions[key] = meta
			if renameReorders(sessionFiles(meta), renamed) {
				for _, p := range sessionFiles(meta) {
					if _, ok := renamed[p]; ok {
						regatherMap[p] = true
					}
				}
			}
		}
	}

	// A row set aside follows its file too, or the transcript that takes
	// its id later loses the span a rebuild gives it (#5563).
	for key, meta := range m.Empty {
		np, ok := oldToNew[meta.Path]
		if !ok {
			continue
		}
		meta.Path = np
		if moved := movedByKey[np]; len(moved) > 0 {
			if s, ok := moved[key]; ok {
				if s.Project != "" {
					meta.Project = s.Project
				}
			} else {
				regatherMap[np] = true
			}
		}
		m.Empty[key] = meta
	}

	for i, str := range m.RecordStrings {
		if np, ok := oldToNew[str]; ok {
			m.RecordStrings[i] = np
		}
	}

	var regather []string
	if len(regatherMap) > 0 {
		regather = sortedKeys(regatherMap)
	}
	return reprojected, regather
}

// reprojectFixes updates moved sessions' Project labels in fixpairs without
// re-mining stored records.
func reprojectFixes(dir string, sessions map[string]SessionMeta, keys map[string]bool) {
	if len(keys) == 0 {
		return
	}
	raw, ok := readFixRaw(dir)
	if !ok || len(raw) == 0 {
		return
	}
	dirty := false
	for i, p := range raw {
		if keys[p.Key] {
			if meta, ok := sessions[p.Key]; ok && p.Project != meta.Project {
				raw[i].Project, dirty = meta.Project, true
			}
		}
	}
	if dirty {
		writeFixTables(dir, raw)
	}
}

// reprojectSidecars files the tables a session feeds under the project a
// rename moved it to without reading records from disk.
func reprojectSidecars(dir string, m Manifest, keys map[string]bool) {
	if m.SearchOnly || BuiltSearchOnly(dir) {
		return
	}
	reprojectFixes(dir, m.Sessions, keys)
	reprojectCommandFails(dir, m)
	buildCommandTables(dir, dir, m, len(m.DeadRecords), nil)
}
