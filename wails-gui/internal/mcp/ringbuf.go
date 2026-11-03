// Package mcp manages MCP (Model Context Protocol) server subprocesses
// on behalf of the Wails GUI agent loop. This file defines a small
// line-oriented ring buffer used to retain a tail of each server's
// stderr without unbounded memory growth.
package mcp

import (
	"bufio"
	"io"
	"sync"
)

// lineRing keeps the last `cap` lines written to it. Writes that share
// a buffer boundary are split on '\n'; partial trailing lines are
// flushed when followed by another newline. The buffer is safe for
// concurrent calls between Append (writer) and Snapshot (reader).
type lineRing struct {
	mu    sync.Mutex
	buf   []string
	cap   int
	start int  // index of oldest line when full
	full  bool // true once buf has wrapped at least once
}

func newLineRing(capacity int) *lineRing {
	if capacity < 1 {
		capacity = 1
	}
	return &lineRing{buf: make([]string, 0, capacity), cap: capacity}
}

// Append records a single fully-formed log line.
func (r *lineRing) Append(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		r.buf = append(r.buf, line)
		if len(r.buf) == r.cap {
			r.full = true
		}
		return
	}
	r.buf[r.start] = line
	r.start = (r.start + 1) % r.cap
}

// Snapshot returns the most recent up to `n` lines in chronological
// order. n<=0 returns everything currently held.
func (r *lineRing) Snapshot(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := len(r.buf)
	if n <= 0 || n > count {
		n = count
	}
	if n == 0 {
		return nil
	}
	out := make([]string, n)
	if !r.full {
		copy(out, r.buf[count-n:])
		return out
	}
	// When full, lines are stored circularly; oldest is at r.start.
	for i := 0; i < n; i++ {
		idx := (r.start + r.cap - n + i + r.cap) % r.cap
		out[i] = r.buf[idx]
	}
	return out
}

// captureLines reads from rd line-by-line and appends each line to ring
// until rd returns EOF or another error. It is intended to run in a
// dedicated goroutine for the lifetime of a child process. Lines are
// truncated to maxLineBytes characters so a single misbehaving server
// can't blow up resident memory by writing megabyte-long lines.
func captureLines(rd io.Reader, ring *lineRing, maxLineBytes int) {
	if rd == nil || ring == nil {
		return
	}
	scanner := bufio.NewScanner(rd)
	if maxLineBytes > 0 {
		// bufio.Scanner default token size is 64KB. Allow callers to
		// raise the soft cap; the underlying buffer grows on demand up
		// to maxLineBytes before splitting.
		scanner.Buffer(make([]byte, 0, 4096), maxLineBytes)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if maxLineBytes > 0 && len(line) > maxLineBytes {
			line = line[:maxLineBytes] + "...(truncated)"
		}
		ring.Append(line)
	}
}
