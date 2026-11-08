package index

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
		lastIngestFiles.Store(0)
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
	for _, name := range []string{fixesFile, fixRawFile, commandsFile, sessionFactsFile, commandFailsFile, cooccurFile} {
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
