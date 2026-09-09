package main

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
)

// mcpConn names the client this server process is answering.
//
// Every injection deja makes through a hook records the agent session it went
// to, and MCP recorded none: measured on this machine, 577 answers an agent
// asked for — 248 blame, 174 recall, 150 recall_context — and not one that
// could be paired with what the agent did next. So "most re-used" counted
// events, and could not tell a memory several agents came back to from one
// loop pulling the same session ninety times.
//
// MCP over stdio is one process per client, so the process is the client. The
// id is random rather than derived from anything about the host: nothing may
// join it to a transcript by accident, and it carries no path, user or machine
// name. It is minted per connection, so it means "this run of this agent" and
// not "this machine".
var mcpConn atomic.Value

func newMCPConn() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A connection with no id records as an injection with no receiver,
		// which is where this started — no reason to fail a recall over it.
		return ""
	}
	return "mcp:" + hex.EncodeToString(b[:])
}

// mcpConnID is the current connection's id, or "" before one is open — a tool
// called outside serveMCP, which the tests do.
func mcpConnID() string {
	s, _ := mcpConn.Load().(string)
	return s
}
