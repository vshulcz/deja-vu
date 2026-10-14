package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// windsurfMCPPath is Cascade's MCP file, beside the conversations under the
// Windsurf / Devin Desktop config root, in the {"mcpServers": …} shape. Not
// the Code OSS-level User/mcp.json that `devin-desktop --add-mcp` writes:
// Cascade does not read that one (#308).
func windsurfMCPPath() string {
	return filepath.Join(sources.WindsurfRoot(), "mcp_config.json")
}

func installWindsurf(exe string, uninstall bool) (installResult, error) {
	return installMCPJSON(windsurfMCPPath(), exe, uninstall)
}
