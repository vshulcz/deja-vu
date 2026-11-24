package redact

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

func TestScanKindsIgnoresDisabledAndDetectsCredentials(t *testing.T) {
	t.Setenv("DEJA_NO_REDACT", "1")
	if !Disabled() {
		t.Fatal("expected DEJA_NO_REDACT=1 to disable Text redaction")
	}

	antKey := "sk-ant-" + "api03-1234567890abcdefghij1234567890abcdefghij"
	ghTok := "ghp_" + "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890"
	awsKey := "AKIA" + "IOSFODNN7EXAMPLE77"
	bearerTok := "Bearer " + "tok_live_9876543210abcdef9876543210"
	pem := "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAnExampleLineOne\n-----END " + "RSA PRIVATE KEY-----"
	sample := "export key=" + antKey + "\n\x1b[31m" + ghTok + "\x1b[0m\n" + awsKey + "\nAuthorization: " + bearerTok + "\n" + pem

	allow := map[string]bool{
		"anthropic-key":  true,
		"github-token":   true,
		"aws-access-key": true,
		"bearer-token":   true,
		"private-key":    true,
	}
	counts := ScanKinds(sample, allow)
	for _, want := range []string{"anthropic-key", "github-token", "aws-access-key", "bearer-token", "private-key"} {
		if counts[want] < 1 {
			t.Errorf("ScanKinds missing %s (got %v)", want, counts)
		}
	}
	if got, c := Text(sample); got != sample || c.Total() != 0 {
		t.Fatalf("Text must remain disabled when DEJA_NO_REDACT=1; got %d counts", c.Total())
	}
}

func TestScanKindsSkipsDisabledPasses(t *testing.T) {
	namedAllow := map[string]bool{
		"aws-access-key":     true,
		"aws-secret":         true,
		"aws-key":            true,
		"openai-key":         true,
		"anthropic-key":      true,
		"google-api-key":     true,
		"github-token":       true,
		"gitlab-token":       true,
		"groq-key":           true,
		"xai-key":            true,
		"npm-token":          true,
		"slack-token":        true,
		"huggingface-token":  true,
		"stripe-key":         true,
		"telegram-bot-token": true,
		"provider-token":     true,
		"private-key":        true,
		"jwt":                true,
		"bearer-token":       true,
		"cookie":             true,
		"url-credentials":    true,
		"webhook-url":        true,
		"command-password":   true,
		"password":           true,
	}

	antKey := "sk-ant-" + "api03-1234567890abcdefghij1234567890abcdefghij"
	ghTok := "ghp_" + "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890"
	awsKey := "AKIA" + "IOSFODNN7EXAMPLE77"
	awsSec := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	bearerTok := "Bearer " + "tok_live_9876543210abcdef9876543210"
	pem := "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAnExampleLineOne\n-----END " + "RSA PRIVATE KEY-----"
	jwtTok := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6y"
	pgURL := "postgres://" + "svc:hunter2" + "@db.example:5432/app"
	hookURL := "https://hooks.slack.com/" + "services/T00000000/B00000000/X9aB2cD4eF6gH8jK0lM2nP4q"
	tgTok := "123456789:AAbcdefghij1234567890abcdefghij1234"
	cmdLine := "sshpass -p hunter22 ssh deploy@203.0.113.10"
	cookieLine := "Cookie: session=3f9a2c1e8b7d6a5f4e3d2c1b"
	netrcLine := "machine api.example.com login deploy password hunter2"

	sample := strings.Join([]string{
		"пароль: очень_секретное_значение_2026",
		`api_key "shortsecret1"`,
		"DATABASE_PASSWORD=correct-horse-battery",
		"qW8zK4mN9pR2vT7xY1bC6dF3gH5jL0A9",
		antKey,
		ghTok,
		awsKey,
		"AWS_SECRET_ACCESS_KEY=" + awsSec,
		bearerTok,
		pem,
		jwtTok,
		pgURL,
		hookURL,
		tgTok,
		cmdLine,
		cookieLine,
		netrcLine,
	}, "\n")

	beforeIntl := intlPatternRuns.Load()
	counts := ScanKinds(sample, namedAllow)
	afterIntl := intlPatternRuns.Load()

	if afterIntl != beforeIntl {
		t.Fatalf("ScanKinds ran international assignment regex when credential is disabled (before=%d after=%d)", beforeIntl, afterIntl)
	}
	for _, disabled := range []string{"credential", "quoted-secret", "entropy"} {
		if counts[disabled] != 0 {
			t.Errorf("ScanKinds counted disabled kind %s=%d", disabled, counts[disabled])
		}
	}
	wantNamed := []string{
		"anthropic-key", "github-token", "aws-access-key", "aws-secret",
		"bearer-token", "private-key", "jwt", "url-credentials",
		"webhook-url", "telegram-bot-token", "command-password", "cookie", "password",
	}
	for _, want := range wantNamed {
		if counts[want] < 1 {
			t.Errorf("ScanKinds missing %s (got %v)", want, counts)
		}
	}
}

