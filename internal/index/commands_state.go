package index

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The command table and the session facts are mined from every command the
// record log holds, and each pass that rewrote a session walked the whole log
// for them twice: 0.6 s of a pass that replaced 20 of 40,000 sessions, and
// more where the log was not in the page cache. Each session's part of the
// command table is kept between passes beside it, as the command-failure walk
// keeps its own (#4288), and a pass reads again only the sessions whose
// commands it changed. The facts are per session already.

const (
	commandsStateFile = "commands-state.bin"
)

// commandsStateMagic changes with what the state means; a state of another
// one is walked again rather than read.
var commandsStateMagic = [8]byte{'D', 'J', 'C', 'M', 'D', 'S', '0', '3'}

func commandsStatePath(dir string) string { return filepath.Join(dir, commandsStateFile) }

// commandsState is each session's commands as the log held them at Size.
type commandsState struct {
	// Generation and Size say which log and how much of it; Dead is how many
	// dead records its manifest listed. A pass that kills records names their
	// sessions, and anything else that kills one leaves the count behind.
	Generation string
	Size       int64
	Dead       int
	// Facts is the size of the session facts file written beside it, -1
	// for none: the facts are carried with the state, and a file another
	// writer left is read from the log again.
	Facts    int64
	Sessions map[string]*commandsSession
}

// factsStamp is the size of dir's session facts file, -1 when there is none.
func factsStamp(dir string) int64 {
	fi, err := os.Stat(sessionFactsPath(dir))
	if err != nil {
		return -1
	}
	return fi.Size()
}

// commandsSession is one session's part of the command table.
type commandsSession struct {
	// Paths are the files its command records name, as ids in the log's
	// string table, which a followed rename rewrites: a record from another
	// file than the row's is two transcripts sharing an id, which the table
	// does not attribute (see buildCommandsFromIndex).
	Paths []uint64
	// Runs[i] is what Paths[i] ran, by normalised command.
	Runs []map[string]*commandsRun
}

// commandsRun is one command in one session: the text and log offset of its
// first run there, how many runs, and the newest.
type commandsRun struct {
	Cmd   string
	First int64
	Runs  int
	Last  time.Time
}

func newCommandsState() *commandsState {
	return &commandsState{Sessions: map[string]*commandsSession{}}
}

// add takes one command record at off, in log order.
func (st *commandsState) add(key string, path uint64, text string, at time.Time, off int64) {
	s := st.Sessions[key]
	if s == nil {
		s = &commandsSession{}
		st.Sessions[key] = s
	}
	i := slices.Index(s.Paths, path)
	if i < 0 {
		i = len(s.Paths)
		s.Paths = append(s.Paths, path)
		s.Runs = append(s.Runs, map[string]*commandsRun{})
	}
	cmd := withoutExitStatus(strings.TrimSpace(firstTextLine(text)))
	if cmd == "" || len(cmd) > commandTextMax {
		return
	}
	low := normalizeCommand(cmd)
	if low == "" {
		return
	}
	r := s.Runs[i][low]
	if r == nil {
		r = &commandsRun{Cmd: cmd, First: off}
		s.Runs[i][low] = r
	}
	r.Runs++
	if at.After(r.Last) {
		r.Last = at
	}
}

// table is the command table of the sessions in m, the one a walk of the
// log gives.
func (st *commandsState) table(m Manifest) (out []CommandUse) {
	type acc struct {
		use       CommandUse
		first     int64
		sessions  int
		byProject map[string]*projAcc
	}
	by := map[string]*acc{}
	t := tablesFromManifest(m)
	for key, s := range st.Sessions {
		meta, ok := m.Sessions[key]
		if !ok {
			continue
		}
		runs := s.Runs[0]
		if len(s.Paths) > 1 || foreignPath(t.lookup(s.Paths[0]), meta) {
			runs = map[string]*commandsRun{}
			for i, id := range s.Paths {
				if foreignPath(t.lookup(id), meta) {
					continue
				}
				for low, r := range s.Runs[i] {
					o := runs[low]
					if o == nil {
						c := *r
						runs[low] = &c
						continue
					}
					if r.First < o.First {
						o.Cmd, o.First = r.Cmd, r.First
					}
					o.Runs += r.Runs
					if r.Last.After(o.Last) {
						o.Last = r.Last
					}
				}
			}
		}
		for low, r := range runs {
			a := by[low]
			if a == nil {
				a = &acc{use: CommandUse{Command: r.Cmd}, first: r.First, byProject: map[string]*projAcc{}}
				by[low] = a
			} else if r.First < a.first {
				a.use.Command, a.first = r.Cmd, r.First
			}
			a.use.Runs += r.Runs
			a.sessions++
			pa := a.byProject[meta.Project]
			if pa == nil {
				pa = &projAcc{sessions: map[string]bool{}}
				a.byProject[meta.Project] = pa
			}
			pa.sessions[key] = true
			if newerRun(r.Last, key, pa) {
				pa.last, pa.lastKey = r.Last, key
			}
			if r.Last.After(a.use.Last) {
				a.use.Last = r.Last
			}
		}
	}
	for _, a := range by {
		if a.sessions < commandMinSessions {
			continue
		}
		a.use.Sessions = a.sessions
		a.use.ByProject = cappedProjects(a.byProject)
		out = append(out, a.use)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Command < out[j].Command
	})
	if len(out) > commandsMax {
		out = out[:commandsMax]
	}
	return out
}

