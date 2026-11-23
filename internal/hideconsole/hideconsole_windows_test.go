//go:build windows

package hideconsole

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
)

func TestApplyHidesTheWindow(t *testing.T) {
	cases := []struct {
		name  string
		start *syscall.SysProcAttr
		flags uint32
	}{
		{name: "no attributes yet", start: nil, flags: createNoWindow},
		{name: "keeps flags already set", start: &syscall.SysProcAttr{CreationFlags: 0x00000200}, flags: 0x00000200 | createNoWindow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(), "cmd", "/c", "exit", "0")
			cmd.SysProcAttr = tc.start
			Apply(cmd)
			if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
				t.Fatalf("HideWindow not set: %+v", cmd.SysProcAttr)
			}
			if cmd.SysProcAttr.CreationFlags != tc.flags {
				t.Fatalf("CreationFlags = %#x, want %#x", cmd.SysProcAttr.CreationFlags, tc.flags)
			}
		})
	}
}

func TestAppliedChildStillRuns(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd", "/c", "exit", "0")
	Apply(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
}