func TestHasProviderHintCoversAllPrefixes(t *testing.T) {
	wantTrue := []string{
		"ghp_", "gho_", "ghs_", "ghu_", "ghr_", "github_pat_",
		"glpat-", "sk_live_", "rk_test_", "sk-proj-", "gsk_",
		"xai-", "hf_", "npm_", "xoxb-", "xoxp-", "xoxc-", "xoxs-", "AIza",
	}
	for _, prefix := range wantTrue {
		if !hasProviderHint(prefix) {
			t.Errorf("hasProviderHint(%q) = false, want true", prefix)
		}
		wrapped := "export TOKEN=" + prefix + "0123456789abcdefghij"
		if !hasProviderHint(wrapped) {
			t.Errorf("hasProviderHint(%q) = false, want true", wrapped)
		}
	}

	wantFalse := []string{
		"thought_process", "my-var", "xox", "pat_", "",
		"task_id", "ask_question", "work_dir", "disk-usage",
		"my_sk_live_foo", "sk_other", "fooAIza012345678901234567890123456789",
		"xAIz", "zzAIz", "-", "_",
	}
	for _, clean := range wantFalse {
		if hasProviderHint(clean) {
			t.Errorf("hasProviderHint(%q) = true, want false", clean)
		}
	}

	rng := rand.New(rand.NewSource(20261124)) //nolint:gosec // seeded so a failure reproduces
	parts := []string{
		"ghp_", "gho_", "ghs_", "ghu_", "ghr_", "ghx_", "gh", "github_pat_",
		"glpat-", "sk_live_", "sk_test_", "rk_live_", "rk_test_", "sk-", "gsk_", "xai-",
		"hf_", "npm_", "xoxb-", "xoxp-", "xoxc-", "xoxs-", "AIza",
		"task-", "work_", "disk-", "my_", "foo", "ant-", "proj-",
		"abcdefghijklmnopqrst", "abcdefghijklmnopqrstuvwxyz0123456789",
		"0123456789012345", " ", "\n", "-", "_", ".",
	}
	for range 20000 {
		var b strings.Builder
		for n := rng.Intn(8); n >= 0; n-- {
			b.WriteString(parts[rng.Intn(len(parts))])
		}
		s := b.String()
		if providerRE.FindStringIndex(s) != nil && !hasProviderHint(s) {
			t.Fatalf("hasProviderHint skipped a real match: %q", s)
		}
	}
}

