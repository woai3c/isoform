package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// magpieChildFileName records the gateway child this app started. The
// bundled gateway is a plain child of the app, so a SIGKILL or crash of the
// app leaves it running with parent PID 1 and nothing to find it by; the
// next start reads this file (and scans for the bundled binary) to stop it.
const magpieChildFileName = "magpie-child.json"

type magpieChildRecord struct {
	PID    int    `json:"pid"`
	Binary string `json:"binary"`
	Port   int    `json:"port"`
}

func (s *magpieSidecar) childRecordPath() string {
	return filepath.Join(s.stateDir, magpieChildFileName)
}

func (s *magpieSidecar) writeChildRecord(record magpieChildRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.childRecordPath(), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("persist magpie child: %w", err)
	}
	return nil
}

func (s *magpieSidecar) readChildRecord() (magpieChildRecord, bool) {
	var record magpieChildRecord
	data, err := os.ReadFile(s.childRecordPath())
	if err != nil || json.Unmarshal(data, &record) != nil || record.PID <= 0 || record.Binary == "" {
		return magpieChildRecord{}, false
	}
	return record, true
}

// clearChildRecord removes the record when it still names pid, so a child
// that exits late never erases the record of its replacement.
func (s *magpieSidecar) clearChildRecord(pid int) {
	if record, ok := s.readChildRecord(); ok && record.PID == pid {
		_ = os.Remove(s.childRecordPath())
	}
}

// isOurMagpieBinary reports whether path is a gateway this app ships, which
// is any binary inside a magpie-bundle directory. A magpie the user installed
// themselves, such as ~/.local/bin/magpie or magpie.app, and a binary named
// by YANSU_MAGPIE_BIN are never ours to stop.
func isOurMagpieBinary(path string) bool {
	return strings.Contains(filepath.ToSlash(strings.TrimSpace(path)), "/"+magpieBundleDirName+"/")
}
