package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func updateIndex(dir, harness, scope string, files map[string]FileState, force bool, progress io.Writer) error {
	err := updateIndexOnce(dir, harness, scope, files, force, progress)
	// A pass that met a hole in the record log stops there rather than
	// committing what it carried before it, and the store is rebuilt from the
	// sources instead. Carrying on dropped every record after the hole and
	// wrote a manifest that called the loss clean.
	if IsCorrupt(err) {
		if progress != nil {
			fmt.Fprintf(progress, "deja: %s (%v), rebuilding ...\n", damagedOrOutdated(err), err)
		}
		return rebuild(dir, harness, scope, files, progress)
	}
	return err
}

func updateIndexOnce(dir, harness, scope string, files map[string]FileState, force bool, progress io.Writer) error {
	return updateIndexPass(dir, harness, scope, files, force, true, progress)
}

// updateIndexPass is one pass. split lets it append what it can and hand the
// rest to a second pass; that second pass replaces without splitting again, so
// a file the append path keeps retrying cannot loop it.
func updateIndexPass(dir, harness, scope string, files map[string]FileState, force, split bool, progress io.Writer) error {
	// The walk as it came in: the pass below writes the kept files into files,
	// and a second pass handed that map would take them for files put back.
	var walked map[string]FileState
	if split {
		walked = maps.Clone(files)
	}
	defer readTo(files)()
	defer sources.PinZstd()()
	defer walkedThisPass(files)()
	// Cleared here rather than beside the other two: this build counts what
	// went away further down, before the incremental paths reset theirs, so a
	// reset down there would zero the number this build is about to report
	// (#1861).
	evicted.Store(0)
	beginPass()
	passesRunning.Add(1)
	defer func() {
		if passesRunning.Add(-1) == 0 {
			passSchemaDBs.Store(nil)
		}
	}()
	old, err := takeHandedManifest(dir)
	if err == nil && !recordsIntact(dir, old) {
		force = true // records.bin lost its tail to a crash; only a rebuild is safe
	}
	if err == nil && miningChanged(old) {
		force = true // search-only turned on or off: the sidecars go or come back whole
	}
	if err == nil && !force && newerIndex(old) {
		sayNewerIndex(progress, dir, old)
		return nil
	}
	// A transcript under a new name is not a new transcript. Settled before the
	// diff below, so the file is neither read again nor left behind as a row
	// pointing at a path that is gone: twenty renames of one 4 KB log left the
	// store holding twenty-one copies of it and twenty dead rows, with search
	// answering once and nothing on any screen saying so (#3546).
	// A renamed file another transcript shares a session with is read again
	// with its copies when the rename moves it past another of the session's
	// files: the row's owner is the smallest path, and a rebuild reads them in
	// path order. Followed by name alone, the row kept the moved file as its
	// owner where a rebuild hands it to a copy. A rename that keeps the order
	// changes nothing a rebuild would, and reading the session again sent it
	// down the replacing path: 20 s for 20 renames at 40,000 sessions (#5466).
	var regather []string
	if err == nil && !force {
		regather = followRenames(dir, &old, files, progress)
	}
	// The format is checked beside the version because the two answer different
	// questions: a store whose content rules are stale still reads, a store
	// whose layout this build cannot read does not. Every format bump so far
	// carried a version bump with it, so this has never fired — and that is the
	// assumption it stops the next one from depending on.
	if force || err != nil || old.Version != version || old.Format != onDiskFormat || old.Scope != scope {
		sayFullPass(progress, dir, old, err, force)
		return rebuild(dir, harness, scope, files, progress)
	}
	changed, removed, touched := diffWalk(files, old.Files)
	// A store on a disk that is not mounted looks exactly like a store whose
	// files were deleted, and the sessions it held leave the index — after
	// which every surface reads "no agent history was found on this machine".
	// The files are still there, on a disk that is not, and reconnecting it
	// restores them; that is what this says (#900).
	//
	// Saying it once was not enough: the eviction still happened, so from the
	// next run on there was nothing left to compare against and the warning
	// stopped too. Records that came off a mount point stay in the index while
	// the volume is away, and the line repeats until it is back.
	gone := goneStores(removed, files, old.Files)
	kept, unseen, newlyKept, dated := keepRemoved(removed, changed, files, old)
	old.KeptDated = true
	// Counted after the keep-backs above: records that came off an unmounted
	// volume, or out of a file the client cleaned up, are still in the index,
	// so they did not go away.
	evicted.Add(int64(len(removed)))
	sayGone(progress, gone)
	rewritten := rewrittenInPlace(kept, changed, files, old.Files, old.Sessions)
	// A transcript deleted this pass is handed over the way a rebuild hands
	// it over, which only the replacement pass can do: the row it owned beside
	// other copies is taken again from them and from its records, and a file
	// arriving in the same pass is paired with it by the rename rule there
	// rather than taking a dead path's row in the append path (#5380, #1086).
	// A file new to the index is the only one that can be the deleted one
	// moved; a live transcript growing beside an unrelated deletion still
	// appends.
	sharedBy := keysByFile(old.Sessions)
	handover := handsOver(changed, files, old.Files, newlyKept, sharedBy, regather)
	if len(changed) == 0 && len(removed) == 0 && !handover {
		sayKept(progress, kept, unseen)
		lastIngestFiles = 0
		return writeQuietPass(dir, old, files, newlyKept, touched, dated)
	}
	takenFrom := sharedTakeovers(dir, harness, old, changed, files)
	if len(removed) == 0 && !rewritten && !handover && !promptLogTakeover(changed, old.Files, old.Sessions) && takenFrom == nil {
		if canAppendIncremental(changed, old.Files) {
			return appendPass(dir, harness, scope, old, files, changed, true, progress, kept, unseen)
		}
		// Decided per file: the appendable ones are appended, and the pass
		// after it, over the same walk, replaces only the rest (#5324).
		if appendable, rest := splitAppendable(changed, old.Files, old.Sessions); split && len(appendable) > 0 && len(rest) > 0 {
			if passErr := appendPass(dir, harness, scope, old, withoutRest(files, rest, old.Files), appendable, false, progress, kept, unseen); passErr != nil {
				return passErr
			}
			return updateIndexPass(dir, harness, scope, walked, false, false, progress)
		}
	}
	rp := &replacePass{dir: dir, harness: harness, scope: scope, progress: progress, old: old, files: files, changed: changed,
		removed: removed, kept: kept, unseen: unseen, newlyKept: newlyKept, sharedBy: sharedBy}
	return rp.run(takenFrom)
}

