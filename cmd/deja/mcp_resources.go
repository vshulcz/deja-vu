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
	// The first colon separates the harness from the id, the way the listing
	// writes it. No harness name carries one, so the first is always the right
	// one; an id that does keeps everything after it.
	id := ref
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		id = ref[i+1:]
	}
	// Every id has the empty string as a prefix, so a URI carrying no id at all
	// matched the first session and handed back a transcript nobody asked for —
	// with the requested URI echoed back, so nothing said which one it was
	// (#1728). `deja://session/` and `deja://session/claude:` both land here,
	// and resources/list only ever emits a full URI.
	if strings.TrimSpace(id) == "" {
		return nil, -32602, "resource uri carries no session id"
	}
	s, found, err := index.FindByPrefix(dir, id)
	if err != nil {
		return nil, -32603, err.Error()
	}
	if !found {
		// The id came from outside and this text lands in the model's context
		// beside deja's own, so it is bounded and defanged like the listing
		// entries above (#1729).
		return nil, -32602, fmt.Sprintf("no session matches %q", neutralizeFrameMarkers(safeForStatusline(id, mcpResourceNameMax)))
	}
	if !policy.Load().Allows(policy.ActivationMCP, s.Project) {
		return nil, -32602, "blocked by trust policy"
	}
	// Every door a person types says when a prefix reached more than one
	// session; this one picked the newest in silence, which is a wrong answer
	// an agent cannot see (#2388). On the CLI the note goes to stderr, beside
	// the transcript rather than inside it; here it goes above the recall
	// frame, so it reads as deja's own words and not as recalled text.
	note := ""
	pol := policy.Load()
	if n := index.PrefixMatchesAllowed(dir, id, func(project string) bool {
		return pol.Allows(policy.ActivationMCP, project)
	}); n > 1 {
		note = fmt.Sprintf("deja: %d sessions match %q — this is the most recent; ask for a longer prefix to read another.\n\n",
			n, neutralizeFrameMarkers(safeForStatusline(id, mcpResourceNameMax)))
	}
	// The same word the CLI and recall_context carry: this door takes an id,
	// so a reader here has named the session as plainly as anyone can (#1624).
	if line := forgottenSourceNote(s, id); line != "" {
		note += "deja: " + line + "\n\n"
	}
	var b bytes.Buffer
	search.PrintContext(&b, s, "")
	// Same transcript, same frame as recall_context. Reading a session through
	// the resources surface used to skip the untrusted-data wrapper and the
	// marker neutralisation entirely (#1077).
	text := note + frameRecall(b.String())
	// It also left no trace: a whole session reached the agent and `deja log`
	// stayed empty — the gap #682 closed for blame.
	usage.RecordServedFrom(dir, usage.KindResource, text, 1, rawSize([]model.Session{s}), []string{s.ID}, projectsOf(s), policy.Load().Describe(policy.ActivationMCP))
	return map[string]any{"contents": []map[string]any{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     text,
	}}}, 0, ""
}
