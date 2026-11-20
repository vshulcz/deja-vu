package main

import (
	"os"
	"path/filepath"
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
	inRun := false
	runBlocks := 0
	for line := range strings.SplitSeq(runPart, "\n") {
		if strings.TrimSpace(line) == "run: |" {
			inRun = true
			runBlocks++
			continue
		}
		if !inRun {
			continue
		}
		if line != "" && !strings.HasPrefix(line, "        ") {
			inRun = false
			continue
		}
		if strings.Contains(line, "${{ inputs.") {
			t.Errorf("run script interpolates ${{ inputs.* }} directly instead of using env vars: %s", line)
		}
	}
	if runBlocks != 2 {
		t.Fatalf("run blocks = %d, want 2", runBlocks)
	}
}
