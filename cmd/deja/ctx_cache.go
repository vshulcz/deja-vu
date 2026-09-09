package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/ctxcache"
)

var ctxCacheCommands = map[string]bool{"resume": true, "refresh": true, "checkpoint": true, "status": true, "diff": true, "explain": true, "invalidate": true, "history": true, "lookup": true, "promote": true}

func isCtxCacheCommand(s string) bool { return ctxCacheCommands[s] }

type ctxOptions struct {
	workspace, task, layer, source, item, query, state, to string
	versions                                               map[string]string
	budget                                                 int
	json                                                   bool
}

func parseCtxOptions(args []string) (ctxOptions, error) {
	var o ctxOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			o.json = true
		case "--workspace", "--task", "--layer", "--source", "--item", "--query", "--state", "--budget", "--to", "--versions":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs a value", a)
			}
			i++
			v := args[i]
			switch a {
			case "--workspace":
				o.workspace = v
			case "--task":
				o.task = v
			case "--layer":
				o.layer = v
			case "--source":
				o.source = v
			case "--to":
				o.to = v
			case "--versions":
				if err := json.Unmarshal([]byte(v), &o.versions); err != nil || o.versions == nil {
					return o, fmt.Errorf("--versions needs an object of component names and version strings")
				}
			case "--item":
				o.item = v
			case "--query":
				o.query = v
			case "--state":
				o.state = v
			case "--budget":
				n, e := strconv.Atoi(v)
				if e != nil || n < 1 {
					return o, fmt.Errorf("--budget needs a positive integer")
				}
				o.budget = n
			}
		default:
			return o, fmt.Errorf("unknown ctx option %q", a)
		}
	}
	if o.layer != "" && o.source != "" {
		return o, fmt.Errorf("use either --layer or --source")
	}
	if o.source != "" {
		o.layer = "source:" + o.source
	}
	return o, nil
}

func runCtxCache(indexDir string, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("ctx needs a subcommand")
	}
	action := args[0]
	o, err := parseCtxOptions(args[1:])
	if err != nil {
		return err
	}
	id, err := ctxcache.ResolveIdentityWithVersions(o.workspace, o.task, o.versions)
	if err != nil {
		return err
	}
	root := ctxcache.Root(indexDir)
	write := func(v any) error {
		b, e := json.MarshalIndent(v, "", "  ")
		if e == nil {
			_, e = fmt.Fprintf(out, "%s\n", b)
		}
		return e
	}
	switch action {
	case "resume":
		r, e := ctxcache.Resume(root, id, o.budget)
		if e != nil {
			return e
		}
		b, e := ctxcache.EncodeResume(r)
		if e != nil {
			return e
		}
		_, e = out.Write(b)
		return e
	case "status":
		s, e := ctxcache.Inspect(root, id)
		if e != nil {
			return e
		}
		return write(s)
	case "refresh":
		r, e := ctxcache.RefreshDetailed(root, id)
		if e != nil {
			return e
		}
		return write(r)
	case "checkpoint":
		var b []byte
		if o.state != "" {
			b = []byte(o.state)
		} else {
			b, err = io.ReadAll(io.LimitReader(in, maxCtxCheckpointBytes+1))
			if err != nil {
				return err
			}
		}
		if len(b) > maxCtxCheckpointBytes {
			return fmt.Errorf("checkpoint exceeds %d bytes", maxCtxCheckpointBytes)
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			return fmt.Errorf("ctx checkpoint needs structured JSON on stdin or in --state")
		}
		state, err := ctxcache.DecodeState(b)
		if err != nil {
			return fmt.Errorf("decode checkpoint state: %w", err)
		}
		s, e := ctxcache.Checkpoint(root, id, state)
		if e != nil {
			return e
		}
		return write(s)
	case "history":
		h, e := ctxcache.History(root, id)
		if e != nil {
			return e
		}
		return write(h)
	case "diff":
		h, e := ctxcache.History(root, id)
		if e != nil {
			return e
		}
		if len(h) < 2 {
			return fmt.Errorf("ctx diff needs at least two snapshots")
		}
		return write(ctxcache.Diff(h[1], h[0]))
	case "invalidate":
		e := ctxcache.Invalidate(root, id, o.layer)
		if e != nil {
			return e
		}
		return write(map[string]any{"invalidated": valueOr(o.layer, "all")})
	case "explain":
		if o.item == "" {
			return fmt.Errorf("ctx explain needs --item")
		}
		r, e := ctxcache.Explain(root, id, o.item)
		if e != nil {
			return e
		}
		return write(r)
	case "promote":
		if o.item == "" || o.to == "" {
			return fmt.Errorf("ctx promote needs --item and --to")
		}
		s, e := ctxcache.Promote(root, id, o.item, o.to)
		if e != nil {
			return e
		}
		return write(s)
	case "lookup":
		if strings.TrimSpace(o.query) == "" {
			return fmt.Errorf("ctx lookup needs --query")
		}
		ctxcache.RecordLookup(root)
		return cmdCtx(indexDir, []string{"--", o.query})
	default:
		return errors.New("unknown ctx subcommand")
	}
}

const maxCtxCheckpointBytes = 8 << 20

// Unlike the general search limit, the context budget is a hard output bound.
// Reject present-but-zero, fractional and overflowing inputs instead of silently
// switching to the default or truncating the client's requested value.
func ctxMCPBudget(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, "\"") {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, fmt.Errorf("token_budget needs a positive integer")
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("token_budget needs a positive integer")
	}
	return n, nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
