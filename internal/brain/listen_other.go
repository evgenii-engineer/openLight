//go:build !linux && !darwin

package brain

import "net"

func newListenConfig() net.ListenConfig {
	return net.ListenConfig{}
}
