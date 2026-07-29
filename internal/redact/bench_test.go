package redact

import (
	"strings"
	"testing"
)

// Redaction runs over every message on every index build, and profiling a real
// rebuild put it at 23.5% of total CPU. These benchmarks are the baseline that
// any change to the matchers has to beat — and the corpus below is deliberately
// ordinary developer chatter, because that is what the cost is actually paid
// on. Text containing an actual secret is rare; text containing the *words*
// "token" or "secret" is not, and that is what makes the gates pass.
var (
	plain = strings.Repeat(
		"the handler returns 500 when the body is empty, and the retry loop "+
			"swallows the error. moved the check above the decode call.\n", 12)

	// No secrets, but every gate word a developer uses in passing.
	chatty = strings.Repeat(
		"the auth token expires after 15m, so the refresh secret has to be read "+
			"from the environment; see the Authorization header and the api_key "+
			"we pass to the client. base64 payload aGVsbG8gd29ybGQgb2YgZGVqYQ==\n", 8)

	// Long identifier-ish runs: hashes, paths, base64 — what the entropy pass
	// exists for and what it spends its time on when nothing is a secret.
	hashy = strings.Repeat(
		"commit 4f3d2b1a9c8e7f6d5b4a3928170695847362514e touched "+
			"internal/index/ingest.go and vendor/github.com/some/long-package-name/file.go\n", 8)

	withSecret = chatty + "\napi_key = \"sk-abcdefghijklmnopqrstuvwxyz012345\"\n"
)

func BenchmarkTextPlain(b *testing.B)  { benchText(b, plain) }
func BenchmarkTextChatty(b *testing.B) { benchText(b, chatty) }
func BenchmarkTextHashy(b *testing.B)  { benchText(b, hashy) }
func BenchmarkTextSecret(b *testing.B) { benchText(b, withSecret) }

func benchText(b *testing.B, s string) {
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, c := Text(s); c.Total() < 0 {
			b.Fatal("impossible")
		}
	}
}

// A sanity print, so a reader of the benchmark output knows which inputs
// actually redact anything.
func TestBenchCorpusShape(t *testing.T) {
	for name, s := range map[string]string{
		"plain": plain, "chatty": chatty, "hashy": hashy, "secret": withSecret,
	} {
		_, c := Text(s)
		t.Logf("%-7s %6d bytes  %d redactions", name, len(s), c.Total())
	}
}
