//go:build linux

package main

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerCred lee uid y pid del proceso del otro lado del socket (SO_PEERCRED).
func peerCred(c *net.UnixConn) (uid uint32, pid int32, err error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, 0, err
	}
	if cerr != nil {
		return 0, 0, cerr
	}
	return cred.Uid, cred.Pid, nil
}