// foreignPath reports whether a command record from the file p belongs to
// another transcript than meta's row: two transcripts in two directories
// sharing an id (#699). The row names the winner's project, and filing the
// other copy's commands under it is the leak ByProject exists to prevent, so
// the table leaves them out until a full build reads each copy as its own
// session. One conversation in two stores, as goose's migration left it, is
// one row of one project and not shared.
func foreignPath(p string, meta SessionMeta) bool {
	return meta.Shared && p != "" && meta.Path != "" && p != meta.Path && filepath.Dir(p) != filepath.Dir(meta.Path)
}

// commandRecordsMined counts the command records the command tables read,
// for the tests that keep a small pass from reading every one.
var commandRecordsMined atomic.Int64

// buildCommandTables writes the command table, the session facts and the
// state behind them for the index in tmp, whose manifest is m. The state dir's
// index left is taken when it describes tmp's log but for the sessions in
// redo, the ones whose command records this pass changed; priorDead is how
// many dead records dir's manifest listed. Without one, every command in the
// log is read.
func buildCommandTables(dir, tmp string, m Manifest, priorDead int, redo map[string]bool) {
	log := filepath.Join(tmp, "records.bin")
	fi, err := os.Stat(log)
	if err != nil {
		return
	}
	size := fi.Size()
	t := tablesFromManifest(m)
	st, facts := carriedCommandsState(dir, m, priorDead, size)
	if st != nil {
		redo = commandsTail(log, t, st.Size, size, redo)
		if redo == nil || int64(len(redo)) > int64(len(m.Sessions))/offsetsWalkShare+1 {
			st = nil
		}
	}
	msgs := map[string][]model.Message{}
	take := func(off int64, payload []byte, want func(string) bool) error {
		kid, pid, rid, _, ok := recordPrefix(payload)
		if !ok {
			return errShortRecord
		}
		if t.lookup(rid) != roleCommand || !want(t.lookup(kid)) {
			return nil
		}
		r, err := decodeRecord(payload, t)
		if err != nil {
			return err
		}
		commandRecordsMined.Add(1)
		st.add(r.Key, pid, r.Text, r.Time, off)
		// The facts are the row's copy's, as the table is (see commandSessions).
		if !foreignPath(r.SourcePath, m.Sessions[r.Key]) {
			msgs[r.Key] = append(msgs[r.Key], model.Message{Role: roleCommand, Text: r.Text, Time: r.Time})
		}
		return nil
	}
	if st != nil {
		for key := range redo {
			delete(st.Sessions, key)
			delete(facts, key)
		}
		want := func(key string) bool { return redo[key] }
		ok, err := eachStoredRecordOfKeys(tmp, size, t, redo, want, map[string]bool{roleCommand: true}, func(off int64, payload []byte) error {
			return take(off, payload, want)
		}, func(string, []int64) {})
		if err != nil || !ok {
			st = nil
		}
	}
	if st == nil {
		st, facts, msgs = newCommandsState(), map[string]SessionFact{}, map[string][]model.Message{}
		f, err := openIndexFile(log)
		if err != nil {
			return
		}
		all := func(string) bool { return true }
		err = walkStored(io.LimitReader(f, size), t, 0, func(off int64, payload []byte) error {
			return take(off, payload, all)
		})
		_ = f.Close()
		if err != nil {
			return
		}
	}
	for key := range facts {
		if _, ok := m.Sessions[key]; !ok {
			delete(facts, key)
		}
	}
	for key, ms := range msgs {
		if _, ok := m.Sessions[key]; !ok {
			continue
		}
		// The log holds a session's commands in the order they were written
		// rather than in time order, and the facts keep them most recent first.
		sort.SliceStable(ms, func(i, j int) bool { return ms[i].Time.Before(ms[j].Time) })
		if f, ok := sessionFactOf(ms); ok {
			facts[key] = f
		}
	}
	if out := st.table(m); len(out) == 0 {
		_ = os.Remove(commandsPath(tmp))
	} else {
		_ = writeGobAtomic(commandsPath(tmp), out)
	}
	if len(facts) == 0 {
		_ = os.Remove(sessionFactsPath(tmp))
	} else {
		_ = writeGob(sessionFactsPath(tmp), facts)
	}
	st.Generation, st.Size, st.Dead = m.Generation, size, len(m.DeadRecords)
	st.Facts = factsStamp(tmp)
	_ = writeCommandsStateFile(tmp, st)
}

