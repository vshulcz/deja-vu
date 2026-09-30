package main

import "testing"

func TestBlameCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "blame", "--"}, []string{"--attribution", "--git-note", "--json"}, []string{}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--attribution", "--git-note"}},
	})
}
