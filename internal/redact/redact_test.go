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

// Modern hyphenated provider keys and the env-var / JSON key shapes that a bare
// `api_key=` pattern misses. Fixtures are assembled at runtime so no literal
// token ever lands in the source (GitHub push protection).
func TestTextRedactsModernKeysAndEnvJSONShapes(t *testing.T) {
	ant := fake("sk-ant-api03-", 40)
	proj := fake("sk-proj-", 32)
	cases := []struct {
		name   string
		in     string
		secret string
	}{
		{"anthropic bare in prose", "the key is " + ant + " use it", ant},
		{"anthropic env export", "export ANTHROPIC_API_KEY=" + ant, ant},
		{"anthropic env assign", "ANTHROPIC_API_KEY=" + ant, ant},
		{"anthropic json", `"ANTHROPIC_API_KEY": "` + ant + `"`, ant},
		{"x-api-key json header", `"x-api-key": "` + ant + `"`, ant},
		{"openai project key", "OPENAI_API_KEY=" + proj, proj},
		{"groq key", "GROQ_API_KEY=" + fake("gsk_", 32), fake("gsk_", 32)},
		{"xai key", "XAI_KEY=" + fake("xai-", 32), fake("xai-", 32)},
		{"huggingface token", "HF_TOKEN=" + fake("hf_", 32), fake("hf_", 32)},
		{"gitlab pat", "GITLAB=" + fake("glpat-", 24), fake("glpat-", 24)},
		{"stripe live secret", "using " + fake("sk_live_", 24) + " to charge", fake("sk_live_", 24)},
		{"stripe test secret", fake("sk_test_", 24), fake("sk_test_", 24)},
		{"github server token", "token " + fake("ghs_", 24), fake("ghs_", 24)},
		{"github user token", fake("ghu_", 24), fake("ghu_", 24)},
		{"aws temporary access key", "id=" + "ASIA" + strings.Repeat("WXYZ", 4), "ASIA" + strings.Repeat("WXYZ", 4)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, counts := Text(c.in)
			if strings.Contains(out, c.secret) {
				t.Fatalf("secret leaked: in=%q out=%q", c.in, out)
			}
			if counts.Total() == 0 {
				t.Fatalf("nothing redacted for %q", c.in)
			}
		})
	}
}

// A password containing '@' must be redacted whole, not just up to the first
// '@' (which would leave the rest of the password in the "host" portion).
func TestTextRedactsConnURLPasswordWithAt(t *testing.T) {
	pw := "p@ss@w0rd"
	in := "mysql://user:" + pw + "@dbhost:3306/app"
	out, counts := Text(in)
	if strings.Contains(out, "ss@w0rd") || strings.Contains(out, pw) {
		t.Fatalf("password fragment leaked: out=%q", out)
	}
	for _, keep := range []string{"mysql://user:", "@dbhost:3306/app"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("surrounding text %q missing from %q", keep, out)
		}
	}
	if counts.Total() == 0 {
		t.Fatalf("nothing redacted for %q", in)
	}
}

