package openanswer

import "strings"

const (
	blockOpen     = "<open_answer>"
	blockClose    = "</open_answer>"
	maxBlockBytes = 32 * 1024
)

// BlockFilter separates structured blocks from visible text, including when a
// delimiter is split across arbitrary stream chunks. The zero value is ready.
// Oversized and unfinished blocks are discarded. Call Finish when the row
// ends; never append raw model output directly to the visible row.
type BlockFilter struct {
	tag            string
	pending        string
	inside         bool
	depth          int
	block          strings.Builder
	overflow       bool
	started        int
	answerText     bool
	textAfterBlock bool
}

// NewBlockFilter creates a block filter for a tag without angle brackets.
func NewBlockFilter(tag string) BlockFilter {
	return BlockFilter{tag: tag}
}

// Feed returns safe visible text and complete bounded blocks from this chunk.
func (f *BlockFilter) Feed(chunk string) (string, []string) {
	open, close := f.delimiters()
	f.pending += chunk
	var visible strings.Builder
	var blocks []string
	for len(f.pending) > 0 {
		delimiter := open
		index := strings.Index(f.pending, delimiter)
		if f.inside {
			closeIndex := strings.Index(f.pending, close)
			if closeIndex >= 0 && (index < 0 || closeIndex < index) {
				index, delimiter = closeIndex, close
			}
		}
		if index >= 0 {
			f.writePart(&visible, f.pending[:index])
			f.pending = f.pending[index+len(delimiter):]
			switch delimiter {
			case close:
				f.depth--
				if f.depth != 0 {
					continue
				}
				if !f.overflow {
					blocks = append(blocks, f.block.String())
				}
				f.block.Reset()
				f.overflow = false
				f.inside = false
			case open:
				f.started++
				f.depth++
				if f.inside {
					// A nested block is malformed. Discard the whole outer
					// block, including text after the inner closing tag.
					f.block.Reset()
					f.overflow = true
				}
				f.inside = true
			}
			continue
		}
		keep := delimiterPrefixSuffix(f.pending, open)
		if f.inside {
			if n := delimiterPrefixSuffix(f.pending, close); n > keep {
				keep = n
			}
		}
		f.writePart(&visible, f.pending[:len(f.pending)-keep])
		f.pending = f.pending[len(f.pending)-keep:]
		break
	}
	return visible.String(), blocks
}

// Complete reports whether the row has exactly one complete block and no
// unfinished opening delimiter. Oversized blocks still count toward the limit.
func (f *BlockFilter) Complete() bool {
	open, _ := f.delimiters()
	return f.started == 1 && !f.inside && !partialOpeningDelimiter(f.pending, open)
}

// CandidateComplete additionally requires the block to follow an answer and
// end the row. Filtering remains independent of whether a block is eligible.
func (f *BlockFilter) CandidateComplete() bool {
	return f.Complete() && f.answerText && !f.textAfterBlock && strings.TrimSpace(f.pending) == ""
}

func (f *BlockFilter) StandaloneBlockComplete() bool {
	return f.Complete() && !f.answerText && !f.textAfterBlock && strings.TrimSpace(f.pending) == ""
}

// BlocksStarted includes oversized and unfinished blocks.
func (f *BlockFilter) BlocksStarted() int { return f.started }

// Interrupted reports whether a segment boundary falls inside a block or its
// opening delimiter. The filter must keep this state across the boundary so
// the continuation cannot be emitted as ordinary assistant text.
func (f *BlockFilter) Interrupted() bool {
	open, _ := f.delimiters()
	return f.inside || partialOpeningDelimiter(f.pending, open)
}

func (f *BlockFilter) writePart(visible *strings.Builder, part string) {
	if !f.inside {
		if strings.TrimSpace(part) != "" {
			if f.started == 0 {
				f.answerText = true
			} else {
				f.textAfterBlock = true
			}
		}
		visible.WriteString(part)
		return
	}
	if f.overflow {
		return
	}
	if f.block.Len()+len(part) > maxBlockBytes {
		f.block.Reset()
		f.overflow = true
		return
	}
	f.block.WriteString(part)
}

// Finish flushes ordinary trailing text, but never reveals an unfinished
// block or a recognizable partial opening delimiter. It resets the filter.
func (f *BlockFilter) Finish() string {
	out := ""
	open, _ := f.delimiters()
	if !f.inside && !partialOpeningDelimiter(f.pending, open) {
		out = f.pending
	}
	*f = BlockFilter{tag: f.tag}
	return out
}

func (f *BlockFilter) delimiters() (string, string) {
	if f.tag == "" || f.tag == "open_answer" {
		return blockOpen, blockClose
	}
	return "<" + f.tag + ">", "</" + f.tag + ">"
}

func partialOpeningDelimiter(value, open string) bool {
	return value != "" && strings.HasPrefix(open, value)
}

func delimiterPrefixSuffix(value, delimiter string) int {
	limit := len(delimiter) - 1
	if len(value) < limit {
		limit = len(value)
	}
	for n := limit; n > 0; n-- {
		if strings.HasSuffix(value, delimiter[:n]) {
			return n
		}
	}
	return 0
}
