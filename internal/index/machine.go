package index

import (
	"os"
	"strings"
)

// MachineName is what this machine calls itself in a sync batch, so the
// receiving side can say where a memory came from.
//
// Without it every imported session reads as "from elsewhere" and nothing
// more: with three machines exchanging history there is no way to ask what the
// server worked on, and no way to see that one of them stopped sending days
// ago. The hostname is what a person already uses to ssh there, which is the
// name they will recognise; DEJA_MACHINE overrides it for anyone whose
// hostname is not something they want stamped on every record.
func MachineName() string {
	if n := sanitizeSyncField(os.Getenv("DEJA_MACHINE"), syncFieldMax); n != "" {
		return n
	}
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	// macOS hands back "name.local" and DHCP hands back fully qualified names;
	// the first label is the part someone recognises.
	host, _, _ = strings.Cut(host, ".")
	return sanitizeSyncField(host, syncFieldMax)
}
