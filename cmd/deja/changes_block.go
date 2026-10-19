package main

import (
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The code a session changed takes what the conversation leaves of a context
// digest, between these bounds. The digest reads a session from the top, and
// a coding session's top is the task and then reading code: on 48
// SWE-ContextBench tasks the digest of the past session that fixed the same
// code held none of the fix. The conversation keeps at least the rest of the
// 8 KB.
const (
	changesBlockMin = 3000
	changesBlockMax = 5000
)

const (
	changeOldMax = 400
	changeNewMax = 1800
)

// changesBlock is the code a session left, read back from its transcript:
// each change from the code as it was to the code as the session left it,
// the project's code before its tests, the agent's scratch files left out.
// Empty for a session that changed nothing, or whose harness it cannot read.
func changesBlock(s model.Session, room int) string {
	room = min(max(room, changesBlockMin), changesBlockMax)
	cs := sources.FinalChanges(sources.Changes(s))
	if len(cs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## what it changed\n")
	// The changes that did not fit are counted, not dropped without a word:
	// the block read as everything the session changed (#5392).
	const leftOutRoom = 40
	shown := 0
	for i, c := range cs {
		var one string
		if c.Old == "" {
			one = fmt.Sprintf("\n%s, new file:\n%s\n", c.Path, clipChange(c.New, changeNewMax))
		} else {
			one = fmt.Sprintf("\n%s:\n%s\n", c.Path, clipChange(changeDiff(c.Old, c.New), changeNewMax+changeOldMax))
		}
		limit := room
		if i < len(cs)-1 {
			limit -= leftOutRoom
		}
		if b.Len()+len(one) > limit {
			if shown == 0 {
				// The first change alone is over: show what fits of it rather
				// than nothing.
				b.WriteString(clipToFit(one, limit-b.Len()))
				shown++
			}
			break
		}
		b.WriteString(one)
		shown++
	}
	// Written after the redaction and the trim, so neither can take it.
	count := ""
	if left := len(cs) - shown; left > 0 {
		count = fmt.Sprintf("\n(%d more change%s not shown)\n", left, pluralS(left))
	}
	text, _ := redact.Text(b.String())
	return trimUTF8(redact.SafeForDisplay(text), max(0, room-len(count))) + count + "\n"
}

// clipToFit cuts one change to n bytes and says so.
func clipToFit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return trimUTF8(s, max(0, n-len("\n…\n"))) + "\n…\n"
}

func clipChange(s string, n int) string {
	s = strings.Trim(s, "\n")
	if len(s) <= n {
		return s
	}
	return trimUTF8(s, n) + "\n…"
}

// withChanges puts the block right under the digest's header line, ahead of
// the conversation, so a digest cut to its budget loses the end of the
// conversation and keeps the code.
func withChanges(digest string, s model.Session) string {
	blk := changesBlock(s, search.ContextBudget-len(digest))
	if blk == "" {
		return digest
	}
	head, rest, ok := strings.Cut(digest, "\n")
	if !ok {
		return digest + "\n\n" + blk
	}
	return head + "\n\n" + blk + rest
}

// changeContext is how many unchanged lines a diff keeps on each side.
const changeContext = 2

// changeDiff shows an edit as the lines it removed and added, with a little of
// the unchanged code around them. An edit's two sides share most of their
// lines — the anchor the agent quoted to place it — and printing both whole
// spent the budget on the same lines twice.
func changeDiff(old, new string) string {
	a, b := strings.Split(old, "\n"), strings.Split(new, "\n")
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var out []string
	for _, l := range a[max(0, pre-changeContext):pre] {
		out = append(out, "  "+l)
	}
	for _, l := range a[pre : len(a)-suf] {
		out = append(out, "- "+l)
	}
	for _, l := range b[pre : len(b)-suf] {
		out = append(out, "+ "+l)
	}
	for _, l := range a[len(a)-suf : min(len(a), len(a)-suf+changeContext)] {
		out = append(out, "  "+l)
	}
	return strings.Join(out, "\n")
}
