//go:build !windows && !linux

package syscollector

func readMemoryUsedPercent() float64     { return 0 }
func readDiskUsedPercent(string) float64 { return 0 }
func readCPUTimes() (uint64, uint64)     { return 0, 0 }
func readNetworkTotals() (int64, int64)  { return 0, 0 }
func readLoad1m() float64                { return 0 }
