package main

import (
	"encoding/json"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// toolsListBudget is the ceiling on the whole tools/list payload, in bytes.
//
// This is the one part of the MCP surface every session pays for whether or not
// it uses deja: the host reads the list before the agent does anything. Nothing
// was watching it, and it is the kind of cost that only ever grows — a sentence
// added to a description looks free at the point of writing and is charged on
// every request from then on, on every machine deja is wired into.
//
// The number is the payload as it stands with a little room, not an aspiration.
// It is deliberately tight: hitting it is supposed to force the question of
// whether the words belong in a description at all, rather than be raised a
// little each time. Long-form guidance about when to call a tool and how to
// report what it found is read once and belongs in the initialize handshake or
// a resource; the description should carry what the tool does and what its
// arguments mean.
const toolsListBudget = 7200

func TestToolsListStaysWithinItsTokenBudget(t *testing.T) {
	resp, _, _ := handleMCP(index.DefaultDir(), rpcRequest{Method: "tools/list"})
	payload, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("tools/list returned %T, not an object", resp)
	}
	whole, err := json.Marshal(payload["tools"])
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) > toolsListBudget {
		tools, _ := payload["tools"].([]map[string]any)
		t.Errorf("tools/list is %d bytes, over the %d-byte budget by %d (~%d tokens a session, on every session)",
			len(whole), toolsListBudget, len(whole)-toolsListBudget, (len(whole)-toolsListBudget)/4)
		for _, tool := range tools {
			one, err := json.Marshal(tool)
			if err != nil {
				continue
			}
			t.Logf("  %-16s %5d bytes  ~%4d tokens", tool["name"], len(one), len(one)/4)
		}
	}
}
