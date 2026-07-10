//go:build windows

package syscollector

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSpaceExW  = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
)

type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

type filetime struct {
	lowDateTime  uint32
	highDateTime uint32
}

func readMemoryUsedPercent() float64 {
	status := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 || status.totalPhys == 0 {
		return 0
	}
	used := status.totalPhys - status.availPhys
	return (float64(used) / float64(status.totalPhys)) * 100
}

func readDiskUsedPercent(path string) float64 {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	var freeAvailable uint64
	var totalBytes uint64
	var totalFree uint64
	ret, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if ret == 0 || totalBytes == 0 {
		return 0
	}
	used := totalBytes - totalFree
	return (float64(used) / float64(totalBytes)) * 100
}

func readCPUTimes() (idle uint64, total uint64) {
	var idleTime filetime
	var kernelTime filetime
	var userTime filetime
	ret, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleTime)),
		uintptr(unsafe.Pointer(&kernelTime)),
		uintptr(unsafe.Pointer(&userTime)),
	)
	if ret == 0 {
		return 0, 0
	}
	idle = filetimeToUint64(idleTime)
	kernel := filetimeToUint64(kernelTime)
	user := filetimeToUint64(userTime)
	return idle, kernel + user
}

func filetimeToUint64(value filetime) uint64 {
	return (uint64(value.highDateTime) << 32) | uint64(value.lowDateTime)
}

func readNetworkTotals() (rxBytes int64, txBytes int64) {
	return 0, 0
}

func readLoad1m() float64 {
	return 0
}