// followRenames points the index at transcripts that moved under a new name,
// and returns the renamed files that have to be read again with their copies.
func followRenames(dir string, old *Manifest, files map[string]FileState, progress io.Writer) []string {
	var regather []string
	if pairs := detectRenamedFiles(old.Files, files); len(pairs) > 0 {
		reprojected := applyRenamedFiles(old, pairs)
		heldBy := keysByFile(old.Sessions)
		for np := range pairs {
			for _, key := range heldBy[np] {
				if renameReorders(sessionFiles(old.Sessions[key]), pairs) {
					regather = append(regather, np)
					break
				}
			}
		}
		// Failing to record it costs the duplicate this exists to avoid,
		// not correctness: the pass below then treats the file as new.
		// A rename read again is recorded by the pass that reads it, which
		// writes the manifest anyway; one that fails leaves the rename to
		// be found again.
		var werr error
		if len(regather) == 0 || len(reprojected) > 0 {
			werr = writeManifestMeta(dir, *old)
		}
		if werr == nil && len(reprojected) > 0 {
			reprojectSidecars(dir, old.Sessions, reprojected)
		}
		if werr == nil && progress != nil {
			fmt.Fprintf(progress, "deja: %d transcript%s renamed — the index followed the new name\n", len(pairs), pluralS(len(pairs)))
		}
	}
	return regather
}

// sayFullPass says why a pass reads every source again.
func sayFullPass(progress io.Writer, dir string, old Manifest, err error, force bool) {
	if progress != nil {
		// A store built by an earlier release re-reads its sources whole,
		// which on a large one is the longest deja ever makes anyone wait
		// — and it said only "indexing sessions", the same line a first
		// install prints. Every other reason for a full pass names itself:
		// damage says it is damage, a changed exclude list says so, and
		// `--rebuild` was asked for (#3500).
		if err == nil && !force && old.Version < version && old.Version != 0 {
			fmt.Fprintf(progress, "deja: this build reads a newer index than the one on disk (%d, was %d) — re-reading your sources once\n", version, old.Version)
		}
		if !hasProgressSink() {
			fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", displayPath(dir))
		}
	}
}

// diffWalk sets the walk against the files the index holds: the ones to read,
// the ones gone, and the ones only touched.
func diffWalk(files, oldFiles map[string]FileState) (map[string]FileState, map[string]bool, map[string]bool) {
	changed := map[string]FileState{}
	removed := map[string]bool{}
	for p, f := range files {
		if of, ok := oldFiles[p]; !ok || !sameFile(of, f) || of.Kept { // a kept one put back is read again
			changed[p] = f
		}
	}
	// Recorded with its new mtime through files, never read (#5324).
	touched := dropTouched(changed, files, oldFiles)
	for p := range oldFiles {
		if p == syncImportPath {
			continue
		}
		if _, ok := files[p]; !ok {
			removed[p] = true
		}
	}
	return changed, removed, touched
}

// goneStores puts back in files what an unmounted or renamed volume held, and
// returns the trees that went away whole.
func goneStores(removed map[string]bool, files, oldFiles map[string]FileState) []missingTree {
	gone := missingTrees(removed)
	for i := range gone {
		gone[i].renamed = renamedMount(gone[i].dir)
		if !gone[i].mount && gone[i].renamed == "" {
			continue
		}
		for p := range removed {
			if !strings.HasPrefix(p, gone[i].dir+string(filepath.Separator)) {
				continue
			}
			if of, ok := oldFiles[p]; ok {
				files[p] = of
				delete(removed, p)
			}
		}
	}
	// A file that is gone while the directory around it is still there is
	// not a store that went away: it is Claude Code's own cleanup, which
	// deletes a transcript once it is older than cleanupPeriodDays (30 by
	// default), or a deletion by hand. Either way the index keeps what it
	// read — outlasting the client's housekeeping is the one thing a memory
	// over transcripts can offer against a 30-day default, and `deja forget`
	// is the deliberate path for a session that must go (#2970). A tree that
	// is gone whole is an uninstall or a move, and is dropped as before.
	// Cursor, Copilot CLI and Kimi keep each session in a directory of its
	// own, so deleting one takes the directory with the file. A missing tree
	// named after a session, under a parent that is still there, is that
	// deletion and not a store that went away (#4195).
	stores := gone[:0]
	for _, g := range gone {
		if g.mount || g.renamed != "" || !deletedSessionDir(g.dir) {
			stores = append(stores, g)
		}
	}
	return stores
}

// keepRemoved takes back out of removed the files the index keeps: ones still
// on disk, behind a directory this run cannot enter, or deleted with sessions
// the index still holds.
func keepRemoved(removed map[string]bool, changed, files map[string]FileState, old Manifest) (map[string]bool, map[string]bool, map[string]bool, bool) {
	// A session directory that turns up under another parent in the same pass
	// moved rather than went: its id is its name. Keeping the old copy then
	// made the session its own second copy, and the append path below, which
	// takes over once nothing is removed, has no pairing to catch it.
	arrivedDirs := sessionDirsUnder(changed)
	superseded := supersededLogs(removed, files)
	kept := map[string]bool{}
	// Kept while still on disk: a store this run cannot see, so not deleted.
	unseen := map[string]bool{}
	// Kept for the first time in this pass: the mark that lets the next run
	// read the index as fresh has to be written even when nothing else is.
	newlyKept := map[string]bool{}
	var view *listedView
	var held map[string]bool
	// Read before the loop and set after it: what a store written before
	// deja dated deletions lost is undated, and from here on it is dated.
	dated := old.KeptDated
	for p := range removed {
		if superseded[p] {
			continue
		}
		// A file still on disk that the listing left out was not deleted. When
		// this pass read the session it hangs off — a file of the same store
		// one or two directories up — the store was in view and a setting left
		// the file out: DEJA_INCLUDE_SUBAGENTS turned off. It goes, as a
		// rebuild would drop it; the incremental pass kept 32 children and
		// called them "no longer on disk" (#4739). Otherwise its store is
		// outside this run's view — a hook started with a stripped environment
		// (#4738), a root no longer set — and that is no reason to drop the
		// store's sessions, so they stay.
		if _, lstatErr := os.Lstat(p); lstatErr == nil {
			if view == nil {
				view = newListedView(files, old.Sessions)
			}
			if view.leftOutBySetting(p) {
				continue
			}
			if of, ok := old.Files[p]; ok {
				files[p] = of
				delete(removed, p)
				kept[p] = true
				unseen[p] = true
			}
			continue
		} else if os.IsPermission(lstatErr) {
			// Behind a directory this run may not enter: not deleted, and
			// not outside the stores read either. The unreadable line names
			// the directory (#5393).
			if of, ok := old.Files[p]; ok {
				files[p] = of
				delete(removed, p)
			}
			continue
		}
		if !deletedFromLiveStore(p) {
			continue
		}
		if d := goneSessionDir(p); d != "" && arrivedDirs[filepath.Base(d)] {
			continue
		}
		// Kept for the sessions it holds, as a rebuild keeps it for its
		// records: a file whose sessions were all forgotten holds nothing.
		// A copy beside another file's row holds them too, and dropping it
		// took the turns only it had.
		if held == nil {
			held = map[string]bool{}
			for _, meta := range old.Sessions {
				for _, q := range sessionFiles(meta) {
					held[q] = true
				}
			}
		}
		if !held[p] {
			continue
		}
		if of, ok := old.Files[p]; ok {
			if !of.Kept {
				newlyKept[p] = true
				if dated {
					of.KeptAt = time.Now().Unix()
				}
			}
			of.Kept = true
			files[p] = of
			delete(removed, p)
			kept[p] = true
		}
	}
	return kept, unseen, newlyKept, dated
}

