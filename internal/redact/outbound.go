package redact

import (
	"regexp"
	"strconv"
	"strings"
)

// Outbound is the second pass, for text that leaves the machine.
//
// The index's own redaction is tuned for what a credential looks like, and that
// is the right bar for a local store. It is not the bar for a standup message
// or a PR description: prototyping `deja recap` against a real corpus surfaced
// an infrastructure IP address from a networking session, which no rule here
// calls a secret and nobody would want pasted into a public thread (#544).
//
// So this masks the things that identify a machine or a network rather than the
// things that authenticate to one, and it is deliberately a short list. Four
// classes, each of which a person would have to remove by hand:
//
//   - an IP address, except loopback and the RFC 5737 documentation ranges
//   - an internal hostname — .local, .internal, .lan, .corp, .svc and friends
//   - an email address
//   - a path under a home directory, which becomes ~/…
//
// What it deliberately does not touch: public hostnames (github.com is not
// private), ports, repository and branch names, and anything the ingest pass
// already masked. A rule that fired on every dotted name would mask half the
// Go import paths in a week's work and the output would be unreadable.

const (
	// OutboundIP and the rest are the marker kinds this pass adds.
	OutboundIP    = "ip"
	OutboundHost  = "internal-host"
	OutboundEmail = "email"
	OutboundHome  = "home-path"
)

var (
	outIPv4RE = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	// Only the spellings that cannot be a clock or a MAC address: a run
	// containing `::`, or all eight groups written out.
	outIPv6RE = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b|(?:[0-9a-f]{1,4}:)+:(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*)?`)
	outHostRE = regexp.MustCompile(`(?i)\b[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*\.(local|internal|lan|corp|home|svc|intranet|localdomain)\b\.?`)
	// A user@host pair, which is how an email and an ssh target are both
	// written. Both are worth masking in outbound text.
	outEmailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b`)
	outHomeRE  = regexp.MustCompile(`(?i)(/Users/|/home/|[A-Z]:\\Users\\)([a-z0-9._-]+)`)
)

// Outbound masks what identifies a machine or a network and reports what it
// masked, by kind.
func Outbound(s string) (string, Counts) {
	c := Counts{}
	if s == "" {
		return s, c
	}
	// Home paths first: the segment that follows is a real directory name, and
	// the host rule would otherwise read `/Users/me/src/api.internal` as a
	// hostname inside a path deja is about to shorten anyway.
	s = outHomeRE.ReplaceAllStringFunc(s, func(m string) string {
		c.Add(OutboundHome, 1)
		if strings.HasPrefix(strings.ToLower(m), "/users/") || strings.HasPrefix(strings.ToLower(m), "/home/") {
			return "~"
		}
		return `~`
	})
	s = outEmailRE.ReplaceAllStringFunc(s, func(m string) string {
		c.Add(OutboundEmail, 1)
		return Marker + OutboundEmail + "]"
	})
	s = outHostRE.ReplaceAllStringFunc(s, func(m string) string {
		c.Add(OutboundHost, 1)
		return Marker + OutboundHost + "]"
	})
	s = outIPv6RE.ReplaceAllStringFunc(s, func(m string) string {
		if !maskableIPv6(m) {
			return m
		}
		c.Add(OutboundIP, 1)
		return Marker + OutboundIP + "]"
	})
	s = outIPv4RE.ReplaceAllStringFunc(s, func(m string) string {
		if !maskableIPv4(m) {
			return m
		}
		c.Add(OutboundIP, 1)
		return Marker + OutboundIP + "]"
	})
	return s, c
}

// maskableIPv4 rejects what is not an address at all and what is safe to print:
// loopback, the unspecified address, and the three RFC 5737 documentation
// ranges, which exist to be written down.
func maskableIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	var oct [4]int
	for i, p := range parts {
		// A leading zero means this is a version or a date, not an address.
		if len(p) > 1 && p[0] == '0' {
			return false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n > 255 {
			return false
		}
		oct[i] = n
	}
	switch {
	case oct[0] == 127, s == "0.0.0.0":
		return false
	case oct[0] == 192 && oct[1] == 0 && oct[2] == 2:
		return false
	case oct[0] == 198 && oct[1] == 51 && oct[2] == 100:
		return false
	case oct[0] == 203 && oct[1] == 0 && oct[2] == 113:
		return false
	}
	return true
}

// maskableIPv6 keeps ::1 and the unspecified address readable, and refuses
// anything with no hex group at all — `::` on its own is punctuation in prose
// far more often than an address.
func maskableIPv6(s string) bool {
	trimmed := strings.Trim(s, ":")
	if trimmed == "" || trimmed == "1" {
		return false
	}
	return strings.Contains(s, ":")
}
