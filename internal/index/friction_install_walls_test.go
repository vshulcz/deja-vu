package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A package install behind a mirror or a proxy fails in a handful of shapes,
// and none of them was a wall: `deja fix` had nothing for the session that
// found the mirror that works (#5599).
func TestThePackageInstallWallsAreWalls(t *testing.T) {
	for _, l := range []string{
		"ERROR: No matching distribution found for vllm==0.6.3",
		"ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)",
		"ERROR: HTTP error 403 while getting https://files.pythonhosted.org/packages/x.whl",
		"curl: (6) Could not resolve host: pypi.org",
		"[SSL: CERTIFICATE_VERIFY_FAILED] certificate verify failed: unable to get local issuer certificate",
		`ERROR: failed to solve: process "/bin/sh -c pip install vllm" did not complete successfully: exit code: 1`,
		"go: example.com/x@v1.2.3: reading https://proxy.golang.org/example.com/x/@v/v1.2.3.mod: 403 Forbidden",
		"dial tcp: lookup proxy.golang.org: no such host",
		"x509: certificate signed by unknown authority",
		"npm error 403 403 Forbidden - GET https://registry.npmjs.org/left-pad",
		"npm error code ECONNREFUSED",
		"error: externally-managed-environment",
		"Temporary failure in name resolution",
		"Error response from daemon: manifest unknown",
		"Error response from daemon: pull access denied for acme/api, repository does not exist",
		"Received HTTP code 407 Proxy Authentication Required from proxy after CONNECT",
	} {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("not recognised as a wall: %q", l)
		}
	}
}

// The same words in a sentence are not the tool saying them.
func TestASentenceAboutAnInstallErrorIsNotAWall(t *testing.T) {
	for _, l := range []string{
		"the mirror returns 403 Forbidden for packages that are not approved",
		"if the host has no such host entry, add it to /etc/hosts first",
		"the build did not complete successfully because of the proxy",
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("read as a wall: %q", l)
		}
	}
}

// The remedy is the same install pointed at the mirror, and it counts the
// first time it is seen: it is the failing command corrected.
func TestAMirrorInstallAnswersTheIndexThatRefused(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "pip3 install vllm==0.6.3", Time: now},
		{Role: "tool-output", Text: "ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)\nERROR: No matching distribution found for vllm==0.6.3", Time: now.Add(time.Second)},
		{Role: "command", Text: "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "Successfully installed vllm-0.6.3", Time: now.Add(2 * time.Minute)},
	}
	pairs := judgeFixes(fixPairsIn(ms, "claude:s1", "p"))
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %+v", pairs)
	}
	p := pairs[0]
	if p.Candidate {
		t.Errorf("the mirror install waits for a second session: %+v", p)
	}
	if p.Command != "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3" {
		t.Errorf("remedy = %q", p.Command)
	}
}
