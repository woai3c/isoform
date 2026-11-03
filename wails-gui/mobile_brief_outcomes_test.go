package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMobileBriefNarrativeCombinedReadingBudget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		words, han int
		allowed    bool
	}{
		{"English allowance", 300, 0, true},
		{"English over", 301, 0, false},
		{"Chinese allowance", 0, 600, true},
		{"Chinese over", 0, 601, false},
		{"mixed allowance", 120, 360, true},
		{"mixed over", 120, 361, false},
		{"two allowances", 237, 480, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Four paragraphs reproduce the provider wire contract without
			// independently exceeding its per-paragraph byte cap.
			paragraphs := make([]MobileBriefPara, 4)
			reply := mobileBriefNarrativeReply{}
			for i := range paragraphs {
				words, han := tc.words/4, tc.han/4
				if i == 0 {
					words += tc.words % 4
					han += tc.han % 4
				}
				text := strings.TrimSpace(strings.Repeat("w ", words)) + strings.Repeat("字", han)
				if text == "" {
					continue
				}
				if len(text) > mobileBriefNarrativeMaxBytes {
					t.Fatal("budget fixture exceeded the independent paragraph byte cap")
				}
				paragraphs[i] = MobileBriefPara{Text: text, Refs: []string{"e1"}}
				reply.Paragraphs = append(reply.Paragraphs, mobileBriefNarrativeReplyParagraph{Text: text, Refs: []string{"e1"}})
			}
			if got := mobileBriefNarrativeWithinBudget(paragraphs); got != tc.allowed {
				t.Errorf("budget allowed=%v want=%v", got, tc.allowed)
			}
			raw, _ := json.Marshal(reply)
			evidence := []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "safe"}}
			parsed, err := parseMobileBriefNarrative(string(raw), evidence)
			if err != nil {
				t.Fatal(err)
			}
			brief := MobileBriefV1{Evidence: evidence}
			// Parsing retains valid paragraphs so Highlights can be trimmed first.
			// These forward-only fixtures must fit the shared budget at preparation.
			_, fits := fitMobileBriefNarrative(brief, parsed, "English")
			if fits != tc.allowed {
				t.Errorf("prepared fit=%v want=%v", fits, tc.allowed)
			}
			err = validateMobileBriefNarrativeVoice(brief, paragraphs, "English")
			if (err == nil) != tc.allowed {
				t.Errorf("voice allowed=%v want=%v: %v", err == nil, tc.allowed, err)
			}
		})
	}
}
