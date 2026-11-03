package main

import (
	"encoding/hex"
	"strconv"
	"strings"
)

const memoryForgetHistoricalPrefix = "memory-version:"

func memoryForgetHistoricalSelector(id, content string) string {
	return memoryForgetHistoricalPrefix + id + ":" + taskContextHash(strings.TrimSpace(content))[:16]
}

// A corrected live record keeps its identity. Only references to an erased
// older version participate in the cascade. Legacy references without version
// evidence are ambiguous and must be shown in the removal preview; a qualified
// reference in the same row resolves an accompanying bare source ID.
func memoryForgetHistoricalRefMatches(ref string, refs []string, ids map[string]bool) bool {
	for selector := range ids {
		value, ok := strings.CutPrefix(selector, memoryForgetHistoricalPrefix)
		if !ok {
			continue
		}
		separator := strings.LastIndexByte(value, ':')
		if separator <= 0 {
			continue
		}
		id, erasedHash := value[:separator], value[separator+1:]
		if !memoryForgetVersionHash(erasedHash) {
			continue
		}
		hash, matches := memoryForgetRefVersion(ref, id)
		if !matches {
			continue
		}
		if hash != "" {
			if hash == erasedHash {
				return true
			}
			continue
		}
		qualified := false
		for _, sibling := range refs {
			if versionHash, sameSource := memoryForgetRefVersion(sibling, id); sameSource && versionHash != "" {
				qualified = true
				break
			}
		}
		if !qualified {
			return true
		}
	}
	return false
}

// The frozen-context format is kind:id[:version]:timestamp:content-hash.
// Match the known ID before parsing the suffix so IDs containing ':' remain
// distinguishable and references to another source kind never inherit it.
func memoryForgetRefVersion(ref, id string) (string, bool) {
	value := strings.TrimSpace(ref)
	if value == id || value == id+".md" {
		return "", true
	}
	kind, value, typed := strings.Cut(value, ":")
	if !typed {
		return "", false
	}
	switch kind {
	case "memory", "memory_markdown":
	default:
		return "", false
	}
	var suffix string
	switch true {
	case value == id || value == id+".md":
		return "", true
	case strings.HasPrefix(value, id+":"):
		suffix = strings.TrimPrefix(value, id+":")
	case strings.HasPrefix(value, id+".md:"):
		suffix = strings.TrimPrefix(value, id+".md:")
	default:
		return "", false
	}
	parts := strings.Split(suffix, ":")
	if len(parts) < 2 {
		return "", true
	}
	hash := parts[len(parts)-1]
	if _, err := strconv.ParseInt(parts[len(parts)-2], 10, 64); err != nil || !memoryForgetVersionHash(hash) {
		return "", true
	}
	return hash, true
}

func memoryForgetVersionHash(value string) bool {
	if len(value) != 16 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
