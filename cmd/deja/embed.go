package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

func runEmbed(dir string, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("embed: unknown flag %q", args[0])
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return err
	}
	client, err := embed.New()
	if err != nil {
		return err
	}
	keep, held, err := embedPolicyKeep(dir)
	if err != nil {
		return err
	}
	_, err = embed.EmbedIndex(dir, client, keep)
	// Said out loud, because the alternative is a sidecar that quietly covers
	// less than the index and semantic search that quietly answers from less.
	if n := held(); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d record%s stayed here — the trust policy withholds them on at least one path, and embedding sends text off the machine (`deja doctor`)\n", n, pluralS(n))
	}
	return err
}

// embedPolicyKeep skips records the trust policy withholds on any activation.
// Embedding is the one operation that puts session text in front of a third
// party, and no activation describes it, so it takes the agreement of all three
// rather than borrowing `search` — which is usually the loosest of them, and
// under which a machine shipped text it refused to show its own agent (#1311).
// A record whose session is not in the manifest is kept: unknown origin should
// not silently drop from search.
func embedPolicyKeep(dir string) (func(index.Record) bool, func() int, error) {
	held := 0
	metas, err := index.AllMeta(dir)
	if err != nil {
		// A nil filter means "embed everything", so an unreadable manifest used
		// to open the gate wide at the one moment nothing could be checked. An
		// egress gate fails closed: no origins known, nothing leaves.
		return nil, nil, fmt.Errorf("embed: cannot read which sessions are local before sending text off the machine: %w", err)
	}
	project := make(map[string]string, len(metas))
	for _, m := range metas {
		project[m.Harness+":"+m.ID] = m.Project
	}
	pol := policy.Load()
	return func(r index.Record) bool {
			p, ok := project[r.Key]
			if !ok {
				return true
			}
			if pol.AllowsEgress(p) {
				return true
			}
			held++
			return false
		}, func() int {
			return held
		}, nil
}

func maybeRerank(dir string, hits []search.Hit, o search.Options, notice *os.File) []search.Hit {
	sidecar, err := embed.Read(dir)
	if err != nil {
		return hits
	}
	if embed.Stale(dir, sidecar) {
		return hits
	}
	client, err := embed.New()
	if err != nil {
		fmt.Fprintln(notice, "deja: semantic rerank unavailable; using lexical order")
		return hits
	}
	out, err := embed.Rerank(context.Background(), hits, o.Query, sidecar, client)
	if err != nil {
		fmt.Fprintln(notice, "deja: semantic rerank failed; using lexical order")
		return hits
	}
	return out
}

func maybeSemantic(dir string, hits []search.Hit, o search.Options, notice *os.File) ([]search.Hit, bool) {
	if len(hits) != 0 || o.NoEmbed || os.Getenv("DEJA_EMBED") == "off" {
		return hits, false
	}
	sidecar, err := embed.Read(dir)
	if err != nil {
		return hits, false
	}
	if embed.Stale(dir, sidecar) {
		return hits, false
	}
	client, err := embed.New()
	if err != nil {
		return hits, false
	}
	out, err := embed.SemanticSearch(context.Background(), dir, o, sidecar, client)
	if err != nil || len(out) == 0 {
		return hits, false
	}
	fmt.Fprintln(notice, "deja: no lexical match, semantic results:")
	return out, true
}
