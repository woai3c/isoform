package pii

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

// unigramTokenizer implements SentencePiece Unigram tokenization
// compatible with DeBERTa-v3 / GLiNER models.
type unigramTokenizer struct {
	pieces   []vocabPiece   // vocabulary pieces with scores
	pieceMap map[string]int // piece string -> index in pieces
	clsID    int            // [CLS] token id
	sepID    int            // [SEP] token id
	padID    int            // [PAD] token id
	unkID    int            // [UNK] token id
	entID    int            // <<ENT>> token id
	esepID   int            // <<SEP>> (entity separator) token id
	maxPiece int            // max piece length in runes (for Viterbi)
}

type vocabPiece struct {
	Token string
	Score float64
	ID    int
}

// tokenizerJSON mirrors the HuggingFace tokenizer.json format.
type tokenizerJSON struct {
	Model struct {
		Type  string          `json:"type"`
		Vocab [][]interface{} `json:"vocab"` // [[string, float64], ...]
	} `json:"model"`
	AddedTokens []struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
		Special bool   `json:"special"`
	} `json:"added_tokens"`
}

func newUnigramTokenizer(path string) (*unigramTokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokenizer.json: %w", err)
	}

	var tj tokenizerJSON
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("parse tokenizer.json: %w", err)
	}

	if tj.Model.Type != "Unigram" {
		return nil, fmt.Errorf("expected Unigram tokenizer, got %s", tj.Model.Type)
	}

	t := &unigramTokenizer{
		pieces:   make([]vocabPiece, 0, len(tj.Model.Vocab)),
		pieceMap: make(map[string]int, len(tj.Model.Vocab)),
		unkID:    3,
	}

	maxPiece := 0
	for i, entry := range tj.Model.Vocab {
		if len(entry) < 2 {
			continue
		}
		token, ok := entry[0].(string)
		if !ok {
			continue
		}
		score, ok := entry[1].(float64)
		if !ok {
			continue
		}

		t.pieces = append(t.pieces, vocabPiece{Token: token, Score: score, ID: i})
		t.pieceMap[token] = i

		rl := len([]rune(token))
		if rl > maxPiece {
			maxPiece = rl
		}
	}
	t.maxPiece = maxPiece

	// Register added tokens (special tokens).
	for _, at := range tj.AddedTokens {
		// Only add if not already in vocab.
		if _, exists := t.pieceMap[at.Content]; !exists {
			t.pieceMap[at.Content] = at.ID
		}
		switch at.Content {
		case "[CLS]":
			t.clsID = at.ID
		case "[SEP]":
			t.sepID = at.ID
		case "[PAD]":
			t.padID = at.ID
		case "[UNK]":
			t.unkID = at.ID
		case "<<ENT>>":
			t.entID = at.ID
		case "<<SEP>>":
			t.esepID = at.ID
		}
	}

	if len(t.pieces) == 0 {
		return nil, fmt.Errorf("empty vocabulary")
	}

	return t, nil
}

// Encode tokenizes text into token IDs using the Viterbi algorithm.
// The metaspace character (▁) is prepended to the text for each word.
func (t *unigramTokenizer) Encode(text string) []int {
	if text == "" {
		return nil
	}

	// Metaspace pre-tokenization: split on whitespace, prepend ▁ to each word.
	words := strings.Fields(text)
	var ids []int
	for _, word := range words {
		piece := "\u2581" + word // ▁ prefix
		ids = append(ids, t.viterbi(piece)...)
	}
	return ids
}

// EncodeWord tokenizes a single word (with ▁ prefix already added if needed).
func (t *unigramTokenizer) EncodeWord(word string) []int {
	piece := "\u2581" + word
	return t.viterbi(piece)
}

