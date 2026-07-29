// Command corpus writes the synthetic session history the README demo is
// recorded against.
//
// It exists so the demo is reproducible and so no real history is ever used to
// record it. The previous demo was made by hand from filler text that repeated
// one sentence twenty times, which showed that search returns many rows and
// nothing about why anyone would want them.
//
// The sessions below are written the way the harnesses write them, so the demo
// runs through the real parsers rather than a mock, and they are spread across
// eight months so the "history from before you installed deja" claim is visible
// rather than asserted.
//
//	go run ./scripts/demo -out /tmp/demo-home
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type turn struct {
	role string
	text string
}

type session struct {
	harness string
	project string
	id      string
	daysAgo int
	turns   []turn
}

// Written as real engineering conversations: a question, a diagnosis, and a
// decision worth recalling months later. That last part is the product.
var sessions = []session{
	{"claude", "payments", "a1f2c93b", 214, []turn{
		{"user", "prepared statements keep failing behind pgbouncer after the driver upgrade"},
		{"assistant", "pgx v5.5 changed prepared-statement caching, and pgbouncer in transaction mode cannot hold those across connections. Two options: set default_query_exec_mode to simple protocol, or pin the driver. We pinned pgx to 5.4.3 and left a note to revisit once pgbouncer 1.24 ships prepared-statement support."},
	}},
	{"claude", "payments", "b7d4e118", 203, []turn{
		{"user", "why is the idempotency key colliding on retries"},
		{"assistant", "The key was derived from the request body hash, and the client retried with a regenerated timestamp inside the body. Moved the key to a client-supplied header and made the server reject a retry that changes the body under the same key."},
	}},
	{"codex", "search-svc", "cx0031", 188, []turn{
		{"user", "the reindex job OOMs at about 80% every night"},
		{"assistant", "It loads the whole postings map before writing. Streaming the writes and flushing per bucket dropped peak RSS from 6.1 GB to 900 MB. The 80% point was simply where the largest tenant landed."},
	}},
	{"cursor", "web-ui", "cu0044", 171, []turn{
		{"user", "hydration mismatch only in production, never locally"},
		{"assistant", "The server rendered dates in UTC and the client in the browser locale. Locally both were UTC so it never showed. Fixed by formatting on the client after mount, and added a test that runs the renderer under TZ=Asia/Tokyo."},
	}},
	{"claude", "infra", "c9a01d55", 152, []turn{
		{"user", "should we move the queue off redis"},
		{"assistant", "We lost messages twice under memory pressure because eviction does not respect list keys. Decision: move the job queue to Postgres advisory locks, keep redis for caching only. Throughput fell from 12k/s to 4k/s, which is well above what we need."},
	}},
	{"codex", "infra", "cx0038", 140, []turn{
		{"user", "terraform wants to recreate the whole cluster"},
		{"assistant", "The node pool name changed because the module was bumped to v4, and that field forces replacement. Pinned the module and imported the existing pool instead of letting it recreate."},
	}},
	{"claude", "api-gateway", "d3f77a20", 121, []turn{
		{"user", "jwt refresh fails for about a minute after every deploy"},
		{"assistant", "Clock skew between the auth pods and the gateway, plus a 30s token lifetime. Added a 60s leeway on validation and moved to 15m tokens with refresh. The minute matched the rollout window."},
	}},
	{"cursor", "web-ui", "cu0051", 98, []turn{
		{"user", "bundle grew 400kb after the icon refactor"},
		{"assistant", "The barrel file re-exported every icon, so tree shaking gave up. Importing each icon by path brought it back to 210 kB. Added a lint rule banning barrel imports from that package."},
	}},
	{"claude", "ml-pipeline", "e5b28c04", 76, []turn{
		{"user", "feature parity check fails only for the batch path"},
		{"assistant", "Streaming used event time, batch used ingestion time, so late events landed in different windows. Standardised on event time and backfilled two days."},
	}},
	{"codex", "search-svc", "cx0052", 61, []turn{
		{"user", "why did we drop the trigram index"},
		{"assistant", "It was 40% of the database size and only helped queries shorter than four characters, which are 2% of traffic. We dropped it and added a length guard that falls back to a prefix scan."},
	}},
	{"claude", "payments", "f1c66b93", 44, []turn{
		{"user", "webhook signatures started failing for one provider"},
		{"assistant", "They rotated to a new signing key and publish both for 30 days. We were pinning a single key. Now we verify against every published key and log which one matched."},
	}},
	{"claude", "infra", "a8e30f71", 22, []turn{
		{"user", "the nightly backup restore test is flaky"},
		{"assistant", "The restore ran before the WAL archive finished uploading, so it occasionally restored a truncated tail. Added a wait on the archive marker rather than a fixed sleep."},
	}},
}

