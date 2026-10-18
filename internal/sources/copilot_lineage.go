package sources

import "regexp"

// copilotForkRe reads the source off the note a /fork leaves in the new
// session: Forked from "<title>" (<source id>) as "<title> (fork)" (<own id>).
// The id is the one right before ` as "`, so neither the fork's own id at the
// end nor an id inside the title stands in for it.
var (
	copilotForkRe     = regexp.MustCompile(`(?s)^Forked from ".*" \((` + copilotUUID + `)\) as "`)
	copilotForkBareRe = regexp.MustCompile(`(?s)^Forked from ".*" \((` + copilotUUID + `)\)\.?$`)
)

const copilotUUID = `[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`

// copilotForkSource is the session a fork note names as its source, or "".
func copilotForkSource(msg string) string {
	for _, re := range []*regexp.Regexp{copilotForkRe, copilotForkBareRe} {
		if m := re.FindStringSubmatch(msg); m != nil {
			return m[1]
		}
	}
	return ""
}
