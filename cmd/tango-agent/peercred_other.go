//go:build !linux

package main

import (
	"net"
	"os"
)

// peerCred fuera de Linux (solo desarrollo y tests): asume el uid propio.
func peerCred(*net.UnixConn) (uint32, int32, error) {
	return uint32(os.Getuid()), 0, nil
}
