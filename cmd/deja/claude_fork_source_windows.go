//go:build windows

package main

// processAncestorArgs reads nothing on Windows: another process's command line
// is only reachable through WMI or its PEB, too slow or too fragile for a
// hook. A fork's first prompt there is answered as before #5128.
func processAncestorArgs(int) [][]string { return nil }