// sayKept says how many removed transcripts the index keeps.
// Said once per pass, and only once the rename filter below has had its
// say — except when nothing else changed, in which case there is no
// rename to filter and the pass returns early.
func sayKept(progress io.Writer, kept, unseen map[string]bool) {
	if progress == nil {
		return
	}
	out := 0
	for p := range kept {
		if unseen[p] {
			out++
		}
	}
	if n := len(kept) - out; n > 0 {
		fmt.Fprintf(progress, "deja: %d transcript%s no longer on disk — still searchable; `deja resume <id> --write-back` puts one back, `deja forget --session <id>` drops one for good\n", n, pluralS(n))
	}
	if out > 0 {
		fmt.Fprintf(progress, "deja: %d transcript%s outside the stores this run reads — still searchable\n", out, pluralS(out))
	}
}

// sayGone names the trees that went away and what the index kept of them.
func sayGone(progress io.Writer, gone []missingTree) {
	if progress != nil {
		for _, g := range gone {
			verb := "is"
			if g.files != 1 {
				verb = "are"
			}
			switch {
			case g.renamed != "":
				fmt.Fprintf(progress, "deja: %s is mounted as %s now — its %d indexed file%s %s still searchable; point deja at the new path\n",
					g.dir, g.renamed, g.files, pluralFiles(g.files), verb)
			case g.mount:
				fmt.Fprintf(progress, "deja: %s is not mounted — its %d indexed file%s %s still searchable; reconnect the disk to pick up anything new\n",
					g.dir, g.files, pluralFiles(g.files), verb)
			default:
				fmt.Fprintf(progress, "deja: %s is gone, and %d indexed file%s with it — if that disk is simply not mounted, reconnect it and run `deja index`\n",
					g.dir, g.files, pluralFiles(g.files))
			}
		}
	}
}

// handsOver reports whether a transcript deleted this pass has its rows handed
// over by the replacing path, and adds the renamed files to read again to changed.
func handsOver(changed, files, oldFiles map[string]FileState, newlyKept map[string]bool, sharedBy map[string][]string, regather []string) bool {
	arrived := false
	for p := range changed {
		if _, ok := oldFiles[p]; !ok {
			arrived = true
			break
		}
	}
	handover := false
	for p := range newlyKept {
		if arrived || len(sharedBy[p]) > 0 {
			handover = true
			break
		}
	}
	for _, p := range regather {
		if f, ok := files[p]; ok {
			changed[p] = f
			handover = true
		}
	}
	return handover
}

// writeQuietPass records what a pass with nothing to read still has to.
func writeQuietPass(dir string, old Manifest, files map[string]FileState, newlyKept, touched map[string]bool, dated bool) error {
	// Nothing to read, but the kept mark has to land, or the next run
	// takes the same file for a fresh deletion.
	if len(newlyKept) > 0 || !dated || len(touched) > 0 || !unreadableCurrent(old, files) {
		for p := range newlyKept {
			old.Files[p] = files[p]
		}
		for p := range touched {
			old.Files[p] = files[p]
		}
		if !unreadableCurrent(old, files) {
			mergeIngestDiag(&old)
		}
		return writeManifestOnly(dir, old)
	}
	return nil
}

// appendPass appends changed to the index in place, or rebuilds it when the log
// turns out damaged. last says this is the pass's last step.
func appendPass(dir, harness, scope string, old Manifest, files, changed map[string]FileState, last bool, progress io.Writer, kept, unseen map[string]bool) error {
	filesTouched, messages, unreadable, appendErr := appendIncremental(dir, harness, scope, old, files, changed)
	if IsCorrupt(appendErr) {
		if progress != nil {
			fmt.Fprintf(progress, "deja: %s (%v), rebuilding ...\n", damagedOrOutdated(appendErr), appendErr)
		}
		return rebuild(dir, harness, scope, files, progress)
	}
	if appendErr != nil {
		return fmt.Errorf("append: %w", appendErr)
	}
	if last {
		sayKept(progress, kept, unseen)
	}
	if progress != nil {
		line := fmt.Sprintf("deja: updated %d file%s (%d new message%s)", filesTouched, pluralS(filesTouched), messages, pluralS(messages))
		// After the first build every run a person sees is this one, so a
		// clause that lives only in the full pass is a clause nobody reads
		// (#2007). Same counters, summed across the stores this pass
		// touched — the line is about the pass rather than one store.
		if unreadable > 0 {
			line += fmt.Sprintf(" — %d line%s skipped, deja could not read %s", unreadable, pluralS(unreadable), pluralThem(unreadable))
		}
		fmt.Fprintln(progress, line)
	}
	return nil
}

// carrySidecars copies the mined sidecars from the live index into the
// incremental build, because the swap replaces the whole directory and this
// path does not mine them again — it deliberately never holds the whole corpus.
//
// Without this, one incremental update deleted all three. `fix` went silent
// outright, since it has no other source; ranking lost its query expansion;
// commands survived only because they have a fallback path. And an incremental
// update is not a rare event — it is what a new session in any store triggers,
// so the sidecars were being destroyed during ordinary use and only came back
// on the next full rebuild.
//
// They are carried, not rebuilt: pairs mined from sessions that arrived in this
// update are missing until a full build, which is the ordinary staleness of a
// derived file and not the same thing as having none.
func carrySidecars(dir, tmp string) {
	for _, name := range []string{fixesFile, fixRawFile, commandsFile, cooccurFile} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		_ = writeIndexFile(filepath.Join(tmp, name), b)
	}
}

// appendableKind reports whether a kind can be read from where the last pass
// stopped. Named by kind rather than by path so the guard and the test that
// keeps it honest ask the same question.
func appendableKind(kind string) bool {
	if kind == "" {
		return false
	}
	resumableKindsOnce.Do(func() {
		resumableKinds = map[string]bool{}
		for _, k := range sources.KindsWithOffsetParsers() {
			resumableKinds[k] = true
		}
	})
	return resumableKinds[kind]
}

