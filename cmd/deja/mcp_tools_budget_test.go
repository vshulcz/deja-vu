package main

import (
	"encoding/json"
	"sort"
	"testing"
)

// Every session that wires deja's MCP server pays for tools/list before the
// agent does anything, whether or not it ever calls a tool. Six tools cost
// about 1700 tokens today, and most of that is prose in the descriptions rather
// than schema. The failure mode this pins is not one big commit — it is a tool
// added here, three sentences of guidance appended there, each too small to
// argue with, until the standing cost has doubled and nobody chose it.
//
// So the budget is on the whole payload, not per tool. Reaching more of deja
// from the agent is worth doing (#1174); paying for it linearly is not. If this
// fails, the question to ask is whether the new capability has to be a tool at
// all — `instructions` is read once, resources/list costs nothing here, and a
// hook already fires at the point of action.
const mcpToolsListCharBudget = 7200

func TestMCPToolsListStaysWithinItsTokenBudget(t *testing.T) {
	hermeticEnv(t)

	resp := driveMCP(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	res, ok := resp[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list returned no result: %#v", resp[0])
	}
	tools, ok := res["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list returned no tools: %#v", res)
	}

	payload, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= mcpToolsListCharBudget {
		return
	}

	// Say where it went. A bare "over budget" sends the next person to measure
	// by hand, which is how the number got stale in the first place.
	type sized struct {
		name string
		n    int
		desc int
	}
	var each []sized
	for _, ti := range tools {
		tool, _ := ti.(map[string]any)
		b, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		each = append(each, sized{name, len(b), len(desc)})
	}
	sort.Slice(each, func(i, j int) bool { return each[i].n > each[j].n })

	t.Errorf("tools/list is %d chars (~%d tokens) over a %d budget, paid every session by every wired agent",
		len(payload), (len(payload)-mcpToolsListCharBudget)/4, mcpToolsListCharBudget)
	for _, e := range each {
		t.Logf("  %-16s %5d chars (~%4d tokens), description %d", e.name, e.n, e.n/4, e.desc)
	}
}
