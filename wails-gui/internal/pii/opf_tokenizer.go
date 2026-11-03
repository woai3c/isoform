package pii

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// bpeTokenizer implements BPE tokenization compatible with the HuggingFace
// tokenizers library format used by the OpenAI privacy-filter model.
type bpeTokenizer struct {
	vocab    map[string]int
	merges   []bpeMerge
	preRegex *regexp.Regexp
	padID    int
}

type bpeMerge struct {
	a, b string
	rank int
}

type hfTokenizerJSON struct {
	Model struct {
		Type   string          `json:"type"`
		Vocab  map[string]int  `json:"vocab"`
		Merges json.RawMessage `json:"merges"`
	} `json:"model"`
	AddedTokens []struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
		Special bool   `json:"special"`
	} `json:"added_tokens"`
}

// bpeEncodeResult holds token IDs and their byte offsets in the original text.
type bpeEncodeResult struct {
	IDs     []int
	Offsets [][2]int // [start, end) byte offsets for each token
}

// parseBPEMerges handles both string ("a b") and array (["a","b"]) merge formats.
func parseBPEMerges(raw json.RawMessage) ([]bpeMerge, error) {
	// Try array-of-arrays format first: [["a","b"], ...]
	var arrayMerges [][2]string
	if err := json.Unmarshal(raw, &arrayMerges); err == nil {
		merges := make([]bpeMerge, len(arrayMerges))
		for i, pair := range arrayMerges {
			merges[i] = bpeMerge{a: pair[0], b: pair[1], rank: i}
		}
		return merges, nil
	}

	// Fallback: array-of-strings format: ["a b", ...]
	var stringMerges []string
	if err := json.Unmarshal(raw, &stringMerges); err != nil {
		return nil, fmt.Errorf("parse merges: %w", err)
	}
	merges := make([]bpeMerge, len(stringMerges))
	for i, m := range stringMerges {
		parts := strings.SplitN(m, " ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid merge line %d: %q", i, m)
		}
		merges[i] = bpeMerge{a: parts[0], b: parts[1], rank: i}
	}
	return merges, nil
}

func newBPETokenizer(path string) (*bpeTokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokenizer: %w", err)
	}

	var hf hfTokenizerJSON
	if err := json.Unmarshal(data, &hf); err != nil {
		return nil, fmt.Errorf("parse tokenizer: %w", err)
	}

	if hf.Model.Type != "BPE" {
		return nil, fmt.Errorf("expected BPE tokenizer, got %s", hf.Model.Type)
	}

	merges, err := parseBPEMerges(hf.Model.Merges)
	if err != nil {
		return nil, err
	}

	// Add special tokens to vocab.
	for _, at := range hf.AddedTokens {
		hf.Model.Vocab[at.Content] = at.ID
	}

	padID := 199999
	if id, ok := hf.Model.Vocab["<|endoftext|>"]; ok {
		padID = id
	}

	// GPT-4 style pre-tokenizer regex (simplified for PII detection context).
	preRegex := regexp.MustCompile(
		`[^\r\n\pL\pN]?[\pL]+` +
			`|\pN{1,3}` +
			`| ?[^\s\pL\pN]+[\r\n]*` +
			`|\s+`,
	)

	return &bpeTokenizer{
		vocab:    hf.Model.Vocab,
		merges:   merges,
		preRegex: preRegex,
		padID:    padID,
	}, nil
}

// Encode tokenizes text and returns token IDs with byte offsets.
func (t *bpeTokenizer) Encode(text string) bpeEncodeResult {
	if text == "" {
		return bpeEncodeResult{}
	}

	chunks := t.preTokenize(text)

	var allIDs []int
	var allOffsets [][2]int

	for _, chunk := range chunks {
		ids, spans := t.bpeEncode(chunk.text)
		for i, id := range ids {
			allIDs = append(allIDs, id)
			allOffsets = append(allOffsets, [2]int{
				chunk.start + spans[i][0],
				chunk.start + spans[i][1],
			})
		}
	}

	return bpeEncodeResult{IDs: allIDs, Offsets: allOffsets}
}

type preChunk struct {
	text  string
	start int
}

func (t *bpeTokenizer) preTokenize(text string) []preChunk {
	locs := t.preRegex.FindAllStringIndex(text, -1)
	chunks := make([]preChunk, 0, len(locs))
	for _, loc := range locs {
		chunks = append(chunks, preChunk{
			text:  text[loc[0]:loc[1]],
			start: loc[0],
		})
	}
	return chunks
}

type bpeSymbol struct {
	text string
	// byte offsets within the pre-tokenized chunk
	start, end int
}

// bpeEncode applies BPE merges to a single pre-tokenized chunk.
// Returns token IDs and byte-offset spans within the chunk.
func (t *bpeTokenizer) bpeEncode(text string) ([]int, [][2]int) {
	if text == "" {
		return nil, nil
	}

	// Initialize: each UTF-8 byte as its own symbol.
	symbols := make([]bpeSymbol, 0, len(text))
	for i := 0; i < len(text); {
		_, size := utf8.DecodeRuneInString(text[i:])
		sym := text[i : i+size]
		symbols = append(symbols, bpeSymbol{text: sym, start: i, end: i + size})
		i += size
	}

	// Build merge rank lookup.
	mergeRank := make(map[[2]string]int, len(t.merges))
	for _, m := range t.merges {
		mergeRank[[2]string{m.a, m.b}] = m.rank
	}

	// Iteratively apply the lowest-rank merge.
	for len(symbols) > 1 {
		bestRank := len(t.merges) + 1
		bestIdx := -1

		for i := 0; i < len(symbols)-1; i++ {
			pair := [2]string{symbols[i].text, symbols[i+1].text}
			if rank, ok := mergeRank[pair]; ok && rank < bestRank {
				bestRank = rank
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}

		merged := bpeSymbol{
			text:  symbols[bestIdx].text + symbols[bestIdx+1].text,
			start: symbols[bestIdx].start,
			end:   symbols[bestIdx+1].end,
		}
		symbols = append(symbols[:bestIdx], append([]bpeSymbol{merged}, symbols[bestIdx+2:]...)...)
	}

	ids := make([]int, len(symbols))
	spans := make([][2]int, len(symbols))
	for i, sym := range symbols {
		id, ok := t.vocab[sym.text]
		if !ok {
			// Unknown token — try byte fallback encoding.
			id = t.encodeByteFallback(sym.text)
		}
		ids[i] = id
		spans[i] = [2]int{sym.start, sym.end}
	}

	return ids, spans
}

// encodeByteFallback encodes unknown bytes using the byte-level fallback
// tokens (0x00..0xFF mapped to specific vocab entries). Returns the first
// byte's fallback ID or 0 (UNK).
func (t *bpeTokenizer) encodeByteFallback(s string) int {
	if len(s) == 0 {
		return 0
	}
	// Try the hex byte token format used by many BPE tokenizers: <0xNN>
	key := fmt.Sprintf("<0x%02X>", s[0])
	if id, ok := t.vocab[key]; ok {
		return id
	}
	return 0
}
