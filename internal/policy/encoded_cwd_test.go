package policy

import "testing"

// A harness that folds the working directory into one directory name hid the
// tree deja is told to keep out of recall: 12 of the 317 rows naming a job tree
// on a real store were served, every one from a store shaped this way (#3746).
func TestTheJobTreeRuleReachesAnEncodedWorkingDirectory(t *testing.T) {
	var p Policy
	cases := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "omp folds the directory into one name",
			path: "/home/u/.omp/agent/sessions/-.claude-jobs-9f0a-tmp-dsh-work/2026-08-21T19-03-26-101Z_a1.jsonl",
			want: true,
		},
		{
			name: "a profile of the same store",
			path: "/home/u/.omp/profiles/probe/agent/sessions/-.claude-jobs-9f0a-tmp/2026-08-21T17-14-49-300Z_b2.jsonl",
			want: true,
		},
		{
			name: "claude's own encoding, where the dot became a second dash",
			path: "/home/u/.claude/projects/-home-u--claude-jobs-9f0a-tmp/1234.jsonl",
			want: true,
		},
		{
			name: "the plain path the rule was written for",
			path: "/home/u/.claude/jobs/9f0a/tmp/x.jsonl",
			want: true,
		},
		{
			name: "a file with dashes in its own name is not a directory to decode",
			path: "/home/u/notes/my-jobs-list.jsonl",
			want: false,
		},
		{
			name: "a directory with dashes that does not begin with one",
			path: "/home/u/src/claude-jobs-notes/session.jsonl",
			want: false,
		},
		{
			name: "ordinary work",
			path: "/home/u/src/app/.omp/agent/sessions/-home-u-src-app/s.jsonl",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Ignored(c.path, "dsh/work"); got != c.want {
				t.Errorf("Ignored(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// The decode is only for matching, and it must not rewrite a string that has
// nothing to decode.
func TestDecodingLeavesAPlainPathAlone(t *testing.T) {
	for _, s := range []string{
		"", "/home/u/src/app/session.jsonl", "relative/path.jsonl", "/",
	} {
		if got := decodeCWDSegments(s); got != s {
			t.Errorf("decodeCWDSegments(%q) = %q, want it untouched", s, got)
		}
	}
	// A leading encoded segment is decoded even with no directory above it,
	// which is how a relative store path arrives.
	if got := decodeCWDSegments("-.claude-jobs-9f0a-tmp/s.jsonl"); got != "/.claude/jobs/9f0a/tmp/s.jsonl" {
		t.Errorf("leading segment decoded to %q", got)
	}
}

// A pattern someone writes themselves replaces the default and reaches the
// same encoded directories, since that is where their work is too.
func TestAWrittenPatternReachesTheEncodedForm(t *testing.T) {
	p := Policy{Ignore: []string{"*/scratch/*"}}
	if !p.Ignored("/home/u/.omp/agent/sessions/-home-u-scratch-try/s.jsonl", "u/try") {
		t.Error("a written rule did not reach the encoded working directory")
	}
	if p.Ignored("/home/u/.omp/agent/sessions/-home-u-src-app/s.jsonl", "u/app") {
		t.Error("a written rule reached a directory it does not name")
	}
}
