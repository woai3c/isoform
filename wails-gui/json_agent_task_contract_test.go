package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var (
	noToolsTruePattern  = regexp.MustCompile(`NoTools:\s*true`)
	noSystemTruePattern = regexp.MustCompile(`NoSystem:\s*true`)
)

func TestPureJSONAgentTasksDisableToolsAndSystem(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		marker string
	}{
		{
			name:   "activity memory generation",
			file:   "activity_triage.go",
			marker: `[triager] memory generation ExecuteTask starting`,
		},
		{
			name:   "activity triage",
			file:   "activity_triage.go",
			marker: `[triager] activity triage ExecuteTask starting`,
		},
		{
			name:   "activity knowledge generation",
			file:   "activity_triage.go",
			marker: `[triager] knowledge generation ExecuteTask starting`,
		},
		{
			name:   "connector memory triage",
			file:   "memory_generator.go",
			marker: `[memory] triage ExecuteTask starting`,
		},
		{
			name:   "connector memory generation",
			file:   "memory_generator.go",
			marker: `[memory] generation ExecuteTask starting`,
		},
		{
			name:   "memory knowledge generation",
			file:   "memory_knowledge_generator.go",
			marker: `[memory] knowledge generation ExecuteTask starting`,
		},
		{
			name:   "suggestion generation",
			file:   "suggestion_generator.go",
			marker: `[suggestions] generate ExecuteTask starting`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srcBytes, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatalf("read %s: %v", tt.file, err)
			}
			src := string(srcBytes)
			markerIdx := strings.Index(src, tt.marker)
			if markerIdx < 0 {
				t.Fatalf("marker %q not found in %s", tt.marker, tt.file)
			}
			taskIdx := strings.Index(src[markerIdx:], "&agent.TaskInput{")
			if taskIdx < 0 {
				t.Fatalf("TaskInput after marker %q not found in %s", tt.marker, tt.file)
			}
			taskStart := markerIdx + taskIdx
			taskEndRel := strings.Index(src[taskStart:], "}, func")
			if taskEndRel < 0 {
				t.Fatalf("TaskInput callback boundary after marker %q not found in %s", tt.marker, tt.file)
			}
			taskLiteral := src[taskStart : taskStart+taskEndRel]
			if !noToolsTruePattern.MatchString(taskLiteral) {
				t.Fatalf("%s must set NoTools: true for pure JSON/text generation", tt.name)
			}
			if !noSystemTruePattern.MatchString(taskLiteral) {
				t.Fatalf("%s must set NoSystem: true for pure JSON/text generation", tt.name)
			}
		})
	}
}
