//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func isoformSkillFileLinkCount(file *os.File) (uint64, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info)
	return uint64(info.NumberOfLinks), err
}
