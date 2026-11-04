package main

import (
	"context"
	"errors"
	"log"
)

var errMorningBriefNoEligibleLead = codeError("morning brief has no source-backed eligible lead")

type morningBriefWriterInput struct {
	Selection  morningBriefSelection
	Opening    morningBriefOpening
	Reasons    map[string]string
	WeekIntent string
	Rules      []string
	PriorLeads []string
}

// runMorningBriefPipeline keeps source checks on both sides of each model
// call. Admission is the caller's responsibility: all candidates, previous
// text, profile, topic totals and their full provenance must be bound before
// invoking it. A missing eligible lead is not proof of a quiet day.
func runMorningBriefPipeline(ctx context.Context, name, weekIntent string, goals, rules, priorLeads, previousBriefs []string,
	candidates []morningBriefCandidate, checkSources func(context.Context) error,
	call func(context.Context, string) (string, error),
	resolveRefs func(context.Context, morningBriefSelection) (map[string][]string, error),
	verifyLead func(context.Context, morningBriefCandidate, morningBriefOpening, func(context.Context, string) (string, error)) (*MobileBriefPara, error),
	write func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error)) (morningBriefDraft, bool, error) {
	return runMorningBriefPipelineAttempt(ctx, name, weekIntent, goals, rules, priorLeads, previousBriefs, candidates, checkSources, call, resolveRefs, verifyLead, write, false)
}

func runMorningBriefPipelineAttempt(ctx context.Context, name, weekIntent string, goals, rules, priorLeads, previousBriefs []string,
	candidates []morningBriefCandidate, checkSources func(context.Context) error,
	call func(context.Context, string) (string, error),
	resolveRefs func(context.Context, morningBriefSelection) (map[string][]string, error),
	verifyLead func(context.Context, morningBriefCandidate, morningBriefOpening, func(context.Context, string) (string, error)) (*MobileBriefPara, error),
	write func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error), fixedOrder bool) (morningBriefDraft, bool, error) {
	if checkSources == nil || call == nil || resolveRefs == nil || verifyLead == nil || write == nil {
		return morningBriefDraft{}, false, codeError("morning brief pipeline is unavailable")
	}
	priorLeads = priorLeads[:min(len(priorLeads), 3)]
	previousBriefs = previousBriefs[:min(len(previousBriefs), 3)]
	var sourceErr error
	check := func(ctx context.Context) error {
		if sourceErr == nil {
			sourceErr = ctx.Err()
			if sourceErr == nil {
				sourceErr = checkSources(ctx)
			}
		}
		return sourceErr
	}
	checkedCall := func(ctx context.Context, prompt string) (string, error) {
		if err := check(ctx); err != nil {
			return "", err
		}
		result, err := call(ctx, prompt)
		if err != nil {
			return "", err
		}
		if err := check(ctx); err != nil {
			return "", err
		}
		return result, nil
	}
	var scores morningBriefScores
	var selection morningBriefSelection
	var err error
	unscored := fixedOrder
	if fixedOrder {
		selection = morningBriefFallbackSelection(candidates)
	} else {
		scores, selection, err = scoreMorningBrief(ctx, name, weekIntent, goals, rules, candidates, checkedCall)
	}
	if err != nil {
		if sourceErr != nil {
			return morningBriefDraft{}, false, withLogStage("score", sourceErr)
		}
		if ctx.Err() != nil {
			return morningBriefDraft{}, false, withLogStage("score", ctx.Err())
		}
		// A brief in plain order is better than no brief: the writer and
		// judges still run, only the scorer's ranking and openings are absent.
		log.Printf("[mobile-brief] ranked morning scoring fell back to the fixed order: %s", safeLogError(err))
		scores, selection, unscored = morningBriefScores{}, morningBriefFallbackSelection(candidates), true
	}
	if selection.Lead == nil || !unscored && len(scores.Openings) == 0 {
		// A real person's day rarely has a lead the fixed gates accept. With
		// admitted candidates, write them in the same plain order as a scoring
		// error; only an empty or wholly ungated set has no brief to write.
		plain := morningBriefPlainOrderSelection(candidates)
		if plain.Lead == nil {
			return morningBriefDraft{}, false, withLogStage("score", errMorningBriefNoEligibleLead)
		}
		switch true {
		case unscored:
			log.Printf("[mobile-brief] ranked morning fixed order has no gated lead; leading with the first of %d candidates", len(candidates))
		case selection.Lead == nil:
			log.Printf("[mobile-brief] ranked morning scorer picked no lead; writing %d candidates in the fixed order", len(candidates))
		default:
			log.Printf("[mobile-brief] ranked morning scorer gave no openings; writing %d candidates in the fixed order", len(candidates))
		}
		scores, selection, unscored = morningBriefScores{}, plain, true
	}
	if err := check(ctx); err != nil {
		return morningBriefDraft{}, false, err
	}
	refs, err := resolveRefs(ctx, selection)
	if err != nil {
		return morningBriefDraft{}, false, withLogStage("resolve refs", err)
	}
	if err := check(ctx); err != nil {
		return morningBriefDraft{}, false, err
	}
	selection, err = morningBriefAttachSelectionRefs(selection, refs)
	if err != nil {
		return morningBriefDraft{}, false, withLogStage("attach refs", err)
	}
	selected := map[string]bool{selection.Lead.ID: true}
	for _, item := range selection.Needs {
		selected[item.ID] = true
	}
	if selection.Slip != nil {
		selected[selection.Slip.ID] = true
	}
	if selection.Handled != nil {
		selected[selection.Handled.ID] = true
	}
	reasons := map[string]string{}
	for _, item := range scores.Items {
		if selected[item.ID] {
			reasons[item.ID] = item.Critique
		}
	}
	var opening morningBriefOpening
	if len(scores.Openings) > 0 {
		opening = scores.Openings[0]
	}
	writerInput := morningBriefWriterInput{Selection: selection, Opening: opening, Reasons: reasons,
		WeekIntent: weekIntent, Rules: append([]string(nil), rules...), PriorLeads: append([]string(nil), priorLeads...)}
	judgeInput := morningBriefJudgeInput{Scores: scores, Previous: previousBriefs, Candidates: candidates}
	draft, fallback, err := writeAndJudgeMorningBriefWithFallback(ctx, judgeInput,
		func(ctx context.Context) (*MobileBriefPara, error) {
			if err := check(ctx); err != nil {
				return nil, err
			}
			if unscored {
				return nil, codeError("no scored opening for the lead fallback")
			}
			lead, err := verifyLead(ctx, *selection.Lead, opening, checkedCall)
			if err != nil {
				return nil, err
			}
			if err := check(ctx); err != nil {
				return nil, err
			}
			return lead, nil
		}, func(ctx context.Context, critique string) (morningBriefDraft, error) {
			if err := check(ctx); err != nil {
				return morningBriefDraft{}, err
			}
			written, err := write(ctx, writerInput, critique)
			if err != nil {
				return morningBriefDraft{}, err
			}
			if err := check(ctx); err != nil {
				return morningBriefDraft{}, err
			}
			return written, nil
		}, func(ctx context.Context, input morningBriefJudgeInput) (string, error) {
			return judgeMorningBrief(ctx, input, checkedCall)
		})
	if !unscored && (err != nil || fallback) && sourceErr == nil && ctx.Err() == nil &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errMobileBriefSourcesChanged) {
		log.Printf("[mobile-brief] ranked morning writing fell back to the fixed order")
		retry, retryFallback, retryErr := runMorningBriefPipelineAttempt(ctx, name, weekIntent, goals, rules, priorLeads, previousBriefs, candidates, check, call, resolveRefs, verifyLead, write, true)
		if retryErr == nil && !retryFallback {
			return retry, false, nil
		}
		if errors.Is(retryErr, context.Canceled) || errors.Is(retryErr, context.DeadlineExceeded) || errors.Is(retryErr, errMobileBriefSourcesChanged) {
			return morningBriefDraft{}, false, retryErr
		}
		if checkErr := check(ctx); checkErr != nil {
			return morningBriefDraft{}, false, withLogStage("write and judge", checkErr)
		}
		// The resolver also constructs the outgoing brief. Restore the original
		// selection before returning its independently verified lead.
		if _, restoreErr := resolveRefs(ctx, selection); restoreErr != nil {
			return morningBriefDraft{}, false, withLogStage("resolve refs", restoreErr)
		}
	}
	if err != nil {
		if sourceErr != nil {
			return morningBriefDraft{}, false, withLogStage("write and judge", sourceErr)
		}
		return morningBriefDraft{}, fallback, withLogStage("write and judge", err)
	}
	if fallback {
		log.Printf("[mobile-brief] ranked morning used the grounded lead-only fallback")
	}
	if err := check(ctx); err != nil {
		return morningBriefDraft{}, false, err
	}
	return draft, fallback, nil
}

