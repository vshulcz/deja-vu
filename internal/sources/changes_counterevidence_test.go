package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func writeTempJSONL(t *testing.T, lines []any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "session.jsonl")
	var raw []byte
	for _, ln := range lines {
		b, err := json.Marshal(ln)
		if err != nil {
			t.Fatalf("marshal line: %v", err)
		}
		raw = append(raw, b...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	return p
}

func TestParseInlineBlocksAndMultilineClaudeChanges(t *testing.T) {
	p := writeTempJSONL(t, []any{
		map[string]any{
			"type":      "assistant",
			"sessionId": "s1",
			"message": map[string]any{
				"content": "Checking code.\n[thinking]\nLet me inspect and patch\n[tool_use Read] {\"file_path\":\"/testbed/pkg/alpha.py\"}\n[tool_use Edit] {\"file_path\":\"/testbed/pkg/alpha.py\",\"old_string\":\"return 1\",\"new_string\":\"return 2\"}",
			},
		},
		map[string]any{
			"type":      "user",
			"sessionId": "s1",
			"message": map[string]any{
				"content": "[tool_result]\n1: def f():\n[tool_result]\n<tool_use_error>String to replace not found",
			},
		},
		map[string]any{
			"type":      "assistant",
			"sessionId": "s1",
			"message": map[string]any{
				"content": "[thinking]\nRetry with exact text\n[tool_use Edit] {\"file_path\":\"/testbed/pkg/alpha.py\",\"old_string\":\"return 0\",\"new_string\":\"return 2\"}",
			},
		},
		map[string]any{
			"type":      "user",
			"sessionId": "s1",
			"message": map[string]any{
				"content": "[tool_result]\nThe file has been updated.",
			},
		},
	})

	cs := FinalChanges(Changes(model.Session{Harness: "claude", ID: "s1", Path: p}))
	if len(cs) != 1 {
		t.Fatalf("got %d changes, want 1: %#v", len(cs), cs)
	}
	if cs[0].Path != "pkg/alpha.py" || cs[0].Old != "return 0" || cs[0].New != "return 2" {
		t.Fatalf("unexpected change: %#v", cs[0])
	}
}

func TestUnpackAndFilterInlineSpeechMultiline(t *testing.T) {
	raw := "Plan step.\n[thinking]\nhidden note\nline 2\n[tool_use Edit] {\"file_path\":\"a.py\",\"old_string\":\"x\",\"new_string\":\"y\"}"
	blocks, _, speech, _ := unpackContentBlocks(raw)
	if len(blocks) != 1 {
		t.Fatalf("got %d unpacked blocks, want 1", len(blocks))
	}
	if speech != "Plan step." {
		t.Fatalf("got speech %q, want %q", speech, "Plan step.")
	}
	filtered, ok := filterInlineSpeechText(raw)
	if !ok || filtered != "Plan step." {
		t.Fatalf("got filtered=(%q, %v), want (%q, true)", filtered, ok, "Plan step.")
	}
}

func TestClaudeCommonDirFallbackAndScratchRejection(t *testing.T) {
	p := writeTempJSONL(t, []any{
		map[string]any{
			"type":      "assistant",
			"sessionId": "s2",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "tool_use", "id": "t1", "name": "Edit", "input": map[string]any{
						"file_path": "/Users/alex/repo/core/handler.py", "old_string": "a", "new_string": "b",
					}},
					map[string]any{"type": "tool_use", "id": "t2", "name": "Edit", "input": map[string]any{
						"file_path": "/Users/alex/repo/tests/test_handler.py", "old_string": "c", "new_string": "d",
					}},
					map[string]any{"type": "tool_use", "id": "t3", "name": "Edit", "input": map[string]any{
						"file_path": "/Users/alex/venv/lib/python3.11/site-packages/pkg/helper.py", "old_string": "e", "new_string": "f",
					}},
					map[string]any{"type": "tool_use", "id": "t4", "name": "Edit", "input": map[string]any{
						"file_path": "./swebench_tmp/run.py", "old_string": "g", "new_string": "h",
					}},
				},
			},
		},
	})

	cs := FinalChanges(Changes(model.Session{Harness: "claude", ID: "s2", Path: p}))
	if len(cs) != 2 {
		t.Fatalf("got %d changes, want 2: %#v", len(cs), cs)
	}
	if cs[0].Path != "core/handler.py" || cs[1].Path != "tests/test_handler.py" {
		t.Fatalf("unexpected cleaned paths: %#v", cs)
	}
}

