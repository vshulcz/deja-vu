package main

import "testing"

func TestSecretsCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "secrets", "--"}, []string{"--limit", "--json", "--scrub", "--dry-run"}, []string{"--re", "--harness", "--role"}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--scrub", "--dry-run"}},
	})
}
