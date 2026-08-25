package main

import "github.com/vshulcz/deja-vu/internal/search"

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

// remoteEchoMax bounds what a peer's own deja is allowed to write on this
// screen. Its lines are short — "deja: exported 5 records" — so anything past
// this is not the report it claims to be.
const remoteEchoMax = 2000

// remoteOutputForEcho is how the other machine's output reaches this terminal.
// It arrives over ssh from a deja this one does not control, and was printed
// raw: an escape byte in it recoloured the local screen and a carriage return
// rewound it, which is the #1090 class with the text coming from somewhere else
// entirely (#1808).
func remoteOutputForEcho(out string) string {
	out = search.SafeText(out)
	if len(out) > remoteEchoMax {
		out = trimUTF8(out, remoteEchoMax) + " …"
	}
	return out
}
