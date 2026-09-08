//go:build !linux

package containerlogs

import "os"

func openLogFile(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
