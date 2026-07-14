package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type sessionStartHookResponse struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func runHookContext() error {
	digest := hookDigest()
	if digest == "" {
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "SessionStart"
	resp.HookSpecificOutput.AdditionalContext = digest
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(os.Stdout, string(b))
	return nil
}

func hookDigest() string {
	defer func() { _ = recover() }()
	dir := index.DefaultDir()
	if !index.HasManifest(dir) {
		return ""
	}
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	project := sources.ClaudeProjectName(cwd)
	ss, err := index.RecentProject(dir, project, 3)
	if err != nil || len(ss) == 0 {
		return ""
	}
	return search.AutoRecallDigest(ss, 2000)
}