func TestShellRevertAndTestFailureCounterevidence(t *testing.T) {
	p := writeTempJSONL(t, []any{
		map[string]any{
			"type":      "assistant",
			"sessionId": "s3",
			"cwd":       "/testbed",
			"message": map[string]any{
				"content": "[tool_use Edit] {\"file_path\":\"/testbed/app/reverted.py\",\"old_string\":\"bad1\",\"new_string\":\"bad2\"}\n[tool_use Edit] {\"file_path\":\"/testbed/app/kept.py\",\"old_string\":\"old\",\"new_string\":\"mid\"}",
			},
		},
		map[string]any{
			"type":      "user",
			"sessionId": "s3",
			"message": map[string]any{
				"content": "[tool_result]\nok\n[tool_result]\nok",
			},
		},
		map[string]any{
			"type":      "assistant",
			"sessionId": "s3",
			"message": map[string]any{
				"content": "[tool_use Bash] {\"command\":\"git checkout -b work && git checkout -- app/reverted.py\"}\n[tool_use Bash] {\"command\":\"pytest tests/test_app.py\"}",
			},
		},
		map[string]any{
			"type":      "user",
			"sessionId": "s3",
			"message": map[string]any{
				"content": "[tool_result]\nok\n[tool_result]\nExit code 1\nFAILED tests/test_app.py::test_x",
			},
		},
		map[string]any{
			"type":      "assistant",
			"sessionId": "s3",
			"message": map[string]any{
				"content": "[tool_use Edit] {\"file_path\":\"/testbed/app/kept.py\",\"old_string\":\"mid\",\"new_string\":\"final\"}",
			},
		},
		map[string]any{
			"type":      "user",
			"sessionId": "s3",
			"message": map[string]any{
				"content": "[tool_result]\nok",
			},
		},
	})

	raw := Changes(model.Session{Harness: "claude", ID: "s3", Path: p})
	cs := FinalChanges(raw)
	if len(cs) != 1 {
		t.Fatalf("got %d final changes, want 1: %#v", len(cs), cs)
	}
	if cs[0].Path != "app/kept.py" || cs[0].Old != "old" || cs[0].New != "final" {
		t.Fatalf("unexpected folded change: %#v", cs[0])
	}
	if !cs[0].TestFailed {
		t.Fatalf("expected TestFailed=true propagated across fold: %#v", cs[0])
	}

	// Now verify a later passing test clears TestFailed.
	p2 := writeTempJSONL(t, []any{
		map[string]any{"type": "assistant", "sessionId": "s4", "cwd": "/testbed", "message": map[string]any{"content": "[tool_use Edit] {\"file_path\":\"/testbed/app/ok.py\",\"old_string\":\"a\",\"new_string\":\"b\"}"}},
		map[string]any{"type": "user", "sessionId": "s4", "message": map[string]any{"content": "[tool_result]\nok"}},
		map[string]any{"type": "assistant", "sessionId": "s4", "message": map[string]any{"content": "[tool_use Bash] {\"command\":\"python tests/runtests.py auth\"}"}},
		map[string]any{"type": "user", "sessionId": "s4", "message": map[string]any{"content": "[tool_result]\nExit code 1\nFAILED (failures=1)"}},
		map[string]any{"type": "assistant", "sessionId": "s4", "message": map[string]any{"content": "[tool_use Edit] {\"file_path\":\"/testbed/app/ok.py\",\"old_string\":\"b\",\"new_string\":\"c\"}"}},
		map[string]any{"type": "user", "sessionId": "s4", "message": map[string]any{"content": "[tool_result]\nok"}},
		map[string]any{"type": "assistant", "sessionId": "s4", "message": map[string]any{"content": "[tool_use Bash] {\"command\":\"python tests/runtests.py auth\"}"}},
		map[string]any{"type": "user", "sessionId": "s4", "message": map[string]any{"content": "[tool_result]\nOK (0 failed)"}},
	})
	cs2 := FinalChanges(Changes(model.Session{Harness: "claude", ID: "s4", Path: p2}))
	if len(cs2) != 1 || cs2[0].TestFailed {
		t.Fatalf("expected verified passing change with TestFailed=false, got: %#v", cs2)
	}

	// Verify benign Python .pth startup tracebacks before a passing test do not mark TestFailed.
	if testOutputFailed("Error processing line 1 of /opt/pkg/lib/site-packages/demo.pth:\n\n  Traceback (most recent call last):\n    File \"site.py\", line 169\n  AttributeError: missing\n\nRemainder of file ignored\nRan 4 tests in 0.01s\nOK") {
		t.Fatalf("expected .pth startup warning followed by OK not to count as test failure")
	}
}

