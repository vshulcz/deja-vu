package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Resume and handoff opened OpenClaw in `openclaw chat`, its local mode, where
// no plugin or hook runs, with a gateway right there whose TUI has deja's
// digest and recall (#5337). With a gateway answering they name
// `openclaw tui`; with none, `openclaw chat` and a line saying what it lacks.
func TestOpenClawResumeAndHandoffUseTheGatewayTUI(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
	dir := filepath.Join(tmp, "openclaw", "agents", "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := `{"agent:main:main":{"sessionId":"7de16196"}}`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(store), 0o644); err != nil {
		t.Fatal(err)
	}
	s := model.Session{Harness: "openclaw", ID: "7de16196", Project: "openclaw-main", Path: filepath.Join(dir, "7de16196.jsonl")}

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", ln.Addr())
	}
	port := strconv.Itoa(tcp.Port)
	saved := openclawGatewayUp
	t.Cleanup(func() { openclawGatewayUp = saved })
	openclawGatewayUp = openclawGatewayAnswers

	// gateway.port in openclaw.json is where the TUI connects.
	if err := os.WriteFile(openclawConfigPath(), []byte(`{"gateway": {"port": `+port+`}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, cmd, err := resumeCommand(s); err != nil || cmd != "openclaw tui --session agent:main:main" {
		t.Fatalf("with a gateway up, resume gave %q, %v", cmd, err)
	}
	if argv, _ := handoffCommand("openclaw", "P"); strings.Join(argv, " ") != "openclaw tui --message P" {
		t.Fatalf("with a gateway up, handoff gave %q", argv)
	}
	if _, note := openclawTUI(); note != "" {
		t.Errorf("a note with the gateway up: %s", note)
	}

	_ = ln.Close()
	if _, cmd, err := resumeCommand(s); err != nil || cmd != "openclaw chat --session agent:main:main" {
		t.Fatalf("with no gateway, resume gave %q, %v", cmd, err)
	}
	if argv, _ := handoffCommand("openclaw", "P"); strings.Join(argv, " ") != "openclaw chat --message P" {
		t.Fatalf("with no gateway, handoff gave %q", argv)
	}
	if _, note := openclawTUI(); !strings.Contains(note, "127.0.0.1:"+port) || !strings.Contains(note, "openclaw tui") {
		t.Errorf("the local-mode line does not name the gateway or the TUI: %s", note)
	}

	// The port OpenClaw is told in its environment wins over the config, and
	// a remote gateway is where the TUI goes in remote mode.
	t.Setenv("OPENCLAW_GATEWAY_PORT", "4242")
	if got := openclawGatewayAddr(); got != "127.0.0.1:4242" {
		t.Errorf("OPENCLAW_GATEWAY_PORT: %s", got)
	}
	if err := os.WriteFile(openclawConfigPath(), []byte(`{"gateway": {"mode": "remote", "remote": {"url": "wss://gw.example.test"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := openclawGatewayAddr(); got != "gw.example.test:443" {
		t.Errorf("remote mode: %s", got)
	}
	t.Setenv("OPENCLAW_GATEWAY_URL", "ws://127.0.0.1:5151")
	if got := openclawGatewayAddr(); got != "127.0.0.1:5151" {
		t.Errorf("OPENCLAW_GATEWAY_URL: %s", got)
	}
}
