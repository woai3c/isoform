package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadDataset reads an evaluation dataset from a JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}
	return &ds, nil
}

// SaveDataset writes a dataset to a JSON file.
func SaveDataset(path string, ds *Dataset) error {
	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// SaveReport persists an evaluation report to a JSON file.
func SaveReport(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadReport loads a previously saved report for comparison.
func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	return &r, nil
}
