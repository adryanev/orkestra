//go:build windows

package pty

import (
	"errors"
	"os"
)

// ErrNotSupported is returned when PTY operations are attempted on Windows.
var ErrNotSupported = errors.New("PTY operations are not supported on Windows")

// Open creates a new PTY master and slave pair.
// Returns ErrNotSupported on Windows.
func Open() (master, slave *os.File, err error) {
	return nil, nil, ErrNotSupported
}

// Setsize sets the terminal size for the given file descriptor.
// Returns ErrNotSupported on Windows.
func Setsize(f *os.File, rows, cols uint16) error {
	return ErrNotSupported
}

// CurrentSize returns the current terminal size for the given file descriptor.
// Returns ErrNotSupported on Windows.
func CurrentSize(fd uintptr) (rows, cols uint16, err error) {
	return 0, 0, ErrNotSupported
}