func TestPreGateSoundness(t *testing.T) {
	t.Run("asciiLower", func(t *testing.T) {
		exactCases := []string{
			"",
			"plain lowercase ascii",
			"UPPERCASE_AND_Mixed-123",
			"проверка UTF-8 и emoji 🚀 Bearer TOK_12345",
			"日本語テスト AWS_SECRET_ACCESS_KEY=XYZ",
			"coo\u212aie: session=abcdef",
			"x\u212ay token",
			"bas\u0130c abcdefghijklmnop",
		}
		for _, tc := range exactCases {
			if got, want := asciiLower(tc), strings.ToLower(tc); got != want {
				t.Errorf("asciiLower(%q) = %q, want %q", tc, got, want)
			}
		}

		// Uppercase Cyrillic stays untouched while ASCII substrings still lowercase.
		mixed := "Проверка UTF-8 и Bearer TOK_12345"
		got := asciiLower(mixed)
		if !strings.HasPrefix(got, "Проверка ") {
			t.Errorf("asciiLower(%q) altered non-ASCII prefix: %q", mixed, got)
		}
		for _, sub := range []string{"utf-8", "bearer", "tok_12345"} {
			if !strings.Contains(got, sub) {
				t.Errorf("asciiLower(%q) = %q missing ASCII substring %q", mixed, got, sub)
			}
		}
	})

	t.Run("containsAnchored", func(t *testing.T) {
		haystacks := []string{
			"", "J", "eyJ", "xxeyJ", "JeyJ", "eyJxx", "eyeyJ", "AAAAKIA", "AASASIA",
			"cookie:", "xCookie: a", "authoriz", "Authorization: token 1", "passw", "machine a password b",
		}
		subs := []struct {
			sub    string
			anchor int
		}{
			{"eyJ", 2},
			{"uthoriz", 6},
			{"AKIA", 1},
			{"ASIA", 2},
			{"passw", 4},
			{"cookie:", 3},
		}
		for _, h := range haystacks {
			for _, s := range subs {
				if got, want := containsAnchored(h, s.sub, s.anchor), strings.Contains(h, s.sub); got != want {
					t.Errorf("containsAnchored(%q, %q, %d) = %v, want %v", h, s.sub, s.anchor, got, want)
				}
			}
		}
	})

	t.Run("awsSecretHint", func(t *testing.T) {
		positives := []string{
			"AWS_SECRET_ACCESS_KEY=" + strings.Repeat("A", 32),
			"x-aws-secret-access-key: " + strings.Repeat("b", 32),
			"awssecretaccesskey=" + strings.Repeat("c", 32),
		}
		for _, s := range positives {
			if awsSecretRE.FindStringIndex(s) == nil {
				t.Fatalf("expected awsSecretRE match for %q", s)
			}
			if !hasAWSLabelHint(asciiLower(s)) {
				t.Errorf("hasAWSLabelHint missed match %q", s)
			}
		}
		for _, clean := range []string{
			"flaws_secret_access_key=" + strings.Repeat("a", 32),
			"aws runs on cloud infrastructure",
			"xaw",
			"aaw",
		} {
			if hasAWSLabelHint(asciiLower(clean)) {
				t.Errorf("hasAWSLabelHint false positive on %q", clean)
			}
		}
	})

	t.Run("bearerOrBasic", func(t *testing.T) {
		positives := []string{
			"Bearer 1234567890abcdef",
			"Authorization: Basic\t1234567890abcdef",
			"prefix/bearer 1234567890abcdef",
		}
		for _, s := range positives {
			if bearerRE.FindStringIndex(s) == nil {
				t.Fatalf("expected bearerRE match for %q", s)
			}
			if !hasBearerOrBasic(asciiLower(s)) {
				t.Errorf("hasBearerOrBasic missed match %q", s)
			}
		}
		for _, clean := range []string{"bearer-token", "mybearer 1234567890abcdef", "basics"} {
			if hasBearerOrBasic(asciiLower(clean)) {
				t.Errorf("hasBearerOrBasic false positive on %q", clean)
			}
		}
	})

	t.Run("cliFlagSpans", func(t *testing.T) {
		filler := strings.Repeat("ж", 60)
		checks := []struct {
			name string
			re   *regexp.Regexp
			in   string
			flag func(cliFlagSpans) bool
		}{
			{"sshpass", sshpassRE, "sshpass -p hunter22 ssh deploy@example.com", func(f cliFlagSpans) bool { return f.sshpass }},
			{"sshpass_utf8", sshpassRE, "sshpass " + filler + " -p=hunter22", func(f cliFlagSpans) bool { return f.sshpass }},
			{"userPair_space", userPairRE, "curl -u admin:hunter22 https://example.com", func(f cliFlagSpans) bool { return f.userPair }},
			{"userPair_long_eq", userPairRE, "curl --user=" + strings.Repeat("ю", 40) + ":hunter22", func(f cliFlagSpans) bool { return f.userPair }},
			{"mysql", mysqlPassRE, "mysql " + strings.Repeat("я", 100) + " -pSecret22 db", func(f cliFlagSpans) bool { return f.mysql }},
			{"mariadb", mysqlPassRE, "MARIADB -u root -pSecret22", func(f cliFlagSpans) bool { return f.mysql }},
			{"login_p", loginPassRE, "docker login -p=hunter22 registry.example", func(f cliFlagSpans) bool { return f.login }},
			{"login_pw_utf8", loginPassRE, "az login " + strings.Repeat("й", 100) + " --password=hunter22", func(f cliFlagSpans) bool { return f.login }},
			{"redis_a", loginPassRE, "redis-cli -a hunter22 ping", func(f cliFlagSpans) bool { return f.login }},
			{"ftp_with_token", loginPassRE, "ftp --with-token<hunter22", func(f cliFlagSpans) bool { return f.login }},
		}
		for _, tc := range checks {
			if tc.re.FindStringIndex(tc.in) == nil {
				t.Fatalf("%s: expected regex match on %q", tc.name, tc.in)
			}
			flags := scanCLIFlagSpans(asciiLower(tc.in))
			if !tc.flag(flags) {
				t.Errorf("%s: scanCLIFlagSpans(%q) = %+v, flag false", tc.name, tc.in, flags)
			}
		}
		for _, clean := range []string{"my-project --co-author name", "2026-11-24T01:00:00Z foo-bar: baz", "trailing-"} {
			flags := scanCLIFlagSpans(asciiLower(clean))
			if flags != (cliFlagSpans{}) {
				t.Errorf("scanCLIFlagSpans(%q) = %+v, want all false", clean, flags)
			}
		}
	})
}