// viterbi finds the optimal tokenization using the Viterbi algorithm.
// For each position, we find the best preceding split point that maximizes
// the total log-probability score.
func (t *unigramTokenizer) viterbi(text string) []int {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return nil
	}

	// bestScore[i] = best total score for runes[0:i].
	// bestEnd[i] = the length of the piece ending at position i in the best path.
	bestScore := make([]float64, n+1)
	bestLen := make([]int, n+1)

	for i := range bestScore {
		bestScore[i] = math.Inf(-1)
	}
	bestScore[0] = 0

	for i := 1; i <= n; i++ {
		// Try all pieces ending at position i.
		maxJ := t.maxPiece
		if maxJ > i {
			maxJ = i
		}
		for j := 1; j <= maxJ; j++ {
			piece := string(runes[i-j : i])
			idx, ok := t.pieceMap[piece]
			if !ok {
				continue
			}
			score := bestScore[i-j] + t.pieces[idx].Score
			if score > bestScore[i] {
				bestScore[i] = score
				bestLen[i] = j
			}
		}

		// If no piece found ending at position i, fall back to single-char UNK.
		if math.IsInf(bestScore[i], -1) {
			bestScore[i] = bestScore[i-1] + (-100) // large penalty for UNK
			bestLen[i] = 1
		}
	}

	// Backtrack to recover the best tokenization.
	var pieces []int
	pos := n
	for pos > 0 {
		pLen := bestLen[pos]
		piece := string(runes[pos-pLen : pos])
		idx, ok := t.pieceMap[piece]
		if ok {
			pieces = append(pieces, idx)
		} else {
			pieces = append(pieces, t.unkID)
		}
		pos -= pLen
	}

	// Reverse.
	for i, j := 0, len(pieces)-1; i < j; i, j = i+1, j-1 {
		pieces[i], pieces[j] = pieces[j], pieces[i]
	}

	return pieces
}

// splitWords splits text into "words" for GLiNER's word-level processing.
// Splits on whitespace first, then separates punctuation from alphanumeric runs.
// Returns words and their character offsets in the original text.
type wordSpan struct {
	Word  string
	Start int // byte offset in original text
	End   int // byte offset (exclusive)
}

func splitWords(text string) []wordSpan {
	var result []wordSpan

	// Split on whitespace first, preserving byte offsets.
	i := 0
	for i < len(text) {
		// Skip whitespace.
		for i < len(text) && text[i] == ' ' {
			i++
		}
		if i >= len(text) {
			break
		}

		// Find end of whitespace-delimited token.
		j := i
		for j < len(text) && text[j] != ' ' {
			j++
		}
		token := text[i:j]
		tokenStart := i

		// Sub-split on punctuation boundaries.
		// "john@example.com" -> "john", "@", "example", ".", "com"
		// "555-0123" stays as one word (hyphen within digits)
		subWords := splitOnPunctBoundaries(token)
		offset := tokenStart
		for _, sw := range subWords {
			// Find sw in token starting from current offset
			idx := strings.Index(text[offset:], sw)
			if idx < 0 {
				// Shouldn't happen, but be safe
				result = append(result, wordSpan{Word: sw, Start: offset, End: offset + len(sw)})
				offset += len(sw)
				continue
			}
			start := offset + idx
			result = append(result, wordSpan{Word: sw, Start: start, End: start + len(sw)})
			offset = start + len(sw)
		}

		i = j
	}

	return result
}

// splitOnPunctBoundaries splits a token at boundaries between punctuation and
// non-punctuation characters. "john@example.com" -> ["john", "@", "example", ".", "com"].
// Hyphens are NOT treated as split points: "555-0123" stays as one word.
func splitOnPunctBoundaries(token string) []string {
	runes := []rune(token)
	if len(runes) == 0 {
		return nil
	}

	var parts []string
	start := 0

	for i := 1; i < len(runes); i++ {
		prevPunct := isPunctSplitChar(runes[i-1])
		currPunct := isPunctSplitChar(runes[i])

		if prevPunct != currPunct {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

func isPunctSplitChar(r rune) bool {
	// Match the GLiNER words_splitter behavior: split on common punctuation
	// but NOT on hyphens within words.
	if r == '@' || r == '.' || r == ',' || r == ';' || r == ':' ||
		r == '!' || r == '?' || r == '(' || r == ')' || r == '[' || r == ']' ||
		r == '{' || r == '}' || r == '"' || r == '\'' || r == '/' || r == '\\' ||
		r == '#' || r == '$' || r == '%' || r == '&' || r == '*' || r == '+' ||
		r == '=' || r == '<' || r == '>' || r == '~' || r == '^' || r == '`' || r == '|' {
		return true
	}
	return false
}
