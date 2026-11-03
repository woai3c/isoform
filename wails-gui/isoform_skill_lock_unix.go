//go:build !windows

package main

import (
	"os"
)

func isoformSkillFileLinkCount(file *os.File) (uint64, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	return fileLinkCount(info), nil
}
