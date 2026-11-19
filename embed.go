// Package dejavu exports build-time embedded assets loaded from the repository root.
package dejavu

import _ "embed"

// Changelog holds CHANGELOG.md for post-upgrade release summaries.
//
//go:embed CHANGELOG.md
var Changelog string

// DSHClientJS is the browser card script installed by `deja install dsh-auto`.
//
//go:embed extensions/dsh/client.js
var DSHClientJS string

// DSHLibJS holds helpers loaded by DSHClientJS.
//
//go:embed extensions/dsh/lib.js
var DSHLibJS string