// The registry is fixed for the life of the process, and this is asked once
// per changed file: walking it each time cost 4.3 KB of garbage a file.
var (
	resumableKinds     map[string]bool
	resumableKindsOnce sync.Once
)

// inlineAppendMax is how many new bytes a search will read before answering.
// Eight megabytes is about a second of parsing on the machine #3021 was
// measured on, and larger than any ordinary turn: the sessions that pass it
// are the ones being written while the question is asked. A variable so a test
// can put the boundary where its fixtures are.
var inlineAppendMax int64 = 8 << 20

// appendTailBytes is how much has been added to the files an append would
// read, which is what the reader waits on.
func appendTailBytes(changed map[string]FileState, old map[string]FileState) int64 {
	var n int64
	for p, f := range changed {
		of, ok := old[p]
		if !ok {
			// A file deja has not read before is new from its first byte, and
			// the append path reads all of it.
			n += f.Size
			continue
		}
		if f.Size > of.Size {
			n += f.Size - of.Size
		}
	}
	return n
}

func canAppendIncremental(changed map[string]FileState, old map[string]FileState) bool {
	if len(changed) == 0 {
		return false
	}
	for p, f := range changed {
		of, ok := old[p]
		if !ok {
			// A transcript deja has never read is new from its first byte, which
			// is what this path writes: parse from offset zero, every session in
			// it new, old records untouched. Refusing it sent every new
			// conversation — each one is a new file — down the replacement path,
			// which rewrites and re-tokenizes the whole store: 4.76s against
			// 0.30s on a 171 MB index, and growing with the store rather than
			// with the file (#3500).
			//
			// Whether the kind can *resume* a parse does not matter here, only
			// whether deja can parse it at all: a new file is read from its
			// first byte either way. Requiring an offset parser refused the
			// whole batch over one such file, and an inline caller hands
			// rewrite-grade work to a detached warmup rather than doing it —
			// so the live index on the machine this was found on had never
			// read 1,013 of the files its own loaders list: five senpi
			// transcripts, five Copilot Chat ones, and a thousand opencode
			// session diffs, a kind that gains a file per session (#3747).
			if _, ok := kindForPath(p); !ok {
				return false
			}
			// Except a new opencode diff file: it is read as its session, whole,
			// and that session is already in the index (#4207).
			if storeHarness(p) == "opencode" {
				return false
			}
			continue
		}
		if f.Size <= of.Size {
			return false
		}
		// A deleted transcript put back is read whole: the row held for it
		// while it was gone kept the span of copies that have left since, and
		// only a read from the start gives the row a rebuild gives (#5380).
		if of.Kept {
			return false
		}
		// A store that hands back touched sessions whole has nothing to append:
		// adding them to what the index holds is the doubling the replacement
		// path exists to avoid. Defensive — sqlite rewrites its page count in
		// the header on growth, so the prefix check below rarely passes (#4207).
		if rereadsWholeSessions(p) {
			return false
		}
		// A prior pass that saw bytes past the last newline leaves SafeSize
		// short of Size. The readers index a last line that parses even with no
		// newline yet, so resuming from SafeSize read that line a second time
		// once the writer finished it; and a torn first line with SafeSize 0
		// would be re-read mid-line. Either way the tail is ambiguous, so these
		// files go through the full re-index path instead (#appendloss).
		if of.SafeSize < of.Size {
			return false
		}
		// Growth is not proof that the earlier bytes are untouched: a rewind
		// that truncates and regrows past the old length looks exactly like
		// an append, and appending onto it leaves the rewritten prefix in the
		// index with its old text. Compare the prefix before trusting it.
		switch {
		case of.PrefixSample != 0:
			if filePrefixSample(p, of.SafeSize) != of.PrefixSample {
				return false
			}
		case of.PrefixHash != 0:
			// A manifest from before the sample existed. Verified the old way
			// this once; the walk above has already recorded a sample, so the
			// next call is bounded.
			if filePrefixHash(p, of.SafeSize) != of.PrefixHash {
				return false
			}
		}
		// Whether the kind can resume a parse, not whether its name is on a
		// list: the list grew one harness at a time and six kinds that declare
		// an offset parser were never added, so their files were re-read whole
		// on every pass (#2870).
		if !appendableKind(harnessForPath(p)) {
			return false
		}
		// The kind can resume, but this tail may rewrite a record already
		// stored: a Cherry Studio reply still streaming when the last pass ran
		// (#4346).
		if k, ok := kindForPath(p); ok && k.Resumes != nil && !k.Resumes(p, resumeOffset(of)) {
			return false
		}
	}
	return true
}

// readTo holds this pass's transcript reads to the sizes its walk recorded,
// which is where the next pass resumes. A line the client wrote after the walk
// was read here and again there, and the copies stayed until a rebuild
// (#4442).
func readTo(files map[string]FileState) func() {
	ends := make(map[string]int64, len(files))
	for p, f := range files {
		if strings.HasSuffix(p, ".jsonl") {
			ends[p] = f.Size
		}
	}
	return sources.LimitReads(ends)
}

// errChangedWhileRead is a file rewritten after the walk stated it: what the
// read found is not what the state the pass would record describes.
var errChangedWhileRead = errors.New("changed while deja read it")

// readAsWalked reports whether p, just read, still holds what f, its walk
// state, describes. A transcript rewritten after the walk was read as it is
// now and recorded as it was, and the pass after it could not tell what it
// held: a rewrite between `deja index`'s check and its pass lost the
// session's commands (#5503). Growth alone is fine: the read stops at the
// walk's size (readTo). A database store is read from its watermark, which
// moves with what was read, not with the walk.
func readAsWalked(p string, f FileState) bool {
	if storeHarness(p) != "" {
		return true
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return false
	}
	if fi.Size() == f.Size && fi.ModTime().UnixNano() == f.MTime {
		return true
	}
	return strings.HasSuffix(p, ".jsonl") && f.PrefixSample != 0 && fi.Size() >= f.Size && filePrefixSample(p, f.SafeSize) == f.PrefixSample
}

// readSettled runs parse for p, read at f, its walk state, and returns what
// it read with the state it was read at. A file rewritten after the walk is
// stated and read again, twice at most, and then left for the next pass.
// again reports a read at a state other than f.
func readSettled(p string, f FileState, parse func() ([]model.Session, error)) (ss []model.Session, at FileState, again bool, err error) {
	for try := 0; ; try++ {
		ss, err = parse()
		if err != nil || readAsWalked(p, f) {
			return ss, f, try > 0, err
		}
		fi, serr := os.Lstat(p)
		if try == 2 || serr != nil || !fi.Mode().IsRegular() {
			return nil, f, false, errChangedWhileRead
		}
		f = walkFileStateIn(sources.Registry(), p, fi, nil)
		if strings.HasSuffix(p, ".jsonl") {
			defer sources.LimitReads(map[string]int64{p: f.Size})() //nolint:gocritic // two at most, each held until the read returns
		}
	}
}

