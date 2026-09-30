package main

import "testing"

func TestBenchCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "bench", ""}, []string{"recall", "context", "prompt", "block", "ingest", "read"}, []string{}},
		{[]string{"deja", "bench", "context", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "prompt", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "block", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "ingest", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "read", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "recall", "--"}, []string{"--json", "--seed"}, []string{}},
	})
}
