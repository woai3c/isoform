package eval

// Annotation represents a single ground-truth PII entity in the dataset.
type Annotation struct {
	Text     string `json:"text"`
	Category string `json:"category"`
	// For text-mode evaluation (character offsets).
	Start int `json:"start,omitempty"`
	End   int `json:"end,omitempty"`
	// For image-mode evaluation (normalized bounding boxes).
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`
}

// Sample is a single evaluation sample (one text or screenshot).
type Sample struct {
	ID          string       `json:"id"`
	Text        string       `json:"text,omitempty"`
	ImagePath   string       `json:"image_path,omitempty"`
	Language    string       `json:"language,omitempty"`
	Annotations []Annotation `json:"annotations"`
}

// Dataset is a collection of evaluation samples.
type Dataset struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version"`
	Samples     []Sample `json:"samples"`
}

// Prediction represents a single PII detection from the system under test.
type Prediction struct {
	Text       string  `json:"text"`
	Category   string  `json:"category"`
	Start      int     `json:"start,omitempty"`
	End        int     `json:"end,omitempty"`
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	Width      float64 `json:"width,omitempty"`
	Height     float64 `json:"height,omitempty"`
	Confidence float64 `json:"confidence"`
}

// DetectorFunc is the interface an evaluated detector must satisfy.
// Given a sample, it returns predictions.
type DetectorFunc func(sample Sample) ([]Prediction, error)
