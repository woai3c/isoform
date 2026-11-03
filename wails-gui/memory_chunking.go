package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// chunkMarkdown splits markdown content into overlapping chunks.
// Replicates openclaw's chunkMarkdown algorithm exactly:
//   - tokens × 4 = maxChars (approximate token→char conversion)
//   - overlap × 4 = overlapChars carried from previous chunk
//   - Long lines are segmented to fit within maxChars
//   - Line numbers are 1-indexed
func chunkMarkdown(content string, maxTokens, overlapTokens int) []memoryChunk {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	maxChars := max(32, maxTokens*4)
	overlapChars := max(0, overlapTokens*4)

	type lineEntry struct {
		line   string
		lineNo int
	}

	var chunks []memoryChunk
	var current []lineEntry
	currentChars := 0

	flush := func() {
		if len(current) == 0 {
			return
		}
		var sb strings.Builder
		for i, entry := range current {
			if i > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(entry.line)
		}
		text := sb.String()
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		chunks = append(chunks, memoryChunk{
			StartLine: current[0].lineNo,
			EndLine:   current[len(current)-1].lineNo,
			Text:      text,
			Hash:      hash,
		})
	}

	carryOverlap := func() {
		if overlapChars <= 0 || len(current) == 0 {
			current = nil
			currentChars = 0
			return
		}
		acc := 0
		var kept []lineEntry
		for i := len(current) - 1; i >= 0; i-- {
			acc += len(current[i].line) + 1
			kept = append([]lineEntry{current[i]}, kept...)
			if acc >= overlapChars {
				break
			}
		}
		current = kept
		currentChars = 0
		for _, entry := range kept {
			currentChars += len(entry.line) + 1
		}
	}

	for i, line := range lines {
		lineNo := i + 1
		var segments []string
		if len(line) == 0 {
			segments = []string{""}
		} else {
			for start := 0; start < len(line); start += maxChars {
				end := min(start+maxChars, len(line))
				segments = append(segments, line[start:end])
			}
		}

		for _, segment := range segments {
			lineSize := len(segment) + 1
			if currentChars+lineSize > maxChars && len(current) > 0 {
				flush()
				carryOverlap()
			}
			current = append(current, lineEntry{line: segment, lineNo: lineNo})
			currentChars += lineSize
		}
	}
	flush()

	return chunks
}
