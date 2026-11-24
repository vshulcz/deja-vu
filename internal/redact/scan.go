package redact

import "strings"

// ScanKinds evaluates the redaction matchers against s and returns match counts
// by class without mutating files or honoring DEJA_NO_REDACT. When allow is
// non-nil, only the named classes are evaluated.
func ScanKinds(s string, allow map[string]bool) Counts {
	out, counts := scanPass(s, allow, true)
	if strings.IndexByte(out, 0x1b) < 0 {
		return counts
	}
	_, more := scanPass(StripEscapes(out), allow, true)
	if len(more) == 0 {
		return counts
	}
	if counts == nil {
		counts = Counts{}
	}
	for k, n := range more {
		counts.Add(k, n)
	}
	return counts
}