// resumeOffset is where an appended read of a known file starts: the end of
// the last complete line indexed, or the old size when none was recorded.
func resumeOffset(old FileState) int64 {
	if old.SafeSize == 0 || old.SafeSize > old.Size {
		return old.Size
	}
	return old.SafeSize
}

func appendIncremental(dir, harness, scope string, old Manifest, files map[string]FileState, changed map[string]FileState) (filesTouched, messages, unreadable int, err error) {
	// This pass's counts, like the two full paths: an incremental that nobody
	// read between builds otherwise reported its own colliding ids plus the
	// ones before it (#1850).
	emptied.Store(0)
	collisions.Store(nil)
	merged.Store(0)
	lastIngestFiles = len(changed)
	readThisPass(changed)
	// Deliberately not parsedThisPass: this path reads the appended tail, not
	// the file, so what it finds adds to the file's count instead of replacing
	// it. Marking the file re-read dropped every bad line in the part already
	// indexed — which is every live session.
	rf, err := openIndexFileWrite(filepath.Join(dir, "records.bin"), os.O_CREATE|os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return 0, 0, 0, err
	}
	// The log is being APPENDED to, so ids must continue where the existing
	// records left off. Starting a fresh table would hand id 0 to a new
	// string and silently repoint every record that already uses it.
	tbl := tablesFromManifest(old)
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return 0, 0, 0, err
	}
	defer func() { _ = rw.Close() }()
	buckets := bucketPostings{}
	// The same rule the two full builds apply: a message repeated verbatim in
	// one pass is written once. Without it the same rollout held one record or
	// two depending on whether it was appended or rebuilt, so an index compared
	// against a rebuilt copy of itself came out unequal (#3934).
	seenMsgs := msgSeen{}
	// Every session this pass read, for the sidecars at the end.
	var appended []model.Session
	var rowMoved map[string]bool
	// Only what this pass adds: it goes into an overlay beside each bucket
	// rather than into the bucket written whole again (#5387).
	loadBucket := func(tok string) map[string][]posting {
		b := bucket(tok)
		data, ok := buckets[b]
		if !ok {
			data = map[string][]posting{}
			buckets[b] = data
		}
		return data
	}
	m := old
	m.Version = version
	m.Format = onDiskFormat
	m.Scope = scope
	m.BuiltAt = time.Now()
	m.SourcesReadAt = m.BuiltAt
	m.ReadZone = zoneFingerprint()
	m.Files = files
	m.Redacted = 0
	carryRedactions(&m, old, map[string]bool{})
	if m.Sessions == nil {
		m.Sessions = map[string]SessionMeta{}
	}
	m.Empty = keepEmpty(old.Empty, m.Sessions, files)
	// The rows this pass made or took back from Empty, and the ones it wrote a
	// record under: one with none goes back to Empty (#5563).
	emptyBefore, wroteUnder := map[string]bool{}, map[string]bool{}
	// Sorted, not map order: two sessions can claim the same harness:id, and
	// which one wins decided the project a whole conversation was filed under
	// — differently on every run (#698).
	// A file deja has never read is read whole here, not resumed, so its counts
	// start over the way the full paths start them. Leaving it out of the
	// parsed set added this pass's bad lines to a count the file never had.
	newFiles := map[string]FileState{}
	for p, f := range changed {
		if _, ok := old.Files[p]; !ok {
			newFiles[p] = f
		}
	}
	parsedThisPass(newFiles)
	// Prompt logs after the transcripts: a session whose transcript this pass
	// reads is then held from it, and its log line dropped as a rebuild drops
	// it (#5503).
	order := sortedKeys(changed)
	sort.SliceStable(order, func(i, j int) bool { return !isCodexHistory(order[i]) && isCodexHistory(order[j]) })
	for _, p := range order {
		of, known := old.Files[p]
		ss, ok := readAppended(harness, p, of, known, &m, changed)
		if !ok {
			continue
		}
		// Scrub before anything is derived from the text, as the other two paths
		// do. Redacting per message inside the write loop below left the derived
		// fields — the friction signatures especially — hashed from the raw
		// text, so the same session got different signatures depending on which
		// path had touched it last.
		preRedactSessions(&m, ss)
		for i := range ss {
			var from *FileState
			if known {
				from = &of
			}
			dropStoredRepeats(rw, dir, &ss[i], m.Sessions[ss[i].Harness+":"+ss[i].ID], seenMsgs, m.Files, harness, from)
		}
		// Kept for the sidecars below, after redaction so a pair cannot carry a
		// credential the records do not.
		appended = append(appended, ss...)
		filesTouched++
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			meta, moved := appendedRow(&m, key, s, p, of, changed[p], known, emptyBefore)
			// Which copy's commands the tables count follows the row's file
			// (foreignPath): one that moved has its runs mined again.
			if moved {
				if rowMoved == nil {
					rowMoved = map[string]bool{}
				}
				rowMoved[key] = true
			}
			m.Sessions[key] = meta
			for _, msg := range s.Messages {
				// Already redacted (and length-capped) by preRedactSessions above.
				text := msg.Text
				// A message that is nothing but harness plumbing strips to empty
				// (#551). Writing it would store a record with no content and give
				// it a posting.
				if strings.TrimSpace(text) == "" {
					continue
				}
				if seenMsgs.dup(key, msg.Role, msg.Time, text) {
					continue
				}
				off, writeErr := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if writeErr != nil {
					return filesTouched, messages, 0, writeErr
				}
				messages++
				wroteUnder[key] = true
				// Keyed the way a rebuild keys it: only the part that earns
				// postings, its date keys, and the tool bit.
				tool := isToolRole(msg.Role)
				eachIndexKey(tokenizedPart(msg.Role, text), msg.Time, func(tok string) {
					data := loadBucket(tok)
					data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord, Tool: tool})
				})
			}
		}
	}
	if closeErr := rw.Close(); closeErr != nil {
		return filesTouched, messages, 0, closeErr
	}
	for key := range emptyBefore {
		if !wroteUnder[key] {
			setAsideEmpty(&m, key)
		}
	}
	stale, err := addOverlays(filepath.Join(dir, "buckets"), filepath.Join(dir, "buckets"), buckets, m.DeadRecords)
	if err != nil {
		return filesTouched, messages, 0, err
	}
	m.bucketsLeaving = staleCount(stale)
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	// Read before writeManifest: the fold in there drains the counters, and the
	// caller prints its line after this returns (#2007).
	unreadable = totalMalformed()
	// A pass that recorded files is past a first build's early slice; what
	// is still missing is what the next walk finds unread.
	m.PartialOf = 0
	if err := writeManifest(dir, m); err != nil {
		return filesTouched, messages, unreadable, err
	}
	dropStale(stale)
	m.bucketsLeaving = 0
	// The sidecars, from what this pass read and from the records already on
	// file. The replacement path does the same three things after it carries
	// them over; this path used to do none of them, which was survivable while
	// it only ever saw a tail and is not once a new conversation comes through
	// here (#3500). A session whose tail brought a tool record is mined again
	// whole, so a pair that starts before the tail is found; the rest of the
	// table is still earned, and a candidate on file is what a second sighting
	// needs to be promoted.
	if m.SearchOnly {
		return filesTouched, messages, unreadable, nil
	}
	whole, rewhole := wholeSessionsForFixes(dir, m, appended)
	mergeFixes(dir, dir, nil, whole, rewhole)
	grew := map[string]bool{}
	for _, s := range appended {
		grew[s.Harness+":"+s.ID] = true
	}
	updateReceipts(dir, dir, m, grew)
	// The two command tables are mined from every record the index holds, which
	// is a scan of records.bin — 2.9s of an 8.6s pass on a 191 MB store. Nothing
	// this pass added can change them unless it added a command or the output of
	// one, and most appends are speech, so ask first.
	// A pass killed after its commit and before the tables left them behind
	// the log, which the next pass heals whatever it appended.
	if carriesWork(appended) || commandTablesBehind(dir, m) || len(rowMoved) > 0 {
		// From the states the last update left: only the sessions this pass
		// appended to are read (#4288). The command state goes last, so it
		// stays behind until both tables are written.
		buildCommandFailsFromIndex(dir, readCommandFailState(dir), maps.Clone(rowMoved))
		buildCommandTables(dir, dir, m, len(m.DeadRecords), maps.Clone(rowMoved))
	}
	return filesTouched, messages, unreadable, nil
}