func TestCodexRevertAndTestCounterevidence(t *testing.T) {
	p := writeTempJSONL(t, []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": "/testbed"}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "call_id": "c1",
			"input": "*** Begin Patch\n*** Update File: mod.py\n@@\n-x = 1\n+x = 2\n*** End Patch",
		}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "c1", "output": "Exit code: 0\nDone",
		}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "function_call", "call_id": "c2", "name": "shell",
			"arguments": "{\"command\":\"pytest tests/test_mod.py\"}",
		}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "function_call_output", "call_id": "c2", "output": "Exit code: 1\n=== FAILURES ===",
		}},
	})
	cs := FinalChanges(Changes(model.Session{Harness: "codex", ID: "c1", Path: p}))
	if len(cs) != 1 || !cs[0].TestFailed {
		t.Fatalf("expected codex TestFailed=true, got: %#v", cs)
	}
}

func TestParseShellRevertVariants(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{
			cmd:  "rm -rf -- app/scratch.py -keep_after_dash.py",
			want: []string{"app/scratch.py"},
		},
		{
			cmd:  "env FOO=bar timeout 10 command rm -f app/tmp1.py > /dev/null 2>&1 2>err.log &>all.log",
			want: []string{"app/tmp1.py"},
		},
		{
			cmd:  "git -C /repo --work-tree /repo -p restore --source HEAD -W -S -- app/restored.py",
			want: []string{"app/restored.py"},
		},
		{
			cmd:  "git restore --staged app/staged_only.py",
			want: nil,
		},
		{
			cmd:  "git restore -s HEAD~1 app/prev.py",
			want: []string{"app/prev.py"},
		},
		{
			cmd:  "git checkout --orphan pages",
			want: nil,
		},
		{
			cmd:  "git checkout HEAD app/checked_out.py",
			want: []string{"app/checked_out.py"},
		},
		{
			cmd:  "git -p",
			want: nil,
		},
		{
			cmd:  "echo 'quoted \"esc\"' \\| cat < in.txt &> out.log; echo \"a\\\"b\" && rm app/q.py",
			want: []string{"app/q.py"},
		},
	}
	for _, tc := range cases {
		got := parseShellRevert(tc.cmd)
		if !slices.Equal(got, tc.want) {
			t.Errorf("parseShellRevert(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestIsTestCommandAndFailureOutputs(t *testing.T) {
	yes := []string{
		"pytest -q",
		"./runtests.py",
		"go test ./...",
		"npm test",
		"uv run pytest",
		"manage.py test",
		"./bin/test",
		"python -W ignore -m pytest",
		"python -m flake8 && python -u tests/test_core.py",
		"python manage.py test",
	}
	for _, cmd := range yes {
		if !isTestCommand(cmd) {
			t.Errorf("isTestCommand(%q) = false, want true", cmd)
		}
	}
	no := []string{
		"python -c 'print(1)'",
		"python script.py",
		"git status",
		"",
	}
	for _, cmd := range no {
		if isTestCommand(cmd) {
			t.Errorf("isTestCommand(%q) = true, want false", cmd)
		}
	}
	if testOutputFailed("") {
		t.Fatalf("empty test output must not count as failure")
	}
	if !testOutputFailed("<tool_use_error>boom") {
		t.Fatalf("expected tool_use_error to fail")
	}
	if !testOutputFailed("Exit code: 2") {
		t.Fatalf("expected nonzero Exit code: 2 to fail")
	}
	if !testOutputFailed("Ran 3 tests\nFAILED (errors=1)") {
		t.Fatalf("expected FAILED (errors=1) to fail")
	}
	if !testOutputFailed("summary: failures=0 errors=2") {
		t.Fatalf("expected nonzero errors=2 to fail")
	}
	if testOutputFailed("summary: failures=0 errors=0") {
		t.Fatalf("expected zero failures/errors not to fail")
	}
}

func TestStructuredClaudeAndScratchFiltering(t *testing.T) {
	if got := decodeClaudeMsgParts(nil); len(got) != 0 {
		t.Fatalf("decodeClaudeMsgParts(nil) = %d, want 0", len(got))
	}
	if got := decodeClaudeMsgParts(json.RawMessage("{bad")); len(got) != 0 {
		t.Fatalf("decodeClaudeMsgParts(bad) = %d, want 0", len(got))
	}
	strParts := decodeClaudeMsgParts(json.RawMessage(`"[tool_use Read] {\"file_path\":\"a.py\"}"`))
	if len(strParts) != 1 || strParts[0].typ != "tool_use" {
		t.Fatalf("expected inline string part, got %#v", strParts)
	}
	arrRaw := json.RawMessage(`[123, "[tool_use Bash] {\"command\":\"ls\"}", {"type":"text","content":[{"text":"[thinking] ok"}]}, {"type":"tool_result","tool_use_id":"x1","content":[{"text":"line1"},{"text":"line2"}]}, {"type":"tool_result","tool_use_id":"x2","text":"direct"}]`)
	arrParts := decodeClaudeMsgParts(arrRaw)
	if len(arrParts) != 3 {
		t.Fatalf("expected 3 decoded array parts, got %d: %#v", len(arrParts), arrParts)
	}
	if arrParts[1].text != "line1\nline2" || arrParts[2].text != "direct" {
		t.Fatalf("unexpected decoded tool_result texts: %#v", arrParts)
	}

	p := writeTempJSONL(t, []any{
		map[string]any{
			"type":      "assistant",
			"sessionId": "s5",
			"cwd":       "/testbed",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "tool_use", "id": "w1", "name": "Write", "input": map[string]any{"file_path": "/testbed/reproduce_issue.py", "content": "print(1)"}},
					map[string]any{"type": "tool_use", "id": "w2", "name": "Write", "input": map[string]any{"file_path": "/testbed/README.md", "content": "# Readme"}},
					map[string]any{"type": "tool_use", "id": "w3", "name": "Write", "input": map[string]any{"file_path": "/testbed/NOTES.md", "content": "scratch notes"}},
					map[string]any{"type": "tool_use", "id": "w4", "name": "Write", "input": map[string]any{"file_path": "/testbed/pkg/test_adhoc.py", "content": "assert True"}},
					map[string]any{"type": "tool_use", "id": "e_rev", "name": "Edit", "input": map[string]any{"file_path": "/testbed/pkg/reverted.py", "old_string": "a", "new_string": "b"}},
					map[string]any{"type": "tool_use", "id": "e_bad", "name": "Edit", "input": map[string]any{"file_path": "bad\npath", "old_string": "a", "new_string": "b"}},
					map[string]any{"type": "tool_use", "id": "m1", "name": "MultiEdit", "input": map[string]any{
						"file_path": "/testbed/pkg/keep.py",
						"edits": []any{
							map[string]any{"old_string": "old1", "new_string": "new1"},
						},
					}},
				},
			},
		},
		map[string]any{
			"type":      "assistant",
			"sessionId": "s5",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "tool_use", "id": "b_rev", "name": "Bash", "input": map[string]any{"command": "git restore pkg/reverted.py"}},
					map[string]any{"type": "tool_use", "id": "b_test", "name": "Bash", "input": map[string]any{"command": "go test ./..."}},
				},
			},
		},
		map[string]any{
			"type":      "user",
			"sessionId": "s5",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "b_rev", "content": []any{map[string]any{"text": "ok"}}},
					map[string]any{"type": "tool_result", "tool_use_id": "b_test", "content": []any{map[string]any{"text": "--- FAIL: TestKeep"}}},
				},
			},
		},
	})

	cs := FinalChanges(Changes(model.Session{Harness: "claude", ID: "s5", Path: p}))
	if len(cs) != 2 {
		t.Fatalf("got %d final changes, want 2: %#v", len(cs), cs)
	}
	if cs[0].Path != "README.md" || cs[1].Path != "pkg/keep.py" || !cs[1].TestFailed {
		t.Fatalf("unexpected structured changes: %#v", cs)
	}
}

