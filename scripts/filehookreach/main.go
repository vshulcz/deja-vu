// Command filehookreach asks why the per-action hook is silent on files it has
// history for.
//
// #2924 measured 43 of 120 edited paths answered and did not chase the rest.
// This counts, for a sample of the store's own touched files, how many sessions
// each has after the hook's own scoping — the number the five-session threshold
// is applied to — so the cost of that threshold can be read off rather than
// guessed at.
//
//	go run ./scripts/filehookreach [-n 200] [-deja ./deja]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

func main() {
	n := flag.Int("n", 200, "how many touched paths to sample")
	bin := flag.String("deja", "", "a built deja, to ask the hook itself and attribute its silence")
	flag.Parse()

	dir := index.DefaultDir()
	paths, err := touchedPaths(dir, *n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "filehookreach:", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Println("no touched paths in the index")
		return
	}
	hist := map[int]int{}
	for _, p := range paths {
		hist[len(index.FileSessions(dir, p))]++
	}
	fmt.Printf("%d touched paths sampled\n", len(paths))
	keys := make([]int, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	running := 0
	for _, k := range keys {
		running += hist[k]
		fmt.Printf("  %3d session(s): %4d paths   (%4d at or below)\n", k, hist[k], running)
	}
	if *bin != "" {
		answered, silentWithHistory, silentWithout := 0, 0, 0
		for _, p := range paths {
			spoke := hookSpeaks(*bin, p)
			switch {
			case spoke:
				answered++
			case len(index.FileSessions(dir, p)) >= 5:
				silentWithHistory++
			default:
				silentWithout++
			}
		}
		fmt.Printf("the hook itself: %d answered, %d silent with five sessions or more, %d silent below that\n",
			answered, silentWithHistory, silentWithout)
	}
	for _, floor := range []int{2, 3, 4, 5, 6} {
		answered := 0
		for k, v := range hist {
			if k >= floor {
				answered += v
			}
		}
		fmt.Printf("threshold %d -> %d of %d paths reach the hook (%.0f%%)\n",
			floor, answered, len(paths), 100*float64(answered)/float64(len(paths)))
	}
}

// touchedPaths samples the files sessions in the index actually touched, which
// is the population where the hook can say anything at all.
func touchedPaths(dir string, n int) ([]string, error) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, meta := range metas {
		for _, p := range meta.Touched {
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
			if len(out) >= n {
				return out, nil
			}
		}
	}
	return out, nil
}

// hookSpeaks asks the built binary what it would say before an edit of this
// path, from the directory the file lives in — which is what an agent editing
// it would be working in.
func hookSpeaks(bin, path string) bool {
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Edit",
		"cwd":        filepath.Dir(path),
		"session_id": "filehookreach",
		"tool_input": map[string]any{"file_path": path},
	})
	if err != nil {
		return false
	}
	cmd := exec.Command(bin, "hook-tool")
	cmd.Stdin = strings.NewReader(string(payload))
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
