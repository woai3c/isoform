//go:build cgo

package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	ort "github.com/yalue/onnxruntime_go"

	"wails-gui/internal/onnxdl"
)

const (
	localONNXIntraOpThreads = 2
	localONNXInterOpThreads = 1
)

// localONNXProvider implements EmbeddingProvider using a local ONNX model.
// Uses all-MiniLM-L6-v2 by default (384-dim embeddings).
type localONNXProvider struct {
	tokenizer *wordPieceTokenizer
	session   *ort.DynamicAdvancedSession
	provKey   string
	modelName string
	// sem guards inference: 1-buffered channel acts as a mutex that
	// respects context cancellation (sync.Mutex.Lock does not).
	sem chan struct{}
}

// newLocalONNXProvider initializes the ONNX Runtime and loads the model.
// configDir is the base config directory (e.g. ~/.yansu-agent).
// onProgress is an optional callback for download progress reporting.
func newLocalONNXProvider(configDir string, onProgress downloadProgressFn) (*localONNXProvider, error) {
	paths, err := ensureLocalModel(configDir, onProgress)
	if err != nil {
		return nil, fmt.Errorf("ensure model: %w", err)
	}

	if err := onnxdl.InitializeRuntime(paths.RuntimeLib); err != nil {
		return nil, fmt.Errorf("init onnx runtime: %w", err)
	}

	// Load tokenizer
	tok, err := newWordPieceTokenizer(paths.VocabFile, localModelMaxSeqLen)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer: %w", err)
	}

	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("create onnx session options: %w", err)
	}
	defer options.Destroy()

	if err := options.SetIntraOpNumThreads(localONNXIntraOpThreads); err != nil {
		return nil, fmt.Errorf("set onnx intra-op threads: %w", err)
	}
	if err := options.SetInterOpNumThreads(localONNXInterOpThreads); err != nil {
		return nil, fmt.Errorf("set onnx inter-op threads: %w", err)
	}
	if err := options.SetExecutionMode(ort.ExecutionModeSequential); err != nil {
		return nil, fmt.Errorf("set onnx execution mode: %w", err)
	}

	// Create session with dynamic shapes
	session, err := ort.NewDynamicAdvancedSession(
		paths.ModelFile,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		options,
	)
	if err != nil {
		return nil, fmt.Errorf("create onnx session: %w", err)
	}

	provKey := computeProviderKey("local", "onnx", localModelName)

	log.Printf("[memory] local ONNX embedding ready (model=%s, dims=%d, intra_threads=%d, inter_threads=%d)",
		localModelName, localModelDims, localONNXIntraOpThreads, localONNXInterOpThreads)

	sem := make(chan struct{}, 1)
	sem <- struct{}{}

	return &localONNXProvider{
		tokenizer: tok,
		session:   session,
		provKey:   provKey,
		modelName: localModelName,
		sem:       sem,
	}, nil
}

func (p *localONNXProvider) Provider() string    { return "local-onnx" }
func (p *localONNXProvider) Model() string       { return p.modelName }
func (p *localONNXProvider) ProviderKey() string { return p.provKey }

func (p *localONNXProvider) EmbedQuery(ctx context.Context, text string) ([]float64, error) {
	results, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("empty embedding result")
	}
	return results[0], nil
}

func (p *localONNXProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	semWait := time.Now()
	select {
	case <-p.sem:
		defer func() { p.sem <- struct{}{} }()
	case <-ctx.Done():
		log.Printf("[onnx] EmbedBatch: semaphore wait timed out after %v (batch=%d)", time.Since(semWait), len(texts))
		return nil, ctx.Err()
	}
	log.Printf("[onnx] EmbedBatch: semaphore acquired in %v (batch=%d)", time.Since(semWait), len(texts))

	batchSize := int64(len(texts))
	seqLen := int64(localModelMaxSeqLen)

	// Tokenize all texts
	allInputIDs := make([]int64, 0, batchSize*seqLen)
	allAttentionMask := make([]int64, 0, batchSize*seqLen)
	allTokenTypeIDs := make([]int64, 0, batchSize*seqLen)

	for _, text := range texts {
		tok := p.tokenizer.Tokenize(text)
		allInputIDs = append(allInputIDs, tok.InputIDs...)
		allAttentionMask = append(allAttentionMask, tok.AttentionMask...)
		allTokenTypeIDs = append(allTokenTypeIDs, tok.TokenTypeIDs...)
	}

	shape := ort.NewShape(batchSize, seqLen)

	inputIDsTensor, err := ort.NewTensor(shape, allInputIDs)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer inputIDsTensor.Destroy()

	attMaskTensor, err := ort.NewTensor(shape, allAttentionMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer attMaskTensor.Destroy()

	typeIDsTensor, err := ort.NewTensor(shape, allTokenTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer typeIDsTensor.Destroy()

	// Create output tensor: [batch_size, seq_len, hidden_dim]
	outputShape := ort.NewShape(batchSize, seqLen, int64(localModelDims))
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer outputTensor.Destroy()

	// Run inference. Serialize against the PII GLiNER model via the shared
	// inference gate so the two ONNX models never saturate the CPU together
	// (which starved foreground-app UI threads — DingTalk typing jank).
	inferStart := time.Now()
	releaseInfer := onnxdl.AcquireInference()
	err = p.session.Run(
		[]ort.Value{inputIDsTensor, attMaskTensor, typeIDsTensor},
		[]ort.Value{outputTensor},
	)
	releaseInfer()
	if err != nil {
		return nil, fmt.Errorf("onnx inference: %w", err)
	}
	log.Printf("[onnx] EmbedBatch: inference done in %v (batch=%d, seq_len=%d)", time.Since(inferStart), batchSize, seqLen)

	// Mean pooling over token embeddings (masked by attention mask)
	outputData := outputTensor.GetData()
	results := make([][]float64, batchSize)

	for i := int64(0); i < batchSize; i++ {
		embedding := make([]float64, localModelDims)
		var tokenCount float64

		for j := int64(0); j < seqLen; j++ {
			if allAttentionMask[i*seqLen+j] == 0 {
				continue
			}
			tokenCount++
			baseIdx := i*seqLen*int64(localModelDims) + j*int64(localModelDims)
			for k := 0; k < localModelDims; k++ {
				embedding[k] += float64(outputData[baseIdx+int64(k)])
			}
		}

		// Average
		if tokenCount > 0 {
			for k := range embedding {
				embedding[k] /= tokenCount
			}
		}

		// L2 normalize
		var norm float64
		for _, v := range embedding {
			norm += v * v
		}
		norm = math.Sqrt(norm)
		if norm > 0 {
			for k := range embedding {
				embedding[k] /= norm
			}
		}

		results[i] = embedding
	}

	return results, nil
}

// Destroy cleans up the ONNX session. Safe to call multiple times.
func (p *localONNXProvider) Destroy() {
	<-p.sem // wait for any running inference to finish
	if p.session != nil {
		p.session.Destroy()
		p.session = nil
	}
	// Don't release sem — no further inference is possible.
}
