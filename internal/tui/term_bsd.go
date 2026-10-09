//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package tui

import "syscall"

const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
