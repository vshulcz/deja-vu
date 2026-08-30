package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every refusal, whatever it was, ended with "check those paths' permissions":
// a config with a `//` comment in it refused five targets for a syntax error
// and sent the reader to look at file permissions on files they can read
// perfectly well (#1663).
func TestRefusalRemedyMatchesTheFailure(t *testing.T) {
	parse := errors.New("invalid character '/' looking for beginning of object key string")
	denied := &fs.PathError{Op: "open", Path: "/x/settings.json", Err: fs.ErrPermission}

	cases := []struct {
		name string
		errs []error
		want string
	}{
		{"parse errors only", []error{parse, parse}, "fix what each one reports"},
		{"mixed", []error{denied, parse}, "fix what each one reports"},
		{"permission errors only", []error{denied, denied}, "check those paths' permissions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refusalRemedy(c.errs); !strings.Contains(got, c.want) {
				t.Errorf("remedy for %s is %q, want it to say %q", c.name, got, c.want)
			}
		})
	}
}

// The end-to-end shape: a config deja cannot parse is refused, and the sentence
// that closes the run does not blame permissions.
//
// It used to be a commented config that stood here. A comment is no longer a
// refusal — the entry is edited as text and the comment survives (#1664) — so
// the case is a file that is genuinely broken, which is what the message was
// always about.
func TestInstallUnparseableJSONDoesNotBlamePermissions(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".gemini")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settings, []byte("{\n  \"theme\": \"dark\",\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := captureRun(t, "install", "gemini")
	if err == nil {
		t.Fatal("install accepted a settings.json it cannot parse")
	}
	msg := err.Error()
	if strings.Contains(msg, "permissions") {
		t.Errorf("a parse error is reported as a permissions problem: %s", msg)
	}
	if !strings.Contains(msg, "unexpected end of JSON input") && !strings.Contains(msg, "invalid character") {
		t.Errorf("the refusal does not carry the parse error: %s", msg)
	}
}

// And the comment itself: the same target takes it now, and gives it back.
func TestInstallTakesACommentedGeminiConfig(t *testing.T) {
	hermeticEnv(t)
	root := filepath.Join(os.Getenv("HOME"), ".gemini")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "settings.json")
	before := "{\n  // mine\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(settings, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "gemini", "--no-index"); err != nil {
		t.Fatalf("a comment refused the target: %v", err)
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "// mine") || !strings.Contains(string(b), `"theme": "dark"`) {
		t.Errorf("the reader's file did not survive:\n%s", b)
	}
	if !strings.Contains(string(b), `"deja"`) {
		t.Errorf("deja's entry was not written:\n%s", b)
	}
}
