package toolserver

import (
	"bytes"
	"container/list"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/native"
)

func newReplayCacheServer(t *testing.T) (*Server, string, *atomic.Int32) {
	t.Helper()
	home := retentionRoot(t)
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(home, "state"))
	s, work := newTestServer(t, []authority.Grant{
		{ID: "read", Capability: "tool.read", ResourcePrefix: "work"},
		{ID: "write", Capability: "tool.write", ResourcePrefix: "work"},
	})
	uses := new(atomic.Int32)
	s.gate.Config.Emit = func(event native.LoopEvent) {
		if event.Type == "tool_use" {
			uses.Add(1)
		}
	}
	return s, work, uses
}

// Inspect the actual retained byte slices, not just the accounting counter.
func assertReplayCacheSize(t *testing.T, s *Server, wantCalls, wantResults int) {
	t.Helper()
	s.callMu.Lock()
	defer s.callMu.Unlock()
	retained := map[string]*list.Element{}
	for element := s.results.Front(); element != nil; element = element.Next() {
		result := element.Value.(cachedCall)
		retained[result.id] = element
		if result.sessionID == "" || s.calls[result.id].cachedResult != element {
			t.Fatal("cached result lost its session or call tombstone")
		}
	}
	actualBytes, cachedCalls := 0, 0
	for id, call := range s.calls {
		if call.cachedResult == nil {
			continue
		}
		cachedCalls++
		result := call.cachedResult.Value.(cachedCall)
		actualBytes += len(result.encoded)
		if result.id != id || retained[id] != call.cachedResult {
			t.Fatal("call tombstone retains a result outside the FIFO cache")
		}
	}
	if cachedCalls != s.results.Len() {
		t.Fatal("FIFO cache and retained call results disagree")
	}
	if actualBytes != s.resultBytes || actualBytes > 8<<20 {
		t.Fatalf("retained bytes = %d, accounting = %d, limit = 8 MiB", actualBytes, s.resultBytes)
	}
	if len(s.calls) != wantCalls || s.results.Len() != wantResults {
		t.Fatalf("cache has %d calls and %d results; want %d and %d", len(s.calls), s.results.Len(), wantCalls, wantResults)
	}
	if wantResults > 0 && actualBytes == 0 {
		t.Fatal("nonempty cache retains no encoded result bytes")
	}
}

