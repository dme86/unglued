package util

import (
	"fmt"
	"runtime"
)

func MemUsage() (alloc, sys uint64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc, m.Sys
}

func HumanBytes(n uint64) string {
	if n < 1024 { return fmt.Sprintf("%d B", n) }
	units := []string{"KiB","MiB","GiB","TiB","PiB","EiB"}
	v := float64(n); i := -1
	for v >= 1024 && i < len(units)-1 { v/=1024; i++ }
	return fmt.Sprintf("%.1f %s", v, units[i])
}

