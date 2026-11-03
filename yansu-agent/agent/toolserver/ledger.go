package toolserver

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"yansu-agent/agent/tools"
)

type ledgerEntry struct {
	State        string    `json:"state"`
	ID           string    `json:"id"`
	Tool         string    `json:"tool,omitempty"`
	Capability   string    `json:"capability,omitempty"`
	Decision     string    `json:"decision,omitempty"`
	ArgumentHash string    `json:"argument_hash,omitempty"`
	ResultHash   string    `json:"result_hash,omitempty"`
	Time         time.Time `json:"time"`
}

type ledger struct {
	mu   sync.Mutex
	file *os.File
	path string
	err  error
}

func newLedger(path string) (*ledger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &ledger{file: f, path: path}, nil
}

func hash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (l *ledger) append(entry ledgerEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	entry.Time = time.Now().UTC()
	if l.file == nil {
		return errors.New("run ledger is closed")
	}
	if err := json.NewEncoder(l.file).Encode(entry); err != nil {
		l.err = err
		return err
	}
	if err := l.file.Sync(); err != nil {
		l.err = err
		return err
	}
	return nil
}

func (l *ledger) start(name string, call tools.ToolCall) error {
	entry := ledgerEntry{State: "started", ID: call.ID, Tool: name, ArgumentHash: hash(call.Input)}
	if call.Authority != nil && call.Authority.Evaluation != nil {
		entry.Capability = call.Authority.Evaluation.Action.Capability
		entry.Decision = string(call.Authority.Evaluation.Decision)
	}
	return l.append(entry)
}

func (l *ledger) finish(id string, result tools.ToolResult) error {
	return l.append(ledgerEntry{State: "finished", ID: id, ResultHash: hash(result)})
}

// snapshot reads the durable record rather than trusting CLI output or an
// in-memory counter. A malformed or unreadable ledger is never an empty one.
func (l *ledger) snapshot() (count int, unfinished bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return 0, true, l.err
	}
	f, err := os.Open(l.path)
	if err != nil {
		return 0, true, err
	}
	defer f.Close()
	started := map[string]bool{}
	finished := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry ledgerEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return count, true, err
		}
		if entry.ID == "" {
			return count, true, errors.New("ledger entry has no call ID")
		}
		switch entry.State {
		case "started":
			if started[entry.ID] {
				return count, true, fmt.Errorf("duplicate started call %s", entry.ID)
			}
			started[entry.ID] = true
			count++
		case "finished":
			if !started[entry.ID] || finished[entry.ID] {
				return count, true, fmt.Errorf("unmatched finished call %s", entry.ID)
			}
			finished[entry.ID] = true
		default:
			return count, true, fmt.Errorf("unknown ledger state %q", entry.State)
		}
	}
	if err := scanner.Err(); err != nil {
		return count, true, err
	}
	return count, len(started) != len(finished), nil
}

func (l *ledger) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	if err != nil {
		l.err = err
	}
	return err
}
