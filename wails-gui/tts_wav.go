package main

import (
	"bytes"
	"encoding/binary"
)

// normalizePeak scales samples so the loudest one hits target (0..1). Gain is
// capped so near-silent clips don't get blown up into noise. No-op on silence.
func normalizePeak(samples []float32, target float32) []float32 {
	var peak float32
	for _, sample := range samples {
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	if peak == 0 {
		return samples
	}
	gain := target / peak
	if gain > 8 {
		gain = 8
	}
	if gain > 0.99 && gain < 1.01 {
		return samples
	}
	for i := range samples {
		samples[i] *= gain
	}
	return samples
}

// applyEdgeFades ramps the first and last ~12ms of a clip linearly so it
// starts and ends at zero amplitude. VITS/Kokoro output doesn't guarantee a
// zero-level tail, and a non-zero final sample becomes an audible click/pop
// when playback jumps to silence. Short enough to be inaudible as a fade.
func applyEdgeFades(samples []float32, sampleRate int) []float32 {
	fade := sampleRate * 12 / 1000
	if fade*2 > len(samples) {
		fade = len(samples) / 2
	}
	for i := 0; i < fade; i++ {
		gain := float32(i) / float32(fade)
		samples[i] *= gain
		samples[len(samples)-1-i] *= gain
	}
	return samples
}

// encodeWAV writes float32 [-1,1] samples as a 16-bit PCM mono WAV file in
// memory.
func encodeWAV(samples []float32, sampleRate int) []byte {
	dataLen := len(samples) * 2
	buf := bytes.NewBuffer(make([]byte, 0, 44+dataLen))

	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataLen))

	data := make([]byte, dataLen)
	for i, sample := range samples {
		if sample > 1 {
			sample = 1
		} else if sample < -1 {
			sample = -1
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(sample*32767)))
	}
	_, _ = buf.Write(data)
	return buf.Bytes()
}
