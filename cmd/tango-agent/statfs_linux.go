//go:build linux

package main

import "golang.org/x/sys/unix"

func usedBytes(path string) uint64 {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0
	}
	return (st.Blocks - st.Bfree) * uint64(st.Bsize)
}

// fsSize es el tamaño total del filesystem montado en path.
func fsSize(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Blocks * uint64(st.Bsize), nil
}