// readAppended reads what p holds past the last pass, or whole when the index
// has never read it. A file it cannot take this pass is set back in m.Files to
// what the index held, and ok is false.
func readAppended(harness, p string, of FileState, known bool, m *Manifest, changed map[string]FileState) (ss []model.Session, ok bool) {
	var parseErr error
	if known {
		// Appended to from where the last pass stopped, which a rewrite
		// moves: the next pass decides how to read it.
		ss, parseErr = parseAppendedFile(harness, p, of, false)
		if parseErr == nil && !readAsWalked(p, changed[p]) {
			parseErr = errChangedWhileRead
		}
	} else {
		var f FileState
		ss, f, _, parseErr = readSettled(p, changed[p], func() ([]model.Session, error) { return parseAppendedFile(harness, p, of, true) })
		if parseErr == nil {
			m.Files[p], changed[p] = f, f
		}
	}
	if parseErr != nil {
		if known {
			m.Files[p] = of // retry this file on the next pass
		} else {
			delete(m.Files, p)
		}
		return nil, false
	}
	ss = dropShadowedPrompts(sources.FilterSessions(filterTombstoned(ss)), m.Sessions)
	// A prompt whose transcript has not landed yet is read again next pass,
	// from the same offset, rather than indexed from the log now and taken
	// over by a full rewrite when the transcript arrives.
	if isCodexHistory(p) && waitsForTranscript(ss, m.Sessions, time.Now()) {
		if known {
			m.Files[p] = of
		} else {
			delete(m.Files, p)
		}
		return nil, false
	}
	return ss, true
}

// appendedRow is the row key holds once s, read from p, is folded in, and
// whether the row moved to another copy's file. of is p's state before the
// pass, now its state as walked.
func appendedRow(m *Manifest, key string, s model.Session, p string, of, now FileState, known bool, emptyBefore map[string]bool) (SessionMeta, bool) {
	meta, listed := m.Sessions[key]
	if row, ok := m.Empty[key]; ok && !listed {
		meta = metaWithOrd(row, nextSessionOrd(m.Sessions))
		delete(m.Empty, key)
	}
	if !listed {
		emptyBefore[key] = true
	}
	before := meta
	named := meta.ID != ""
	if !named {
		meta = metaWithOrd(metaForSession(s), nextSessionOrd(m.Sessions))
	}
	if meta.Started.IsZero() || (!s.Started.IsZero() && s.Started.Before(meta.Started)) {
		meta.Started = s.Started
	}
	if s.Updated.After(meta.Updated) {
		meta.Updated = s.Updated
	}
	// A recorded path that is not there cannot own the row, and
	// without this the row kept the dead path and was marked as sharing
	// its id with it (#1086). A rename in one pass no longer reaches
	// here: a file deleted this pass sends the pass to the replacement
	// path, whose rename rule pairs the two. A transcript deleted in an
	// earlier pass and kept is a copy, as a rebuild reads it, and owns
	// the row by the same rule as any other (#5380). This is what is
	// left: a path gone without being kept.
	// Not for an arrival with nothing to index: that is Gemini's resume
	// stub beside a deleted transcript, not the transcript moving (#4213).
	if meta.Path != "" && s.Path != "" && meta.Path != s.Path && holdsText(s) && !m.Files[meta.Path].Kept {
		if _, lstatErr := os.Lstat(meta.Path); lstatErr != nil {
			meta.Path = ""
		}
	}
	owns, collided := claimSession(meta, s)
	// A row with nothing to index that changes hands describes the
	// stub, not the transcript taking it over: folding the transcript
	// in on top counted the stub's preamble too, one more message than
	// a rebuild of the same files gives (#4213). Start the row over
	// from the session that owns it, as the full build does.
	// Only for a file read whole: a known file hands over its tail,
	// which is not the session. The span stays what both files cover,
	// as the full build keeps it.
	// A new file that sorts ahead of the one holding the row takes it
	// the same way: a rebuild derives the row from its owner alone.
	if owns && (meta.NoText || collided) && meta.Path != s.Path && !known {
		// The span the row had before s widened it: s's own span is
		// not one the files beside it cover.
		prev := meta
		if named {
			prev.Started, prev.Updated = before.Started, before.Updated
		}
		meta = ownerRow(prev, s, metaForSession(s), prev.Ord, collided)
		if !prev.Started.IsZero() && (meta.Started.IsZero() || prev.Started.Before(meta.Started)) {
			meta.Started = prev.Started
		}
		if prev.Updated.After(meta.Updated) {
			meta.Updated = prev.Updated
		}
	}
	if !owns {
		meta.SharedStarted, meta.SharedUpdated = widerSpan(meta.SharedStarted, meta.SharedUpdated, s.Started, s.Updated)
	}
	if collided {
		collisions.Add(key)
		meta.Shared = true
	}
	// A tail with no files of its own names only the directory the
	// session started in; the row already holds what the whole file
	// said, which a rebuild also takes.
	if s.Project != "" && s.Project != "-" && owns && (!known || !named || !s.ProjectFromDir) {
		meta.Project = s.Project
	}
	if s.Path != "" && owns {
		meta.Path = s.Path
	}
	retitleAppended(&meta, s, p, of, now, known, named, owns)
	// Only the session that owns this row. The full build writes these
	// fields under the same condition; here the merge ran regardless,
	// so growing the loser of an id collision moved the owner's Words,
	// flipped its GaveUp and put the loser's files in its Touched —
	// the wrong conversation's files surfacing in blame (#1304).
	if owns {
		extendOwnedRow(&meta, s, p, known)
	}
	meta.Copies = withCopies(meta.Path, before.Copies, before.Path, s.Path)
	return meta, named && (before.Shared != meta.Shared || meta.Shared && filepath.Dir(before.Path) != filepath.Dir(meta.Path))
}

