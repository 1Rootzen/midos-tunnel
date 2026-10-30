//go:build linux

package main

import "golang.org/x/sys/unix"

func diskUsage(path string) (total, free uint64) {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0, 0
	}
	return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize)
}
