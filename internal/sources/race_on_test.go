//go:build race

package sources

// raceEnabled skips the allocation bound on pooled buffers: under -race
// sync.Pool drops a share of what is put back on purpose.
const raceEnabled = true
