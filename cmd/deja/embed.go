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
	_, err = embed.EmbedIndex(dir, client, embedPolicyKeep(dir))
	return err
}

// embedPolicyKeep skips records the trust policy withholds from search, so
// embed does not ship a peer's imported content to the external endpoint under
// a deny that every read path already honours. A record whose session is not in
// the manifest is kept: unknown origin should not silently drop from search.
func embedPolicyKeep(dir string) func(index.Record) bool {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil
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
		return pol.Allows(policy.ActivationSearch, p)
	}
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
