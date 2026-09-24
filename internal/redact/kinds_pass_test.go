package redact

import (
	"reflect"
	"strings"
	"testing"
)

// corpus holds one line per rule, plus the shapes the counted rules fire on and
// the prose they must leave alone.
var corpus = []string{
	"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA7Xq\n-----END RSA PRIVATE KEY-----",
	"psql postgres://svc:hunter2@db.internal:5432/app",
	`AWS_SECRET_ACCESS_KEY="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`,
	"aws sts get-caller-identity --profile AKIAIOSFODNN7EXAMPLE",
	"GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a",
	"export OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl012mno345pqr678",
	`curl -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"`,
	"Cookie: session=3f9a2c1e8b7d6a5f4e3d2c1b0a9f8e7d6c5b4a39",
	"machine api.example.com login deploy password s3cr3t-deploy-pw",
	"mysql -u root -pMyR00tPass app",
	"sshpass -p 'Tr0ub4dor&3' ssh deploy@10.0.0.4",
	`config = {"api_key": "abcdefghijklmnopqrstuvwxyz012345"}`,
	"DATABASE_PASSWORD=correct-horse-battery",
	"the build id is 9f2c4b7ae1d8306f5b4c2a190e7d6f38b1c4e5a2",
	"we decided to keep the retry budget at three and log the attempt",
	"пароль: очень-секретное-значение",
	"docker run -e TOKEN=abcd1234efgh5678ijkl90mnop image:latest",
}

func allKinds() map[string]bool {
	out := map[string]bool{}
	for _, k := range Kinds() {
		out[k] = true
	}
	return out
}

// TextKinds with every rule allowed has to be Text, byte for byte and count for
// count. The filter was threaded through the helpers rather than duplicated, and
// this is what keeps the two from drifting: if a rule is ever added to one path
// and not the other, this fails.
func TestEveryKindAllowedIsTheSameAsText(t *testing.T) {
	for _, in := range corpus {
		wantText, wantCounts := Text(in)
		gotText, gotCounts := TextKinds(in, allKinds())
		if gotText != wantText {
			t.Errorf("filtered pass differs on %q:\n got %q\nwant %q", in, gotText, wantText)
		}
		if !reflect.DeepEqual(map[string]int(gotCounts), map[string]int(wantCounts)) {
			t.Errorf("counts differ on %q: got %v, want %v", in, gotCounts, wantCounts)
		}
	}
}

// The point of the filter: a pass restricted to the named rules must never write
// a marker for one of the counted ones. `deja secrets --scrub` rewrites
// someone's own history, and a digest turned into `[redacted:entropy]` is a
// change nobody asked for and cannot undo (#3823).
func TestANamedPassNeverWritesACountedMarker(t *testing.T) {
	named := map[string]bool{
		"private-key": true, "url-credentials": true, "aws-secret": true,
		"aws-access-key": true, "github-token": true, "openai-key": true,
		"bearer-token": true, "jwt": true, "cookie": true, "password": true,
		"command-password": true,
	}
	for _, in := range corpus {
		got, counts := TextKinds(in, named)
		for _, counted := range []string{"entropy", "credential", "quoted-secret"} {
			if strings.Contains(got, "[redacted:"+counted+"]") {
				t.Errorf("a named pass wrote %s on %q:\n%s", counted, in, got)
			}
			if counts[counted] != 0 {
				t.Errorf("a named pass counted %d of %s on %q", counts[counted], counted, in)
			}
		}
	}
}

// And it still does its own job: every named rule in the corpus fires.
func TestANamedPassStillRedactsWhatItNames(t *testing.T) {
	named := map[string]bool{"url-credentials": true, "jwt": true, "github-token": true}
	cases := map[string]string{
		"psql postgres://svc:hunter2@db.internal:5432/app": "url-credentials",
		"GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a": "github-token",
	}
	for in, kind := range cases {
		got, counts := TextKinds(in, named)
		if !strings.Contains(got, "[redacted:"+kind+"]") {
			t.Errorf("%s was not redacted in %q:\n%s", kind, in, got)
		}
		if counts[kind] == 0 {
			t.Errorf("%s was replaced without being counted in %q", kind, in)
		}
	}
}

// An empty allow set is a pass that may change nothing, which is what a scrub
// with no named findings has to be.
func TestAnEmptyAllowSetChangesNothing(t *testing.T) {
	for _, in := range corpus {
		if got, counts := TextKinds(in, map[string]bool{}); got != in || len(counts) != 0 {
			t.Errorf("an empty allow set rewrote %q to %q (%v)", in, got, counts)
		}
	}
}
