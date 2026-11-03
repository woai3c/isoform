package pii

import (
	"fmt"
	"log"
	"math"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"wails-gui/internal/onnxdl"
	"wails-gui/internal/platform/ocr"
)

const (
	opfNumLabels        = 33
	opfONNXIntraThreads = 2
	opfONNXInterThreads = 1
)

// id2label maps output class indices to BIOES labels.
var opfID2Label = [opfNumLabels]string{
	0: "O",
	1: "B-account_number", 2: "I-account_number", 3: "E-account_number", 4: "S-account_number",
	5: "B-private_address", 6: "I-private_address", 7: "E-private_address", 8: "S-private_address",
	9: "B-private_date", 10: "I-private_date", 11: "E-private_date", 12: "S-private_date",
	13: "B-private_email", 14: "I-private_email", 15: "E-private_email", 16: "S-private_email",
	17: "B-private_person", 18: "I-private_person", 19: "E-private_person", 20: "S-private_person",
	21: "B-private_phone", 22: "I-private_phone", 23: "E-private_phone", 24: "S-private_phone",
	25: "B-private_url", 26: "I-private_url", 27: "E-private_url", 28: "S-private_url",
	29: "B-secret", 30: "I-secret", 31: "E-secret", 32: "S-secret",
}

// opfDetector implements Detector using the OpenAI privacy-filter ONNX model.
type opfDetector struct {
	session   *ort.DynamicAdvancedSession
	tokenizer *bpeTokenizer
	mu        sync.Mutex
	unhealthy bool
}

// NewOPFDetector initializes the OpenAI privacy-filter detector.
func NewOPFDetector(configDir string, onProgress ProgressFn) (Detector, error) {
	paths, err := ensureOPFModel(configDir, onProgress)
	if err != nil {
		return nil, err
	}

	if err := onnxdl.InitializeRuntime(paths.RuntimeLib); err != nil {
		return nil, err
	}

	tok, err := newBPETokenizer(paths.TokenizerFile)
	if err != nil {
		return nil, err
	}

	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()

	if err := configureGLiNERSessionOptions(options); err != nil {
		return nil, err
	}

	session, err := ort.NewDynamicAdvancedSession(
		paths.ModelFile,
		[]string{"input_ids", "attention_mask"},
		[]string{"logits"},
		options,
	)
	if err != nil {
		return nil, err
	}

	d := &opfDetector{session: session, tokenizer: tok}

	if err := d.smokeTest(); err != nil {
		d.session.Destroy()
		return nil, err
	}

	log.Printf("[opf] OpenAI privacy-filter detector ready")
	return d, nil
}

func (d *opfDetector) IsReady() bool {
	return d.session != nil && !d.unhealthy
}

func (d *opfDetector) Destroy() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.session != nil {
		d.session.Destroy()
		d.session = nil
	}
}

func (d *opfDetector) smokeTest() error {
	dummy := []ocr.TextObservation{{Text: "test", X: 0.1, Y: 0.1, Width: 0.1, Height: 0.02, Confidence: 1.0}}
	_ = d.Detect(dummy)
	if d.unhealthy {
		return fmt.Errorf("OPF model smoke test failed")
	}
	return nil
}

func (d *opfDetector) Detect(observations []ocr.TextObservation) []DetectionResult {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.session == nil || d.unhealthy || len(observations) == 0 {
		return nil
	}

	// Concatenate all observation text with newlines, tracking byte offsets
	// back to observations for bounding box mapping.
	var textBuilder strings.Builder
	ranges := make([]obsRange, 0, len(observations))

	for i, obs := range observations {
		text := strings.TrimSpace(obs.Text)
		if text == "" {
			continue
		}
		if i > 0 {
			textBuilder.WriteByte('\n')
		}
		start := textBuilder.Len()
		textBuilder.WriteString(text)
		ranges = append(ranges, obsRange{obs: obs, byteStart: start, byteEnd: textBuilder.Len()})
	}

	fullText := textBuilder.String()
	if fullText == "" {
		return nil
	}

	encoded := d.tokenizer.Encode(fullText)
	if len(encoded.IDs) == 0 {
		return nil
	}

	// Run inference.
	seqLen := int64(len(encoded.IDs))

	inputIDs := make([]int64, seqLen)
	attMask := make([]int64, seqLen)
	for i, id := range encoded.IDs {
		inputIDs[i] = int64(id)
		attMask[i] = 1
	}

	inputTensor, err := ort.NewTensor(ort.NewShape(1, seqLen), inputIDs)
	if err != nil {
		log.Printf("[opf] create input tensor: %v", err)
		d.unhealthy = true
		return nil
	}
	defer inputTensor.Destroy()

	attTensor, err := ort.NewTensor(ort.NewShape(1, seqLen), attMask)
	if err != nil {
		log.Printf("[opf] create attention tensor: %v", err)
		d.unhealthy = true
		return nil
	}
	defer attTensor.Destroy()

	outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, seqLen, opfNumLabels))
	if err != nil {
		log.Printf("[opf] create output tensor: %v", err)
		d.unhealthy = true
		return nil
	}
	defer outTensor.Destroy()

	if err := d.session.Run(
		[]ort.Value{inputTensor, attTensor},
		[]ort.Value{outTensor},
	); err != nil {
		log.Printf("[opf] inference error: %v", err)
		d.unhealthy = true
		return nil
	}

	logits := outTensor.GetData()

	// Decode BIOES spans.
	spans := decodeBIOES(logits, int(seqLen), encoded.Offsets, fullText)

	// Map spans back to OCR observation bounding boxes.
	var results []DetectionResult
	for _, span := range spans {
		obs := findOverlappingObs(ranges, span.byteStart, span.byteEnd)
		if obs == nil {
			continue
		}
		results = append(results, DetectionResult{
			Text:       span.text,
			Category:   span.category,
			X:          obs.X,
			Y:          obs.Y,
			Width:      obs.Width,
			Height:     obs.Height,
			Confidence: float64(span.confidence),
		})
	}

	return results
}

