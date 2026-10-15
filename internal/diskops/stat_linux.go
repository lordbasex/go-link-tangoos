//go:build linux

package diskops

import (
	"os"
	"syscall"
)

func devOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Dev
	}
	return 0
}
