// Command semanticgap asks the question #2120 asks: does the embedding sidecar
// answer the questions the lexical ladder cannot, the ones where the answer and
// the question share no words.
//
// It seeds a store of ordinary work plus a handful of pairs whose question and
// answer are written in different vocabularies, then ranks each question two
// ways — through the production lexical ladder, and through the sidecar alone —
// and prints where the answering session landed in each.
//
// Needs an embedding endpoint (DEJA_EMBED_URL, or Ollama/LM Studio on their
// usual ports). Without one it says so and stops, because the number it would
// print instead is the lexical column twice.
//
//	go run ./scripts/semanticgap
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// pairs are written so the question and the session that answers it share no
// content word at all. That is the case the lexical ladder cannot reach by
// construction, and the only case where a sidecar earns its cost.
var pairs = []struct {
	id, session, question string
}{
	{"p1", "the nightly rollup was moved to 03:00 so the warehouse is quiet when it starts",
		"when does the aggregation job kick off now"},
	{"p2", "we cap uploads at 25 megabytes because the proxy in front buffers the whole body",
		"what is the largest file a user can attach"},
	{"p3", "the retry ladder is 1s, 4s, 16s and then it gives up and pages someone",
		"how many times does a failed delivery get another chance"},
	{"p4", "customers on the legacy plan keep their old seat price until they change tier",
		"do grandfathered accounts pay the new rate"},
	{"p5", "the mobile client refuses to start when the clock is more than five minutes off",
		"why would the phone app quit immediately on launch"},
	{"p6", "we write the audit trail to a separate volume so a full disk cannot lose it",
		"where do compliance records live"},
	{"p7", "the search box waits 250 milliseconds after the last keystroke before it asks",
		"how is typing debounced in the ui"},
	{"p8", "invoices are generated on the first working day, not the first calendar day",
		"when do bills go out each month"},
}

func main() {
	client, err := embed.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "semanticgap: no embedding endpoint —", err)
		os.Exit(1)
	}
	dir, err := seed()
	if err != nil {
		fmt.Fprintln(os.Stderr, "semanticgap:", err)
		os.Exit(1)
	}
	if _, err := embed.EmbedIndex(dir, client, nil); err != nil {
		fmt.Fprintln(os.Stderr, "semanticgap: embedding the store:", err)
		os.Exit(1)
	}
	sidecar, err := embed.Read(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "semanticgap: reading the sidecar:", err)
		os.Exit(1)
	}

	lexHits, semHits := 0, 0
	fmt.Printf("%-4s %-52s %10s %10s\n", "", "question", "lexical", "semantic")
	for _, p := range pairs {
		o := query.Options{Query: p.question, All: true}
		res, err := index.SearchDetailed(dir, o)
		if err != nil {
			fmt.Fprintln(os.Stderr, "semanticgap:", err)
			os.Exit(1)
		}
		lex := rankOf(res.Sessions, p.id)
		sem, served := -1, 0
		if hits, err := embed.SemanticSearch(context.Background(), dir, o, sidecar, client); err == nil {
			sem, served = rankOfHits(hits, p.id), len(hits)
		}
		_ = served
		if lex == 1 {
			lexHits++
		}
		if sem == 1 {
			semHits++
		}
		fmt.Printf("%-4s %-52s %10s %10s\n", p.id, cut(p.question, 52), place(lex), place(sem))
	}
	fmt.Printf("\nanswer ranked first: lexical %d of %d, semantic %d of %d\n",
		lexHits, len(pairs), semHits, len(pairs))
	fmt.Println("(a dash is \"not in the results at all\", not \"ranked low\")")
}

func place(rank int) string {
	if rank < 1 {
		return "—"
	}
	return fmt.Sprint(rank)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func rankOf(ss []model.Session, id string) int {
	for i, s := range ss {
		if s.ID == id {
			return i + 1
		}
	}
	return -1
}

func rankOfHits(hits []search.Hit, id string) int {
	for i, h := range hits {
		if h.Session.ID == id {
			return i + 1
		}
	}
	return -1
}

// seed writes the store: the pairs, and enough ordinary work that a question's
// words are not rare by accident. Written as transcripts and read through the
// ordinary ingest, so what is measured is the path a user has.
func seed() (string, error) {
	root, err := os.MkdirTemp("", "semanticgap")
	if err != nil {
		return "", err
	}
	claude := filepath.Join(root, "claude", "-tmp-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		return "", err
	}
	now := time.Now()
	write := func(id, text string, at time.Time) error {
		line, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": id, "cwd": "/tmp/app",
			"timestamp": at.UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(claude, id+".jsonl"), append(line, '\n'), 0o644)
	}
	for i := 0; i < 200; i++ {
		if err := write(fmt.Sprintf("noise%03d", i),
			fmt.Sprintf("weekly notes %d: deploys, dashboards, a flaky test and the usual queue backlog", i),
			now.Add(-time.Duration(i+len(pairs))*time.Hour)); err != nil {
			return "", err
		}
	}
	for i, p := range pairs {
		if err := write(p.id, p.session, now.Add(-time.Duration(i)*time.Hour)); err != nil {
			return "", err
		}
	}
	for key, value := range map[string]string{
		"HOME": root, "USERPROFILE": root,
		"DEJA_CLAUDE_ROOT": filepath.Join(root, "claude"),
		"DEJA_INDEX_DIR":   filepath.Join(root, "idx"),
		"DEJA_NOTES_FILE":  filepath.Join(root, "notes.jsonl"),
		"XDG_CONFIG_HOME":  filepath.Join(root, "config"),
	} {
		if err := os.Setenv(key, value); err != nil {
			return "", err
		}
	}
	dir := filepath.Join(root, "idx")
	if err := index.EnsureForSearch(dir, query.Options{Query: "", All: true}, true, io.Discard); err != nil {
		return "", err
	}
	return dir, nil
}
