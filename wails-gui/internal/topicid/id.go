// Package topicid defines the topic registry's identifier form.
package topicid

import "strings"

// Valid matches the registry's admission rule; it does not assert membership.
func Valid(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 2048 }
