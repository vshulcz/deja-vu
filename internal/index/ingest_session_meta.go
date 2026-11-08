package index

import (
	"encoding/binary"
	"hash/fnv"
	"hash/maphash"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// indexTextParallel hands the feed a push callback and moves jobs to the
// workers in batches: one channel send per message caused enough scheduler
// wakeups to show up as ~20% of a cold rebuild profile.
func indexTextParallel(feed func(push func(tokenJob)) error) (bucketPostings, error) {
	workers := max(runtime.NumCPU(), 1)
	const batchSize = 512
	jobs := make(chan []tokenJob, workers*4)
	partials := make([]bucketPostings, workers)
	var wg sync.WaitGroup
	for i := range workers {

		partials[i] = bucketPostings{}
		wg.Go(func() {
			for batch := range jobs {
				for _, job := range batch {
					addIndexKeys(partials[i], job.text, job.offset, job.sid, job.when, job.tool)
				}
			}
		})
	}
	batch := make([]tokenJob, 0, batchSize)
	push := func(j tokenJob) {
		batch = append(batch, j)
		if len(batch) == batchSize {
			jobs <- batch
			batch = make([]tokenJob, 0, batchSize)
		}
	}
	err := feed(push)
	if len(batch) > 0 {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()
	if err != nil {
		return nil, err
	}
	merged := bucketPostings{}
	for _, part := range partials {
		for b, toks := range part {
			if merged[b] == nil {
				merged[b] = map[string][]posting{}
			}
			for tok, offsets := range toks {
				merged[b][tok] = append(merged[b][tok], offsets...)
			}
		}
	}
	return merged, nil
}

// dateTokens makes a message findable by when it happened: the month name,
// the year, and year-month land in the postings like ordinary words, so
// "deja \"what did we do in may\"" matches May sessions structurally.
//
// The month is UTC's. A record read back from the store carries the local zone
// and a parsed one mostly UTC, so near a month boundary the incremental pass
// and a rebuild gave the same record different months, and west of UTC-10 the
// two indexes disagreed.
func dateTokens(when time.Time) []string {
	if when.IsZero() {
		return nil
	}
	when = when.UTC()
	return []string{
		"t" + strings.ToLower(when.Month().String()),
		"t" + when.Format("2006"),
		"t" + when.Format("2006-01"),
	}
}

// tokenizedPart is what of a record earns postings. For most records that is
// the whole text; for a replaced span it is only the path on the first line.
// Nobody searches for the body of a span — `deja restore` finds it by path —
// and indexing 1 MB of source code puts every `func` and `return` in it into
// the postings, which cost the median query 0.5 ms for nothing.
//
// A written side is the same shape and the argument is stronger: its body is
// 200,000 lines of hex on this machine, and no query will ever be a hash.
func tokenizedPart(role, text string) string {
	switch role {
	case roleEdit, roleWrote:
		if before, _, ok := strings.Cut(text, "\n"); ok {
			return before
		}
		return text
	case roleToolOutput:
		return signalLines(text)
	}
	return text
}

// signalTail is how much of the end of an unmatched output is kept beside the
// head. Half the head: measured on a 1637-output store it adds 1.6 MB to the
// postings, and the end of a long output is where a failure states itself.
const signalTail = signalFloor / 2

// signalLines keeps the part of a command's output anyone would search for.
//
// A build log is mostly progress: files compiled, tests named, packages
// downloaded. Measured over 147,575 lines of real output, 3% carry an error or
// a warning and they are 7% of the bytes — the rest is noise that nobody
// queries and that doubles the sessions a common word matches. Indexing all of
// it took the commonest word in a 1150-session store from 22 ms to 59 ms.
//
// The full text is still stored and still served: this decides what earns
// postings, exactly as a replaced span stores its body and indexes its path.
func signalLines(text string) string {
	if len(text) < signalFloor {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) / 8)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !signalLine(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		// The line after a verdict is usually what the verdict was about — the
		// assertion, the missing symbol, the path. Keeping the marker and
		// dropping the explanation made a test name unfindable while its
		// "--- FAIL" line was indexed.
		if i+1 < len(lines) && !signalLine(lines[i+1]) {
			b.WriteString(lines[i+1])
			b.WriteByte('\n')
		}
	}
	if b.Len() == 0 {
		// Nothing matched, which is the common case rather than the safety net:
		// a contributor measured 234 of 533 filtered records on their store
		// matching none of the substrings, and found a vendor error — `Model
		// name is not valid`, present verbatim in 8 rollouts — unrecoverable
		// because it sat past the head and "not valid" is not "not found"
		// (#614). The list of substrings is the limit, not its contents, so
		// the fallback keeps both ends: errors cluster at the end of a long
		// output, and the head alone is the least informative part of exactly
		// the records that matched nothing.
		if len(text) > signalFloor {
			// Rune-safe: this text is indexed and served back through recall,
			// so a character split here is a broken byte in an answer (#1319).
			head := text[:runeBoundary(text, signalFloor)]
			if len(text) <= signalFloor+signalTail {
				return text
			}
			// Both ends: the tail is cut from the other side, and a character
			// split there is the same broken byte in the same answer.
			tail := text[len(text)-signalTail:]
			for len(tail) > 0 && !utf8.ValidString(tail) {
				tail = tail[1:]
			}
			return head + "\n" + tail
		}
		return text
	}
	return b.String()
}

// signalFloor is the length below which output is indexed whole. A short
// output is usually the answer to something — a test run, a failed build, a
// one-line error — and filtering it costs recall on exactly the queries this
// data exists to serve. Only the long logs get filtered: measured on a real
// store, outputs above this threshold are 7% of the records and 74% of the
// bytes, and they are where the posting explosion comes from.
const signalFloor = 8192

// runeBoundary is n, or the largest offset below it that does not split a
// character.
func runeBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return n
}

