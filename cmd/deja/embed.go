package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

func runEmbed(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("embed: unknown flag %q", args[0])
	}
	if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err != nil {
		return err
	}
	client, err := embed.New()
	if err != nil {
		return err
	}
	_, err = embed.EmbedIndex(index.DefaultDir(), client)
	return err
}

func maybeRerank(hits []search.Hit, o search.Options, notice *os.File) []search.Hit {
	sidecar, err := embed.Read(index.DefaultDir())
	if err != nil {
		return hits
	}
	gen, err := index.Generation(index.DefaultDir())
	if err != nil || gen != sidecar.Generation {
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
