package digest

import "testing"

func TestStripHarnessBlocksLeavesThePersonsWords(t *testing.T) {
	cases := map[string]string{
		// The host alone: nothing is left.
		"<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n</task-notification>": "",
		"<system-reminder>\nCAVEMAN MODE ACTIVE\n</system-reminder>":                                       "",
		// A person, with the host's reminder appended: the reminder goes, the words stay.
		"why does the pool use the simple protocol?\n<system-reminder>\nthe file changed on disk\n</system-reminder>": "why does the pool use the simple protocol?",
		// Attributes on the tag, and a block that is never closed.
		`<teammate-message teammate_id="triage2" color="cyan">{"type":"idle"}</teammate-message> fix the flaky test`: "fix the flaky test",
		"what did we decide about token refresh?\n<task-notification>\n<task-id>x</task-id>":                         "what did we decide about token refresh?",
		// deja's own block echoed back is not the person either.
		"<deja-recall>\nRecalled history…\n</deja-recall>\nand the second one?": "and the second one?",
		// The `!` shell: what ran and what it printed is not a question.
		"<bash-input>git status</bash-input>\n<bash-stdout>On branch main</bash-stdout>": "",
		// A tag name that is only a prefix of a word in prose is not a tag.
		"the <metadata> table has no meta column": "the <metadata> table has no meta column",
		// Amp's attachment block ahead of the question, and Cursor's wrapper:
		// the file bodies go, the question stays (#3182).
		"<attached_files>\n<file path=\"zebraquux/fetch.go\">package zebraquux\nfunc Fetch() {}\n</file>\n</attached_files>\nwhy does the zebraquux fetcher time out?":                                                                                         "why does the zebraquux fetcher time out?",
		"<additional_data>\nBelow are some potentially helpful/relevant pieces of information\n<attached_files>\n<file_contents>x</file_contents>\n</attached_files>\n</additional_data>\n\n<user_query>why does the pager quokkabloom on scroll</user_query>": "why does the pager quokkabloom on scroll",
		// A closing tag inside a sentence is the sentence's.
		"I removed the </attached_files> line, is that right?": "I removed the </attached_files> line, is that right?",
		// No tags at all: untouched.
		"plain question about <T> generics": "plain question about <T> generics",
	}
	for in, want := range cases {
		if got := StripHarnessBlocks(in); got != want {
			t.Errorf("StripHarnessBlocks(%q) = %q, want %q", in, got, want)
		}
	}
}
