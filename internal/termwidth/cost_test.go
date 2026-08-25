package termwidth

import (
	"strings"
	"testing"
)

// Columns runs on every aligned line the tool prints, so composing first has to
// stay free for text that holds no combining mark — which is nearly all of it.
func BenchmarkColumnsPlain(b *testing.B) {
	s := strings.Repeat("build-server-in-the-other-office ", 4)
	b.ReportAllocs()
	for b.Loop() {
		_ = Columns(s)
	}
}

func BenchmarkColumnsDecomposed(b *testing.B) {
	s := strings.Repeat("über-server ", 4)
	b.ReportAllocs()
	for b.Loop() {
		_ = Columns(s)
	}
}
