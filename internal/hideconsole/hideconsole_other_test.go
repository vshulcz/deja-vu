//go:build !windows

package hideconsole

import (
	"context"
	"os/exec"
	"testing"
)

func TestApplyLeavesTheCommandAsItIs(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	Apply(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatalf("SysProcAttr set outside Windows: %+v", cmd.SysProcAttr)
	}
}
