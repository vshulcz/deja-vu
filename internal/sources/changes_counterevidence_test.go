package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
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
