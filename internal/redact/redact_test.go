package redact

import (
	"strings"
	"testing"
)

// Secret-shaped fixtures are assembled at runtime so this file never contains
// token literals — GitHub push protection (rightly) blocks those.
func fake(prefix string, n int) string { return prefix + strings.Repeat("a1B2", n/4+1)[:n] }

func TestTextRedactsSupportedPatterns(t *testing.T) {
	jwt := "eyJ" + strings.Repeat("hA9", 8) + "." + "eyJ" + strings.Repeat("zQ4", 8) + "." + strings.Repeat("Kf7", 10)
	samples := []string{
		"AKIA" + strings.Repeat("ABCD", 4),
		"aws_secret_access_key=" + fake("", 32),
		"api_key=" + fake("", 16),
		"Bearer " + fake("", 24),
		"-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
		fake("ghp_", 24),
		fake("gho_", 24),
		fake("github_pat_", 24),
		fake("sk-", 24),
		fake("npm_", 32),
		"xoxb-123456789012-" + fake("", 12),
		"xoxp-123456789012-" + fake("", 12),
		"xoxc-123456789012-" + fake("", 12),
		fake("AIza", 32),
		jwt,
		"mysql://user:" + fake("", 13) + "@host/db",
	}
	in := strings.Join(samples, " ")
	out, counts := Text("before " + in + " after")
	if counts.Total() < 16 {
		t.Fatalf("counts=%#v out=%s", counts, out)
	}
	secrets := []string{
		"AKIA" + strings.Repeat("ABCD", 4), fake("", 32), fake("ghp_", 24),
		strings.Repeat("Kf7", 10), "xoxc-123456789012-" + fake("", 12),
	}
	for _, sec := range secrets {
		if strings.Contains(out, sec) {
			t.Fatalf("secret %q was not redacted from %q", sec, out)
		}
	}
	for _, keep := range []string{"before", "api_key=", "Bearer ", "mysql://user:", "@host/db", "after"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("surrounding text %q missing from %q", keep, out)
		}
	}
}

func TestDisabledEscapeHatch(t *testing.T) {
	t.Setenv("DEJA_NO_REDACT", "1")
	in := "api_key=" + fake("", 16)
	out, counts := Text(in)
	if out != in || counts.Total() != 0 || !Disabled() {
		t.Fatalf("escape hatch failed: out=%q counts=%#v", out, counts)
	}
}
