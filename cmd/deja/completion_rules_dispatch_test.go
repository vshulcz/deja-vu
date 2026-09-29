package main

import "testing"

func TestRulesCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "rules", ""}, []string{"sync", "status", "candidates"}, []string{"--re", "--no-embed"}},
		{[]string{"deja", "rules", "candidates", "--"}, []string{"--json", "--limit", "--since"}, []string{"--re"}},
		{[]string{"deja", "rules", "sync", ""}, []string{}, []string{"--json", "--limit", "export", "import", "ssh"}},
		{[]string{"deja", "rules", "status", ""}, []string{}, []string{"--json", "--limit"}},
	})
}