// morningBriefAttachSelectionRefs can only add refs to the scored selection.
// It rejects missing or extraneous identities and never changes ranking facts.
func morningBriefAttachSelectionRefs(selection morningBriefSelection, refs map[string][]string) (morningBriefSelection, error) {
	seen := map[string]bool{}
	attach := func(item morningBriefCandidate) (morningBriefCandidate, error) {
		ids, ok := refs[item.ID]
		if !ok || item.ID == "" || seen[item.ID] || len(ids) == 0 {
			return morningBriefCandidate{}, codeError("selected item has no evidence")
		}
		seen[item.ID] = true
		unique := map[string]bool{}
		for _, id := range ids {
			if id == "" || unique[id] {
				return morningBriefCandidate{}, codeError("invalid selected evidence")
			}
			unique[id] = true
		}
		item.Refs = append([]string(nil), ids...)
		return item, nil
	}
	var out morningBriefSelection
	if selection.Lead != nil {
		item, err := attach(*selection.Lead)
		if err != nil {
			return morningBriefSelection{}, err
		}
		out.Lead = &item
	}
	if len(selection.Needs) > 3 {
		return morningBriefSelection{}, codeError("too many ranked needs")
	}
	for _, need := range selection.Needs {
		item, err := attach(need)
		if err != nil {
			return morningBriefSelection{}, err
		}
		out.Needs = append(out.Needs, item)
	}
	if selection.Slip != nil {
		item, err := attach(*selection.Slip)
		if err != nil {
			return morningBriefSelection{}, err
		}
		out.Slip = &item
	}
	if selection.Handled != nil {
		item, err := attach(*selection.Handled)
		if err != nil {
			return morningBriefSelection{}, err
		}
		out.Handled = &item
	}
	if len(seen) != len(refs) {
		return morningBriefSelection{}, codeError("evidence includes an unselected item")
	}
	return out, nil
}
