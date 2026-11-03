package activitymon

import "sync"

// captureBufferPool recycles the per-call pixel buffers allocated by
// CaptureScreen. A full-resolution capture is ~8–14 MB on typical displays;
// at 1.5 s debounce + 3 s visual-change ticker this is the hottest allocation
// path in the activity monitor. Reusing buffers eliminates most of the GC
// churn without changing the callers' ownership semantics — they still treat
// the returned slice as owned, and return it explicitly via
// ReleaseCaptureBuffer when done.
var captureBufferPool sync.Pool

// AcquireCaptureBuffer returns a byte slice of length `size` drawn from the
// pool when a buffer of at least that capacity is available, otherwise a
// fresh allocation. Returns nil when size <= 0. Callers must treat the
// contents as uninitialised and overwrite every byte they read (CaptureScreen
// does a full copy before returning).
func AcquireCaptureBuffer(size int) []byte {
	if size <= 0 {
		return nil
	}
	if v := captureBufferPool.Get(); v != nil {
		if bp, ok := v.(*[]byte); ok {
			b := *bp
			if cap(b) >= size {
				return b[:size]
			}
		}
	}
	return make([]byte, size)
}

// ReleaseCaptureBuffer returns b to the capture-buffer pool. Safe to call
// with a nil / zero-capacity slice. Pass the exact slice returned by
// AcquireCaptureBuffer (or CaptureScreen / CaptureRaw); aliases of a smaller
// window will shrink the pooled capacity.
func ReleaseCaptureBuffer(b []byte) {
	if cap(b) == 0 {
		return
	}
	b = b[:0]
	captureBufferPool.Put(&b)
}
