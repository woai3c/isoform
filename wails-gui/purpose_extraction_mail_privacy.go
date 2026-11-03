package main

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"wails-gui/internal/platform/mailheaders"
)

const purposeExtractionMailPolicyVersion = "purpose-mail-privacy-v1"

// PurposeExtractionMailPrivacy is local validation context, never model evidence.
// Keeping it in the immutable input also changes the scheduler fingerprint when
// a newly observed mail header makes an older projection unsafe to reuse.
type PurposeExtractionMailPrivacy struct {
	Version       string `json:"version"`
	SubjectDigest string `json:"subjectDigest"`
	SubjectCount  int    `json:"subjectCount"`
}

func purposeExtractionNormalizedMailSubjects(subjects []string) []string {
	normalized := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		if folded := purposeMailTextFold(mailheaders.NormalizeSubject(subject)); folded != "" {
			normalized = append(normalized, folded)
		}
	}
	return uniqueSortedStrings(normalized)
}

func newPurposeExtractionMailPrivacy(subjects []string) *PurposeExtractionMailPrivacy {
	normalized := purposeExtractionNormalizedMailSubjects(subjects)
	return &PurposeExtractionMailPrivacy{
		Version:       purposeExtractionMailPolicyVersion,
		SubjectDigest: agencyPayloadHash([]byte(strings.Join(normalized, "\x00"))),
		SubjectCount:  len(normalized),
	}
}

func validatePurposeExtractionMailPrivacy(policy *PurposeExtractionMailPrivacy) error {
	if policy == nil || policy.Version != purposeExtractionMailPolicyVersion || policy.SubjectCount < 0 {
		return errors.New("Purpose extraction requires current mail privacy context")
	}
	_, err := normalizePurposeHash(policy.SubjectDigest, "Purpose mail context digest", true)
	return err
}

func purposeExtractionMailSubjects(app *App) ([]string, error) {
	if app == nil || app.store == nil {
		return nil, errors.New("Purpose mail privacy store is unavailable")
	}
	rows, err := app.store.ListMailMessages(context.Background(), 0)
	if err != nil {
		return nil, err
	}
	subjects := make([]string, 0, len(rows))
	for _, row := range rows {
		subjects = append(subjects, row.Subject)
	}
	return purposeExtractionNormalizedMailSubjects(subjects), nil
}

func buildPurposeExtractionPromptV3(input PurposeExtractionInput) (string, error) {
	// Header contents exist only for the deterministic local check. Do not send
	// unrelated mail subjects to the extractor or let it cite them as evidence.
	input.MailPrivacy = nil
	prompt, err := buildPurposeExtractionPromptV2(input)
	if err != nil {
		return "", err
	}
	rule := "Mail subjects and mail body excerpts are not eligible as Purpose titles or desiredOutcome text unless a cited source proves the user typed or said those exact words. Activity summaries, generated memories, knowledge and approved Self statements are not proof of authorship. Use the user's own words from explicit-user authored evidence, or a neutral paraphrase of the intended work without copying source text. Do not carry a mail subject or body excerpt forward from currentPurpose; omit an unsupported hypothesis instead."
	return strings.Replace(prompt, "Your output is a hypothesis only.", rule+"\n\nYour output is a hypothesis only.", 1), nil
}

func purposeMailTextFold(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func purposeMailContains(text, fragment string) bool {
	if fragment == "" {
		return false
	}
	// Word boundaries prevent a short subject such as "Hi" matching "ship".
	if strings.Contains(" "+text+" ", " "+fragment+" ") {
		return true
	}
	for _, r := range fragment {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return strings.Contains(text, fragment)
		}
	}
	return false
}

