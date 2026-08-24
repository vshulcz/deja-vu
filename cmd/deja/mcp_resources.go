package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const mcpResourceLimit = 20

// mcpResourceNameMax bounds a listing entry. Titles are already truncated on
// ingest; this is the second bound, on text that reaches a model.
const mcpResourceNameMax = 80

// mcpResourcesList exposes recent sessions as browsable MCP resources, so an
// agent can look around without guessing search terms first.
func mcpResourcesList(dir string) (any, int, string) {
	// A browse is not worth a rebuild inside the handler: on a stale store
	// index.Ensure ran the whole build here, and on a cold one the answer was
	// a raw errno carrying an internal manifest path (#1306).
	if line := buildingNowForAgent(dir); line != "" {
		// An empty list, in the shape the protocol defines — the browse has
		// nothing to show yet, and the sentence belongs on the tools an agent
		// calls rather than in a result schema clients validate.
		return map[string]any{"resources": []map[string]any{}}, 0, ""
	}
	if _, err := index.EnsureForSearchStale(dir, search.Options{}, mcpProgress()); err != nil {
		return nil, -32603, err.Error()
	}
	ss, err := index.Recent(dir, mcpResourceLimit*2)
	if err != nil {
		return nil, -32603, err.Error()
	}
	pol := policy.Load()
	resources := make([]map[string]any, 0, mcpResourceLimit)
	for _, s := range ss {
		if !pol.Allows(policy.ActivationMCP, s.Project) {
			continue
		}
		// A title is the first thing someone typed into some session, which may
		// have arrived by sync, import or a shared repo. Hosts render this
		// field, so it gets the same treatment as the status bar — control and
		// format characters out — plus the frame markers defanged, because a
		// listing entry sits in the model's context beside deja's own text
		// (#1077).
		name := neutralizeFrameMarkers(safeForStatusline(s.Title, mcpResourceNameMax))
		if strings.TrimSpace(name) == "" {
			name = s.ID
		}
		desc := safeForStatusline(s.Project, mcpResourceNameMax)
		if !s.Updated.IsZero() {
			// The reader's zone, like every human surface since #849: an
			// agent quoting this date to the user must not name a different
			// day from the one on their screen (#856).
			desc += " · " + s.Updated.Local().Format("2006-01-02")
		}
		resources = append(resources, map[string]any{
			"uri":         "deja://session/" + s.Harness + ":" + s.ID,
			"name":        name,
			"description": desc,
			"mimeType":    "text/markdown",
		})
		if len(resources) >= mcpResourceLimit {
			break
		}
	}
	return map[string]any{"resources": resources}, 0, ""
}

// mcpResourceRead serves one session's digest by its deja://session/ URI.
func mcpResourceRead(dir, uri string) (any, int, string) {
	ref, ok := strings.CutPrefix(uri, "deja://session/")
	if !ok {
		return nil, -32602, "unknown resource uri"
	}
	id := ref
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		id = ref[i+1:]
	}
	// Every id has the empty string as a prefix, so an empty ref would make
	// FindByPrefix return whichever session it reaches first — a whole
	// transcript the agent never asked for, echoed back under the URI it did
	// send. `deja://session/` and `deja://session/claude:` both land here.
	// resources/list only ever emits full URIs, so nothing legitimate
	// arrives empty; refuse it exactly the way an unknown id is refused.
	if id == "" {
		return nil, -32602, fmt.Sprintf("no session matches %q", id)
	}
	s, found, err := index.FindByPrefix(dir, id)
	if err != nil {
		return nil, -32603, err.Error()
	}
	if !found {
		return nil, -32602, fmt.Sprintf("no session matches %q", id)
	}
	if !policy.Load().Allows(policy.ActivationMCP, s.Project) {
		return nil, -32602, "blocked by trust policy"
	}
	var b bytes.Buffer
	search.PrintContext(&b, s, "")
	// Same transcript, same frame as recall_context. Reading a session through
	// the resources surface used to skip the untrusted-data wrapper and the
	// marker neutralisation entirely (#1077).
	text := frameRecall(b.String())
	// It also left no trace: a whole session reached the agent and `deja log`
	// stayed empty — the gap #682 closed for blame.
	usage.RecordServedSessions(dir, usage.KindResource, len(text), 1, false, rawSize([]model.Session{s}), []string{s.ID})
	usage.SnapshotPolicy(dir, usage.KindResource, text, 1, policy.Load().Describe(policy.ActivationMCP))
	return map[string]any{"contents": []map[string]any{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     text,
	}}}, 0, ""
}
