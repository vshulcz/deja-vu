package redact

import (
	"strings"
	"testing"
)

// The bar here is different from the ingest pass on purpose: none of these is a
// credential, and every one of them identifies a machine or a person. The IP
// that prompted the rule came out of a real recap prototype (#544).
func TestOutboundMasksWhatIdentifiesAMachineAndKeepsTheRest(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		gone   string
		kind   string
		intact bool
	}{
		{name: "a routable address", in: "default route now via 10.14.3.7", gone: "10.14.3.7", kind: OutboundIP},
		{name: "an address in prose", in: "the gateway answered from 172.16.9.200 instead", gone: "172.16.9.200", kind: OutboundIP},
		{name: "an internal hostname", in: "deploy hit api.svc.internal twice", gone: "api.svc.internal", kind: OutboundHost},
		{name: "a .local host", in: "ssh to mini.local was refused", gone: "mini.local", kind: OutboundHost},
		{name: "an email", in: "the token belongs to someone@example.com", gone: "someone@example.com", kind: OutboundEmail},
		{name: "a home path", in: "wrote /Users/rivera/src/api/main.go", gone: "/Users/rivera", kind: OutboundHome},
		{name: "a linux home path", in: "the log is at /home/rivera/.cache/deja", gone: "/home/rivera", kind: OutboundHome},
		// Kept: printable by design, or not an address at all.
		{name: "loopback", in: "bound to 127.0.0.1:8080", intact: true},
		{name: "documentation range", in: "the example uses 192.0.2.10", intact: true},
		{name: "a public host", in: "pushed to github.com/vshulcz/deja-vu", intact: true},
		{name: "a file name", in: "the fix is in internal/index/recap.go", intact: true},
		{name: "a version", in: "go 1.27.0 and node 22.4.1", intact: true},
		{name: "a clock", in: "it failed at 10:30:45 and again at 11:02:07", intact: true},
	}
	for _, c := range cases {
		got, counts := Outbound(c.in)
		if c.intact {
			if got != c.in {
				t.Errorf("%s: masked something it should keep: %q became %q", c.name, c.in, got)
			}
			if counts.Total() != 0 {
				t.Errorf("%s: counted a mask it did not need: %v", c.name, counts)
			}
			continue
		}
		if strings.Contains(got, c.gone) {
			t.Errorf("%s: %q survived in %q", c.name, c.gone, got)
		}
		if counts[c.kind] != 1 {
			t.Errorf("%s: want one %s, got %v", c.name, c.kind, counts)
		}
	}
}

// A home path keeps the part that says what the work was — the file — and loses
// only the part that names the account.
func TestAHomePathKeepsThePathAndLosesTheAccount(t *testing.T) {
	got, _ := Outbound("the span is in /Users/rivera/coding/api/internal/fetch/pool.go")
	if want := "the span is in ~/coding/api/internal/fetch/pool.go"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOutboundLeavesEmptyTextAlone(t *testing.T) {
	got, counts := Outbound("")
	if got != "" || counts.Total() != 0 {
		t.Fatalf("empty text produced %q and %v", got, counts)
	}
}

// IPv6 is only read in the two spellings that cannot be something else: a run
// holding `::`, or all eight groups written out. `10:30:45` is a clock and
// `::1` is printable by design.
func TestOutboundReadsTheIPv6SpellingsThatCannotBeSomethingElse(t *testing.T) {
	cases := []struct {
		in     string
		gone   string
		intact bool
	}{
		{in: "the peer answered from 2001:db8:85a3::8a2e:370:7334", gone: "2001:db8"},
		{in: "listening on fe80::1c2d:5aff:fe12:3456 only", gone: "fe80::1c2d"},
		{in: "bound to 0:0:0:0:0:0:0:9 for the test", gone: "0:0:0:0"},
		{in: "loopback ::1 answered", intact: true},
		{in: "the run took 10:30:45 and the next 11:02:07", intact: true},
		{in: "see the note above :: and below", intact: true},
	}
	for _, c := range cases {
		got, counts := Outbound(c.in)
		if c.intact {
			if got != c.in {
				t.Errorf("masked something it should keep: %q became %q", c.in, got)
			}
			continue
		}
		if strings.Contains(got, c.gone) {
			t.Errorf("%q survived in %q", c.gone, got)
		}
		if counts[OutboundIP] == 0 {
			t.Errorf("%q was masked without being counted: %v", c.in, counts)
		}
	}
}

// A quad that is not an address: an octet over 255, a leading zero, or a
// date written with dots.
func TestOutboundRefusesWhatIsNotAnAddress(t *testing.T) {
	for _, in := range []string{
		"the build is 2026.09.21.0722",
		"version 1.02.3.4 shipped",
		"the ratio was 300.2.3.4 which is nonsense",
	} {
		got, counts := Outbound(in)
		if got != in || counts.Total() != 0 {
			t.Errorf("%q was masked as an address: %q %v", in, got, counts)
		}
	}
}

// Windows writes the same path with a drive and backslashes.
func TestOutboundShortensAWindowsHomePath(t *testing.T) {
	got, counts := Outbound(`the log is at C:\Users\rivera\AppData\deja`)
	if strings.Contains(got, "rivera") {
		t.Errorf("the account name survived: %q", got)
	}
	if counts[OutboundHome] != 1 {
		t.Errorf("want one home path, got %v", counts)
	}
}
