//go:build !linux

package control

// freeBytes is not reported off Linux (nyxd runs on Linux only).
func freeBytes(string) uint64 { return 0 }