func signalLine(l string) bool {
	for _, p := range []string{"FAIL", "fail", "Error", "error", "panic:", "fatal",
		"Traceback", "not found", "undefined", "cannot", "refused", "denied",
		"timeout", "exit status", "warning", "WARN", "Exception", "No such"} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// eachIndexKey calls fn once per distinct key a record contributes. Both the
// in-memory incremental path and the spilling full build key through a keyer,
// so neither can drift from the other on what a message indexes to. The keys
// are streamed rather than collected: a CJK message produces a bigram per rune
// pair, and building the slice only to walk it once cost a third of the keying
// time on a Han corpus (#492).
func eachIndexKey(text string, when time.Time, fn func(tok string)) {
	var k keyer
	// The three key sources overlap — a word is a token and can be an
	// identifier part as well — and a posting list must not hold the same
	// offset twice.
	seen := make(map[string]bool, 64)
	k.keys(text, when, func(b []byte) {
		if seen[string(b)] {
			return
		}
		tok := string(b)
		seen[tok] = true
		fn(tok)
	})
}

func addIndexKeys(buckets bucketPostings, text string, off int64, sid uint32, when time.Time, tool bool) {
	eachIndexKey(text, when, func(tok string) {
		b := bucket(tok)
		if buckets[b] == nil {
			buckets[b] = map[string][]posting{}
		}
		buckets[b][tok] = append(buckets[b][tok], posting{Off: off, Sid: sid, Tool: tool})
	})
}

func writeBucketsConcurrent(dir string, buckets bucketPostings) error {
	if len(buckets) == 0 {
		return nil
	}
	workers := max(min(runtime.NumCPU(), 8), 1)
	type bucketWrite struct {
		name string
		data map[string][]posting
	}
	jobs := make(chan bucketWrite)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for job := range jobs {
				reportAdvance(1)
				if err := writeBucket(filepath.Join(dir, job.name+".bin"), job.data); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}
		})
	}
	for b, data := range buckets {
		jobs <- bucketWrite{name: b, data: data}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (m msgSeen) dup(key, role string, ts time.Time, text string) bool {
	k := seenKey(key, role, ts, text)
	if m[k] {
		return true
	}
	m[k] = true
	return false
}

// seenK is a message as msgSeen tells it apart: session, role, time and text,
// hashed to 128 bits. The joined string it was, time formatted and hash in
// hex, was an allocation per message and 430 MB live at the peak of a cold
// build over 3.7M messages; this is 16 bytes inline.
type seenK [2]uint64

var seenSeeds = [2]maphash.Seed{maphash.MakeSeed(), maphash.MakeSeed()}

func seenKey(key, role string, ts time.Time, text string) seenK {
	var k seenK
	var stamp [12]byte
	binary.LittleEndian.PutUint64(stamp[:8], uint64(ts.Unix()))       //nolint:gosec // the stamp keeps the bits, it is only hashed
	binary.LittleEndian.PutUint32(stamp[8:], uint32(ts.Nanosecond())) //nolint:gosec // nanoseconds are below 1e9
	for i, seed := range seenSeeds {
		var h maphash.Hash
		h.SetSeed(seed)
		_, _ = h.WriteString(key)
		_ = h.WriteByte(0)
		_, _ = h.WriteString(role)
		_ = h.WriteByte(0)
		_, _ = h.Write(stamp[:])
		_, _ = h.WriteString(text)
		k[i] = h.Sum64()
	}
	return k
}

// repeatKey is a message as the record dedupe sees it: role, time and text.
type repeatKey struct {
	role string
	at   int64
	text uint64
}

func repeatKeyOf(role string, at time.Time, text string) repeatKey {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return repeatKey{role, at.UnixNano(), h.Sum64()}
}

// dropRepeatedMessages drops a message that repeats an earlier one of the
// session verbatim, time included, before anything is derived from it. The
// record log already kept one copy, and the counts and sidecars took both, so
// a line a store wrote twice counted as two turns in a rebuild and as however
// many passes it was split across in an update. A message without a time or
// text is kept: two of those are not evidence of a duplicate.
func dropRepeatedMessages(ms []model.Message) []model.Message {
	var seen map[repeatKey]bool
	out := ms[:0]
	for _, m := range ms {
		if !m.Time.IsZero() && strings.TrimSpace(m.Text) != "" {
			if seen == nil {
				seen = make(map[repeatKey]bool, len(ms))
			}
			k := repeatKeyOf(m.Role, m.Time, m.Text)
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		out = append(out, m)
	}
	return out
}

// dropStoredRepeats is dropRepeatedMessages for a tail: it drops the messages
// of s the index already holds from the same file, and marks every stored
// message of the session in seen so its records are not written twice. Only a
// message no newer than the row can repeat one, so a tail that is all new
// reads nothing.
// files says which copies are kept, and from is where the tail was read from
// in a file the last pass held, nil for one read whole.
func dropStoredRepeats(rw *recordWriter, dir string, s *model.Session, row SessionMeta, seen msgSeen, files map[string]FileState, harness string, from *FileState) {
	if row.ID == "" {
		return
	}
	older := false
	for _, m := range s.Messages {
		if !m.Time.IsZero() && !m.Time.After(row.Updated) && strings.TrimSpace(m.Text) != "" {
			older = true
			break
		}
	}
	if !older || rw.w.Flush() != nil {
		return
	}
	key := s.Key()
	stored := map[repeatKey]bool{}
	elsewhere := map[repeatKey]bool{}
	if err := eachRecordForKeys(filepath.Join(dir, "records.bin"), rw.tables, map[string]bool{key: true}, func(r Record) {
		seen.dup(key, r.Role, r.Time, r.Text)
		if r.SourcePath == s.Path {
			stored[repeatKeyOf(r.Role, r.Time, r.Text)] = true
		} else if files[r.SourcePath].Kept {
			elsewhere[repeatKeyOf(r.Role, r.Time, r.Text)] = true
		}
	}); err != nil {
		return
	}
	if from != nil {
		heldBefore(s, stored, elsewhere, harness, *from)
	}
	out := s.Messages[:0]
	for _, m := range s.Messages {
		if !m.Time.IsZero() && stored[repeatKeyOf(m.Role, m.Time, m.Text)] {
			continue
		}
		out = append(out, m)
	}
	s.Messages = out
}

// heldBefore adds to stored the messages of the tail s the file already held
// before it whose record is filed under a deleted copy of the session: a turn a
// deleted transcript shares with one on disk stays filed under the deleted
// one, so the file's own records do not show the live file held it, and its
// repeat in the tail was counted as a new turn where a rebuild of the file
// counts it once (#5544). Only then is the file read again, whole and from
// where the tail starts, to tell such a repeat from a turn of the other copy
// the file takes up for the first time: what the file holds beyond the tail
// is what it held before. Keyed on the redacted text, as the tail and the
// records are.
func heldBefore(s *model.Session, stored, elsewhere map[repeatKey]bool, harness string, from FileState) {
	want := map[repeatKey]bool{}
	for _, m := range s.Messages {
		if k := repeatKeyOf(m.Role, m.Time, m.Text); elsewhere[k] && !stored[k] && !m.Time.IsZero() {
			want[k] = true
		}
	}
	if len(want) == 0 {
		return
	}
	count := func(ss []model.Session, sign int, into map[repeatKey]int) {
		for _, w := range ss {
			if w.Harness != s.Harness || w.ID != s.ID {
				continue
			}
			for _, m := range w.Messages {
				text, _, _ := indexedText(m.Text)
				if k := repeatKeyOf(m.Role, m.Time, text); want[k] {
					into[k] += sign
				}
			}
		}
	}
	whole, err := parseAppendedFile(harness, s.Path, FileState{}, true)
	if err != nil {
		return
	}
	tail, err := parseAppendedFile(harness, s.Path, from, false)
	if err != nil {
		return
	}
	before := map[repeatKey]int{}
	count(whole, 1, before)
	count(tail, -1, before)
	for k, n := range before {
		if n > 0 {
			stored[k] = true
		}
	}
}

func metaForSession(s model.Session) SessionMeta { return textOf(s).row(s) }

// sessionText is the part of a session's row read off its messages: the
// derived title and every field mined from the text. It is the expensive part,
// mostly redaction, so a build computes it for every session in parallel ahead
// of the write loop, which then only lays the session's own fields over it.
// title is the s.Title it was computed for: the loop can still hand a session
// the title of an earlier row with its id.
type sessionText struct {
	meta  SessionMeta
	title string
}

// row is the session's row: its own fields over what its text gave.
func (t sessionText) row(s model.Session) SessionMeta {
	meta := t.meta
	if s.Title != t.title {
		meta.Title, meta.AgentTitle = sessionRowTitle(s)
	}
	meta.ID, meta.Harness, meta.Project, meta.Path = s.ID, s.Harness, s.Project, s.Path
	meta.Started, meta.Updated = s.Started, s.Updated
	meta.Kind, meta.Parent, meta.Agent = s.Kind, s.Parent, s.Agent
	meta.OrigID, meta.RemoteID, meta.From = s.OrigID, s.RemoteID, s.From
	meta.Lifecycle, meta.LifecycleNote, meta.LifecycleAt = s.Lifecycle, s.LifecycleNote, s.LifecycleAt
	return meta
}

// sessionTexts is textOf for every session, spread over the cores.
func sessionTexts(ss []model.Session) []sessionText {
	out := make([]sessionText, len(ss))
	workers := min(runtime.GOMAXPROCS(0), len(ss))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(ss) {
					return
				}
				out[i] = textOf(ss[i])
			}
		})
	}
	wg.Wait()
	return out
}

