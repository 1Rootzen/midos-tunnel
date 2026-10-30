//go:build !linux

package main

func diskUsage(string) (total, free uint64) { return 0, 0 }