// extendOwnedRow folds what s's tail says into the row it owns.
func extendOwnedRow(meta *SessionMeta, s model.Session, p string, known bool) {
	ran := meta.RanCommand
	extendDerived(meta, s.Messages)
	// Folding a turn twice changes nothing, so a store that hands the whole
	// session over again is safe here.
	meta.Rules, meta.Revokes = foldUserRules(meta.Rules, meta.Revokes, s.Typed)
	meta.Planted = foldPlanted(meta.Planted, s.Planted)
	// The session's first command: its line can come from what it
	// said before this tail, which the fold never saw. Once per
	// session, so the whole read is affordable here.
	if !ran && meta.RanCommand && known {
		if whole, ok := wholeSession(p, s); ok {
			meta.Settled = sessionSettled(whole)
		}
	}
	// The bridge record can land in any append, and a later one
	// read from the watermark does not carry it again.
	if s.RemoteID != "" {
		meta.RemoteID = s.RemoteID
	}
}

// retitleAppended names the row from s the way a rebuild would name it.
func retitleAppended(meta *SessionMeta, s model.Session, p string, of, now FileState, known, named, owns bool) {
	// A row named from the agent's words or tool output takes the
	// first question that arrives later, as a rebuild names it. Only
	// the owner names it: a copy's tail renamed the session after its
	// own newest turn (#5358).
	outranked := false
	if owns && s.Title == "" && !titlePlaceholder(meta.Title) {
		t, agent := sessionTitleFrom(s)
		outranked = titleRankOfRow(t, agent) > titleRankOfRow(meta.Title, meta.AgentTitle)
	}
	if owns && (titlePlaceholder(meta.Title) || outranked) {
		// The incremental fallback redacted nothing at all before; keep
		// sessionTitleFrom's correct fromAgent bit and redact before the
		// cut, as the full rebuild does.
		meta.Title, meta.AgentTitle = sessionTitleFrom(s)
		meta.Title, _ = redact.Text(meta.Title)
		meta.Title = truncateTitle(meta.Title, 60)
	} else if owns && s.Title != "" {
		// A title the source authored is a live value, not a one-time
		// naming: a promoted note's carries its state, and the loader
		// rewrites it on every correction. Only the full rebuild took
		// the new one, so `promote --state rejected` left every
		// one-line surface — `deja last`, the digest, the citation the
		// hook hands the agent to say aloud — reading "[accepted]"
		// until an unrelated rebuild happened to run (#R11).
		// The same widening the first naming does, or a session that
		// gets its thin title later — dsh and opencode both retitle
		// after the fact — would keep it until an unrelated rebuild.
		//
		// A thin title is widened from the session's first substantial
		// turn, and a tail does not hold it: one appended turn renamed
		// the session after itself. A row already named from the whole
		// session keeps that name (#4452). Unless the sidecar the title
		// lives in changed too: that is a rename, the row holds the old
		// name, and only the whole session can widen the new one
		// (#4592).
		t, _ := redact.Text(s.Title)
		t = boundSourceTitle(s.Harness, t)
		fromTail := known && named && s.Harness != "deja" && thinTitle(t) && !thinTitle(meta.Title)
		w := widenThinSourceTitle(s, t)
		if fromTail && sidecarChanged(of, now) {
			if whole, ok := wholeSession(p, s); ok {
				w, fromTail = widenThinSourceTitle(whole, t), false
			}
		}
		if !fromTail && w != meta.Title {
			meta.Title = w
			meta.AgentTitle = s.AgentTitle
		}
	}
}

// rereadSharedCopies reads again each held transcript that shares a removed
// one's file name, where that name is the id of a row two transcripts share:
// the filename-derived id two projects can share (#699). A turn both copies
// held was written once, under the copy read first; dropping that copy's
// records by path took the survivor's commands and outputs with them, and the
// row stayed on the deleted file until a rebuild (#4310).
func rereadSharedCopies(removed map[string]bool, changed, files map[string]FileState, sessions map[string]SessionMeta) {
	shared := map[string]bool{}
	for _, meta := range sessions {
		if meta.Shared {
			shared[meta.ID] = true
		}
	}
	names := map[string]bool{}
	for p := range removed {
		base := filepath.Base(p)
		if storeHarness(p) == "" && shared[strings.TrimSuffix(base, filepath.Ext(base))] {
			names[base] = true
		}
	}
	if len(names) == 0 {
		return
	}
	for p, f := range files {
		if _, ok := changed[p]; !ok && !removed[p] && names[filepath.Base(p)] && storeHarness(p) == "" {
			changed[p] = f
		}
	}
}

// rewrittenInPlace reports whether a kept file has another beside it under
// the same name in another form: Codex compressing x.jsonl to x.jsonl.zst,
// Gemini rewriting x.json as x.jsonl. That is the file moving, which only the
// replacement path's rename rule pairs up; the append path wrote the new
// file's records beside the old ones and kept the dead path (#4252). A pass
// that ran between zstd writing the .zst and removing the .jsonl held both,
// the row marked shared; the .zst is read again so the rule can pair them.
func rewrittenInPlace(kept map[string]bool, changed, files, held map[string]FileState, sessions map[string]SessionMeta) bool {
	if len(kept) == 0 {
		return false
	}
	stems := map[string]bool{}
	for p := range kept {
		stems[fileStem(p)] = true
	}
	shared := map[string]bool{}
	for _, meta := range sessions {
		if meta.Shared && kept[meta.Path] {
			shared[fileStem(meta.Path)] = true
		}
	}
	found := false
	for p, f := range files {
		if kept[p] || !stems[fileStem(p)] {
			continue
		}
		if _, ok := changed[p]; ok {
			if _, known := held[p]; !known {
				found = true
			}
		} else if shared[fileStem(p)] {
			changed[p] = f
			found = true
		}
	}
	return found
}

// fileStem is p without its compression suffix and its extension.
func fileStem(p string) string {
	p = strings.TrimSuffix(p, ".zst")
	return strings.TrimSuffix(p, filepath.Ext(p))
}

