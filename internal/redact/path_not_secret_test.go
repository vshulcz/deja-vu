package redact

import (
	"strings"
	"testing"
)

// A scratch path is not a credential. The uppercase-only exemption was standing
// in for "looks like base64", and a macOS agent directory defeats it: the
// `-Users-<name>` segment supplies the case mix and a uuid directory supplies
// the entropy. When the record was nothing but that path — the output of a
// `pwd` — the whole message became `[redacted:entropy]`.
func TestAPathIsNotRedactedAsASecret(t *testing.T) {
	for _, path := range []string{
		"/private/tmp/claude-501/-Users-shulcz/9f0aa059-0c09-41f3-96a5-755b7b560be5/scratchpad/codexprobe",
		"/Users/Alice/Library/Application Support/Code/User/globalStorage",
		"~/Projects/Acme-Gateway/internal/db/pool.go",
		"./build/Release-iphoneos/App.app/Contents/MacOS/App",
	} {
		t.Run(strings.Split(path, "/")[1], func(t *testing.T) {
			got, _ := Text(path)
			if strings.Contains(got, "[redacted") {
				t.Fatalf("path was redacted:\n  in:  %s\n  out: %s", path, got)
			}
		})
	}
}

// The exemption must not become a way to smuggle a secret past the scan: a
// blob is not rooted, and anything carrying '=' or ':' is an assignment, a
// scheme or base64 padding rather than a bare path.
func TestThePathExemptionDoesNotCoverBlobs(t *testing.T) {
	for name, tok := range map[string]string{
		"base64 with slashes":  "dGhpcy9pcy9hL2Jsb2IvY29udGVudA==",
		"rooted assignment":    "/etc/key=aB3dEf9hIjKlMnOpQrStUvWxYz012345",
		"url with credentials": "https://user:aB3dEf9hIjKlMnOpQrStUvWx@example.com/path",
	} {
		t.Run(name, func(t *testing.T) {
			if looksLikePath(tok) {
				t.Fatalf("%q was treated as a path", tok)
			}
		})
	}
	// And the shape the exemption is for still qualifies.
	if !looksLikePath("/Users/Bob/src/app/main.go") {
		t.Error("a plain rooted path was not recognised")
	}
	// One separator is a token like /tmp, not a path worth exempting.
	if looksLikePath("/AbCdEfGh0123456789") {
		t.Error("a single-segment token was treated as a path")
	}
}
