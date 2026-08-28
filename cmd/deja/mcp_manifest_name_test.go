package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestMCPServerNamesStayConsistentAcrossManifests(t *testing.T) {
	root := filepath.Join("..", "..")
	var manifests []string
	var failures []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || isClaudeWorktreeDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		collectMCPServerNameFailures(rel, doc, &manifests, &failures)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(manifests)
	if len(manifests) != 7 {
		t.Fatalf("found %d JSON manifests with mcpServers objects, want 7: %v", len(manifests), manifests)
	}
	if len(failures) > 0 {
		t.Fatalf("MCP server names drifted:\n%s", strings.Join(failures, "\n"))
	}
}

func isClaudeWorktreeDir(path string) bool {
	return filepath.Base(path) == "worktrees" && filepath.Base(filepath.Dir(path)) == ".claude"
}

func collectMCPServerNameFailures(rel string, node any, manifests *[]string, failures *[]string) {
	switch v := node.(type) {
	case map[string]any:
		if servers, ok := v["mcpServers"].(map[string]any); ok {
			*manifests = append(*manifests, rel)
			names := make([]string, 0, len(servers))
			for name := range servers {
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) != 1 || names[0] != "deja" {
				*failures = append(*failures, fmt.Sprintf("%s declares %v, want [deja]", rel, names))
			}
		}
		for _, child := range v {
			collectMCPServerNameFailures(rel, child, manifests, failures)
		}
	case []any:
		for _, child := range v {
			collectMCPServerNameFailures(rel, child, manifests, failures)
		}
	}
}
