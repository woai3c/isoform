package models

import (
	"fmt"
	"os"
	"syscall"
)

func fileChangeStamp(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprint(stat.Ctim)
}
