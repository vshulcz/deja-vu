package embed

import "strings"

// IsOllama reports whether endpoint is Ollama's /api/embed.
func IsOllama(endpoint string) bool {
	return strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/api/embed")
}
