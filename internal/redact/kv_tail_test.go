package redact

import (
	"strings"
	"testing"
)

// A password that ends in punctuation used to lose only its head: the value
// class stopped at the first '!' or '#', and the rest was indexed in the clear.
func TestKVValueTakesThePunctuatedTail(t *testing.T) {
	for _, c := range []struct{ in, tail string }{
		{"run with --password=Sup3rS3cretValue!!xyz", "!!xyz"},
		{"export DB_PASSWORD=Sup3rS3cretValue#1x", "#1x"},
		{"api_key=abcdefghij0123456789$tail", "$tail"},
		{"MY_API_KEY=abcdefghij0123456789^z9", "^z9"},
		{"token: abcdefghij0123456789~end", "~end"},
		{"password=Abcdefghij0123456789@Prod", "@Prod"},
		{"SERVICE_KEY=abcdefghij0123456789*x", "*x"},
		{"secret = 'abcdefghij0123456789!q'", "!q"},
		{"пароль: abcdefghij0123456789!q7", "!q7"},
		{"пароль от стейджа: abcdefghij0123456789#q7", "#q7"},
	} {
		out, _ := Text(c.in)
		if strings.Contains(out, c.tail) {
			t.Errorf("%q -> %q: tail %q survived", c.in, out, c.tail)
		}
		if !strings.Contains(out, "[redacted:") {
			t.Errorf("%q -> %q: nothing masked", c.in, out)
		}
	}
}

// What follows the value and is not part of it stays.
func TestKVValueStopsAtTheRest(t *testing.T) {
	for _, c := range []struct{ in, keep string }{
		{"token=abcdefghij0123456789xyzw&page=2", "&page=2"},
		{"token=abcdefghij0123456789xyzw, then retry", ", then retry"},
		{"(api_key=abcdefghij0123456789xyzw) failed", ") failed"},
		{"url?token=abcdefghij0123456789xyzw?x=1", "?x=1"},
	} {
		out, _ := Text(c.in)
		if !strings.Contains(out, c.keep) {
			t.Errorf("%q -> %q: lost %q", c.in, out, c.keep)
		}
		if strings.Contains(out, "abcdefghij0123456789") {
			t.Errorf("%q -> %q: value survived", c.in, out)
		}
	}
}
