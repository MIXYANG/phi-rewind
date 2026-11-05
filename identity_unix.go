//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(f *os.File) (string, uint64, error) {
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), uint64(stat.Nlink), nil
}
