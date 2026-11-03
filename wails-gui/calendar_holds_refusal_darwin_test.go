//go:build darwin

package main

import "testing"

func TestHoldEventKitRefusalClassification(t *testing.T) {
	if !eventKitWriteRefused(eventKitErrorNotMutable) || !eventKitWriteRefused(eventKitErrorReadOnly) {
		t.Fatal("EventKit calendar refusal was not classified as unwritable")
	}
	if eventKitWriteRefused(eventKitErrorInternal) {
		t.Fatal("an internal failure was incorrectly classified as a calendar refusal")
	}
}
