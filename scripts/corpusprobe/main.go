// Command corpusprobe answers the questions the idea backlog (#526-#546) rests
// on, against a real index. It prints counts and shapes only — never corpus
// text — because the corpus is someone's private history.
//
//	DEJA_INDEX_DIR=~/.cache/deja/index.db go run ./scripts/corpusprobe
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

func main() {
	dir := index.DefaultDir()
	recs, err := index.ReadRecords(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	fmt.Printf("index %s · %d records\n\n", dir, len(recs))

	// 1. What is in there at all. Several ideas need tool calls and command
	// output; if the index holds only prose turns, they need the raw
	// transcripts instead and are a different, larger project.
	byRole := map[string]int{}
	bytesByRole := map[string]int{}
	for _, or := range recs {
		r := or.Record
		byRole[r.Role]++
		bytesByRole[r.Role] += len(r.Text)
	}
	fmt.Println("== roles")
	for _, k := range sortedKeys(byRole) {
		fmt.Printf("  %-12s %7d records · %6.1f MB\n", k, byRole[k], float64(bytesByRole[k])/1e6)
	}

	// 2. Command runs and their output (#539, #546, #541).
	cmdRe := regexp.MustCompile(`(?m)^\s*\$ |(?m)^\s*(go test|go build|npm |pnpm |yarn |cargo |pytest|make |git |docker |kubectl )`)
	exitRe := regexp.MustCompile(`(?i)(exit code|exit status|command failed|non-zero)`)
	testRe := regexp.MustCompile(`(?i)(--- FAIL|--- PASS|\bok\s+github|PASS\b|FAIL\b|\d+ passed|\d+ failed)`)
	var withCmd, withExit, withTest int
	for _, or := range recs {
		r := or.Record
		if cmdRe.MatchString(r.Text) {
			withCmd++
		}
		if exitRe.MatchString(r.Text) {
			withExit++
		}
		if testRe.MatchString(r.Text) {
			withTest++
		}
	}
	fmt.Println("\n== command evidence (#539 #541 #546)")
	fmt.Printf("  records that look like a command run   %6d (%.1f%%)\n", withCmd, pct(withCmd, len(recs)))
	fmt.Printf("  records mentioning an exit status      %6d\n", withExit)
	fmt.Printf("  records with test result output        %6d\n", withTest)

	// 3. File content in the record (#537). A restore needs whole files, so
	// count records that look like a file read and how big they are.
	fileRe := regexp.MustCompile(`(?m)^\s*\d+[\t→|]`) // numbered listing, the common shape
	pkgRe := regexp.MustCompile(`(?m)^package \w+|^import \(|^func \w+\(`)
	var numbered, sourceish, bigSource int
	for _, or := range recs {
		r := or.Record
		if fileRe.MatchString(r.Text) {
			numbered++
		}
		if pkgRe.MatchString(r.Text) {
			sourceish++
			if len(r.Text) > 4000 {
				bigSource++
			}
		}
	}
	fmt.Println("\n== file content (#537)")
	fmt.Printf("  numbered file listings                 %6d\n", numbered)
	fmt.Printf("  records containing source structure    %6d\n", sourceish)
	fmt.Printf("  of those, over 4 KB                    %6d\n", bigSource)

	// 4. Corrections (#530): a short imperative user turn that pushes back.
	corr := regexp.MustCompile(`(?i)^(не |no,|don'?t|stop|перестань|не надо|instead|why did you|зачем ты|опять ты|again you)`)
	negWord := regexp.MustCompile(`(?i)(не надо|don'?t|never|instead of|прекрати|убери|не пиши|не добавляй)`)
	var shortUser, corrections int
	for _, or := range recs {
		r := or.Record
		if r.Role != "user" {
			continue
		}
		t := strings.TrimSpace(r.Text)
		if len(t) < 200 {
			shortUser++
		}
		if len(t) < 400 && (corr.MatchString(t) || negWord.MatchString(t)) {
			corrections++
		}
	}
	fmt.Println("\n== corrections (#530)")
	fmt.Printf("  short user turns (<200 B)              %6d\n", shortUser)
	fmt.Printf("  of which look like a correction        %6d\n", corrections)

	// 5. Claims about work done (#541).
	claim := regexp.MustCompile(`(?i)(ran the (tests|suite)|tests pass|all green|прогнал тесты|тесты проход|verified|проверил|запустил тесты|suite is green)`)
	var claims int
	for _, or := range recs {
		r := or.Record
		if r.Role != "user" && claim.MatchString(r.Text) {
			claims++
		}
	}
	fmt.Println("\n== claims of work done (#541)")
	fmt.Printf("  assistant turns claiming a run/check   %6d\n", claims)

	// 6. Failure recurrence (#527): the same error line seen in more than one
	// session. Normalized so paths and numbers do not split identical errors.
	errRe := regexp.MustCompile(`(?m)^.{0,120}?(error|Error|ERROR|panic:|FAIL|Exception|cannot|failed)[^\n]{0,160}`)
	num := regexp.MustCompile(`\d+`)
	hexish := regexp.MustCompile(`[0-9a-f]{8,}`)
	sessionsFor := map[string]map[string]bool{}
	for _, or := range recs {
		r := or.Record
		for _, m := range errRe.FindAllString(r.Text, 8) {
			k := strings.TrimSpace(m)
			k = hexish.ReplaceAllString(k, "H")
			k = num.ReplaceAllString(k, "N")
			if len(k) < 30 {
				continue
			}
			if sessionsFor[k] == nil {
				sessionsFor[k] = map[string]bool{}
			}
			sessionsFor[k][r.Key] = true
		}
	}
	var repeated, repeated3 int
	for _, ss := range sessionsFor {
		if len(ss) >= 2 {
			repeated++
		}
		if len(ss) >= 3 {
			repeated3++
		}
	}
	fmt.Println("\n== repeated failures (#527)")
	fmt.Printf("  distinct error-ish lines              %7d\n", len(sessionsFor))
	fmt.Printf("  seen in 2+ sessions                  %7d\n", repeated)
	fmt.Printf("  seen in 3+ sessions                  %7d\n", repeated3)
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return m[out[i]] > m[out[j]] })
	return out
}
