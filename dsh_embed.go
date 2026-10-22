package dejavu

import _ "embed"

// The dsh npm plugin's browser card and its helpers. `deja install dsh-auto`
// writes the same files into DSH_HOME, so a profile wired by the CLI shows the
// person the card the npm package does, from one copy of the code (#4983).

// DSHClientJS is the browser card script.
//
//go:embed extensions/dsh/client.js
var DSHClientJS string

// DSHLibJS holds the helpers the card script loads.
//
//go:embed extensions/dsh/lib.js
var DSHLibJS string
