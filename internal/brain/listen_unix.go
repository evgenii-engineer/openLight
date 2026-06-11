//go:build linux || darwin

package brain

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func newListenConfig() net.ListenConfig {
	return net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			})
		},
	}
}
