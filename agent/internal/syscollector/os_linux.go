//go:build linux

package syscollector

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func readMemoryUsedPercent() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	values := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseFloat(fields[1], 64)
		if err == nil {
			values[key] = value
		}
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total <= 0 || available < 0 {
		return 0
	}
	return ((total - available) / total) * 100
}

func readDiskUsedPercent(path string) float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil || stat.Blocks == 0 {
		return 0
	}
	total := float64(stat.Blocks) * float64(stat.Bsize)
	free := float64(stat.Bavail) * float64(stat.Bsize)
	return ((total - free) / total) * 100
}

func readCPUTimes() (idle uint64, total uint64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var values []uint64
		for _, field := range fields[1:] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return 0, 0
			}
			values = append(values, value)
			total += value
		}
		idle = values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		return idle, total
	}
	return 0, 0
}

func readNetworkTotals() (rxBytes int64, txBytes int64) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}
		rx, _ := strconv.ParseInt(fields[0], 10, 64)
		tx, _ := strconv.ParseInt(fields[8], 10, 64)
		rxBytes += rx
		txBytes += tx
	}
	return rxBytes, txBytes
}

func readLoad1m() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return value
}
