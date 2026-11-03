package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var supportLogPrefix = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})(?:\.\d+)? \[([a-z-]+)\]`)
var supportCrashName = regexp.MustCompile(`^crash-(\d{8}T\d{6}\.\d{9}Z)-\d+\.txt$`)

// Only known component names and timestamps survive. Free-form messages can
// include screen text, speech, secrets and paths, so none are exported.
func sanitizedSupportLog(input io.Reader, output io.Writer, now time.Time) error {
	scanner := bufio.NewScanner(io.LimitReader(input, maxLogSize))
	scanner.Buffer(make([]byte, 4096), maxLogSize)
	for scanner.Scan() {
		fields := supportLogPrefix.FindStringSubmatch(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		stamp, err := time.ParseInLocation("2006/01/02 15:04:05", fields[1], now.Location())
		if err != nil || stamp.Before(now.AddDate(0, 0, -7)) || stamp.After(now) {
			continue
		}
		switch fields[2] {
		case "main", "update", "startup", "activity", "audio", "storage", "mobile", "env":
			if _, err := fmt.Fprintf(output, "%s [%s]\n", fields[1], fields[2]); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func writeSupportArchive(output io.Writer, root string, now time.Time) error {
	archive := zip.NewWriter(output)
	for _, name := range []string{"yansu-gui.log", "yansu-gui.log.old", "yansu-gui.log.old.2"} {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			_ = archive.Close()
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			_ = archive.Close()
			return err
		}
		entry, err := archive.Create(strings.TrimPrefix(name, "yansu-") + ".sanitized.txt")
		if err == nil {
			err = sanitizedSupportLog(file, entry, now)
		}
		_ = file.Close()
		if err != nil {
			_ = archive.Close()
			return err
		}
	}
	// Crash output can contain private text. Export only the timestamp and size
	// of completed, nonempty reports, without reading their contents.
	logsDir := filepath.Join(root, "logs")
	if info, err := os.Lstat(logsDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(logsDir)
		if err != nil {
			_ = archive.Close()
			return err
		}
		var summary strings.Builder
		for _, entry := range entries {
			match := supportCrashName.FindStringSubmatch(entry.Name())
			if len(match) != 2 {
				continue
			}
			stamp, err := time.Parse("20060102T150405.000000000Z", match[1])
			if err != nil || stamp.Before(now.AddDate(0, 0, -7)) || stamp.After(now) {
				continue
			}
			info, err := os.Lstat(filepath.Join(logsDir, entry.Name()))
			if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				fmt.Fprintf(&summary, "%s %d bytes\n", stamp.Format(time.RFC3339Nano), info.Size())
			}
		}
		if summary.Len() > 0 {
			entry, err := archive.Create("crash-summary.txt")
			if err == nil {
				_, err = io.WriteString(entry, summary.String())
			}
			if err != nil {
				_ = archive.Close()
				return err
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		_ = archive.Close()
		return err
	}
	return archive.Close()
}

// SaveSupportLogs saves a local archive via a native Save dialog; nothing uploads.
func (a *App) SaveSupportLogs() error {
	title := "Save logs for support"
	if normalizeUILanguage(a.doingNowLanguage()) == "zh-CN" {
		title = "保存支持日志"
	}
	dest, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{Title: title, DefaultFilename: "Isoform-support.zip"})
	if err != nil || dest == "" {
		return err
	}
	root, err := flavorDataDir()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = writeSupportArchive(file, root, time.Now())
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	return closeErr
}
