//go:build !darwin

package pii

func newExternalDetector(_ string) (Detector, bool, error) {
	return nil, false, nil
}
