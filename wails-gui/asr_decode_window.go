package main

// ownedDecodeWindow adds context only within a segment's ownership interval.
// Adjacent intervals meet in a pause (or exactly at a forced split), so no
// sample is decoded twice. Bounds and offsets are absolute sample positions.
func ownedDecodeWindow(samples []float32, historyStart, start, length, pad, ownedFrom, ownedTo int) []float32 {
	from := max(historyStart, ownedFrom, start-pad)
	to := min(historyStart+len(samples), ownedTo, start+length+pad)
	if from >= to || from > start || to < start+length {
		return nil
	}
	return samples[from-historyStart : to-historyStart]
}
