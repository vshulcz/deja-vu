package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// fakePiOnPath puts a pi command on PATH beside the npm package it starts,
// the layout npm leaves on Windows and the one piPackageVersion falls back to.
func fakePiOnPath(t *testing.T, pkg, version string) {
	t.Helper()
	bin := t.TempDir()
	name := "pi"
	if runtime.GOOS == "windows" {
		name = "pi.cmd"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(bin, "node_modules", filepath.FromSlash(pkg))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + pkg + `","version":"` + version + `"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func piMCPRow(t *testing.T) doctorMCPStatus {
	t.Helper()
	for _, r := range collectDoctorMCP() {
		if r.Name == "pi" {
			return r
		}
	}
	t.Fatal("no pi row")
	return doctorMCPStatus{}
}

// pi 0.99 reads ~/.pi/agent/mcp.json itself: `pi mcp list` on 1.3.0 shows
// deja connected with no adapter installed. Install still told the person to
// install pi-mcp-adapter and doctor called the row `no adapter`.
func TestPiBuiltinMCPReadsTheFile(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	fakePiOnPath(t, "@earendil-works/pi-coding-agent", "1.3.0")
	r, err := installPiAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if strings.Contains(r.Note, "pi-mcp-adapter") {
		t.Errorf("pi 1.3.0 reads mcp.json itself and install asks for the adapter: %q", r.Note)
	}
	if row := piMCPRow(t); row.State != "wired" {
		t.Errorf("pi 1.3.0 reads mcp.json itself and doctor says %q: %+v", row.State, row)
	}
}

// With the built-in extension switched off and no adapter, nothing reads the
// file, and the note names the setting that did it.
func TestPiBuiltinMCPSwitchedOff(t *testing.T) {
	for _, tc := range []struct {
		name, user, project string
		off                 bool
	}{
		{"user minus", `["-builtin:mcp"]`, "", true},
		{"user glob", `["!builtin:*"]`, "", true},
		{"glob brought back", `["!builtin:*", "+builtin:mcp"]`, "", false},
		{"minus beats plus", `["+builtin:mcp", "-builtin:mcp"]`, "", true},
		{"project minus", "", `["-builtin:mcp"]`, true},
		{"project brings it back", `["-builtin:mcp"]`, `["+builtin:mcp"]`, false},
		{"other builtin", `["-builtin:llama"]`, "", false},
		{"dot slash minus", `["-./builtin:mcp"]`, "", true},
		{"dot slash plus", `["!builtin:*", "+./builtin:mcp"]`, "", false},
		{"globstar", `["!**/*"]`, "", true},
		{"braces", `["!{builtin:mcp,builtin:llama}"]`, "", true},
		{"braces other", `["!{builtin:llama,builtin:codemode}"]`, "", false},
		{"untrusted project ignored", "", `["-builtin:mcp"]`, false},
		{"brace without comma is literal", `["!builtin:{mcp}"]`, "", false},
		{"class body is literal", `["![\\w]uiltin:mcp"]`, "", false},
		{"class", `["![ab]uiltin:mcp"]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			proj := t.TempDir()
			t.Chdir(proj)
			fakePiOnPath(t, "@earendil-works/pi-coding-agent", "1.3.0")
			write := func(p, list string) {
				if list == "" {
					return
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(`{"extensions": `+list+`}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(sources.PiConfigDir(), "settings.json"), tc.user)
			write(filepath.Join(proj, ".pi", "settings.json"), tc.project)
			if tc.project != "" && !strings.HasPrefix(tc.name, "untrusted") {
				resolved, _ := filepath.EvalSymlinks(proj)
				trust, _ := json.Marshal(map[string]bool{resolved: true})
				if err := os.MkdirAll(sources.PiConfigDir(), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sources.PiConfigDir(), "trust.json"), trust, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r, err := installPiAuto("/bin/deja", false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			row := piMCPRow(t)
			if tc.off {
				if !strings.Contains(r.Note, "built-in MCP is switched off") || row.State != "no-adapter" {
					t.Errorf("builtin:mcp is off and nothing reads mcp.json:\nnote %q\nrow %+v", r.Note, row)
				}
				return
			}
			if strings.Contains(r.Note, "switched off") || strings.Contains(r.Note, "pi-mcp-adapter") || row.State != "wired" {
				t.Errorf("pi's built-in MCP reads mcp.json here:\nnote %q\nrow %+v", r.Note, row)
			}
		})
	}
}

// pi moves its whole agent directory with PI_CODING_AGENT_DIR. deja kept
// writing ~/.pi/agent, so pi loaded neither the extension nor the MCP entry.
func TestPiAgentDirFollowsTheEnvironment(t *testing.T) {
	hermeticEnv(t)
	agent := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	if _, err := installPiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, p := range []string{filepath.Join(agent, "mcp.json"), filepath.Join(agent, "extensions", "deja.ts")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("PI_CODING_AGENT_DIR=%s and %s was not written: %v", agent, p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sources.Home(), ".pi")); err == nil {
		t.Errorf("install wrote ~/.pi while pi lives in %s", agent)
	}
	if got := sources.PiRoot(); got != filepath.Join(agent, "sessions") {
		t.Errorf("sessions read from %s, pi writes them under %s", got, agent)
	}
}

// A shim that hides the package (pnpm, Volta, a Nix wrapper) still says its
// version when asked, so an old pi behind one keeps the adapter note.
func TestPiVersionFromTheCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	hermeticEnv(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\necho 0.73.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if v := piPackageVersion(); v != "0.73.1" {
		t.Fatalf("version %q, the command says 0.73.1", v)
	}
	if note := piMCPUnreadNote(); !strings.Contains(note, "pi-mcp-adapter") {
		t.Errorf("pi 0.73.1 without the adapter and no note: %q", note)
	}
}

// PI_CODING_AGENT_SESSION_DIR moves pi's sessions alone.
func TestPiSessionDirFollowsTheEnvironment(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	if got := sources.PiRoot(); got != dir {
		t.Errorf("sessions read from %s, pi writes them to %s", got, dir)
	}
}

// The version shim's own output may carry a notice, and a wrapper that does
// not exec its child must not hold doctor past the timeout.
func TestPiVersionCommandShapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	for _, tc := range []struct{ name, script, want string }{
		{"notice after", "echo 0.73.1; echo 'Update available: 1.3.0'", "0.73.1"},
		{"noise before", "echo 'pi v0.73.1'", "0.73.1"},
		{"silent", ":", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\n"+tc.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if v := piPackageVersion(); v != tc.want {
				t.Errorf("version %q, want %q", v, tc.want)
			}
		})
	}
	t.Run("wrapper that never exits", func(t *testing.T) {
		hermeticEnv(t)
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nsleep 30; echo 0.73.1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		start := time.Now()
		piPackageVersion()
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("pi --version held the caller %s", d)
		}
	})
}
