package main

import (
	"strings"
	"testing"
)

func TestPerfDeriveWarningsAddsGLiNERHeadroom(t *testing.T) {
	snap := PerfSnapshot{
		UptimeMs: 80 * 60 * 1000,
		Runtime: PerfRuntimeStats{
			Goroutines: 53,
			OSThreads:  54,
			RSSmb:      1020,
		},
		ActivityPipeline: PerfActivityPipelineStats{
			PII: PerfPIIPipelineStats{Mode: "gliner"},
		},
	}

	warnings := perfDeriveWarnings(snap)
	for _, warning := range warnings {
		if strings.Contains(warning, "Main process RSS") {
			t.Fatalf("unexpected RSS warning with GLiNER headroom: %v", warnings)
		}
	}
}

func TestPerfDeriveWarningsFlagsRSSAboveGLiNERHeadroom(t *testing.T) {
	snap := PerfSnapshot{
		UptimeMs: 80 * 60 * 1000,
		Runtime: PerfRuntimeStats{
			Goroutines: 53,
			OSThreads:  54,
			RSSmb:      1500,
		},
		ActivityPipeline: PerfActivityPipelineStats{
			PII: PerfPIIPipelineStats{Mode: "gliner"},
		},
	}

	warnings := perfDeriveWarnings(snap)
	for _, warning := range warnings {
		if strings.Contains(warning, "Main process RSS") && strings.Contains(warning, "800 GLiNER") {
			return
		}
	}
	t.Fatalf("expected RSS warning with GLiNER headroom, got %v", warnings)
}

func TestPerfDeriveWarningsOmitsZeroGLiNERHeadroom(t *testing.T) {
	snap := PerfSnapshot{
		UptimeMs: 80 * 60 * 1000,
		Runtime: PerfRuntimeStats{
			Goroutines: 53,
			OSThreads:  54,
			RSSmb:      700,
		},
		ActivityPipeline: PerfActivityPipelineStats{
			PII: PerfPIIPipelineStats{Mode: "regex"},
		},
	}

	warnings := perfDeriveWarnings(snap)
	for _, warning := range warnings {
		if strings.Contains(warning, "Main process RSS") {
			if strings.Contains(warning, "GLiNER") {
				t.Fatalf("regex-mode RSS warning should not mention GLiNER headroom: %v", warnings)
			}
			return
		}
	}
	t.Fatalf("expected RSS warning without GLiNER headroom, got %v", warnings)
}
