package index

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// neighbourStore is a Claude store of 16 sessions where four hold word beside
// pelicanrotor, so the neighbour map pairs them. inOutput puts word in the
// middle of a long command output, a part of the record the postings leave
// out. rewrite writes the four again without word and indexes them.
func neighbourStore(t *testing.T, word string, inOutput bool) (inc string, rewrite func()) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	body := func(id string, with bool) string {
		w := ""
		if with && !inOutput {
			w = word + " "
		}
		b := parityClaudeLine(id, "00:00Z", "user", fmt.Sprintf(`"why does %s spin on ledger, %s"`, id, map[bool]string{true: "pelicanrotor " + w, false: ""}[id < "s004"]))
		out := strings.Repeat("compiling package ok\n", 1200)
		if with && inOutput {
			out = strings.Repeat("compiling package ok\n", 600) + "linking " + word + " ok\n" + strings.Repeat("compiling package ok\n", 600)
		}
		b += parityClaudeLine(id, "01:01Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make build"}}]`)
		b += parityClaudeLine(id, "01:02Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":`+fmt.Sprintf("%q", out)+`}]`)
		return b
	}
	for i := range 16 {
		id := fmt.Sprintf("s%03d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body(id, i < 4), false)
	}
	inc = filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if !slices.Contains(readCooccur(inc)["pelicanrotor"], word) {
		t.Fatalf("control: the neighbour map does not pair %q: %v", word, readCooccur(inc)["pelicanrotor"])
	}
	return inc, func() {
		later := time.Now().Add(2 * time.Second)
		for i := range 4 {
			p := filepath.Join(proj, fmt.Sprintf("s%03d.jsonl", i))
			parityWrite(t, p, body(fmt.Sprintf("s%03d", i), false), false)
			_ = os.Chtimes(p, later, later)
		}
		parityPass(t, inc, false)
	}
}

// A word taken out of every transcript that held it stayed in cooccur.gob,
// the one index file a pass did not write, until a full build (#5429).
func TestRemovedTextLeavesTheNeighbourMapOnTheReplacingPass(t *testing.T) {
	for _, tc := range []struct {
		name             string
		inOutput, append bool
	}{
		{"appended", false, true},
		{"written anew", false, false},
		{"in an output's middle, appended", true, true},
		{"in an output's middle, written anew", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const word = "zzquokkamarmot"
			if !tc.append {
				appendReplacing = false
				defer func() { appendReplacing = true }()
			}
			inc, rewrite := neighbourStore(t, word, tc.inOutput)
			if tc.inOutput && filesHold(t, filepath.Join(inc, "buckets"), word) {
				t.Fatal("control: the postings hold the word, so the unposted part is not under test")
			}
			inode := indexFiles(t, inc)["records.bin"]
			rewrite()
			if tc.append != os.SameFile(inode, indexFiles(t, inc)["records.bin"]) {
				t.Fatalf("the pass did not take the path under test (append %v)", tc.append)
			}
			if got := indexFilesHolding(t, inc, word); len(got) > 0 {
				t.Errorf("the removed word is still in %v", got)
			}
		})
	}
}

// A word that another session still holds stays, as that session's.
func TestANeighbourAnotherSessionHoldsStaysInTheMap(t *testing.T) {
	const word = "zzquokkamarmot"
	inc, _ := neighbourStore(t, word, false)
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	// One of the four loses the word; three still hold it.
	proj := filepath.Join(filepath.Dir(inc), "claude", "-tmp-proj")
	p := filepath.Join(proj, "s000.jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	parityWrite(t, p, strings.ReplaceAll(string(b), word+" ", ""), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
	parityPass(t, inc, false)
	if after, _ := readManifest(inc); len(after.DeadRecords) == len(m.DeadRecords) {
		t.Fatal("the pass killed no record, so the scrub is not under test")
	}
	if !slices.Contains(readCooccur(inc)["pelicanrotor"], word) {
		t.Errorf("a word three sessions still hold left the map: %v", readCooccur(inc)["pelicanrotor"])
	}
}

// A word a replaced session held only in an output's middle stays while other
// sessions' prompts still hold it: the postings are asked, not assumed.
func TestAnUnpostedWordOthersStillHoldStaysInTheMap(t *testing.T) {
	for _, appends := range []bool{true, false} {
		t.Run(fmt.Sprintf("append %v", appends), func(t *testing.T) {
			if !appends {
				appendReplacing = false
				defer func() { appendReplacing = true }()
			}
			const word = "zzquokkamarmot"
			inc, _ := neighbourStore(t, word, false)
			p := filepath.Join(filepath.Dir(inc), "claude", "-tmp-proj", "x999.jsonl")
			body := func(with bool) string {
				out := strings.Repeat("compiling package ok\n", 600)
				if with {
					out += "linking " + word + " ok\n"
				}
				out += strings.Repeat("compiling package ok\n", 600)
				return parityClaudeLine("x999", "00:00Z", "user", `"unrelated prompt here"`) +
					parityClaudeLine("x999", "01:01Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make build"}}]`) +
					parityClaudeLine("x999", "01:02Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":`+fmt.Sprintf("%q", out)+`}]`)
			}
			parityWrite(t, p, body(true), false)
			parityPass(t, inc, false)
			parityWrite(t, p, body(false), false)
			later := time.Now().Add(3 * time.Second)
			_ = os.Chtimes(p, later, later)
			parityPass(t, inc, false)
			if !slices.Contains(readCooccur(inc)["pelicanrotor"], word) {
				t.Errorf("a word four prompts still hold left the map: %v", readCooccur(inc)["pelicanrotor"])
			}
		})
	}
}
