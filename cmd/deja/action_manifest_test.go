package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestActionManifest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "action.yml"))
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}
	src := string(b)

	required := []string{
		"name: Setup deja",
		"using: composite",
		"shell: bash",
		"default: latest",
		"args:",
		"checksums.txt",
		"deja-vu_${version}_${os}_${arch}.${ext}",
		`>> "$GITHUB_PATH"`,
		`echo "version=${tag}"`,
		`echo "binary-path=${install_dir}/${bin_name}"`,
		"steps.install.outputs.version",
		"steps.install.outputs.binary-path",
		"if: inputs.args != ''",
		"INPUT_VERSION: ${{ inputs.version }}",
		"INPUT_ARGS: ${{ inputs.args }}",
	}
	for _, want := range required {
		if !strings.Contains(src, want) {
			t.Errorf("action.yml missing %q", want)
		}
	}

	// Guard against workflow script injection: inputs.* must flow through env vars, never inside run blocks.
	_, runPart, hasRuns := strings.Cut(src, "\nruns:\n")
	if !hasRuns {
		t.Fatal("action.yml missing runs: section")
	}
	runBlockRe := regexp.MustCompile(`(?ms)^\s+run:\s*\|\n(.*?)(?:^\s{4}- |\z)`)
	for _, m := range runBlockRe.FindAllStringSubmatch(runPart, -1) {
		if strings.Contains(m[1], "${{ inputs.") {
			t.Errorf("run script interpolates ${{ inputs.* }} directly instead of using env vars:\n%s", m[1])
		}
	}
}