// sessionRowTitle is the title a session's row carries, and whether it is the
// agent's words rather than the user's.
func sessionRowTitle(s model.Session) (string, bool) {
	title := s.Title
	agentTitle := s.AgentTitle
	if title == "" {
		// A session with no user turn borrows the assistant's opening line
		// (#692), and one that is only tool output is named after its first
		// output; the listing needs to say so rather than print it where the
		// reader's own question goes (#1100). sessionTitleFrom carries the
		// right fromAgent bit — a computed one calls tool-only sessions
		// agent-titled.
		title, agentTitle = sessionTitleFrom(s)
		// Redact before the cut: slicing a secret in half leaves a prefix no
		// pattern matches, and it survives into sessions.gob (G/#…).
		title, _ = redact.Text(title)
		title = truncateTitle(title, 60)
	} else {
		// Titles come from unredacted places — an agent-generated summary, a
		// composer name, the first user message — and are persisted in
		// sessions.gob, so they need the same scrubbing as record text.
		title, _ = redact.Text(title)
		// And the same bound. A title the source authored went to the
		// one-line surfaces whole: measured at 384 characters with the
		// newlines still in it, so one session printed several rows of
		// `deja last` and the text after the break began "[claude · …",
		// which is deja's own listing format (#1090 covers the escape bytes;
		// this is the line break). Derived titles have been collapsed and cut
		// since they existed.
		title = widenThinSourceTitle(s, boundSourceTitle(s.Harness, title))
	}
	return title, agentTitle
}

