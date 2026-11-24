//go:build windows

package proc

import "testing"

func TestCommandAppliesHideWindowOnWindows(t *testing.T) {
	exe := testExe(t)
	c := Command(exe, "alpha")
	if c.SysProcAttr == nil || !c.SysProcAttr.HideWindow {
		t.Fatalf("expected HideWindow=true on Windows for Command")
	}
	cc := CommandContext(t.Context(), exe, "beta")
	if cc.SysProcAttr == nil || !cc.SysProcAttr.HideWindow {
		t.Fatalf("expected HideWindow=true on Windows for CommandContext")
	}
}
