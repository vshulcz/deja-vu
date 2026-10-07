package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// aider has no hook on a user message, but it reads every read-only file from
// disk again each time it builds a request, a few milliseconds after it appends
// the message to its chat history. Too short to write a recall into a plain
// file in time; long enough when the file is a pipe deja serves: aider's read
// waits until deja has read the history and answered with the recall for the
// message being sent. Unix only — the stand-in on Windows is the context file
// written once at start.

// aiderLive is the content `deja aider` serves for one aider run.
type aiderLive struct {
	dir, cwd, sid, history string
	digest                 string
	// offset is where the history ended when the run started: what is before
	// it was asked in an earlier session.
	offset int64

	mu    sync.Mutex
	asked string
	block string
}

func newAiderLive(dir, cwd, history, digest string) *aiderLive {
	l := &aiderLive{dir: dir, cwd: cwd, history: history,
		sid: "aider-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	if st, err := os.Stat(history); err == nil {
		l.offset = st.Size()
	}
	if digest != aiderPlaceholder {
		l.digest = digest
	}
	return l
}

// body is the file as aider reads it now: the digest, the recall for the
// message being sent, and the rules block `deja rules sync` keeps in the shared
// context file.
func (l *aiderLive) body() (out string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	defer func() {
		// A read deja fails to answer would hang aider, so a panic answers
		// with what is already known.
		if recover() != nil {
			out = l.digest
		}
	}()
	if q := aiderLastAsk(l.history, l.offset); q != "" && q != l.asked {
		l.asked = q
		l.block = antigravityPromptBlock(l.dir, q, l.sid, l.cwd)
	}
	body := l.digest
	if l.block != "" {
		if body != "" {
			body = strings.TrimRight(body, "\n") + "\n\n"
		}
		body += strings.TrimRight(l.block, "\n") + "\n"
	}
	if old, err := os.ReadFile(aiderContextPath()); err == nil {
		body = withAiderRules(body, string(old))
	}
	return body
}

// aiderHistoryTail bounds how much of the chat history is read per request.
const aiderHistoryTail = 256 << 10

// aiderLastAsk is the last message the user sent in this run, from aider's chat
// history past offset, or "" for none, for an aider command that is not a
// question, or for the answer to a confirmation.
func aiderLastAsk(path string, offset int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	if st.Size() < offset {
		offset = 0 // the file was replaced
	}
	if st.Size()-offset > aiderHistoryTail {
		offset = st.Size() - aiderHistoryTail
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	// aider writes a message as one "#### " line per line of it.
	lines := strings.Split(lfText(b), "\n")
	end := len(lines) - 1
	for end >= 0 && !strings.HasPrefix(lines[end], "#### ") {
		end--
	}
	if end < 0 {
		return ""
	}
	start := end
	for start > 0 && strings.HasPrefix(lines[start-1], "#### ") {
		start--
	}
	parts := make([]string, 0, end-start+1)
	for _, l := range lines[start : end+1] {
		parts = append(parts, strings.TrimRight(strings.TrimPrefix(l, "#### "), " "))
	}
	q := strings.TrimSpace(strings.Join(parts, "\n"))
	if strings.Contains(q, "(Y)es/(N)o") {
		return ""
	}
	return aiderQuestion(q)
}

// aiderQuestion is what a message asks: the text after a chat-mode command, ""
// for any other aider command.
func aiderQuestion(q string) string {
	if !strings.HasPrefix(q, "/") {
		return q
	}
	name, rest, _ := strings.Cut(q[1:], " ")
	switch strings.ToLower(name) {
	case "ask", "code", "architect", "context":
		return strings.TrimSpace(rest)
	}
	if strings.ContainsAny(name, "/.") {
		return q // a path, not a command
	}
	return ""
}

// startAiderLive serves the live file for the aider about to start and
// returns the extra arguments that point aider at it, and a stop to call once
// aider has exited. ok is false where it cannot be served.
func startAiderLive(dir string, args []string, cwd, digest string) (extra []string, stop func(), ok bool) {
	l := newAiderLive(dir, cwd, aiderChatHistory(args, cwd), digest)
	path, stop, err := serveAiderLive(l.body)
	if err != nil {
		if err != errNoAiderLive {
			fmt.Fprintf(os.Stderr, "deja: per-message recall is off for this run: %v\n", err)
		}
		return nil, nil, false
	}
	return aiderReadArgs(args, cwd, path), stop, true
}
