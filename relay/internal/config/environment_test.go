package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestEnvironmentPrefersISOFORMName(t *testing.T) {
	values := map[string]string{
		"ISOFORM_RELAY_ADDR": "127.0.0.1:8792",
	}
	env := NewEnvironment(func(name string) string { return values[name] }, nil)
	if got := env.Get("ISOFORM_RELAY_ADDR"); got != "127.0.0.1:8792" {
		t.Fatalf("Get() = %q", got)
	}
	if err := env.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentFallsBackAndWarnsOnce(t *testing.T) {
	values := map[string]string{"YANSU_RELAY_ADDR": "127.0.0.1:8792"}
	var logs []string
	env := NewEnvironment(
		func(name string) string { return values[name] },
		func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	)
	for range 2 {
		if got := env.Get("ISOFORM_RELAY_ADDR"); got != values["YANSU_RELAY_ADDR"] {
			t.Fatalf("Get() = %q", got)
		}
	}
	if err := env.Err(); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "YANSU_RELAY_ADDR") || !strings.Contains(logs[0], "ISOFORM_RELAY_ADDR") {
		t.Fatalf("deprecation logs = %#v", logs)
	}
	if strings.Contains(logs[0], values["YANSU_RELAY_ADDR"]) {
		t.Fatalf("deprecation log exposed the value: %q", logs[0])
	}
}

func TestEnvironmentRejectsConflictingNames(t *testing.T) {
	values := map[string]string{
		"ISOFORM_RELAY_ADDR": "127.0.0.1:8791",
		"YANSU_RELAY_ADDR":   "127.0.0.1:8792",
	}
	env := NewEnvironment(func(name string) string { return values[name] }, nil)
	_ = env.Get("ISOFORM_RELAY_ADDR")
	err := env.Err()
	if err == nil || !strings.Contains(err.Error(), "ISOFORM_RELAY_ADDR") || !strings.Contains(err.Error(), "YANSU_RELAY_ADDR") {
		t.Fatalf("conflict error = %v", err)
	}
	if strings.Contains(err.Error(), "8791") || strings.Contains(err.Error(), "8792") {
		t.Fatalf("conflict error exposed a value: %v", err)
	}
}