// agentOwnedFile drops the agent's own working files. They are touched
// constantly while a subject is being worked on, so left in they take the top
// slots from the source that was actually being changed — measured: the six
// stored paths held no repository file at all for a session whose work was
// entirely in one.
func agentOwnedFile(p string) bool {
	// A worktree Claude Code made for an isolated agent sits under .claude/
	// and holds the repository's own source: the edits in it are the work,
	// not the agent's bookkeeping. Only the .claude/ segment is forgiven;
	// every other rule still applies inside it (#4164).
	p = strings.ReplaceAll(p, "/.claude/worktrees/", "/")
	for _, seg := range []string{"/scratchpad/", "/tasks/", "/.claude/", "/.gemini/", "/.agents/", "/.cache/", "/claude-501/", "/node_modules/", "/.git/"} {
		if strings.Contains(p, seg) {
			return true
		}
	}
	return strings.HasSuffix(p, ".log") || strings.HasSuffix(p, ".output")
}

// askedHashOf returns the stem hash for one user turn, or false when the text
// is not a question worth tracking. Shared by the message and record paths so
// they agree on what counts as an asking.
func askedHashOf(text string) (uint64, bool) {
	if notAsked(text) || !looksLikeQuestion(text) {
		return 0, false
	}
	stem := questionStem(text)
	if stem == "" {
		return 0, false
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(stem))
	return h.Sum64(), true
}