// carriedCommandsState is the state and the session facts dir's index left,
// when they describe the log a pass over it leaves in tmp up to size: the
// same generation, nothing killed since but by that pass, and no more than
// size of it. A table capped at its bound is not the whole of it.
func carriedCommandsState(dir string, m Manifest, priorDead int, size int64) (*commandsState, map[string]SessionFact) {
	st, ok := readCommandsStateFile(dir)
	if !ok {
		return nil, nil
	}
	if m.Generation == "" || st.Generation != m.Generation || st.Dead != priorDead || st.Size > size {
		return nil, nil
	}
	facts := map[string]SessionFact{}
	if err := readGob(sessionFactsPath(dir), &facts); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if factsStamp(dir) != st.Facts {
		return nil, nil
	}
	return st, facts
}

// commandTablesBehind reports whether a command or an output in dir's log is
// past what the command tables were mined from. A pass that appends one
// commits the log first and the tables after, so one killed in between left
// them stale, and a pass appending none never asked again.
func commandTablesBehind(dir string, m Manifest) bool {
	gen, from, ok := commandsStateHead(dir)
	if !ok || gen != m.Generation {
		return true
	}
	path := filepath.Join(dir, "records.bin")
	fi, err := os.Stat(path)
	if err != nil || from == fi.Size() {
		return false
	}
	// A tail this long is walked by the rebuild that moves the state past
	// it, rather than again on every pass that adds none.
	if from > fi.Size() || fi.Size()-from > commandsTailWalkMax {
		return true
	}
	f, err := openIndexFile(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Seek(from, io.SeekStart); err != nil {
		return false
	}
	t := tablesFromManifest(m)
	errFound := errors.New("found")
	err = walkStored(io.LimitReader(f, fi.Size()-from), t, from, func(_ int64, payload []byte) error {
		_, _, rid, _, ok := recordPrefix(payload)
		if !ok {
			return errShortRecord
		}
		if r := t.lookup(rid); r == roleCommand || r == roleToolOutput {
			return errFound
		}
		return nil
	})
	return errors.Is(err, errFound)
}

// commandsTailWalkMax is how much of the log past the command state a pass
// that appended no command reads to find one.
const commandsTailWalkMax = 4 << 20

// commandsStateHead is the generation and log size the state in dir names,
// read without the rest of it.
func commandsStateHead(dir string) (gen string, size int64, ok bool) {
	f, err := os.Open(commandsStatePath(dir))
	if err != nil {
		return "", 0, false
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, 512)
	n, _ := io.ReadFull(f, b)
	b = b[:n]
	if len(b) < len(commandsStateMagic) || [8]byte(b[:8]) != commandsStateMagic {
		return "", 0, false
	}
	b = b[8:]
	l, k := binary.Uvarint(b)
	if k <= 0 || l > uint64(len(b)-k) { //nolint:gosec // Uvarint used at most len(b) bytes
		return "", 0, false
	}
	gen, b = string(b[k:k+int(l)]), b[k+int(l):] //nolint:gosec // l is checked against len(b) above
	v, k := binary.Uvarint(b)
	if k <= 0 {
		return "", 0, false
	}
	return gen, int64(v), true //nolint:gosec // written from a non-negative size
}

// commandsTail adds to redo the sessions with a command in the log at path
// between from and to, which the state does not cover. nil when the log
// cannot be read there.
func commandsTail(path string, t *recordTables, from, to int64, redo map[string]bool) map[string]bool {
	out := make(map[string]bool, len(redo))
	for key := range redo {
		out[key] = true
	}
	if from >= to {
		return out
	}
	f, err := openIndexFile(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Seek(from, io.SeekStart); err != nil {
		return nil
	}
	err = walkStored(io.LimitReader(f, to-from), t, from, func(_ int64, payload []byte) error {
		kid, _, rid, _, ok := recordPrefix(payload)
		if !ok {
			return errShortRecord
		}
		if t.lookup(rid) == roleCommand {
			out[t.lookup(kid)] = true
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return out
}

// writeCommandsState leaves st, taken from the log a build wrote into tmp,
// for the first pass after it.
func writeCommandsState(tmp string, st *commandsState, m Manifest) {
	fi, err := os.Stat(filepath.Join(tmp, "records.bin"))
	if err != nil || m.Generation == "" {
		return
	}
	st.Generation, st.Size, st.Dead = m.Generation, fi.Size(), len(m.DeadRecords)
	st.Facts = factsStamp(tmp)
	_ = writeCommandsStateFile(tmp, st)
}

// writeCommandsStateFile writes st into dir. No fsync, like the offset table:
// it is a cache checked against the log it names, and one that does not
// decode is walked again.
func writeCommandsStateFile(dir string, st *commandsState) error {
	texts := map[string]uint64{}
	var tb []byte
	text := func(s string) uint64 {
		if i, ok := texts[s]; ok {
			return i
		}
		i := uint64(len(texts))
		texts[s] = i
		tb = binary.AppendUvarint(tb, uint64(len(s)))
		tb = append(tb, s...)
		return i
	}
	var body []byte
	str := func(s string) {
		body = binary.AppendUvarint(body, uint64(len(s)))
		body = append(body, s...)
	}
	keys := slices.Sorted(maps.Keys(st.Sessions))
	body = binary.AppendUvarint(body, uint64(len(keys)))
	for _, key := range keys {
		s := st.Sessions[key]
		str(key)
		body = binary.AppendUvarint(body, uint64(len(s.Paths)))
		for i, id := range s.Paths {
			body = binary.AppendUvarint(body, id)
			lows := slices.Sorted(maps.Keys(s.Runs[i]))
			body = binary.AppendUvarint(body, uint64(len(lows)))
			for _, low := range lows {
				r := s.Runs[i][low]
				body = binary.AppendUvarint(body, text(low))
				body = binary.AppendUvarint(body, text(r.Cmd))
				body = binary.AppendUvarint(body, uint64(r.First)) //nolint:gosec // record offsets are non-negative
				body = binary.AppendUvarint(body, uint64(r.Runs))  //nolint:gosec // run counts are non-negative
				body = binary.AppendVarint(body, recordNanos(r.Last))
			}
		}
	}
	out := append([]byte{}, commandsStateMagic[:]...)
	out = binary.AppendUvarint(out, uint64(len(st.Generation)))
	out = append(out, st.Generation...)
	out = binary.AppendUvarint(out, uint64(st.Size)) //nolint:gosec // file sizes are non-negative
	out = binary.AppendUvarint(out, uint64(st.Dead)) //nolint:gosec // dead counts are non-negative
	out = binary.AppendVarint(out, st.Facts)
	out = binary.AppendUvarint(out, uint64(len(texts)))
	out = append(out, tb...)
	return writeFileAtomic(commandsStatePath(dir), append(out, body...))
}

// readCommandsStateFile is the state in dir, false when there is none this
// build can read.
func readCommandsStateFile(dir string) (*commandsState, bool) {
	b, err := os.ReadFile(commandsStatePath(dir))
	if err != nil || len(b) < len(commandsStateMagic) || [8]byte(b[:8]) != commandsStateMagic {
		return nil, false
	}
	b = b[8:]
	bad := false
	num := func() uint64 {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			bad = true
			return 0
		}
		b = b[n:]
		return v
	}
	str := func() string {
		n := num()
		if bad || n > uint64(len(b)) {
			bad = true
			return ""
		}
		s := string(b[:n])
		b = b[n:]
		return s
	}
	st := newCommandsState()
	st.Generation = str()
	st.Size, st.Dead = int64(num()), int(num()) //nolint:gosec // written from non-negative values
	facts, n := binary.Varint(b)
	if bad || n <= 0 {
		return nil, false
	}
	b = b[n:]
	st.Facts = facts
	nt := num()
	if bad || nt > uint64(len(b)) {
		return nil, false
	}
	texts := make([]string, nt)
	for i := range texts {
		texts[i] = str()
	}
	textAt := func() string {
		i := num()
		if bad || i >= uint64(len(texts)) {
			bad = true
			return ""
		}
		return texts[i]
	}
	ns := num()
	for i := uint64(0); i < ns && !bad; i++ {
		key := str()
		s := &commandsSession{}
		np := num()
		for j := uint64(0); j < np && !bad; j++ {
			s.Paths = append(s.Paths, num())
			runs := map[string]*commandsRun{}
			s.Runs = append(s.Runs, runs)
			nr := num()
			for k := uint64(0); k < nr && !bad; k++ {
				low, cmd := textAt(), textAt()
				r := &commandsRun{Cmd: cmd, First: int64(num()), Runs: int(num())} //nolint:gosec // written from non-negative values
				ns, n := binary.Varint(b)
				if n <= 0 {
					bad = true
					break
				}
				b = b[n:]
				if ns != zeroTimeUnixNano {
					r.Last = time.Unix(0, ns)
				}
				runs[low] = r
			}
		}
		if len(s.Paths) == 0 {
			bad = true
		}
		st.Sessions[key] = s
	}
	if bad || len(b) != 0 {
		return nil, false
	}
	return st, true
}
