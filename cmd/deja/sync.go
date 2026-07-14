package main

import (
	"fmt"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

func runSync(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("sync needs export <dir> or import <dir>")
	}
	switch args[0] {
	case "export":
		if err := index.EnsureForSearch(index.DefaultDir(), search.Options{All: true}, false, os.Stderr); err != nil {
			return err
		}
		n, err := index.Export(index.DefaultDir(), args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "deja: exported %d records\n", n)
		return nil
	case "import":
		n, err := index.Import(index.DefaultDir(), args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "deja: imported %d records\n", n)
		return nil
	default:
		return fmt.Errorf("unknown sync command %q", args[0])
	}
}