// askedHashes fingerprints the substantial things a person asked in a session.
// Short turns are excluded the way stats excludes them: "ok" and "continue"
// repeat in every session and mean nothing.
func askedHashes(ms []model.Message) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, m := range ms {
		if m.Role != "user" {
			continue
		}
		v, ok := askedHashOf(m.Text)
		if !ok || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= askedQuestionCap {
			break
		}
	}
	return out
}

// notAsked rejects the text a harness writes under the user role: hook
// envelopes, interruption notices, resume preambles, the compaction summary.
// It repeats across sessions by construction, so without this the most
// "repeated question" in any store is a piece of plumbing — measured: the first
// candidate this produced was "The following tool was executed by the user",
// spanning April to July.
func notAsked(text string) bool {
	// "no visible output" is a shell record's phrase and not an opening, so it
	// is asked about the whole turn here; a title candidate asks only
	// harnessPreamble, because a person can write those words in a question
	// ("why does the button have no visible output when clicked?") and that
	// question is a perfectly good name for a session (review of #3328).
	return harnessPreamble(text) || strings.Contains(strings.TrimSpace(text), "no visible output")
}

// harnessPreamble reports whether a turn opens with something the harness
// wrote: an envelope, an interruption notice, a resume preamble, the
// compaction caveat.
func harnessPreamble(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range []string{
		"<local-command", "<command-", "<task-notification", "<teammate-message",
		"<bash-", "<system-reminder", "<deja-recall", "Caveat:",
		"[Request interrupted", "The following tool was executed",
		"This session is being continued", "Continue from where you left off",
		"You are picking up work handed off from a ",
	} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return compactionSummary(t)
}

// compactionSummary reports the summary a harness writes when it compacts a
// conversation and hands the rest back to itself.
//
// The preamble that introduced it — "This session is being continued…" — is
// stripped as an injected line before anything reads the turn, so what is left
// starts at the summary and no longer looks like the harness's. 23 of the last
// 800 sessions on a real store were named after it: "Summary: 1. Primary
// Request and Intent: …" reached `deja last`, the recall block and the
// repeated-question counter as if a person had typed it.
//
// Matched on the template rather than on the word: someone writing "Summary: we
// moved the retry budget" is naming their own session, and the headings below
// are the ones a compaction writes.
func compactionSummary(t string) bool {
	if !strings.HasPrefix(strings.ToLower(t), "summary:") {
		return false
	}
	head := t
	if len(head) > 600 {
		head = head[:600]
	}
	for _, heading := range []string{
		"Primary Request", "Key Technical Concepts", "Files and Code Sections",
		"Pending Tasks", "Current Work",
	} {
		if strings.Contains(head, heading) {
			return true
		}
	}
	return false
}

