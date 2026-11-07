//go:build windows

package main

import (
	"net"
	"syscall"
)

// exclusiveListenConfig disables Windows address reuse for the availability
// probe. Without SO_EXCLUSIVEADDRUSE, Windows lets a wildcard bind succeed even
// when another process owns a specific address on the same port, so the setup
// wizard would report a busy port as free.
func exclusiveListenConfig() *net.ListenConfig {
	return &net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var setErr error
			if err := c.Control(func(fd uintptr) {
				// SO_EXCLUSIVEADDRUSE is the bitwise complement of SO_REUSEADDR.
				const soExclusiveAddrUse = ^syscall.SO_REUSEADDR
				setErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, soExclusiveAddrUse, 1)
			}); err != nil {
				return err
			}
			return setErr
		},
	}
}
