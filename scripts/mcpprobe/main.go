// Command mcpprobe speaks MCP to deja and reports anything a client would
// choke on.
//
// Eight harnesses reach deja through this server, so a malformed reply breaks
// all of them at once — and it is the one surface the harness sweep cannot see,
// because a client only calls a tool when its model decides to. This drives the
// protocol directly: the handshake, the tool and resource listings, a real
// recall, and the inputs a client sends when something has gone wrong.
//
//	go run ./scripts/mkcorpus -out /tmp/corpus
//	deja index                       # with DEJA_CLAUDE_ROOT at /tmp/corpus/projects
//	go run ./scripts/mcpprobe -deja ./deja -want "47 days"
//
// Exits non-zero if anything failed, so it can gate a release.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type rpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type client struct {
	in   io.WriteCloser
	out  *bufio.Reader
	next int
}

func (c *client) send(method string, params any, notify bool) (rpc, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	if !notify {
		c.next++
		msg["id"] = c.next
	}
	b, _ := json.Marshal(msg)
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		return rpc{}, err
	}
	if notify {
		return rpc{}, nil
	}
	line, err := c.out.ReadString('\n')
	if err != nil {
		return rpc{}, fmt.Errorf("no reply to %s: %w", method, err)
	}
	var r rpc
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		return rpc{}, fmt.Errorf("%s returned something that is not JSON-RPC: %s", method, strings.TrimSpace(line))
	}
	return r, nil
}

var failures []string

func check(ok bool, what string) {
	if ok {
		fmt.Println("  ok   " + what)
		return
	}
	fmt.Println("  BAD  " + what)
	failures = append(failures, what)
}

func main() {
	deja := flag.String("deja", "deja", "the deja binary to speak to")
	want := flag.String("want", "", "a string the recall must contain (a fact only the corpus holds)")
	query := flag.String("query", "deploy token rotate", "what to recall")
	flag.Parse()

	cmd := exec.Command(*deja, "mcp")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	fatal(err)
	stdout, err := cmd.StdoutPipe()
	fatal(err)
	fatal(cmd.Start())
	c := &client{in: stdin, out: bufio.NewReader(stdout)}

	init, err := c.send("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mcpprobe", "version": "1"},
	}, false)
	fatal(err)
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities map[string]any `json:"capabilities"`
	}
	_ = json.Unmarshal(init.Result, &initResult)
	check(init.Error == nil, "initialize succeeds")
	check(initResult.ServerInfo.Name != "", "the server names itself")
	check(initResult.ProtocolVersion != "", "the server states a protocol version")

	_, _ = c.send("notifications/initialized", map[string]any{}, true)

	list, err := c.send("tools/list", map[string]any{}, false)
	fatal(err)
	var tools struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(list.Result, &tools)
	check(len(tools.Tools) > 0, "tools/list returns tools")
	for _, t := range tools.Tools {
		// A tool with no description is a tool a model will not choose, and a
		// schema that is not an object is one a client cannot render.
		check(t.Description != "", t.Name+" describes itself")
		check(t.InputSchema["type"] == "object", t.Name+" has an object input schema")
	}

	for _, t := range tools.Tools {
		if !strings.Contains(t.Name, "recall") {
			continue
		}
		r, err := c.send("tools/call", map[string]any{
			"name": t.Name, "arguments": map[string]any{"query": *query},
		}, false)
		fatal(err)
		body := string(r.Result)
		check(r.Error == nil, t.Name+" answers")
		check(strings.Contains(body, `"type":"text"`) || strings.Contains(body, `"type": "text"`),
			t.Name+" returns a text block")
		if *want != "" {
			check(strings.Contains(body, *want), t.Name+" found what only the corpus holds")
		}
	}

	// Everything a client does when something has gone wrong. A server that
	// dies here takes the whole session with it.
	unknown, err := c.send("tools/call", map[string]any{"name": "no_such_tool", "arguments": map[string]any{}}, false)
	fatal(err)
	check(unknown.Error != nil || strings.Contains(string(unknown.Result), "isError"),
		"an unknown tool is an error, not a crash")

	if len(tools.Tools) > 0 {
		_, err = c.send("tools/call", map[string]any{"name": tools.Tools[0].Name, "arguments": map[string]any{}}, false)
		check(err == nil, "missing arguments do not end the session")
	}

	if _, ok := initResult.Capabilities["resources"]; ok {
		res, err := c.send("resources/list", map[string]any{}, false)
		fatal(err)
		check(res.Error == nil, "resources/list answers, since the server advertises resources")
		var resources struct {
			Resources []struct {
				URI string `json:"uri"`
			} `json:"resources"`
		}
		_ = json.Unmarshal(res.Result, &resources)
		if len(resources.Resources) > 0 {
			read, err := c.send("resources/read", map[string]any{"uri": resources.Resources[0].URI}, false)
			fatal(err)
			check(read.Error == nil, "a listed resource can be read")
		}
		// What a hostile or confused client sends.
		for _, uri := range []string{"", "not-a-uri", "deja://session/../../etc/passwd", "deja://session/claude:nope"} {
			bad, err := c.send("resources/read", map[string]any{"uri": uri}, false)
			fatal(err)
			check(bad.Error != nil, fmt.Sprintf("resources/read refuses %q", uri))
		}
	}

	after, err := c.send("tools/list", map[string]any{}, false)
	fatal(err)
	check(after.Error == nil, "the server still answers after all of that")

	_ = stdin.Close()
	_ = cmd.Wait()

	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d problem(s):\n  %s\n", len(failures), strings.Join(failures, "\n  "))
		os.Exit(1)
	}
	fmt.Println("\nno problems")
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpprobe:", err)
		os.Exit(1)
	}
}
