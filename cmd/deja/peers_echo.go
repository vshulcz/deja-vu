package main

// hostForEcho is how a peer's name is written to a screen. The name comes from
// a config file — typed once, or shared with a team — and reached the terminal
// exactly as stored: an escape byte recoloured the line a user reads when sync
// is misbehaving, a carriage return rewound it, and a 300-character host filled
// three lines of one `deja sync` run (#1808). The same bound the listing
// surfaces have used since #1090.
//
// The name is not altered anywhere it is used as an address: ssh still gets the
// host as written, and `deja sync forget` still matches on it.
func hostForEcho(host string) string {
	out := neutralizeFrameMarkers(safeForStatusline(host, mcpResourceNameMax))
	if out == "" && host != "" {
		return "a name with no printable characters"
	}
	return out
}
