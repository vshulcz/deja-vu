//go:build race

package redact

// raceEnabled cuts the random parity sweep: under -race it ran 470-840 s of
// a 20-minute test timeout and timed out on a loaded macOS runner.
const raceEnabled = true
