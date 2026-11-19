package search

import "strings"

// extractHeaderDay returns the first YYYY-MM-DD date token in a formatted session header.
func extractHeaderDay(header string) string {
	for i := 0; i+10 <= len(header); i++ {
		if header[i+4] != '-' || header[i+7] != '-' {
			continue
		}
		if i > 0 && header[i-1] >= '0' && header[i-1] <= '9' {
			continue
		}
		if i+10 < len(header) && header[i+10] >= '0' && header[i+10] <= '9' {
			continue
		}
		cand := header[i : i+10]
		if (strings.HasPrefix(cand, "19") || strings.HasPrefix(cand, "20")) &&
			isASCIIDigits(cand[0:4]) && isASCIIDigits(cand[5:7]) && isASCIIDigits(cand[8:10]) {
			return cand
		}
	}
	return ""
}

func isASCIIDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// annotateEarlierHeader appends an explicit chronological warning before the trailing newline.
func annotateEarlierHeader(header, newerDay string) string {
	note := " (earlier than " + newerDay + " above)"
	if strings.Contains(header, "(earlier than ") {
		return header
	}
	if trimmed, ok := strings.CutSuffix(header, "\n"); ok {
		return trimmed + note + "\n"
	}
	return header + note
}
