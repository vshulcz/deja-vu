// Command fieldcover says, for every harness deja reads, how much of what the
// client recorded survives the parser: the share of sessions that carry a user
// turn, an assistant turn, tool output, commands, files, edits, a project and
// timestamps, and how long the load took.
//
// A parser that stops seeing one record type after a client update keeps
// returning sessions, so search still works and nothing fails; the share of
// sessions with commands or edits just drops to zero. This is the number that
// moves when that happens. It only reads the stores, through the same loaders
// the index uses, and never opens the index.
//
//	go run ./scripts/fieldcover            # table
//	go run ./scripts/fieldcover -json      # for diffing two runs
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

var roles = []string{"user", "assistant", sources.RoleToolOutput, sources.RoleCommand, sources.RoleFiles, sources.RoleEdit, sources.RoleWrote}

type row struct {
	Harness  string             `json:"harness"`
	Sessions int                `json:"sessions"`
	Messages int                `json:"messages"`
	Share    map[string]float64 `json:"share"`
	LoadMS   int64              `json:"load_ms"`
}

func main() {
	asJSON := flag.Bool("json", false, "print JSON")
	only := flag.String("harness", "", "one harness")
	flag.Parse()
	var rows []row
	for _, h := range sources.AllHarnesses() {
		if *only != "" && h.Name != *only {
			continue
		}
		t := time.Now()
		ss := h.Load()
		r := row{Harness: h.Name, Sessions: len(ss), Share: map[string]float64{}, LoadMS: time.Since(t).Milliseconds()}
		has := map[string]int{}
		for _, s := range ss {
			r.Messages += len(s.Messages)
			seen := map[string]bool{}
			stamped := false
			for _, m := range s.Messages {
				seen[m.Role] = true
				if !m.Time.IsZero() {
					stamped = true
				}
			}
			for _, k := range roles {
				if seen[k] {
					has[k]++
				}
			}
			if s.Project != "" {
				has["project"]++
			}
			if stamped || !s.Started.IsZero() {
				has["time"]++
			}
		}
		for _, k := range append(append([]string{}, roles...), "project", "time") {
			if r.Sessions > 0 {
				r.Share[k] = float64(has[k]) / float64(r.Sessions)
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Sessions > rows[j].Sessions })
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(rows)
		return
	}
	fmt.Printf("%-14s %6s %7s", "harness", "sess", "msgs")
	for _, k := range append(append([]string{}, roles...), "project", "time") {
		fmt.Printf(" %8s", k)
	}
	fmt.Printf(" %7s\n", "load")
	for _, r := range rows {
		fmt.Printf("%-14s %6d %7d", r.Harness, r.Sessions, r.Messages)
		for _, k := range append(append([]string{}, roles...), "project", "time") {
			if r.Sessions == 0 {
				fmt.Printf(" %8s", "-")
			} else {
				fmt.Printf(" %7.0f%%", 100*r.Share[k])
			}
		}
		fmt.Printf(" %6dms\n", r.LoadMS)
	}
}
