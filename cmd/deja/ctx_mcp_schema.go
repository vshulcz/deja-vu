package main

// addCtxToolSchema runs only when DEJA_CTX_MCP=1. Explicit calls remain usable
// without advertising the experimental workflow to every history client.
func addCtxToolSchema(tool map[string]any) {
	tool["description"] = tool["description"].(string) + "\nctx_*: experimental local working-context checkpoints; use only when explicitly requested. History recall never requires a checkpoint."
	props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	mode := props["mode"].(map[string]any)
	mode["enum"] = append(mode["enum"].([]string), "ctx_resume", "ctx_status", "ctx_refresh", "ctx_checkpoint", "ctx_diff", "ctx_lookup", "ctx_explain", "ctx_invalidate", "ctx_history", "ctx_promote", "ctx_prune")
	for name, property := range map[string]any{
		"workspace":          map[string]any{"type": "string"},
		"task_id":            map[string]any{"type": "string"},
		"token_budget":       map[string]any{"type": "integer", "minimum": 1},
		"component_versions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		"state":              map[string]any{"type": "object"},
		"item_id":            map[string]any{"type": "string"},
		"layer":              map[string]any{"type": "string"},
		"source":             map[string]any{"type": "string"},
		"to":                 map[string]any{"type": "string", "enum": []string{"permanent", "project", "task", "ephemeral"}},
		"keep":               map[string]any{"type": "integer", "minimum": 1, "description": "ctx_prune: snapshots to retain for this workspace/task (default 100)."},
	} {
		props[name] = property
	}
}
