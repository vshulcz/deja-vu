// Command mkcorpus writes a synthetic history to sweep a harness against.
//
// harnesssweep needs a store to recall from, and pointing it at a real one
// makes the run unrepeatable and puts private transcripts in the way. This
// writes transcripts in Claude Code's on-disk shape, holding the three things
// deja indexes separately: what was said, the commands that were run, and the
// files that were touched.
//
// The counts are chosen to clear the bars deja applies before it will speak,
// which is easy to get wrong by hand. A command is remembered once it has
// recurred in two sessions; a file is a place with a past at five. A corpus
// under those bars produces a hook that says nothing, which reads exactly like
// a hook that is broken — an afternoon was spent on that.
//
//	go run ./scripts/mkcorpus -out /tmp/corpus
//	go run ./scripts/harnesssweep -corpus /tmp/corpus/projects ...
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The one fact a sweep asks about, planted deep enough that the ranking has to
// find it rather than stumble on it.
//
// The interval is a flag, not a constant, because an agent with tools reads
// the repository: given permission to run commands, antigravity answered "47
// days" from this file with no memory involved at all, in the arm that was
// supposed to have none. A sweep that wants an answer only memory can give
// passes -interval with a number this checkout does not contain.
const answerQuestion = "how often does the deploy token for the billing gateway rotate?"

func answerFact(days int) string {
	return fmt.Sprintf("we settled it: the billing gateway deploy token rotates every %d days, "+
		"pinned to the vault lease", days)
}

var chatter = [][2]string{
	{"the login page flashes on reload", "a layout shift: the avatar image had no width attribute"},
	{"nightly export is slow again", "it re-reads the whole ledger; it wants a cursor by updated_at"},
	{"tests are flaky on the runner", "two of them share a temp directory and race on cleanup"},
	{"the webhook signature check fails", "the body was read twice, so the second read hashed nothing"},
	{"staging deploy hung", "the health check waited on a port the container never opened"},
	{"search results feel stale", "the index only refreshes on write; reads never trigger it"},
	{"rate limiter rejects valid traffic", "the window is per-process, not per-cluster"},
	{"migration locked the table", "it rewrote the whole table; add the column nullable first"},
}

var commands = [][3]string{
	{"npm run build", "Error: Cannot find module 'esbuild'", "the lockfile was stale; `npm ci` fixed it"},
	{"go test ./...", "--- FAIL: TestRetryBudget", "the budget counts attempts, not elapsed time"},
	{"docker compose up -d", "Error: port is already allocated", "the old stack was still running; compose down first"},
}

var files = [][2]string{
	{"internal/index/retrieval.go", "the AND across query words is why plain questions come back empty"},
	{"cmd/deja/install.go", "install writes through a symlink, so dotfiles repos keep their file"},
}

type writer struct {
	dir   string
	clock time.Time
	n     int
}

func (w *writer) session(id string, turns []turn) error {
	path := filepath.Join(w.dir, id+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, t := range turns {
		var content any = t.text
		if t.role == "assistant" {
			blocks := []any{map[string]any{"type": "text", "text": t.text}}
			if t.tool != nil {
				blocks = append(blocks, t.tool)
			}
			content = blocks
		}
		if t.result != "" {
			content = []any{map[string]any{
				"type": "tool_result", "tool_use_id": t.toolID, "content": t.result}}
		}
		if err := enc.Encode(map[string]any{
			"type":      t.role,
			"sessionId": id,
			"timestamp": w.clock.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": t.role, "content": content},
		}); err != nil {
			return err
		}
	}
	w.clock = w.clock.Add(12 * time.Hour)
	w.n++
	return nil
}

type turn struct {
	role, text string
	tool       map[string]any
	toolID     string
	result     string
}

func say(role, text string) turn { return turn{role: role, text: text} }

func main() {
	out := flag.String("out", "", "where to write the corpus")
	project := flag.String("project", "-Users-you-code-deja-vu",
		"the transcript directory name, which is the project deja records")
	interval := flag.Int("interval", 47,
		"the rotation interval the planted fact states; pass a number this "+
			"checkout does not contain so an agent with tools cannot grep it")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "mkcorpus: -out is required")
		os.Exit(2)
	}
	dir := filepath.Join(*out, "projects", *project)
	if err := os.RemoveAll(*out); err != nil {
		fmt.Fprintln(os.Stderr, "mkcorpus:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkcorpus:", err)
		os.Exit(1)
	}
	w := &writer{dir: dir, clock: time.Now().AddDate(0, 0, -60)}
	fail := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "mkcorpus:", err)
			os.Exit(1)
		}
	}

	// Plain conversation, with the answer buried early so it has to be ranked
	// rather than found by recency.
	for i, c := range chatter {
		turns := []turn{say("user", c[0]), say("assistant", c[1])}
		if i == 1 {
			turns = append([]turn{say("user", answerQuestion), say("assistant", answerFact(*interval))}, turns...)
		}
		fail(w.session(fmt.Sprintf("chat-%02d", i), turns))
	}

	// Commands, twice each: once is not yet a habit deja will mention.
	for i, c := range commands {
		for rep := range 2 {
			id := fmt.Sprintf("cmd-%02d-%d", i, rep)
			toolID := fmt.Sprintf("t%d%d", i, rep)
			fail(w.session(id, []turn{
				say("user", "let's try "+c[0]),
				{role: "assistant", text: "running " + c[0], toolID: toolID,
					tool: map[string]any{"type": "tool_use", "id": toolID, "name": "Bash",
						"input": map[string]any{"command": c[0]}}},
				{role: "user", toolID: toolID, result: c[1]},
				say("assistant", c[2]),
			}))
		}
	}

	// Files, six sessions each: five is where deja calls a file a place with a
	// past rather than ordinary work.
	for i, f := range files {
		for rep := range 6 {
			id := fmt.Sprintf("file-%02d-%d", i, rep)
			toolID := fmt.Sprintf("e%d%d", i, rep)
			fail(w.session(id, []turn{
				say("user", "have a look at "+f[0]),
				{role: "assistant", text: "reading it", toolID: toolID,
					tool: map[string]any{"type": "tool_use", "id": toolID, "name": "Edit",
						"input": map[string]any{"file_path": "/Users/you/code/deja-vu/" + f[0],
							"old_string": "before", "new_string": "after"}}},
				{role: "user", toolID: toolID, result: "edited"},
				say("assistant", f[1]),
			}))
		}
	}

	fmt.Printf("%d sessions in %s\n", w.n, dir)
	fmt.Printf("ask it: %q — the answer is only in there\n", answerQuestion)
}
