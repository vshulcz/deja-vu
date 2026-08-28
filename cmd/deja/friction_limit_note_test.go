package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// friction claims to print "what this machine keeps tripping over", so a cut
// list with nothing after it reads as the whole of it — the sentence `how` and
// `files` print for the same flag (#2311).
func TestFrictionSaysWhatTheLimitLeftOut(t *testing.T) {
	var lines []string
	errs := []string{
		"undefined: snorblefunc in vendor/blarg/api.go",
		"panic: quaxbolt deadline exceeded",
		"TypeError: cannot read property 'zonk' of undefined",
	}
	n := 0
	for _, e := range errs {
		for k := 0; k < 3; k++ {
			lines = append(lines, fmt.Sprintf(
				`{"type":"user","sessionId":"s%d","cwd":"/w/p","timestamp":"2026-07-2%dT10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`,
				n, k+1, e))
			n++
		}
	}
	seedFrictionStore(t, lines)

	var all bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &all); err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(all.String(), " sessions  "); rows != 3 {
		t.Fatalf("seeded %d recurring errors, friction found %d:\n%s", len(errs), rows, all.String())
	}

	said, err := captureRunStderr(t, "friction", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "2 of 3") {
		t.Errorf("friction cut the list without saying so: %q", said)
	}

	// Nothing held back, nothing announced.
	said, err = captureRunStderr(t, "friction", "--limit", "9")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "showing") {
		t.Errorf("friction announced a window it did not apply: %q", said)
	}
}
