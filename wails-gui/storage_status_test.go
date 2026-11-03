package main

import (
	"errors"
	"fmt"
	"testing"

	"wails-gui/storage"
)

func TestStorageStartupStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want StorageStartupStatus
	}{
		{name: "ready"},
		{
			name: "newer schema wrapped by storage initialization",
			err: fmt.Errorf("initialize storage: %w", &storage.SchemaVersionError{
				StoredVersion: 61, SupportedVersion: 60,
			}),
			want: StorageStartupStatus{Code: "schema_too_new", StoredVersion: 61, SupportedVersion: 60},
		},
		{
			name: "other storage failure",
			err:  errors.New("permission denied"),
			want: StorageStartupStatus{Code: "storage_unavailable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storageStartupStatus(tt.err); got != tt.want {
				t.Fatalf("storageStartupStatus() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