func main() {
	out := flag.String("out", "", "directory to write the fake home into")
	fill := flag.Int("fill", 380, "background sessions, so the demo shows a believable amount of history")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "corpus: -out is required")
		os.Exit(1)
	}
	// A fixed clock keeps the demo reproducible; the dates the demo shows are
	// relative to it.
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	all := append(background(*fill), sessions...)
	for _, s := range all {
		if err := write(*out, s, now); err != nil {
			fmt.Fprintln(os.Stderr, "corpus:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d sessions to %s\n", len(all), *out)
}

// background fills the store out to the size of a store someone has actually
// been working in. Without it the demo shows a dozen sessions and two-message
// conversations, which reads as a toy. The text is combinatorial rather than
// repeated: the previous demo printed one identical sentence on every row, and
// that is exactly what made it unconvincing.
func background(n int) []session {
	components := []string{"the scheduler", "the ingest worker", "the auth middleware", "the migration runner", "the cache layer", "the webhook consumer", "the rate limiter", "the report exporter", "the session store", "the search indexer"}
	symptoms := []string{"times out under load", "leaks goroutines after a reload", "double-writes on retry", "drops the trace id", "returns 500 on an empty body", "deadlocks during a rolling restart", "misses the last page of results", "rejects valid unicode", "keeps a stale connection", "silently truncates long fields"}
	causes := []string{"a context cancelled one layer too early", "an unbuffered channel nobody drained", "the retry used the same idempotency key", "the middleware ran after the logger", "a nil check that only fired in tests", "a pool sized from the wrong config key", "an offset computed before the filter", "a byte-wise compare on a multibyte string", "a keepalive shorter than the proxy timeout", "a column narrower than the payload"}
	fixes := []string{"passing the parent context through", "buffering and closing on the writer side", "deriving the key from the client header", "reordering the middleware chain", "covering the nil path in a test first", "reading the pool size from the same key as the pod limit", "applying the filter before truncating", "comparing runes rather than bytes", "lowering the keepalive under the proxy timeout", "widening the column and backfilling"}
	projects := []string{"payments", "infra", "search-svc", "web-ui", "api-gateway", "ml-pipeline", "billing", "notifications"}
	harnesses := []string{"claude", "codex", "cursor"}

	out := make([]session, 0, n)
	for i := 0; i < n; i++ {
		c := components[i%len(components)]
		sym := symptoms[(i/3)%len(symptoms)]
		cause := causes[(i/7)%len(causes)]
		fix := fixes[(i/7)%len(fixes)]
		turns := []turn{
			{"user", c + " " + sym},
			{"assistant", "Traced it to " + cause + ". Fixed by " + fix + "."},
		}
		// Real stores hold a few long sessions among the short ones, and the
		// stats screen reports the longest.
		if i%23 == 0 {
			for k := 0; k < 9+i%11; k++ {
				turns = append(turns,
					turn{"user", "still seeing it after " + fixes[(i+k)%len(fixes)]},
					turn{"assistant", "That path is different: " + causes[(i+k+3)%len(causes)] + ". Try " + fixes[(i+k+5)%len(fixes)] + "."})
			}
		}
		out = append(out, session{
			harness: harnesses[i%len(harnesses)],
			project: projects[(i/2)%len(projects)],
			id:      fmt.Sprintf("bg%04d", i),
			daysAgo: 4 + (i*331)%355,
			turns:   turns,
		})
	}
	return out
}

func write(root string, s session, now time.Time) error {
	start := now.AddDate(0, 0, -s.daysAgo)
	switch s.harness {
	case "claude":
		return writeClaude(root, s, start)
	case "codex":
		return writeCodex(root, s, start)
	default:
		return writeCursor(root, s, start)
	}
}

func writeClaude(root string, s session, start time.Time) error {
	path := filepath.Join(root, ".claude", "projects", s.project, s.id+".jsonl")
	var lines []byte
	for i, t := range s.turns {
		line, err := json.Marshal(map[string]any{
			"type":      t.role,
			"sessionId": s.id,
			"timestamp": start.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			"message":   map[string]any{"role": t.role, "content": t.text},
		})
		if err != nil {
			return err
		}
		lines = append(lines, line...)
		lines = append(lines, '\n')
	}
	return writeFile(path, lines)
}

func writeCodex(root string, s session, start time.Time) error {
	stamp := start.Format("2006-01-02T15-04-05")
	path := filepath.Join(root, ".codex", "sessions",
		start.Format("2006"), start.Format("01"), start.Format("02"),
		"rollout-"+stamp+"-"+s.id+".jsonl")

	rows := []map[string]any{{
		"timestamp": start.Format(time.RFC3339),
		"type":      "session_meta",
		"payload":   map[string]any{"session_id": s.id, "cwd": "/work/" + s.project},
	}}
	for i, t := range s.turns {
		kind := "input_text"
		if t.role == "assistant" {
			kind = "output_text"
		}
		rows = append(rows, map[string]any{
			"timestamp": start.Add(time.Duration(i+1) * time.Minute).Format(time.RFC3339),
			"type":      "response_item",
			"payload": map[string]any{
				"role":    t.role,
				"content": []any{map[string]any{"type": kind, "text": t.text}},
			},
		})
	}
	return writeLines(path, rows)
}

func writeCursor(root string, s session, start time.Time) error {
	path := filepath.Join(root, ".cursor", "projects", s.project, "agent-transcripts", s.id+".jsonl")
	var rows []map[string]any
	for _, t := range s.turns {
		rows = append(rows, map[string]any{
			"role":    t.role,
			"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": t.text}}},
		})
	}
	rows = append(rows, map[string]any{"type": "turn_ended", "status": "success"})
	if err := writeLines(path, rows); err != nil {
		return err
	}
	// Cursor transcripts carry no timestamps, so deja dates them by file mtime.
	// Setting it is what makes the demo store look like a real one rather than
	// one where every cursor session happened this morning.
	end := start.Add(time.Duration(len(s.turns)) * time.Minute)
	return os.Chtimes(path, end, end)
}

func writeLines(path string, rows []map[string]any) error {
	var out []byte
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return writeFile(path, out)
}

func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
