package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func tokenDigest(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func TestPolicyClientAuthScopesAndWrite(t *testing.T) {
	p := Policy{
		Write:          map[string]bool{"cli": true},
		RequireClients: true,
		Clients: map[string]ClientRule{
			"Claude-Code": {Read: boolPtr(true), Write: boolPtr(true), Projects: []string{"billing"}},
			"readonly":    {Read: boolPtr(true), Write: boolPtr(false), Projects: []string{"*"}},
			"revoked":     {Read: boolPtr(false), Write: boolPtr(true)},
		},
		ClientTokens: map[string]string{
			"Claude-Code": tokenDigest("secret-claude-tok"),
		},
	}

	if ok, reason := p.AllowsClientRead("mcp", "", "", "billing"); ok || reason == "" {
		t.Fatalf("empty client allowed: (%v, %q)", ok, reason)
	}
	if ok, reason := p.AllowsClientRead("mcp", "unknown", "", "billing"); ok || reason != "client not enrolled" {
		t.Fatalf("unknown client = (%v, %q)", ok, reason)
	}
	if ok, reason := p.AllowsClientRead("mcp", "claude-code", "", "billing"); ok || reason != "client token required" {
		t.Fatalf("missing token = (%v, %q)", ok, reason)
	}
	if ok, reason := p.AllowsClientRead("mcp", "claude-code", "wrong-tok", "billing"); ok || reason != "invalid client token" {
		t.Fatalf("bad token = (%v, %q)", ok, reason)
	}
	if ok, reason := p.AllowsClientRead("mcp", "claude-code", "secret-claude-tok", "other"); ok || reason != "project outside client scope" {
		t.Fatalf("out of scope project = (%v, %q)", ok, reason)
	}
	if ok, reason := p.AllowsClientRead("mcp", "claude-code", "secret-claude-tok", "billing"); !ok || reason != "" {
		t.Fatalf("enrolled scoped client denied: (%v, %q)", ok, reason)
	}

	pOpenTokens := p
	pOpenTokens.ClientTokens = nil
	if ok, reason := pOpenTokens.AllowsClientRead("mcp", "revoked", "", "billing"); ok || reason != "client read denied" {
		t.Fatalf("revoked read = (%v, %q)", ok, reason)
	}
	if d, ok := p.expectedClientToken("Claude-Code"); !ok || d == "" {
		t.Fatalf("exact client token lookup failed: (%q, %v)", d, ok)
	}
	if !pOpenTokens.AllowsClientProject("readonly", "anything") {
		t.Fatal("wildcard client project denied")
	}
	if !pOpenTokens.AllowsClientProject("revoked", "anything") || !pOpenTokens.AllowsClientProject("revoked", "") {
		t.Fatal("unrestricted client project denied")
	}

	// Write checks: RequireClients requires token for non-cli when ClientTokens is non-empty.
	if ok, reason := p.AllowsClientWrite("mcp", "readonly", "", "billing"); ok || reason != "client token required" {
		t.Fatalf("readonly without token write = (%v, %q)", ok, reason)
	}
	if ok, reason := pOpenTokens.AllowsClientWrite("mcp", "readonly", "", "billing"); ok || reason != "client write not granted" {
		t.Fatalf("readonly write = (%v, %q)", ok, reason)
	}
	if ok, reason := pOpenTokens.AllowsClientWrite("mcp", "claude-code", "", "other"); ok || reason != "project outside client scope" {
		t.Fatalf("scoped write out-of-scope = (%v, %q)", ok, reason)
	}
	if ok, reason := p.AllowsClientWrite("cli", "claude-code", "secret-claude-tok", "billing"); !ok || reason != "" {
		t.Fatalf("enrolled client write failed: (%v, %q)", ok, reason)
	}

	pOpenTokens.RequireClients = false
	if ok, reason := pOpenTokens.AllowsClientWrite("mcp", "readonly", "", "billing"); ok || reason != "client write denied" {
		t.Fatalf("explicit write:false denied check = (%v, %q)", ok, reason)
	}

	pSurfaceDeny := Policy{Write: map[string]bool{"mcp": false}}
	if ok, reason := pSurfaceDeny.AllowsClientWrite("mcp", "any", "", ""); ok || reason != "write denied on mcp" {
		t.Fatalf("surface write deny = (%v, %q)", ok, reason)
	}
	pWildcardDeny := Policy{Write: map[string]bool{"*": false}}
	if ok, reason := pWildcardDeny.AllowsClientWrite("cli", "any", "", ""); ok || reason != "write denied" {
		t.Fatalf("wildcard write deny = (%v, %q)", ok, reason)
	}
	if ok, _ := pWildcardDeny.AllowsClientWrite("cli", "bad", "x", ""); ok {
		t.Fatal("should still deny write")
	}
	// Auth failure before write gate
	if ok, _ := p.AllowsClientWrite("mcp", "unknown", "", "billing"); ok {
		t.Fatal("unauthenticated client allowed write")
	}
}

func TestDiagnoseWriteAndClientTokens(t *testing.T) {
	f := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", f)
	raw := `{
	  "write": {"cli": true, "badface": false},
	  "require_clients": true,
	  "clients": {"ok": {"read": true}},
	  "client_tokens": {"ok": "` + tokenDigest("t") + `", "bad": "not-sha256-hex!", "lenbad": "` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaG" + `"}
	}`
	if err := os.WriteFile(f, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	exists, unknown, err := Diagnose()
	if !exists || err != nil {
		t.Fatalf("Diagnose = (%v, %v, %v)", exists, unknown, err)
	}
	for _, want := range []string{"write.badface", "client_tokens.bad", "client_tokens.lenbad"} {
		if !slices.Contains(unknown, want) {
			t.Errorf("unknown %v missing %q", unknown, want)
		}
	}

	pDirect := Policy{
		RequireClients: true,
		Clients:        map[string]ClientRule{"ok": {Read: boolPtr(true)}},
		ClientTokens:   map[string]string{"ok": tokenDigest("tok")},
	}
	if ok, reason := pDirect.AllowsClientRead("mcp", "ok", "tok", ""); !ok || reason != "" {
		t.Fatalf("direct normalized token lookup = (%v, %q)", ok, reason)
	}
}
