package main

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(f *os.File) (string, uint64, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow),
		uint64(info.NumberOfLinks), nil
}
