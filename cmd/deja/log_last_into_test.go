package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The record knows which agent session received the digest and which terms
// fired it — the JSON hands both over. The header a person reads printed
// neither, so `deja log --last` said what was injected and never to whom or
// why (#2301).
func TestLogLastNamesTheRecipientAndTheTerms(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigestInto(dir, usage.KindDejaVu, "<deja-recall>\n  - Session: **a** `1`\n", "agent3", 2, 4096,
		[]string{"panic", "quaxbolt", "overflow"}, "r0", "r1")

	var out bytes.Buffer
	if err := runLogTo(&out, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	head := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(head, "agent3") {
		t.Errorf("header does not say which session received it: %q", head)
	}
	if !strings.Contains(head, "quaxbolt") {
		t.Errorf("header does not say what fired it: %q", head)
	}

	// A record without them keeps the short header — no empty labels.
	hermeticEnv(t)
	dir = index.DefaultDir()
	usage.RecordDigestPolicy(dir, usage.KindHook, "<deja-recall>\n  - Session: **b** `2`\n", 1, 4096, "local-only")
	out.Reset()
	if err := runLogTo(&out, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	head = strings.SplitN(out.String(), "\n", 2)[0]
	if strings.Contains(head, "into:") || strings.Contains(head, "terms:") {
		t.Errorf("header labels fields the record does not carry: %q", head)
	}
	if !strings.Contains(head, "policy: local-only") {
		t.Errorf("header lost the policy: %q", head)
	}
}
