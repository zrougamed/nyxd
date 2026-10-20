//go:build !linux

package cgstats

func clockTicks() int64 { return 100 }
