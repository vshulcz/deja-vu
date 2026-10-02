package sources

import "time"

// markCopies finds compaction's copies. In-place compaction archives every
// live row (compacted=1) and writes the kept head, a summary and the kept
// tail as one batch of live rows (hermes_state.py archive_and_compact): the
// head repeats the start of what it archived, the tail repeats its end. The
// batch is written in one transaction, each row at the summary's time or
// under its original timestamp (_insert_message_rows), so a row is a copy
// only in that position and with such a timestamp; a request and a run
// repeated later are kept even with the same text and, with no provider id,
// the same call id Hermes derives from name and arguments.
//
// Every summary anchors a batch, archived or not: a second compaction
// archives the first one's batch, and its copies are still copies. The rows a
// compaction summarised start where the previous batch did.
func (h *hermesSession) markCopies() {
	from := 0
	for i := range h.rows {
		if h.rows[i].summary == "" || h.rows[i].copy {
			continue
		}
		head := h.headCopies(from, i)
		h.tailCopies(from, i-head, i)
		from = i - head
	}
}

// hermesBatchSlack is how far from its summary a copy written at the batch's
// time may sit; Hermes writes them in one transaction.
const hermesBatchSlack = 100 * time.Millisecond

// copyOf reports whether row c can be compaction's copy of row o, written in
// the batch whose summary is at t.
func (h *hermesSession) copyOf(c, o int, t time.Time) bool {
	rc, ro := &h.rows[c], &h.rows[o]
	if rc.key() != ro.key() {
		return false
	}
	if d := rc.t.Sub(t); !rc.t.Equal(ro.t) && (d > hermesBatchSlack || d < -hermesBatchSlack) {
		return false
	}
	// A result the batch kept is the archived one or a compressor stub;
	// anything else answered a later call under a reused id.
	return rc.role != "tool" || rc.text == ro.text || hermesCompressorStub(rc.text, rc.tool)
}

// headCopies marks the longest run of rows before the summary at i that
// repeats, in order, the start of the rows from `from` on, and returns its
// length.
func (h *hermesSession) headCopies(from, i int) int {
	t := h.rows[i].t
	for m := (i - from) / 2; m > 0; m-- {
		ok := true
		for p := 0; p < m && ok; p++ {
			ok = h.copyOf(i-m+p, from+p, t)
		}
		if ok {
			for p := 0; p < m; p++ {
				h.rows[i-m+p].copy = true
			}
			return m
		}
	}
	return 0
}

// tailCopies marks the longest run of rows from the summary at i on — from the
// summary itself when the tail's first message was merged into it — that
// repeats, in order, the end of the rows in [from, end).
func (h *hermesSession) tailCopies(from, end, i int) {
	t := h.rows[i].t
	start := i + 1
	if h.rows[i].text != "" {
		start = i
	}
	limit := start
	for limit < len(h.rows) && limit-start < end-from && (limit == i || h.rows[limit].summary == "") {
		limit++
	}
	for k := limit - start; k > 0; k-- {
		ok := true
		for p := 0; p < k && ok; p++ {
			ok = h.copyOf(start+p, end-k+p, t)
		}
		if !ok {
			continue
		}
		for j := start; j < start+k; j++ {
			if j == i {
				// Its copy goes; the summary it carries stays.
				h.rows[j].text, h.rows[j].calls = "", ""
				continue
			}
			h.rows[j].copy = true
		}
		return
	}
}
