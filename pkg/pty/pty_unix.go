//go:build !windows

package pty

import (
	"os"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// Open creates a new PTY master and slave pair.
// The caller is responsible for closing both file descriptors.
func Open() (master, slave *os.File, err error) {
	return pty.Open()
}

// Setsize sets the terminal size for the given file descriptor.
func Setsize(f *os.File, rows, cols uint16) error {
	if f == nil {
		return os.ErrInvalid
	}
	ws := &pty.Winsize{
		Rows: rows,
		Cols: cols,
	}
	return pty.Setsize(f, ws)
}

// CurrentSize returns the current terminal size for the given file descriptor.
func CurrentSize(fd uintptr) (rows, cols uint16, err error) {
	width, height, err := term.GetSize(int(fd))
	if err != nil {
		return 0, 0, err
	}
	return uint16(height), uint16(width), nil
}
