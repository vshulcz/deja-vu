//go:build !linux && !darwin && !windows

package main

// processStart has no portable source for a process's start time here, and a
// bare pid can be reused, so a claim on these systems is judged by its age.
func processStart(int) (start string, alive, known bool) {
	return "", false, false
}