func TestRedactEdgeBranches(t *testing.T) {
	t.Run("StripEscapesAndDisplay", func(t *testing.T) {
		if got := StripEscapes("plain ascii"); got != "plain ascii" {
			t.Errorf("StripEscapes plain = %q", got)
		}
		if got := StripEscapes("end\x1b"); got != "end" {
			t.Errorf("StripEscapes trailing esc = %q", got)
		}
		if got := StripEscapes("a\x1b[12"); got != "a" {
			t.Errorf("StripEscapes unterminated CSI = %q", got)
		}
		if got := StripEscapes("a\x1b]0;title"); got != "a" {
			t.Errorf("StripEscapes unterminated OSC = %q", got)
		}
		if got := StripEscapes("a\x1b]0;title\x1b\\b"); got != "ab" {
			t.Errorf("StripEscapes OSC ST = %q", got)
		}
		if got := StripEscapes("a\x1bMb"); got != "ab" {
			t.Errorf("StripEscapes two-rune = %q", got)
		}
	})

	t.Run("ScanKindsAndTextEscapes", func(t *testing.T) {
		if got, counts := scanPass("", nil, true); got != "" || counts != nil {
			t.Errorf("scanPass empty = (%q, %v)", got, counts)
		}
		if got := ScanKinds("clean\x1b[31m line\x1b[0m", nil); len(got) != 0 {
			t.Errorf("ScanKinds clean escapes = %v", got)
		}
		splitTok := "ghp" + "\x1b[0m_" + "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890"
		allow := map[string]bool{"github-token": true}
		if counts := ScanKinds(splitTok, allow); counts["github-token"] != 1 {
			t.Errorf("ScanKinds split escape = %v", counts)
		}
		if redacted, counts := Text(splitTok); counts["github-token"] != 1 || redacted == splitTok {
			t.Errorf("Text split escape = (%q, %v)", redacted, counts)
		}
		if got, counts := TextKinds(`secret="shortsecret1"`, map[string]bool{"quoted-secret": true}); !strings.Contains(got, "[redacted:quoted-secret]") || counts["quoted-secret"] != 1 {
			t.Errorf("TextKinds quoted-secret = (%q, %v)", got, counts)
		}
	})

	t.Run("HelpersAndDisabledPasses", func(t *testing.T) {
		if asciiLower("İ") != strings.ToLower("İ") || asciiLower("K") != strings.ToLower("K") {
			t.Errorf("asciiLower Turkish I / Kelvin fallback mismatch")
		}
		if !containsAnchored("abc", "", 0) || containsAnchored("ab", "abcd", 1) {
			t.Errorf("containsAnchored edge checks failed")
		}
		if !notASecretValue("") || chosenSecret("short") || chosenSecret("onlylettersvalue") || !chosenSecret("hasdigit123") {
			t.Errorf("notASecretValue/chosenSecret edge checks failed")
		}
		if !looksLikeGoTest("Benchmark_16/sub") || looksLikeGoTest("Testing_lowercase/sub") {
			t.Errorf("looksLikeGoTest edge checks failed")
		}
		disabled := &kindPass{allow: map[string]bool{}}
		if replaceWhole("jwt", jwtRE, "jwt", disabled) != "jwt" ||
			replaceSubmatch("jwt", jwtRE, "jwt", disabled, nil) != "jwt" ||
			replaceGroup("jwt", jwtRE, 0, "jwt", disabled, nil) != "jwt" {
			t.Errorf("disabled replace helpers modified input")
		}
		optRE := regexp.MustCompile(`(opt)?(val)`)
		if got := replaceGroup("val", optRE, 1, "credential", &kindPass{}, nil); got != "val" {
			t.Errorf("replaceGroup missing optional group = %q, want unchanged", got)
		}
	})

	t.Run("OutboundAndEntropyEdges", func(t *testing.T) {
		if maskableIPv4("1.2.3") || maskableIPv4("198.51.100.1") || maskableIPv4("203.0.113.1") {
			t.Errorf("maskableIPv4 rejected doc/short address check failed")
		}
		if out, c := Outbound(":: and ::1 and 198.51.100.1 and 203.0.113.1"); c[OutboundIP] != 0 || !strings.Contains(out, "::1") {
			t.Errorf("Outbound non-maskable IPs = (%q, %v)", out, c)
		}
		if out, c := Text("key=aBcDeFgHiJkLmNoPqRsTuVwXyZ123456\nval=" + strings.Repeat("A", 20)); out == "" || len(c) == 0 {
			t.Errorf("Text entropy baseline failed")
		}
	})
}
