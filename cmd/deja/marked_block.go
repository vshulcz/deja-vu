package main

import (
	"fmt"
	"strings"
)

// replaceMarkedBlock takes every complete start…end block out of a file deja
// shares with its reader and, when block is not empty, appends block below what
// is left. Everything outside the markers is the reader's and comes back as it
// was. what names the block in a refusal ("guidance", "rules").
func replaceMarkedBlock(old, startMarker, endMarker, what, block string) (string, error) {
	newline := "\n"
	if strings.Contains(old, "\r\n") {
		newline = "\r\n"
	}
	// A marker without its pair means the block cannot be bounded. Appending a
	// fresh one left the file with two starts and one end, and the uninstall
	// after that cut from the first start to the only end — across the user's
	// own text — and deleted the file, because what was left was empty (#1705).
	if err := checkMarkerPairs(old, startMarker, endMarker, what); err != nil {
		return "", err
	}
	// Every complete block, not just the first: a file carrying two kept both
	// for ever, since the install removed one and appended one.
	for {
		start, end := markerLines(old, startMarker, endMarker)
		if start < 0 || end < 0 {
			break
		}
		// The blank line install put between their text and the block goes
		// with it. Install trims the trailing newlines and appends
		// `newline + newline + block`; cutting the block alone left that
		// separator behind, so a round trip gave the reader's AGENTS.md back
		// one line longer than it was — the same cost #2606 fixed on the goose
		// side from the other direction (#3703).
		if strings.HasSuffix(old[:start], newline+newline) {
			start -= len(newline)
		}
		old = old[:start] + old[end:]
	}
	if block == "" {
		return old, nil
	}
	old = strings.TrimRight(old, "\r\n")
	if old != "" {
		old += newline + newline
	}
	return old + strings.ReplaceAll(block, "\n", newline), nil
}

// checkMarkerPairs reports whether every start marker has an end marker after
// it. Only whole-line markers count, which is what markerLines pairs — a marker
// written inline in a sentence is prose, and deja appends its own block below
// such a file rather than claiming that text.
//
// A lone end marker is left alone for the same reason: it is what an inline
// start looks like to a line scanner, and appending below it costs nobody
// anything. A start with no end is different — the block cannot be bounded,
// and cutting from it to the next end takes whatever the user wrote in
// between, which is how an uninstall came to delete the file (#1705).
func checkMarkerPairs(doc, startMarker, endMarker, what string) error {
	open := false
	for _, line := range strings.Split(doc, "\n") {
		switch strings.TrimSuffix(line, "\r") {
		case startMarker:
			if open {
				return fmt.Errorf("deja's %s block has a start marker with no end marker after it — put the pair back, or delete the block entirely", what)
			}
			open = true
		case endMarker:
			open = false
		}
	}
	if open {
		return fmt.Errorf("deja's %s block has no end marker — put it back, or delete the block entirely", what)
	}
	return nil
}

// markerLines finds the first complete block: the byte offset where its start
// line begins and the one just past its end line, or -1 for either that is not
// there.
func markerLines(s, startMarker, endMarker string) (start, end int) {
	start, end = -1, -1
	offset := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		content := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if start < 0 && content == startMarker {
			start = offset
		} else if start >= 0 && content == endMarker {
			end = offset + len(line)
			break
		}
		offset += len(line)
	}
	return start, end
}
