package main

import "testing"

func TestSyncCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "sync", ""}, []string{"export", "import", "ssh", "forget"}, []string{}},
		{[]string{"deja", "sync", "export", "--"}, []string{"--full", "--include-imported", "--peer"}, []string{"--both"}},
		{[]string{"deja", "sync", "ssh", "--"}, []string{"--pull", "--full", "--both"}, []string{"--include-imported", "--peer"}},
		{[]string{"deja", "sync", "forget", "--"}, []string{}, []string{"--list", "--pull", "--full", "--both", "--peer"}},
		{[]string{"deja", "sync", "import", "--"}, []string{}, []string{"--include-imported", "--peer", "--both"}},
	})
}
