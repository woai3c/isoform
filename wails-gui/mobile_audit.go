package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var mobileAuditMu sync.Mutex

// mobileAuditEntry is one line of mobile_audit.jsonl. Bodies, query strings and
// names are never written here, only the route and its outcome.
type mobileAuditEntry struct {
	TS       string `json:"ts"`
	DeviceID string `json:"deviceId"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Status   int    `json:"status"`
}

// appendMobileAudit appends one entry to the owner-only audit log in dir.
func appendMobileAudit(dir string, entry mobileAuditEntry) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	mobileAuditMu.Lock()
	defer mobileAuditMu.Unlock()
	file, err := os.OpenFile(filepath.Join(dir, "mobile_audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(line, '\n'))
	return err
}
