package main

// Test helpers for synthesizing WAV blobs in unit tests. Lives in a
// dedicated _test.go file so it isn't compiled into the production binary.

func makeTestWAV(sampleRate, numSamples int) []byte {
	dataSize := numSamples * 2 // 16-bit samples
	fileSize := 36 + dataSize

	buf := make([]byte, 44+dataSize)
	// RIFF header
	copy(buf[0:], "RIFF")
	le32(buf[4:], uint32(fileSize))
	copy(buf[8:], "WAVE")
	// fmt chunk
	copy(buf[12:], "fmt ")
	le32(buf[16:], 16) // chunk size
	le16(buf[20:], 1)  // PCM
	le16(buf[22:], 1)  // mono
	le32(buf[24:], uint32(sampleRate))
	le32(buf[28:], uint32(sampleRate*2)) // byte rate
	le16(buf[32:], 2)                    // block align
	le16(buf[34:], 16)                   // bits per sample
	// data chunk
	copy(buf[36:], "data")
	le32(buf[40:], uint32(dataSize))
	// PCM data is already zeroed (silence)

	return buf
}

func le32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func le16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}