// looksLikeQuestion keeps this to things a person actually asked. Without it
// the candidates a real store produces are overwhelmingly instructions to an
// agent — "Use the X tool", "Call Y with scope all", "Reply with only the raw
// JSON" — which repeat verbatim by construction and say nothing about the work.
//
// A question mark, or an interrogative opening in either language deja sees
// most. This under-includes on purpose: a missed repeat costs a line on one
// screen, a wrong one costs the reader's trust in the screen.
func looksLikeQuestion(text string) bool {
	t := strings.TrimSpace(text)
	// A person's question is short. A pasted report that happens to contain a
	// question mark two paragraphs in is not one, and shown truncated on a
	// screen it reads as noise — measured: the first candidate to survive the
	// earlier version was a 900-character critique of some charts.
	if len([]rune(t)) > askedMaxRunes {
		return false
	}
	low := strings.ToLower(t)
	if cjkfold.HasCJK(low) {
		low = cjkfold.String(low)
		low = strings.NewReplacer("麼", "么", "麽", "么", "裡", "里", "裏", "里").Replace(low)
	}
	for _, p := range []string{
		"do not ", "don't ", "never ", "always ", "reply with ",
		"не надо ", "никогда ", "всегда ",
		"不要", "禁止", "切勿", "永远", "只回复",
	} {
		if strings.HasPrefix(low, p) {
			return false
		}
	}
	if strings.HasSuffix(t, "?") || strings.HasSuffix(t, "？") ||
		strings.HasSuffix(low, "吗") || strings.HasSuffix(low, "呢") {
		return true
	}
	for _, p := range []string{
		"为什么", "怎么", "如何", "哪里", "哪儿", "哪个", "哪些", "什么", "谁",
		"何时", "什么时候", "多少", "为何", "可否", "是否", "能不能", "有没有", "是不是",
	} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return false
	}
	first := strings.Trim(strings.ToLower(fields[0]), ",.:;!\"'")
	switch first {
	case "what", "why", "how", "when", "where", "which", "who", "whose",
		"did", "does", "do", "is", "are", "was", "were", "can", "should", "would",
		"что", "чем", "почему", "зачем", "как", "какой", "какая", "какие", "когда",
		"где", "куда", "откуда", "сколько", "кто", "чей", "можно", "нужно", "надо":
		return true
	}
	return false
}

