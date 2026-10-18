package main

import (
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// `openclaw chat` is `openclaw tui --local`: the embedded runtime, which
// fires no plugin or hook, so a session resumed or handed off there gets no
// digest, per-prompt recall or live stamp (OpenClaw 2026.10.4, #5337).
// `openclaw tui` takes the same --session and --message (one runTui, one
// session-key resolver, 2026.7.1-2) and runs on the gateway, which has both;
// with no gateway to connect to it only waits. So the gateway TUI when one
// answers, the local mode with a line saying what it lacks when none does.

// openclawGatewayAddr is the host:port `openclaw tui` connects to, resolved
// the way OpenClaw resolves it: OPENCLAW_GATEWAY_URL, then gateway.remote.url
// in remote mode, then ws://127.0.0.1 on OPENCLAW_GATEWAY_PORT, gateway.port
// or 18789.
func openclawGatewayAddr() string {
	root, _ := openclawConfigResolved()
	raw := strings.TrimSpace(os.Getenv("OPENCLAW_GATEWAY_URL"))
	if raw == "" && jsonAt(root, "gateway", "mode") == "remote" {
		raw, _ = jsonAt(root, "gateway", "remote", "url").(string)
		raw = strings.TrimSpace(raw)
	}
	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return ""
		}
		port := u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "wss" || u.Scheme == "https" {
				port = "443"
			}
		}
		return net.JoinHostPort(u.Hostname(), port)
	}
	port := 18789
	if p, ok := jsonAt(root, "gateway", "port").(float64); ok && p > 0 && p < 65536 {
		port = int(p)
	}
	if p, err := strconv.Atoi(strings.TrimSpace(os.Getenv("OPENCLAW_GATEWAY_PORT"))); err == nil && p > 0 && p < 65536 {
		port = p
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// openclawGatewayUp reports whether something accepts a connection where
// `openclaw tui` would connect; asked once per run, a refused loopback port
// answers at once.
var openclawGatewayUp = sync.OnceValue(openclawGatewayAnswers)

func openclawGatewayAnswers() bool {
	addr := openclawGatewayAddr()
	if addr == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// openclawTUI is the subcommand that opens an OpenClaw session: "tui" when a
// gateway answers, where deja's plugin and hooks run, else "chat" and the
// line saying what the local mode leaves out.
func openclawTUI() (sub, note string) {
	if openclawGatewayUp() {
		return "tui", ""
	}
	return "chat", openclawLocalOnlyNote()
}

// openclawLocalOnlyNote names the gateway that did not answer and what the
// local mode leaves out.
func openclawLocalOnlyNote() string {
	where := openclawGatewayAddr()
	if where == "" {
		where = "the configured gateway URL"
	}
	return "no OpenClaw gateway answers at " + where + ", so this opens OpenClaw's local mode, which runs no deja plugin or hook — no digest or recall there; start the gateway (`openclaw gateway`) and run it as `openclaw tui` to have both"
}
