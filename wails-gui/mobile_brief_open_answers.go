package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wails-gui/internal/openanswer"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func (a *App) appendOpenAnswerBriefParagraph(ctx context.Context, brief *MobileBriefV1, start, end time.Time, language string) {
	if a == nil || a.store == nil || brief == nil || a.IsClosedEyesModeEnabled() || ctx.Err() != nil {
		return
	}
	posts, err := a.store.OpenAnswerBriefPosts(ctx, start, end)
	if err != nil || ctx.Err() != nil || a.IsClosedEyesModeEnabled() {
		return
	}
	type entry struct {
		post storage.OpenAnswerBriefPost
		line string
	}
	var entries []entry
	for _, post := range posts {
		if post.HasMoment && post.OpenedOrActed {
			continue
		}
		candidate := openanswer.Candidate{Question: post.Question, Topic: post.Topic, Source: post.Source}
		if !a.openAnswerBriefPostAllowed(ctx, candidate, post.Content) {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(post.Content), "\n", 2)[0])
		question := strings.TrimSpace(post.Question)
		if line == "" || question == "" {
			continue
		}
		entries = append(entries, entry{post: post, line: line})
		if len(entries) == 3 {
			break
		}
	}
	if len(entries) == 0 || len(brief.Evidence)+len(entries) > mobileBriefEvidenceLimit || ctx.Err() != nil || a.IsClosedEyesModeEnabled() {
		return
	}
	var parts []string
	for _, item := range entries {
		question := strings.Join(strings.Fields(item.post.Question), " ")
		parts = append(parts, question+": "+item.line)
	}
	text := "You asked: " + strings.Join(parts, "; ") + "."
	if briefChinese(language) {
		parts = parts[:0]
		for _, item := range entries {
			question := strings.Join(strings.Fields(item.post.Question), " ")
			parts = append(parts, question+"："+item.line)
		}
		text = "你问过：" + strings.Join(parts, "；") + "。"
	}
	if !mobileBriefCopySafe(text) {
		return
	}
	var refs []string
	for _, item := range entries {
		ref := fmt.Sprintf("e%d", len(brief.Evidence)+1)
		brief.Evidence = append(brief.Evidence, MobileBriefRef{
			Ref:        ref,
			SourceKind: "chat_message",
			SourceID:   item.post.MessageID,
			Label:      item.post.Question,
			ObservedAt: formatMobileBriefTime(time.UnixMilli(item.post.CreatedAt)),
		})
		refs = append(refs, ref)
	}
	brief.Narrative = append(brief.Narrative, MobileBriefPara{Text: text, Refs: refs})
	if brief.NarrativeStatus == "quiet" {
		brief.NarrativeStatus = "ok"
	}
}

func (a *App) openAnswerBriefPostAllowed(ctx context.Context, candidate openanswer.Candidate, content string) bool {
	if a == nil || a.store == nil || ctx.Err() != nil || a.IsClosedEyesModeEnabled() ||
		!openAnswerCandidateTextAllowed(candidate) || !openAnswerBriefTextAllowed(content) {
		return false
	}
	muted, err := a.store.OpenAnswerKindMuted(ctx, candidate.Topic)
	return err == nil && !muted && ctx.Err() == nil && !a.IsClosedEyesModeEnabled()
}

func openAnswerBriefTextAllowed(content string) bool {
	return !pii.HasRedaction(content) && pii.RedactCodes(content) == content && !proactiveVoiceRestrictedText(content)
}

func mobileBriefIsOpenAnswerParagraph(brief MobileBriefV1, paragraph MobileBriefPara) bool {
	if !(strings.HasPrefix(paragraph.Text, "You asked: ") || strings.HasPrefix(paragraph.Text, "你问过：")) || len(paragraph.Refs) == 0 {
		return false
	}
	evidence := mobileBriefEvidenceByRef(brief)
	for _, id := range paragraph.Refs {
		if evidence[id].SourceKind != "chat_message" {
			return false
		}
	}
	return true
}
