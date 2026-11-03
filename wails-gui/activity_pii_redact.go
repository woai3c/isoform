package main

import (
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon/appparser"
)

// redactParsedContentPII applies the same regex text redaction used for flat
// AX text to structured parser payloads before they are persisted.
func redactParsedContentPII(p *appparser.ParsedContent) int {
	if p == nil {
		return 0
	}

	count := 0
	redact := func(s *string) {
		if s == nil || *s == "" {
			return
		}
		redacted, n := pii.RedactText(*s)
		if n == 0 {
			return
		}
		*s = redacted
		count += n
	}

	redact(&p.Title)
	redact(&p.Body)
	redact(&p.URL)
	redact(&p.Channel)

	for i := range p.Messages {
		redact(&p.Messages[i].Sender)
		redact(&p.Messages[i].Content)
		redact(&p.Messages[i].ThreadParent)
	}

	for k, v := range p.Metadata {
		redacted, n := pii.RedactText(v)
		if n == 0 {
			continue
		}
		p.Metadata[k] = redacted
		count += n
	}

	return count
}
