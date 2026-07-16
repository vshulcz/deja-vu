//go:build !windows

package main

import "os"

func replaceExecutable(staged, destination string) error {
	return os.Rename(staged, destination)
}
