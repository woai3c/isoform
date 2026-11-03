package eval

// Sample is a single OCR evaluation sample: one image with ground-truth text.
type Sample struct {
	ID        string `json:"id"`
	ImagePath string `json:"image_path"`
	Language  string `json:"language"`
	Category  string `json:"category"`
	// GroundTruth is the exact text that should be recognized from the image.
	GroundTruth string `json:"ground_truth"`
	// Lines holds line-level ground truth when per-line evaluation is needed.
	// If empty, GroundTruth is split on newlines.
	Lines []string `json:"lines,omitempty"`
}

// Dataset is a collection of OCR evaluation samples.
type Dataset struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version"`
	Samples     []Sample `json:"samples"`
}

// OCRResult holds the raw OCR output for one sample.
type OCRResult struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// SampleMetrics holds per-sample evaluation results.
type SampleMetrics struct {
	ID         string  `json:"id"`
	Language   string  `json:"language"`
	Category   string  `json:"category"`
	CER        float64 `json:"cer"`
	WER        float64 `json:"wer"`
	Confidence float64 `json:"confidence"`
	GroundLen  int     `json:"ground_len"`
	PredLen    int     `json:"pred_len"`
}

// GroupMetrics holds aggregated metrics for a group (language, category, etc).
type GroupMetrics struct {
	Group     string  `json:"group"`
	Samples   int     `json:"samples"`
	MeanCER   float64 `json:"mean_cer"`
	MedianCER float64 `json:"median_cer"`
	MeanWER   float64 `json:"mean_wer"`
	MeanConf  float64 `json:"mean_confidence"`
}

// Report holds the full OCR evaluation results.
type Report struct {
	TotalSamples int             `json:"total_samples"`
	Overall      GroupMetrics    `json:"overall"`
	ByLanguage   []GroupMetrics  `json:"by_language"`
	ByCategory   []GroupMetrics  `json:"by_category"`
	Samples      []SampleMetrics `json:"samples"`
}