// carriesWork reports whether any of these sessions holds a command or the
// output of one, which is all the two command tables are mined from.
func carriesWork(ss []model.Session) bool {
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == sources.RoleCommand || m.Role == sources.RoleToolOutput {
				return true
			}
		}
	}
	return false
}

// sidecarChanged reports whether the metadata file read with a transcript
// (Kimi's state.json, say) changed between two walks.
func sidecarChanged(a, b FileState) bool {
	return a.MetadataSize != b.MetadataSize || a.MetadataMTime != b.MetadataMTime
}

// wholeSession reads s's file from its first byte and returns s as the full
// build sees it, for what an appended tail cannot tell.
func wholeSession(p string, s model.Session) (model.Session, bool) {
	ss, err := parseAppendedFile("", p, FileState{}, true)
	if err != nil {
		return model.Session{}, false
	}
	for _, w := range ss {
		if w.Harness == s.Harness && w.ID == s.ID {
			w.Messages = dropRepeatedMessages(w.Messages)
			return w, true
		}
	}
	return model.Session{}, false
}

// lastCompleteLineOffset finds the offset just past the final newline, so an
// append can resume without re-reading or losing a torn tail line. Reads at
// most the last 64KB; a longer unterminated tail falls back to full size.
func lastCompleteLineOffset(p string, size int64) int64 {
	if size == 0 {
		return 0
	}
	f, err := os.Open(p)
	if err != nil {
		return size
	}
	defer func() { _ = f.Close() }()
	// Walk backwards window by window: a torn line longer than one window
	// (a fat tool result caught mid-write) must not fool us into treating
	// the whole file as complete, or its message is lost after completion.
	const window = 64 * 1024
	end := size
	for end > 0 {
		start := max(end-window, 0)
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return size
		}
		for i, b := range slices.Backward(buf) {
			if b == '\n' {
				return start + int64(i) + 1
			}
		}
		end = start
	}
	return 0
}

// indexedText is a message's text as the index stores it: plumbing stripped,
// NFC-canonicalised (this path does not go through redactForIngest, #1098),
// redacted, then cut to maxIndexedText on a rune boundary.
func indexedText(text string) (string, redact.Counts, bool) {
	redacted, counts := redact.Text(nfcfold.Compose(stripSelfRecall(text)))
	if len(redacted) <= maxIndexedText {
		return redacted, counts, false
	}
	cut := maxIndexedText
	for cut > 0 && !utf8.RuneStart(redacted[cut]) {
		cut--
	}
	return redacted[:cut], counts, true
}

// preRedactSessions redacts every message concurrently before the write
// loop. Redaction is regex-heavy and was the serial bottleneck of a cold
// build; the write loop stays sequential (append-only log), but by the time
// it runs every text is already clean. Counters land in the manifest exactly
// as the serial path recorded them.
func preRedactSessions(m *Manifest, ss []model.Session) {
	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := min(runtime.GOMAXPROCS(0), len(ss))
	if workers < 1 {
		return
	}
	for range workers {
		wg.Go(func() {
			for si := range jobs {
				s := &ss[si]
				for mi := range s.Messages {
					redacted, counts, clipped := indexedText(s.Messages[mi].Text)
					if clipped {
						mu.Lock()
						countClipped(m, s.Path, s.ID, 1)
						mu.Unlock()
					}
					s.Messages[mi].Text = redacted
					if n := counts.Total(); n > 0 && m != nil {
						mu.Lock()
						m.Redacted += n
						if m.RedactionRules == nil {
							m.RedactionRules = map[string]int{}
						}
						// The store, as above (#2238).
						h := sources.HarnessForKind(harnessForPath(s.Path))
						if h == "" {
							if _, ok := m.Files[sources.OpencodeDB()]; ok {
								h = "opencode"
							}
						}
						for rule, c := range counts {
							m.RedactionRules[h+":"+rule] += c
						}
						if s.Path != "" && m.Files != nil {
							if fs, ok := m.Files[s.Path]; ok {
								fs.Redactions += n
								m.Files[s.Path] = fs
							}
						}
						mu.Unlock()
					}
				}
				s.Messages = dropRepeatedMessages(s.Messages)
			}
		})
	}
	for si := range ss {
		jobs <- si
	}
	close(jobs)
	wg.Wait()
}

// prefixSampleWindow is how much is read at each end. Large enough that a
// rewrite cannot plausibly reproduce it byte for byte, small enough that the
// cost does not depend on how long the session has been running.
const prefixSampleWindow = 512 << 10

// filePrefixSample fingerprints the first n bytes by reading at most two
// windows of them: the head and the bytes ending at n. n is mixed in, so a
// file that grew and one that was rewritten to the same content at a
// different length do not collide.
func filePrefixSample(path string, n int64) uint64 {
	if n <= 0 {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	_, _ = h.Write(b[:])
	head := min(n, prefixSampleWindow)
	if _, err := io.Copy(h, io.LimitReader(f, head)); err != nil {
		return 0
	}
	if n > head {
		start := max(head, n-prefixSampleWindow)
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return 0
		}
		if _, err := io.Copy(h, io.LimitReader(f, n-start)); err != nil {
			return 0
		}
	}
	return h.Sum64()
}

// filePrefixHash fingerprints the first n bytes of a file. Only used to decide
// whether an append is safe, so a fast non-cryptographic hash is the right
// tool: a collision costs one unnecessary full reparse, never a wrong index.
//
// Kept for manifests written before filePrefixSample: those store a hash of
// every byte, and it takes one more read to verify them before the walk
// records a sample instead.
func filePrefixHash(path string, n int64) uint64 {
	if n <= 0 {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	h := fnv.New64a()
	if _, err := io.Copy(h, io.LimitReader(f, n)); err != nil {
		return 0
	}
	return h.Sum64()
}

// countClipped records messages stored short of the transcript, against the
// file that holds them. The caller holds the lock where one is needed;
// redactForIngest runs single-threaded.
func countClipped(m *Manifest, sourcePath, sessionID string, n int) {
	if m == nil || n == 0 {
		return
	}
	p := sourcePath
	if harnessForPath(p) == "" {
		// opencode sessions carry their project dir as Path; the store on
		// record is the database file.
		if db := sources.OpencodeDB(); db != "" {
			if _, ok := m.Files[db]; ok {
				p = db
			}
		}
	}
	if harnessForPath(p) == "" {
		return
	}
	if m.IngestFiles == nil {
		m.IngestFiles = map[string]FileIngest{}
	}
	e := m.IngestFiles[p]
	e.Clipped += n
	if sessionID != "" {
		if e.ClippedSessions == nil {
			e.ClippedSessions = map[string]int{}
		}
		e.ClippedSessions[sessionID] += n
	}
	m.IngestFiles[p] = e
}