func TestInlinePrettyJSONAndUnpackVariants(t *testing.T) {
	pretty := "[tool_use Edit]\n{\n  \"file_path\": \"a.py\",\n  \"old_string\": \"1\",\n  \"new_string\": \"2\"\n}\n[thinking]\ncheck\n[tool_result]\nExit code 2\nbad"
	blks, speech := splitInlineBlocks(pretty)
	if speech != "" || len(blks) != 3 {
		t.Fatalf("got %d blocks speech=%q, want 3 blocks", len(blks), speech)
	}
	if blks[0].typ != "tool_use" || blks[1].typ != "thinking" || blks[2].typ != "tool_result" || !blks[2].isErr {
		t.Fatalf("unexpected pretty inline blocks: %#v", blks)
	}

	rawArr := []any{
		"[tool_result]\nok",
		123,
		map[string]any{"type": "thinking", "thinking": "x"},
		map[string]any{"type": "tool_use", "name": "Read"},
		map[string]any{"type": "tool_result", "text": "out1"},
		map[string]any{"type": "text", "content": "hello"},
		map[string]any{"type": "other", "text": "note"},
	}
	unpacked, results, arrSpeech, onlyTool := unpackContentBlocks(rawArr)
	if onlyTool || len(unpacked) != 3 || len(results) != 2 || arrSpeech != "hello\nnote" {
		t.Fatalf("unexpected unpackContentBlocks output: blocks=%d results=%v speech=%q onlyTool=%v", len(unpacked), results, arrSpeech, onlyTool)
	}
}

func TestCodexShellArrayRevertAndJSONOutput(t *testing.T) {
	p := writeTempJSONL(t, []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": "/testbed"}},
		map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": "/ignored/fork"}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "call_id": "c1",
			"input": "*** Begin Patch\n*** Update File: a.py\n@@\n-x = 1\n+x = 2\n*** Update File: b.py\n@@\n-y = 1\n+y = 2\n*** End Patch",
		}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "local_shell_call", "call_id": "r1",
			"action": map[string]any{"command": []any{"bash", "-lc", "git restore b.py"}},
		}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "r1", "output": "Exit code: 0",
		}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "function_call", "call_id": "t1", "name": "exec_command",
			"arguments": `{"cmd":["pytest","-q"]}`,
		}},
		map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "function_call_output", "call_id": "t1",
			"output": map[string]any{"metadata": map[string]any{"exit_code": 0}, "stdout": "=== ERRORS ==="},
		}},
	})
	cs := FinalChanges(Changes(model.Session{Harness: "codex", ID: "c2", Path: p}))
	if len(cs) != 1 || cs[0].Path != "a.py" || !cs[0].TestFailed {
		t.Fatalf("expected only a.py with TestFailed=true, got: %#v", cs)
	}
}
