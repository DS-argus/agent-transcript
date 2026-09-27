package main

import "golang.org/x/sys/unix"

func terminalAttributes(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}
