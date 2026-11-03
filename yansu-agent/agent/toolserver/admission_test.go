package toolserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"yansu-agent/agent/authority"
)

func TestTimeoutAdmissionLeavesLedgerEmpty(t *testing.T) {
	s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
	path := filepath.Join(work, "must-not-exist")
	// Force the timeout to have won its mutex while the server is dispatching
	// an authorized call. The old receipt check rejected only after fsync.
	s.onActionStarted = func(int) error { return context.DeadlineExceeded }
	admitted := false
	ctx := WithActionAdmission(s.ctx, func(func() error) error { admitted = true; return context.DeadlineExceeded })
	result := s.call(withRequestID(ctx, []byte(`{"id":"write-call"}`), "test-session"), "Write", map[string]any{"path": path, "content": "must not write"})
	if !result.IsError {
		t.Fatalf("expired attempt executed: %+v", result)
	}
	count, unfinished, err := s.Snapshot()
	if err != nil || count != 0 || unfinished {
		t.Fatalf("timeout admission contaminated ledger: count=%d unfinished=%v err=%v", count, unfinished, err)
	}
	if !admitted {
		t.Fatal("dispatch skipped atomic timeout admission")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("timed-out attempt wrote file: %v", err)
	}
}

func TestActionAdmissionPrecedesDurableReceiptAndWrite(t *testing.T) {
	s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
	path := filepath.Join(work, "artifact")
	admitted := false
	ctx := WithActionAdmission(s.ctx, func(start func() error) error {
		count, unfinished, err := s.Snapshot()
		if err != nil || count != 0 || unfinished {
			t.Errorf("admission followed ledger start: %d %v %v", count, unfinished, err)
		}
		admitted = true
		return start()
	})
	s.onActionStarted = func(int) error {
		if !admitted {
			t.Error("receipt preceded timeout admission")
		}
		count, unfinished, err := s.Snapshot()
		if err != nil || count != 1 || !unfinished {
			t.Errorf("receipt preceded durable start: %d %v %v", count, unfinished, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("tool ran before receipt: %v", err)
		}
		return nil
	}
	result := s.call(withRequestID(ctx, []byte(`{"id":"write-call"}`), "test-session"), "Write", map[string]any{"path": path, "content": "artifact"})
	if result.IsError {
		t.Fatalf("admitted action failed: %+v", result)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "artifact" {
		t.Fatalf("admitted action did not write: %q %v", content, err)
	}
	count, unfinished, err := s.Snapshot()
	if err != nil || count != 1 || unfinished {
		t.Fatalf("finished action not recorded: %d %v %v", count, unfinished, err)
	}
}

func TestDeniedToolDoesNotConsumeActionAdmission(t *testing.T) {
	s, work := newTestServer(t, nil)
	called := false
	ctx := WithActionAdmission(s.ctx, func(start func() error) error { called = true; return start() })
	result := s.call(withRequestID(ctx, []byte(`{"id":"write-call"}`), "test-session"), "Write", map[string]any{"path": filepath.Join(work, "denied"), "content": "no"})
	if !result.IsError || called {
		t.Fatalf("authority denial reached action admission: result=%+v admitted=%v", result, called)
	}
	count, unfinished, err := s.Snapshot()
	if err != nil || count != 0 || unfinished {
		t.Fatalf("denied call changed ledger: %d %v %v", count, unfinished, err)
	}
}

func TestActionAdmissionRequiresSuccessfulLedgerStart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failLedger bool
	}{
		{"ledger write failure", true},
		{"admission skips recording", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
			path := filepath.Join(work, "must-not-exist")
			if tc.failLedger {
				s.ledger.mu.Lock()
				s.ledger.err = errors.New("durable write failed")
				s.ledger.mu.Unlock()
			}
			ctx := WithActionAdmission(s.ctx, func(start func() error) error {
				if !tc.failLedger {
					return nil
				}
				return start()
			})
			result := s.call(withRequestID(ctx, []byte(`{"id":"write-call"}`), "test-session"), "Write", map[string]any{"path": path, "content": "no"})
			if !result.IsError {
				t.Fatalf("unrecorded action passed admission: %+v", result)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("tool executed without a durable start: %v", err)
			}
		})
	}
}