func purposeExtractionUserWords(source PurposeExtractionSource) string {
	if source.EvidenceClass != PurposeEvidenceExplicitUser {
		return ""
	}
	switch source.SourceKind {
	case taskContextSourceMemoryKind, taskContextSourceKnowledgeKind:
		document, ok := parseOKFDocument(source.Content)
		if !ok {
			return ""
		}
		origin, _ := document.Frontmatter["source"].(string)
		dimension, _ := document.Frontmatter["dimension"].(string)
		if taskContextMarkdownEvidenceClass(origin, dimension) != TaskContextEvidenceExplicitUser {
			return ""
		}
		// The title, tags and metadata are not evidence of the user's words.
		return purposeMailTextFold(document.Body)
	default:
		// A confirmed/generated Self statement is still a projection.
		return ""
	}
}

func purposeExtractionMailTextReason(hypothesis PurposeExtractionHypothesis, input PurposeExtractionInput, currentSubjects []string) string {
	sources := purposeSourcesByRef(input)
	userWords := []string{}
	for _, ref := range hypothesis.EvidenceRefs {
		if words := purposeExtractionUserWords(sources[ref]); words != "" {
			userWords = append(userWords, words)
		}
	}
	userSaid := func(fragment string) bool {
		for _, words := range userWords {
			if purposeMailContains(words, fragment) {
				return true
			}
		}
		return false
	}
	// Subjects remain ephemeral and are read again before recovery. Only their
	// digest/count is frozen, so immutable extraction logs cannot extend Mail's
	// header retention or copy unrelated private subjects.
	normalizedSubjects := purposeExtractionNormalizedMailSubjects(currentSubjects)
	for _, field := range []struct{ name, text string }{
		{"title", hypothesis.Title}, {"desired outcome", hypothesis.DesiredOutcome},
	} {
		folded := purposeMailTextFold(field.text)
		for _, subject := range normalizedSubjects {
			fragment := subject
			if !purposeMailContains(folded, subject) {
				if utf8.RuneCountInString(folded) < 12 || !purposeMailContains(subject, folded) {
					continue
				}
				fragment = folded
			}
			if !userSaid(fragment) {
				return "mail privacy: " + field.name + " repeats a mail subject without cited user-authored words"
			}
		}
		if userSaid(folded) {
			continue
		}
		// Mail bodies are not stored by Mail sync. Their excerpts can survive in
		// generated activity/memory/knowledge, so copying any projection is
		// conservatively disallowed. Neutral paraphrases remain eligible.
		for _, source := range input.Sources {
			content := source.Content
			if document, ok := parseOKFDocument(content); ok {
				// A generated headline can preserve a mail excerpt even when
				// its body was paraphrased. Metadata is not authorship proof,
				// including on otherwise user-authored markdown.
				title, _ := document.Frontmatter["title"].(string)
				content = title
				if purposeExtractionUserWords(source) == "" {
					content += "\n" + document.Body
				}
			}
			content = purposeMailTextFold(content)
			if purposeMailContains(content, folded) {
				return "mail privacy: " + field.name + " copies a non-user source excerpt; use a neutral paraphrase"
			}
			// East Asian text may have no word separators. Bound its copied
			// spans by runes instead, preserving the same authorship exception.
			runes := []rune(folded)
			for start := 0; start+12 <= len(runes); start++ {
				fragment := string(runes[start : start+12])
				if purposeMailHasUnsegmentedText(fragment) && strings.Contains(content, fragment) && !userSaid(fragment) {
					return "mail privacy: " + field.name + " contains a non-user source excerpt; use a neutral paraphrase"
				}
			}
			words := strings.Fields(folded)
			for start := 0; start+4 <= len(words); start++ {
				fragment := strings.Join(words[start:start+4], " ")
				if utf8.RuneCountInString(fragment) >= 24 && purposeMailContains(content, fragment) && !userSaid(fragment) {
					return "mail privacy: " + field.name + " contains a non-user source excerpt; use a neutral paraphrase"
				}
			}
		}
	}
	return ""
}

func purposeMailHasUnsegmentedText(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return true
		}
	}
	return false
}
