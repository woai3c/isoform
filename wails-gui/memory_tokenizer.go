package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// wordPieceTokenizer implements BERT-style WordPiece tokenization in pure Go.
// Compatible with all-MiniLM-L6-v2 and other BERT-based models.
type wordPieceTokenizer struct {
	vocab   map[string]int32
	maxLen  int // max sequence length (model-dependent)
	clsID   int32
	sepID   int32
	padID   int32
	unkID   int32
	maxWord int // max characters per sub-word lookup
}

// tokenizeResult holds the output of tokenization ready for ONNX input.
type tokenizeResult struct {
	InputIDs      []int64
	AttentionMask []int64
	TokenTypeIDs  []int64
}

// newWordPieceTokenizer loads a vocab.txt and creates a tokenizer.
func newWordPieceTokenizer(vocabPath string, maxLen int) (*wordPieceTokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("open vocab: %w", err)
	}
	defer f.Close()

	vocab := make(map[string]int32)
	scanner := bufio.NewScanner(f)
	var idx int32
	for scanner.Scan() {
		token := scanner.Text()
		vocab[token] = idx
		idx++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read vocab: %w", err)
	}

	if len(vocab) == 0 {
		return nil, fmt.Errorf("empty vocabulary")
	}

	t := &wordPieceTokenizer{
		vocab:   vocab,
		maxLen:  maxLen,
		maxWord: 200,
	}

	// Resolve special token IDs
	if id, ok := vocab["[CLS]"]; ok {
		t.clsID = id
	}
	if id, ok := vocab["[SEP]"]; ok {
		t.sepID = id
	}
	if id, ok := vocab["[PAD]"]; ok {
		t.padID = id
	}
	if id, ok := vocab["[UNK]"]; ok {
		t.unkID = id
	} else {
		t.unkID = 100 // BERT default
	}

	return t, nil
}

// Tokenize converts text into model-ready token IDs with attention mask.
func (t *wordPieceTokenizer) Tokenize(text string) tokenizeResult {
	// 1. Lowercase + clean
	text = strings.ToLower(text)
	text = cleanText(text)

	// 2. Basic tokenization: split on whitespace and punctuation
	words := basicTokenize(text)

	// 3. WordPiece sub-word tokenization
	var tokens []int32
	tokens = append(tokens, t.clsID) // [CLS]

	for _, word := range words {
		subTokens := t.wordPiece(word)
		// Check if adding these tokens would exceed maxLen - 1 (need room for [SEP])
		if len(tokens)+len(subTokens)+1 > t.maxLen {
			break
		}
		tokens = append(tokens, subTokens...)
	}

	tokens = append(tokens, t.sepID) // [SEP]

	// 4. Pad to maxLen
	seqLen := len(tokens)
	inputIDs := make([]int64, t.maxLen)
	attentionMask := make([]int64, t.maxLen)
	tokenTypeIDs := make([]int64, t.maxLen)

	for i := 0; i < seqLen; i++ {
		inputIDs[i] = int64(tokens[i])
		attentionMask[i] = 1
	}
	// Rest stays 0 (pad)

	return tokenizeResult{
		InputIDs:      inputIDs,
		AttentionMask: attentionMask,
		TokenTypeIDs:  tokenTypeIDs,
	}
}

// wordPiece applies the WordPiece algorithm to a single word.
func (t *wordPieceTokenizer) wordPiece(word string) []int32 {
	if len(word) > t.maxWord {
		return []int32{t.unkID}
	}

	var tokens []int32
	start := 0
	runes := []rune(word)

	for start < len(runes) {
		end := len(runes)
		var found bool
		for end > start {
			substr := string(runes[start:end])
			if start > 0 {
				substr = "##" + substr
			}
			if id, ok := t.vocab[substr]; ok {
				tokens = append(tokens, id)
				found = true
				break
			}
			end--
		}
		if !found {
			tokens = append(tokens, t.unkID)
			break
		}
		start = end
	}

	return tokens
}

// cleanText removes control characters and normalizes whitespace.
func cleanText(text string) string {
	var sb strings.Builder
	for _, r := range text {
		if r == 0 || r == 0xFFFD || unicode.IsControl(r) {
			continue
		}
		if unicode.IsSpace(r) {
			sb.WriteRune(' ')
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// basicTokenize splits on whitespace and punctuation, matching BERT's BasicTokenizer.
func basicTokenize(text string) []string {
	// Insert spaces around punctuation
	var sb strings.Builder
	for _, r := range text {
		if unicode.IsPunct(r) || isCJK(r) {
			sb.WriteRune(' ')
			sb.WriteRune(r)
			sb.WriteRune(' ')
		} else {
			sb.WriteRune(r)
		}
	}
	return strings.Fields(sb.String())
}

// isCJK checks if a rune is in a CJK Unicode block (each character is a separate token).
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}