type bioesSpan struct {
	category   string
	text       string
	byteStart  int
	byteEnd    int
	confidence float32
}

func decodeBIOES(logits []float32, seqLen int, offsets [][2]int, fullText string) []bioesSpan {
	type activeSpan struct {
		category  string
		byteStart int
		byteEnd   int
		sumScore  float32
		count     int
	}

	var spans []bioesSpan
	var active *activeSpan

	closeActive := func() {
		if active == nil {
			return
		}
		if active.byteStart < active.byteEnd && active.byteEnd <= len(fullText) {
			spans = append(spans, bioesSpan{
				category:   active.category,
				text:       fullText[active.byteStart:active.byteEnd],
				byteStart:  active.byteStart,
				byteEnd:    active.byteEnd,
				confidence: active.sumScore / float32(active.count),
			})
		}
		active = nil
	}

	for i := 0; i < seqLen; i++ {
		if i >= len(offsets) {
			break
		}
		off := offsets[i]
		if off[0] == off[1] {
			continue
		}

		// Argmax over labels.
		bestLabel := 0
		bestScore := float32(-math.MaxFloat32)
		for j := 0; j < opfNumLabels; j++ {
			score := logits[i*opfNumLabels+j]
			if score > bestScore {
				bestScore = score
				bestLabel = j
			}
		}

		label := opfID2Label[bestLabel]
		prefix, category := parseBIOESLabel(label)
		if category == "account_number" && (!accountTokenOffsetsAllowed(fullText, off[0], off[1]) ||
			active != nil && active.category == category && (prefix == "I" || prefix == "E") && off[0] < active.byteEnd) {
			// Invalid token offsets must not become a valid enclosing span or
			// reach a string slice before the final account boundary check.
			active = nil
			continue
		}

		switch prefix {
		case "S":
			closeActive()
			if off[0] < off[1] && off[1] <= len(fullText) {
				spans = append(spans, bioesSpan{
					category:   category,
					text:       fullText[off[0]:off[1]],
					byteStart:  off[0],
					byteEnd:    off[1],
					confidence: sigmoid32(bestScore),
				})
			}
		case "B":
			closeActive()
			active = &activeSpan{
				category: category, byteStart: off[0], byteEnd: off[1],
				sumScore: sigmoid32(bestScore), count: 1,
			}
		case "I", "E":
			if active != nil && active.category == category {
				active.byteEnd = off[1]
				active.sumScore += sigmoid32(bestScore)
				active.count++
			} else {
				closeActive()
				active = &activeSpan{
					category: category, byteStart: off[0], byteEnd: off[1],
					sumScore: sigmoid32(bestScore), count: 1,
				}
			}
			if prefix == "E" {
				closeActive()
			}
		default:
			closeActive()
		}
	}
	closeActive()

	filtered := spans[:0]
	for _, span := range spans {
		if span.category != "account_number" || accountSpanAllowed(fullText, span.byteStart, span.byteEnd) {
			filtered = append(filtered, span)
		}
	}
	return filtered
}

func parseBIOESLabel(label string) (prefix, category string) {
	if label == "O" {
		return "O", ""
	}
	idx := strings.IndexByte(label, '-')
	if idx < 0 {
		return label, ""
	}
	return label[:idx], label[idx+1:]
}

func sigmoid32(x float32) float32 {
	return float32(1.0 / (1.0 + math.Exp(-float64(x))))
}

type obsRange struct {
	obs       ocr.TextObservation
	byteStart int
	byteEnd   int
}

func findOverlappingObs(ranges []obsRange, start, end int) *ocr.TextObservation {
	for i := range ranges {
		if ranges[i].byteStart <= start && end <= ranges[i].byteEnd {
			return &ranges[i].obs
		}
	}
	for i := range ranges {
		if ranges[i].byteStart < end && start < ranges[i].byteEnd {
			return &ranges[i].obs
		}
	}
	return nil
}
