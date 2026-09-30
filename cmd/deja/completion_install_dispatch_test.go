package main

import "testing"

func TestInstallCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "install", "--"}, []string{"--no-index", "--force", "--no-guidance"}, []string{}},
		{[]string{"deja", "uninstall", "--"}, []string{"--no-guidance"}, []string{"--force", "--no-index"}},
	})
}
