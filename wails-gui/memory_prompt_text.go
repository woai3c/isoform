package main

import (
	"regexp"
	"strings"
)

// A belief reads as a sentence about the person (Y42). Text that came from a
// prompt, a tool's instructions or an unfilled template is never a belief or
// its detail, so generators drop it before saving and the About you read hides
// what was already saved. Nothing on disk is deleted.
//
// memoryPromptTextPattern mirrors memoryTextIsPromptOrTemplate in
// frontend/src/lib/memory-content.ts; change both together.
var memoryPromptTextPattern = regexp.MustCompile(`(?i)\(s\)|\{[a-z_][a-z0-9_]*\}|system-reminder|<system|output[- ]format|\bthe task asks\b|\bonly the (?:translated|json|markdown)\b|\b(?:reply|respond|return) (?:with |only |just |one )*json\b|\bjson only\b|\bone json object\b|\b(?:return|respond|reply) only\b`)

// memoryPromptLabelPattern catches a prompt's labelled lines ("Rules: ...").
var memoryPromptLabelPattern = regexp.MustCompile(`(?im)^\s*(?:rules|instructions|system prompt|output)\s*:`)

// memoryTextIsPromptOrTemplate reports whether text reads as a prompt, a tool
// instruction or an unfilled template ("sentence(s)", "{company}").
func memoryTextIsPromptOrTemplate(text string) bool {
	return memoryPromptTextPattern.MatchString(text) || memoryPromptLabelPattern.MatchString(text)
}

// memoryEntryIsPromptText reports whether a saved memory's title or any of its
// memory points is prompt or template text. Such an entry is hidden from the
// About you read.
func memoryEntryIsPromptText(title, content string) bool {
	if memoryTextIsPromptOrTemplate(title) {
		return true
	}
	for _, point := range parseMemoryPoints(content) {
		if memoryTextIsPromptOrTemplate(point) {
			return true
		}
	}
	return false
}

// dropPromptMemoryPoints screens a generated memory before it is saved. A
// prompt title drops every point, so the save is skipped; prompt points are
// removed one by one.
func dropPromptMemoryPoints(resp *mcMemoryGenerationResponse) {
	if resp == nil {
		return
	}
	if memoryTextIsPromptOrTemplate(resp.Title) {
		resp.MemoryPoints = nil
		return
	}
	kept := make([]string, 0, len(resp.MemoryPoints))
	for _, point := range resp.MemoryPoints {
		if strings.TrimSpace(point) == "" || memoryTextIsPromptOrTemplate(point) {
			continue
		}
		kept = append(kept, point)
	}
	resp.MemoryPoints = kept
}
