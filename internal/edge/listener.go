package edge

import (
	"context"
	"net"
	"strings"
	"syscall"
)

// ListenTCP keeps wildcard IPv6 dual-stack while explicit IPv6 addresses
// remain IPv6-only. The caller owns address selection and privilege policy.
func ListenTCP(address string) (net.Listener, error) {
	listener := net.ListenConfig{}
	network := "tcp"
	if strings.HasPrefix(address, "[") && !strings.HasPrefix(address, "[::]:") {
		network = "tcp6"
		listener.Control = func(network, address string, conn syscall.RawConn) error {
			var sockErr error
			if err := conn.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 1)
			}); err != nil {
				return err
			}
			return sockErr
		}
	}
	return listener.Listen(context.Background(), network, address)
}
