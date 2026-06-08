//go:build windows

package pty

import (
	"context"
	"fmt"
	"io"
)

// RunAttach is unsupported on Windows because Orkestra PTY sessions use Unix
// sockets and POSIX terminal control.
func RunAttach(ctx context.Context, socketPath string, stdinFd int, stdout io.Writer) error {
	return fmt.Errorf("PTY attach is not supported on Windows")
}