// Ordinary prose that merely mentions credential words must not be redacted:
// the value class + length floor should keep false positives out.
func TestTextKeepsOrdinaryProse(t *testing.T) {
	for _, s := range []string{
		"this is a token of my appreciation",
		"the secret to success is grit",
		"password reset link sent to your inbox",
		"authorization is pending review",
		"rebase the xai-oauth-correction-loop-retry branch first",
		"see docs/xai-rate-limit-troubleshooting-notes.md for details",
	} {
		out, counts := Text(s)
		if out != s || counts.Total() != 0 {
			t.Fatalf("false positive on %q: out=%q counts=%#v", s, out, counts)
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

func TestRedactionGapFixes(t *testing.T) {
	leaks := map[string]string{
		"empty-user redis url":  "redis://:s3cr3tpassword@cache.example.com:6379",
		"empty-user pg url":     "postgres://:mypassword123456@db:5432/app",
		"http basic auth":       "Authorization: Basic dXNlcm5hbWU6cGFzc3dvcmQxMjM0",
		"proxy basic auth":      "Proxy-Authorization: Basic YWJjZGVmZ2hpamtsbW5vcA==",
		"pgp armored key block": "-----BEGIN PGP PRIVATE KEY BLOCK-----\nabcdefghij\n-----END PGP PRIVATE KEY BLOCK-----",
	}
	for name, in := range leaks {
		if out, c := Text(in); out == in || c.Total() == 0 {
			t.Errorf("%s: not redacted: %q", name, out)
		}
	}
	realKeys := []string{
		"sk-abcdefghijklmnopqrstuvwxyz012345",
		"sk-proj-abcdefghijklmnopqrstuvwxyz012345",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz012345",
		"postgres://user:passw0rd1234567@host:5432/db",
	}
	for _, in := range realKeys {
		if out, _ := Text(in); out == in {
			t.Errorf("real secret missed: %q", in)
		}
	}
	prose := []string{
		"sk-my-really-long-feature-branch-name-here",
		"rebased the xai-oauth-correction-loop-retry branch",
		"just a basic english sentence with no secrets",
	}
	for _, in := range prose {
		if out, c := Text(in); out != in || c.Total() != 0 {
			t.Errorf("false positive on prose: %q -> %q", in, out)
		}
	}
}

func TestEntropyAssignmentRedacted(t *testing.T) {
	in := `DB_PASS=V9rT2xK8mQ4nW7jL5hP3sD1f`
	out, counts := Text(in)
	if !strings.Contains(out, "DB_PASS=[redacted:entropy]") || counts["entropy"] != 1 {
		t.Fatalf("assignment entropy missed: %q %v", out, counts)
	}
	in2 := `"private_key": "MIIEvQ2xK8mQ4nW7jL5hP3sD1fV9rT+ab/CDef=="`
	out2, counts2 := Text(in2)
	if !strings.Contains(out2, "[redacted:entropy]") || counts2["entropy"] != 1 {
		t.Fatalf("json entropy missed: %q %v", out2, counts2)
	}
}

func TestEntropyTelegramShape(t *testing.T) {
	in := `token is 8247579861:AAHrT2xK8mQ4nW7jL5hP3sD1fV9x`
	out, counts := Text(in)
	if counts["entropy"] != 1 || strings.Contains(out, "AAHrT2xK8mQ4nW7jL5hP3sD1fV9x") {
		t.Fatalf("telegram-shaped token missed: %q %v", out, counts)
	}
}

func TestEntropyStandaloneLineRedacted(t *testing.T) {
	in := "paste the key below:\n  VqrT2xK8mQ4nW7jL5hP3sD1fV9rT2xK8\nthanks"
	out, counts := Text(in)
	if counts["entropy"] != 1 || !strings.Contains(out, "[redacted:entropy]") {
		t.Fatalf("standalone entropy missed: %q %v", out, counts)
	}
}

func TestEntropyLeavesOrdinaryContentAlone(t *testing.T) {
	for _, in := range []string{
		"the function getUserAccountByIdentifier handles retries",
		"commit 3f1a9c27e4b8d6015a2f3c4d5e6f7a8b9c0d1e2f fixed it",
		"file=/private/tmp/claude-501/-users-shulcz/scratchpad/notes.txt",
		"moved to: L3Zhci9mb2xkZXJzL2puL2NsYXVkZS41MDEvLXVzZXJz",
		"id: 550e8400-e29b-41d4-a716-446655440000",
		"see https://github.com/vshulcz/deja-vu/releases/download/v0.14.1",
	} {
		out, counts := Text(in)
		if counts["entropy"] != 0 {
			t.Fatalf("false positive on %q -> %q %v", in, out, counts)
		}
	}
}

func TestEntropySkipsAlreadyRedacted(t *testing.T) {
	in := `api_key=sk-ant-abcdefghijklmnopqrstuvwxyz0123456789`
	out, counts := Text(in)
	if counts["entropy"] != 0 || strings.Count(out, "[redacted:") != 1 {
		t.Fatalf("double redaction: %q %v", out, counts)
	}
}