func replayLedger(t *testing.T, s *Server, wantActions int) []byte {
	t.Helper()
	if count, unfinished, err := s.Snapshot(); err != nil || unfinished || count != wantActions {
		t.Fatalf("ledger count = %d, unfinished = %v, error = %v; want %d completed actions", count, unfinished, err, wantActions)
	}
	raw, err := os.ReadFile(s.LedgerPath())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReplayCacheBoundsCumulativeTextAndImageResults(t *testing.T) {
	s, work, uses := newReplayCacheServer(t)
	c := newRequestIDClient(t, s)
	textPath := filepath.Join(work, "large.txt")
	if err := os.WriteFile(textPath, []byte(strings.Repeat("x", 3<<20)), 0600); err != nil {
		t.Fatal(err)
	}
	// A real uncompressed PNG produces roughly 3 MiB of base64 image data.
	// Text and image payloads must share the same cumulative byte budget.
	var imageFile bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&imageFile, image.NewGray(image.Rect(0, 0, 1536, 1536))); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(work, "large.png")
	if err := os.WriteFile(imagePath, imageFile.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var lastImage json.RawMessage
	for i := range 8 {
		if i == 2 {
			// A cache hit must not promote the oldest entry as an LRU would.
			c.call(t, "read-0", "Read", map[string]any{"path": textPath}, false)
		}
		path := textPath
		if i%2 == 1 {
			path = imagePath
		}
		result := c.call(t, fmt.Sprintf("read-%d", i), "Read", map[string]any{"path": path}, false)
		if len(result) < 3<<20 || len(result) > 4<<20 {
			t.Fatalf("large result %d has %d bytes; fixture no longer exercises the 8 MiB cumulative limit", i, len(result))
		}
		if i%2 == 1 {
			lastImage = result
		}
		wantResults := min(i+1, 2)
		assertReplayCacheSize(t, s, i+1, wantResults)
		if i == 2 {
			c.call(t, "read-0", "Read", map[string]any{"path": textPath}, true)
			c.call(t, "read-1", "Read", map[string]any{"path": imagePath}, false)
		}
	}
	before := replayLedger(t, s, 8)
	if result := c.call(t, "read-0", "Read", map[string]any{"path": textPath}, true); !bytes.Contains(result, []byte("result no longer available, not run again")) {
		t.Fatalf("evicted replay = %s", result)
	}
	for _, changed := range []struct {
		name string
		path string
	}{{"Read", imagePath}, {"Write", textPath}} {
		if result := c.call(t, "read-0", changed.name, map[string]any{"path": changed.path}, true); !bytes.Contains(result, []byte("reused with different arguments or a different tool")) {
			t.Fatalf("evicted ID accepted a changed payload: %s", result)
		}
	}
	// FIFO leaves exactly the final two results, regardless of their kind.
	c.call(t, "read-5", "Read", map[string]any{"path": imagePath}, true)
	c.call(t, "read-6", "Read", map[string]any{"path": textPath}, false)
	replayed := c.call(t, "read-7", "Read", map[string]any{"path": imagePath}, false)
	if !bytes.Equal(replayed, lastImage) {
		t.Fatal("cached image replay changed the MCP wire result")
	}
	var imageResult struct {
		Content []struct {
			Type string `json:"type"`
			Data string `json:"data"`
		} `json:"content"`
	}
	if err := json.Unmarshal(replayed, &imageResult); err != nil {
		t.Fatal(err)
	}
	imageBlocks := 0
	for _, block := range imageResult.Content {
		if block.Type == "image" {
			imageBlocks++
			if block.Data != base64.StdEncoding.EncodeToString(imageFile.Bytes()) {
				t.Fatal("cached image replay changed image bytes")
			}
		}
	}
	if imageBlocks != 1 {
		t.Fatalf("cached replay has %d image blocks", imageBlocks)
	}
	if uses.Load() != 8 || !bytes.Equal(before, replayLedger(t, s, 8)) {
		t.Fatal("cached or evicted replay dispatched again or changed the ledger")
	}
	assertReplayCacheSize(t, s, 8, 2)
	s.callMu.Lock()
	defer s.callMu.Unlock()
	for _, call := range s.calls {
		if call.outcome != callSucceeded {
			t.Fatal("eviction lost the completed call outcome")
		}
	}
}

func TestReplayCacheDoesNotRetainOversizedResults(t *testing.T) {
	s, work, uses := newReplayCacheServer(t)
	c := newRequestIDClient(t, s)
	smallPath := filepath.Join(work, "small.txt")
	if err := os.WriteFile(smallPath, []byte("keep this cached result"), 0600); err != nil {
		t.Fatal(err)
	}
	smallArgs := map[string]any{"path": smallPath}
	smallResult := c.call(t, "small", "Read", smallArgs, false)
	path := filepath.Join(work, "oversized.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("z", 9<<20)), 0600); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"path": path}
	if result := c.call(t, "oversized", "Read", args, false); len(result) <= 8<<20 {
		t.Fatalf("oversized fixture returned only %d bytes", len(result))
	}
	assertReplayCacheSize(t, s, 2, 1)
	before := replayLedger(t, s, 2)
	if result := c.call(t, "oversized", "Read", args, true); !bytes.Contains(result, []byte("result no longer available, not run again")) {
		t.Fatalf("oversized replay = %s", result)
	}
	if replay := c.call(t, "small", "Read", smallArgs, false); !bytes.Equal(replay, smallResult) {
		t.Fatal("oversized result displaced an existing cached result")
	}
	if uses.Load() != 2 || !bytes.Equal(before, replayLedger(t, s, 2)) {
		t.Fatal("oversized replay dispatched again or changed the ledger")
	}
	assertReplayCacheSize(t, s, 2, 1)
	s.callMu.Lock()
	defer s.callMu.Unlock()
	for _, call := range s.calls {
		if call.outcome != callSucceeded {
			t.Fatal("oversized result lost its successful outcome")
		}
	}
}

func waitForReplayResults(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.callMu.Lock()
		count := s.results.Len()
		s.callMu.Unlock()
		if count == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("disconnect did not release session results; want %d remaining", want)
}

func TestReplayCacheDisconnectReleasesOnlyItsSessionResults(t *testing.T) {
	s, work, uses := newReplayCacheServer(t)
	path := filepath.Join(work, "small.txt")
	if err := os.WriteFile(path, []byte("session-local result"), 0600); err != nil {
		t.Fatal(err)
	}
	first, second := newRequestIDClient(t, s), newRequestIDClient(t, s)
	args := map[string]any{"path": path}
	first.call(t, "first", "Read", map[string]any{"path": filepath.Join(work, "missing.txt")}, true)
	secondResult := second.call(t, "second", "Read", args, false)
	assertReplayCacheSize(t, s, 2, 2)
	s.callMu.Lock()
	firstID := s.results.Front().Value.(cachedCall).id
	secondID := s.results.Back().Value.(cachedCall).id
	s.callMu.Unlock()
	before := replayLedger(t, s, 2)
	if err := first.conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitForReplayResults(t, s, 1)
	assertReplayCacheSize(t, s, 2, 1)
	s.callMu.Lock()
	firstCall, firstFound := s.calls[firstID]
	secondCall, secondFound := s.calls[secondID]
	s.callMu.Unlock()
	if !firstFound || !secondFound || firstCall.cachedResult != nil || secondCall.cachedResult == nil || firstCall.outcome != callFailed || secondCall.outcome != callSucceeded {
		t.Fatal("disconnect removed tombstones or released the other session's result")
	}
	if replay := second.call(t, "second", "Read", args, false); !bytes.Equal(replay, secondResult) {
		t.Fatal("another session's cached result changed after disconnect")
	}
	if err := second.conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitForReplayResults(t, s, 0)
	assertReplayCacheSize(t, s, 2, 0)
	if uses.Load() != 2 || !bytes.Equal(before, replayLedger(t, s, 2)) {
		t.Fatal("disconnect or surviving-session replay changed execution history")
	}
}

func TestReplayCacheCallLimitRefusesNewIDsBeforeDispatch(t *testing.T) {
	s, work, uses := newReplayCacheServer(t)
	c := newRequestIDClient(t, s)
	// Fill only metadata to avoid thousands of fsyncs. The last admitted
	// action and the first refused request both cross the real MCP gate.
	s.callMu.Lock()
	for i := range 4095 {
		s.calls[fmt.Sprintf("prior-%d", i)] = completedCall{name: "Read", argumentHash: "old", outcome: callSucceeded}
	}
	s.callMu.Unlock()
	path := filepath.Join(work, "artifact.txt")
	args := map[string]any{"path": path, "content": "original"}
	last := c.call(t, "last-admitted", "Write", args, false)
	assertReplayCacheSize(t, s, 4096, 1)
	before := replayLedger(t, s, 1)
	if err := os.WriteFile(path, []byte("changed by user"), 0600); err != nil {
		t.Fatal(err)
	}
	refused := c.call(t, "over-limit", "Write", map[string]any{"path": path, "content": "must not execute"}, true)
	if !bytes.Contains(refused, []byte("call ID limit")) {
		t.Fatalf("new ID refusal = %s", refused)
	}
	if replay := c.call(t, "last-admitted", "Write", args, false); !bytes.Equal(replay, last) {
		t.Fatal("call limit prevented replay of an admitted cached result")
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "changed by user" {
		t.Fatalf("new or replayed call repeated a write: %q, %v", content, err)
	}
	if uses.Load() != 1 || !bytes.Equal(before, replayLedger(t, s, 1)) {
		t.Fatal("call cap emitted a tool-use event or appended a ledger action")
	}
	assertReplayCacheSize(t, s, 4096, 1)
	if err := c.conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitForReplayResults(t, s, 0)
	reconnected := newRequestIDClient(t, s)
	if refused := reconnected.call(t, "new-session", "Write", args, true); !bytes.Contains(refused, []byte("call ID limit")) {
		t.Fatalf("disconnect reset the run's call limit: %s", refused)
	}
	if uses.Load() != 1 || !bytes.Equal(before, replayLedger(t, s, 1)) {
		t.Fatal("new session bypassed the run's call cap")
	}
	assertReplayCacheSize(t, s, 4096, 0)
}