// questionStem folds a message to the form two askings of the same question
// share: lowercase, letters and digits only. Fewer than five words is not a
// question worth matching on.
func questionStem(text string) string {
	var b strings.Builder
	folded := strings.ToLower(text)
	if cjkfold.HasCJK(folded) {
		folded = cjkfold.String(folded)
		folded = strings.NewReplacer("麼", "么", "麽", "么", "裡", "里", "裏", "里").Replace(folded)
	}
	for _, r := range folded {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	fields := strings.Fields(b.String())
	if len(fields) < 5 && cjkfold.CountCJK(text) < 5 {
		// Chinese, Japanese and Korean write no separator between words, so a
		// question in those scripts is a single field however much it asks, and
		// the five-word bar dropped every one of them: a person could ask the
		// same thing weekly and deja never noticed (#1346). Their characters are
		// the words.
		return ""
	}
	return strings.Join(fields, " ")
}

// countTouchedPaths tallies the file paths in one `files` record's text, one
// per line, skipping deja's own injected artifacts.
func countTouchedPaths(count map[string]int, text string) {
	for p := range strings.SplitSeq(text, "\n") {
		if p = strings.TrimSpace(p); p != "" && !agentOwnedFile(p) {
			count[p]++
		}
	}
}

// rankTouchedCounted returns the counts behind the ranking, which the
// incremental merge needs: two ranked lists cannot be fused into a correct one
// from position alone (#1304).
func rankTouchedCounted(count map[string]int) ([]string, []int) {
	paths := rankTouched(count)
	hits := make([]int, len(paths))
	for i, p := range paths {
		hits[i] = count[p]
	}
	return paths, hits
}

// topTouchedCounted is topTouchedFiles with those counts. Files modified via
// edit/write/apply_patch records that did not also emit a `files` record (e.g.
// opencode apply_patch on a file not read first) are included so co-change and
// file-history lookups see every file the session modified.
func topTouchedCounted(ms []model.Message) ([]string, []int) {
	count := map[string]int{}
	edited := map[string]int{}
	for _, m := range ms {
		switch m.Role {
		case roleFiles:
			countTouchedPaths(count, m.Text)
		case sources.RoleEdit, sources.RoleWrote:
			if p := strings.TrimSpace(firstLineOf(m.Text)); p != "" && looksLikeEditedPath(p) && !agentOwnedFile(p) {
				edited[p]++
			}
		}
	}
	for p, n := range edited {
		if count[p] == 0 {
			count[p] = n
		}
	}
	return rankTouchedCounted(count)
}

// rankTouched orders touched paths by recurrence and caps the list, the shape
// SessionMeta.Touched holds.
func rankTouched(count map[string]int) []string {
	if len(count) == 0 {
		return nil
	}
	out := make([]string, 0, len(count))
	for p := range count {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > touchedFileCap {
		out = out[:touchedFileCap]
	}
	return out
}

func metaWithOrd(meta SessionMeta, ord uint32) SessionMeta {
	meta.Ord = ord
	return meta
}

func nextSessionOrd(sessions map[string]SessionMeta) uint32 {
	var maxOrd uint32
	for _, meta := range sessions {
		if meta.Ord > maxOrd {
			maxOrd = meta.Ord
		}
	}
	return maxOrd + 1
}

// sessionWords counts the words of a whole session, which is the document
// length BM25 is supposed to normalise by. Runs of letters, digits and the
// characters identifiers are made of, matching how the ranking side counts.
func sessionWords(ms []model.Message) int {
	n := 0
	for _, m := range ms {
		inWord := false
		for _, r := range m.Text {
			word := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
			if word && !inWord {
				n++
			}
			inWord = word
		}
	}
	return n
}

// sessionFromMeta is the one place a manifest entry becomes a session. It
// carries Touched, which retrieval used to copy by hand in a second, nearly
// identical constructor — so a caller reading it off `Recent` got an empty
// slice on every session and no error. Silence is the failure mode, and it
// already cost one wrong measurement: reading Touched from Recent reported 0
// of 1153 sessions carrying files while the manifest held them (#633).
//
// SessionMeta.Asked and SessionMeta.Hit have no counterpart on model.Session
// and are read from the manifest directly; they are not dropped here.
func sessionFromMeta(meta SessionMeta) model.Session {
	return model.Session{
		ID: meta.ID, Harness: meta.Harness, Project: meta.Project, Path: meta.Path,
		Title: meta.Title, AgentTitle: meta.AgentTitle, Started: meta.Started, Updated: meta.Updated, Touched: meta.Touched,
		GaveUp: meta.GaveUp,
		Words:  meta.Words,
		Kind:   meta.Kind, Parent: meta.Parent, Agent: meta.Agent,
		OrigID: meta.OrigID, RemoteID: meta.RemoteID, From: meta.From, Lifecycle: meta.Lifecycle, LifecycleNote: meta.LifecycleNote, LifecycleAt: meta.LifecycleAt,
	}
}

// carriedText is a stored or synced turn's text as this version's reader
// hands it back. A rebuild re-reads every transcript it still can, but a
// session whose transcript is gone, compacted away or imported is rebuilt from
// records.bin, which holds what an older reader stored, and a peer on an older
// build sends the same — for Claude Code, a prompt still inside its
// <pasted_content> wrapper.
func carriedText(harness, role, text string) string {
	if harness == "claude" && role == "user" {
		return sources.UnwrapPastedContent(text)
	}
	return text
}

// retitleWrappedPaste clears a title an older reader cut from a prompt still
// inside its paste wrapper, so metaForSession derives it again from the
// unwrapped turns.
func retitleWrappedPaste(s *model.Session) {
	if s.Harness == "claude" && strings.Contains(s.Title, "<pasted_content id=") {
		s.Title = ""
		s.AgentTitle = false
	}
}
