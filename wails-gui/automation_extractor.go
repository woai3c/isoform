package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"wails-gui/storage"
)

// promptPlaceholderRe detects abstract variable slots in an automationPrompt
// that the agent cannot resolve at trigger time: bracketed tokens like
// [COMPANY_NAME], brace templates like {target}, and the English phrases
// "the target" / "the chosen" which signal fill-in-the-blank intent. Catches
// the over-generalization failure mode where the triage LLM replaces an
// observed entity with an abstract placeholder instead of quoting the
// concrete value from activity. Case-insensitive to handle [CompanyName],
// {Target}, etc.
var promptPlaceholderRe = regexp.MustCompile(`(?i)\[[a-z_]+\]|\{[a-z_]+\}|\bthe (target|chosen)\b`)

// detectPromptPlaceholder returns the first placeholder match in prompt, or
// an empty string if none is found. Used to reject activity-sourced
// automations whose prompt contains unresolved variable slots — such
// automations cannot execute meaningfully on their own and must be skipped
// at creation time rather than shipped as "manual only" jobs that always
// run empty.
func detectPromptPlaceholder(prompt string) string {
	return promptPlaceholderRe.FindString(prompt)
}

type automationTriageSpec interface {
	GetAutomationName() string
	GetAutomationDescription() string
	GetAutomationPrompt() string
	GetAutomationScheduleKind() string
	GetAutomationScheduleConfig() json.RawMessage
}

// scheduleEvidenceProvider is optionally implemented by triage specs that
// support LLM-inferred schedules (currently only activity triage). Specs that
// do not implement it are treated as having no evidence.
type scheduleEvidenceProvider interface {
	GetAutomationScheduleEvidence() string
}

type automationRationaleProvider interface {
	GetAutomationRationale() string
}

type automationConfidenceProvider interface {
	GetAutomationConfidence() float64
}

type initiativePredictionProvider interface {
	GetPredictedUserAction() string
	GetInitiativeGoal() string
	GetInitiativeRationale() string
	GetInitiativeConfidence() float64
}

const maxScheduleEvidenceLen = 256

// resolveActivitySchedule applies the activity-source schedule policy:
//
//   - If the LLM returned kind="cron" with a valid cron config (parseable expr
//     passing the 15-minute floor) AND a non-empty verbatim evidence quote,
//     trust its judgment and keep the cron schedule.
//   - Otherwise fall back to manual. This catches missing schedule, "manual",
//     malformed kind, cron-without-evidence (LLM broke its own contract), and
//     cron-with-invalid-config (e.g. empty object, missing expr, sub-15min).
//     Falling back here is critical: if we let an invalid cron config through,
//     downstream validateAutomationFrequency returns an error and the entire
//     automation is skipped — losing the user's intent entirely.
//
// Evidence is trimmed to maxScheduleEvidenceLen runes (not bytes) via the
// shared truncateRunes helper, which bounds DB storage without corrupting
// multi-byte UTF-8 sequences like Chinese. The helper appends "…" when it
// truncates, so long evidence shows up as "<prefix>…" in the UI.
func resolveActivitySchedule(kind string, config json.RawMessage, evidence string) (string, json.RawMessage, string) {
	evidence = truncateRunes(strings.TrimSpace(evidence), maxScheduleEvidenceLen)

	if kind != "cron" || len(config) == 0 || evidence == "" {
		return "manual", json.RawMessage(`{}`), ""
	}
	if err := validateAutomationFrequency("cron", config); err != nil {
		return "manual", json.RawMessage(`{}`), ""
	}
	return "cron", config, evidence
}

type initiativeCreationResult struct {
	Status       string // "observed", "shadowed", "skipped", "error"
	InitiativeID string // non-empty when Status is "observed" or "shadowed"
	Reason       string // human-readable explanation for skipped/error; observability only
}

const minAutomationIntervalMs = 15 * 60 * 1000

func (a *App) createShadowInitiativeFromTriage(ctx context.Context, spec automationTriageSpec, source, sourceID, projectDir, userIntent string) initiativeCreationResult {
	name := strings.TrimSpace(spec.GetAutomationName())
	description := strings.TrimSpace(spec.GetAutomationDescription())
	prompt := strings.TrimSpace(spec.GetAutomationPrompt())
	scheduleKind := spec.GetAutomationScheduleKind()
	scheduleConfig := spec.GetAutomationScheduleConfig()
	scheduleEvidence := ""
	if provider, ok := spec.(scheduleEvidenceProvider); ok {
		scheduleEvidence = provider.GetAutomationScheduleEvidence()
	}

	isActivitySource := source == "activity"

	if name == "" || prompt == "" {
		reason := fmt.Sprintf("missing required fields (name=%q, prompt=%d bytes)", name, len(prompt))
		log.Printf("[initiative] skipping kind=missing_fields: %s", activityPrivateLogField("reason", reason))
		return initiativeCreationResult{Status: "skipped", Reason: reason}
	}

	if match := detectPromptPlaceholder(prompt); match != "" {
		reason := fmt.Sprintf("prompt contains unresolved placeholder %q — agent cannot fill it at trigger time", match)
		log.Printf("[initiative] skipping kind=unresolved_placeholder %s: %s", activityPrivateLogField("title", name), activityPrivateLogField("reason", reason))
		return initiativeCreationResult{Status: "skipped", Reason: reason}
	}

	if isActivitySource {
		// Trust the LLM's judgment when it commits to a recurring schedule with
		// a verbatim quote; otherwise fall back to manual. This preserves
		// explicit time anchors like "every Monday" while protecting against
		// hallucinated cadences from fragmentary session slices.
		scheduleKind, scheduleConfig, scheduleEvidence = resolveActivitySchedule(scheduleKind, scheduleConfig, scheduleEvidence)
	} else if scheduleKind == "" || len(scheduleConfig) == 0 {
		reason := fmt.Sprintf("missing schedule (kind=%q, configBytes=%d)", scheduleKind, len(scheduleConfig))
		log.Printf("[initiative] skipping kind=missing_schedule %s: %s", activityPrivateLogField("title", name), activityPrivateLogField("reason", reason))
		return initiativeCreationResult{Status: "skipped", Reason: reason}
	}

	if err := validateAutomationFrequency(scheduleKind, scheduleConfig); err != nil {
		reason := fmt.Sprintf("frequency validation failed: %v", err)
		log.Printf("[initiative] skipping kind=invalid_schedule %s: %s", activityPrivateLogField("title", name), activityPrivateLogField("error", reason))
		return initiativeCreationResult{Status: "skipped", Reason: reason}
	}

	targetConfig, _ := json.Marshal(map[string]string{
		"prompt":      prompt,
		"project_dir": projectDir,
	})
	if a.activity == nil || a.activity.store == nil {
		reason := "activity store is unavailable"
		log.Printf("[initiative] cannot shadow %s: %s", activityPrivateLogField("title", name), activityPrivateLogField("error", reason))
		return initiativeCreationResult{Status: "error", Reason: reason}
	}

	fingerprint := computeAutomationFingerprint(description, "agent", string(targetConfig))
	if fingerprint != "" {
		if a.store != nil {
			jobs, err := a.store.ListCronJobs(ctx)
			if err == nil {
				for _, job := range jobs {
					if job.Fingerprint != "" && job.Fingerprint == fingerprint {
						reason := fmt.Sprintf("duplicate existing automation %q (%s)", job.Name, job.ID)
						log.Printf("[initiative] skipping kind=existing_automation %s: %s", activityPrivateLogField("title", name), activityPrivateLogField("reason", reason))
						return initiativeCreationResult{Status: "skipped", Reason: reason}
					}
				}
			}
		}
	}

	intent := strings.TrimSpace(userIntent)
	if intent == "" {
		intent = description
	}
	rationale := ""
	if provider, ok := spec.(automationRationaleProvider); ok {
		if value := strings.TrimSpace(provider.GetAutomationRationale()); value != "" {
			rationale = value
		}
	}
	confidence := 0.5
	if provider, ok := spec.(automationConfidenceProvider); ok {
		confidence = clampConfidence(provider.GetAutomationConfidence())
	}
	predictedAction := description
	if predictedAction == "" {
		predictedAction = prompt
	}
	goal := description
	if provider, ok := spec.(initiativePredictionProvider); ok {
		if value := strings.TrimSpace(provider.GetPredictedUserAction()); value != "" {
			predictedAction = value
		}
		if value := strings.TrimSpace(provider.GetInitiativeGoal()); value != "" {
			goal = value
		}
		if value := strings.TrimSpace(provider.GetInitiativeRationale()); value != "" &&
			strings.TrimSpace(rationale) == "" {
			rationale = value
		}
		if confidence == 0 && provider.GetInitiativeConfidence() > 0 {
			confidence = clampConfidence(provider.GetInitiativeConfidence())
		}
	}
	if goal == "" {
		goal = predictedAction
	}
	if rationale == "" {
		rationale = intent
	}

	evidence := []InitiativeEvidence{{
		Kind: "source", SourceKind: source, SourceID: sourceID, Summary: intent,
	}}
	if scheduleEvidence != "" {
		evidence = append(evidence, InitiativeEvidence{
			Kind: "schedule_quote", SourceKind: source, SourceID: sourceID, Quote: scheduleEvidence,
		})
	}
	now := time.Now().UnixMilli()
	item := Initiative{
		ID:              uuid.New().String(),
		Kind:            "automation",
		Status:          InitiativeStatusObserved,
		AutonomyMode:    InitiativeModeShadow,
		SourceKind:      source,
		SourceID:        sourceID,
		Title:           name,
		Description:     description,
		Goal:            goal,
		UserIntent:      intent,
		PredictedAction: predictedAction,
		Rationale:       rationale,
		Confidence:      confidence,
		RiskLevel:       "unknown",
		Reversibility:   "unknown",
		Evidence:        evidence,
		ExecutionPlan: InitiativeExecutionPlan{Kind: "automation", Automation: &AutomationExecutionPlan{
			Name: name, Description: description, Prompt: prompt, ProjectDir: projectDir,
			ScheduleKind: scheduleKind, ScheduleConfig: scheduleConfig, ScheduleEvidence: scheduleEvidence,
		}},
		Fingerprint: fingerprint,
		MemoryState: InitiativeMemoryUnverified,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	a.applyInitiativeConfidenceAdjustment(&item)
	immediate := item.Confidence >= immediateShadowConfidence || scheduleEvidence != ""
	if immediate {
		item.Status = InitiativeStatusShadowed
	}
	return a.persistShadowInitiative(item, scheduleKind, immediate)
}

func computeInitiativeFingerprint(kind, title, predictedAction string) string {
	normalized := strings.Join([]string{
		normalizeAutomationFingerprintText(kind),
		normalizeAutomationFingerprintText(title),
		normalizeAutomationFingerprintText(predictedAction),
	}, "|")
	if strings.ReplaceAll(normalized, "|", "") == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", hash)
}

func (a *App) createPredictionInitiativeFromTriage(
	resp *activityTriageResponse,
	source, sourceID, userIntent string,
) initiativeCreationResult {
	if resp == nil {
		return initiativeCreationResult{Status: "skipped", Reason: "missing triage response"}
	}
	title := strings.TrimSpace(resp.InitiativeTitle)
	predictedAction := strings.TrimSpace(resp.PredictedUserAction)
	if title == "" || predictedAction == "" {
		return initiativeCreationResult{
			Status: "skipped",
			Reason: fmt.Sprintf("missing prediction fields (title=%q, action=%d bytes)", title, len(predictedAction)),
		}
	}
	goal := strings.TrimSpace(resp.InitiativeGoal)
	if goal == "" {
		goal = predictedAction
	}
	intent := strings.TrimSpace(userIntent)
	if intent == "" {
		intent = goal
	}
	rationale := strings.TrimSpace(resp.InitiativeRationale)
	if rationale == "" {
		rationale = intent
	}
	confidence := clampConfidence(resp.InitiativeConfidence)
	now := time.Now().UnixMilli()
	item := Initiative{
		ID:              uuid.New().String(),
		Kind:            "prediction",
		Status:          InitiativeStatusObserved,
		AutonomyMode:    InitiativeModeShadow,
		SourceKind:      source,
		SourceID:        sourceID,
		Title:           title,
		Description:     goal,
		Goal:            goal,
		UserIntent:      intent,
		PredictedAction: predictedAction,
		Rationale:       rationale,
		Confidence:      confidence,
		RiskLevel:       "unknown",
		Reversibility:   "unknown",
		Evidence: []InitiativeEvidence{{
			Kind: "source", SourceKind: source, SourceID: sourceID, Summary: intent,
		}},
		ExecutionPlan: InitiativeExecutionPlan{Kind: "none"},
		Fingerprint:   computeInitiativeFingerprint("prediction", title, predictedAction),
		MemoryState:   InitiativeMemoryUnverified,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	a.applyInitiativeConfidenceAdjustment(&item)
	immediate := item.Confidence >= immediateShadowConfidence
	if immediate {
		item.Status = InitiativeStatusShadowed
	}
	return a.persistShadowInitiative(item, "", immediate)
}

func (a *App) persistShadowInitiative(item Initiative, scheduleKind string, immediate bool) initiativeCreationResult {
	if a.activity == nil || a.activity.store == nil {
		return initiativeCreationResult{Status: "error", Reason: "activity store is unavailable"}
	}
	if item.Fingerprint != "" {
		existing, err := a.activity.store.FindOpenInitiativeByFingerprint(item.Fingerprint)
		if err == nil {
			if existing.Status != InitiativeStatusObserved {
				reason := fmt.Sprintf("duplicate active initiative %q (%s)", existing.Title, existing.ID)
				log.Printf("[initiative] skipping kind=active_duplicate %s: %s", activityPrivateLogField("title", item.Title), activityPrivateLogField("reason", reason))
				return initiativeCreationResult{Status: "skipped", InitiativeID: existing.ID, Reason: reason}
			}
			updated, added, addErr := a.activity.store.AddInitiativeObservation(existing.ID, item, immediate)
			if addErr != nil {
				reason := fmt.Sprintf("add initiative observation: %v", addErr)
				log.Printf("[initiative] failed to observe %s: %s", activityPrivateLogField("title", item.Title), activityPrivateLogField("error", reason))
				return initiativeCreationResult{Status: "error", Reason: reason}
			}
			if !added {
				reason := fmt.Sprintf("source already observed for initiative %q (%s)", existing.Title, existing.ID)
				log.Printf("[initiative] skipping kind=duplicate_source %s: %s", activityPrivateLogField("title", item.Title), activityPrivateLogField("reason", reason))
				return initiativeCreationResult{Status: "skipped", InitiativeID: existing.ID, Reason: reason}
			}
			if updated.Status == InitiativeStatusShadowed {
				a.emitBrowserEvent("initiative:created", updated)
				a.captureInitiativeInferenceTelemetry("initiative.shadowed", updated, scheduleKind)
				log.Printf("[initiative] promoted %s after %d observations (id=%s)", activityPrivateLogField("title", updated.Title), updated.ObservationCount, updated.ID)
				return initiativeCreationResult{Status: "shadowed", InitiativeID: updated.ID}
			}
			a.captureInitiativeInferenceTelemetry("initiative.observed", updated, scheduleKind)
			log.Printf("[initiative] observed %s (%d/%d observations, id=%s)",
				activityPrivateLogField("title", updated.Title), updated.ObservationCount, requiredInitiativeObservations(updated.Confidence), updated.ID)
			return initiativeCreationResult{Status: "observed", InitiativeID: updated.ID}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return initiativeCreationResult{Status: "error", Reason: fmt.Sprintf("initiative dedup lookup: %v", err)}
		}
	}

	if err := a.activity.store.SaveInitiative(item); err != nil {
		reason := fmt.Sprintf("save initiative observation: %v", err)
		log.Printf("[initiative] failed to save %s: %s", activityPrivateLogField("title", item.Title), activityPrivateLogField("error", reason))
		return initiativeCreationResult{Status: "error", Reason: reason}
	}

	item.ObservationCount = 1
	if item.Status == InitiativeStatusShadowed {
		a.emitBrowserEvent("initiative:created", item)
		a.captureInitiativeInferenceTelemetry("initiative.shadowed", item, scheduleKind)
		log.Printf("[initiative] shadowed %s immediately (id=%s, source=%s, sourceID=%s)", activityPrivateLogField("title", item.Title), item.ID, item.SourceKind, item.SourceID)
		return initiativeCreationResult{Status: "shadowed", InitiativeID: item.ID}
	}
	a.captureInitiativeInferenceTelemetry("initiative.observed", item, scheduleKind)
	log.Printf("[initiative] observed %s (1/%d observations, id=%s, source=%s, sourceID=%s)",
		activityPrivateLogField("title", item.Title), requiredInitiativeObservations(item.Confidence), item.ID, item.SourceKind, item.SourceID)
	return initiativeCreationResult{Status: "observed", InitiativeID: item.ID}
}

func (a *App) captureInitiativeInferenceTelemetry(event string, item Initiative, scheduleKind string) {
	if a.telemetry != nil {
		a.telemetry.Capture(event, map[string]any{
			"initiative_id": item.ID,
			"source_kind":   item.SourceKind,
			"confidence":    item.Confidence,
			"schedule_kind": scheduleKind,
			"observations":  item.ObservationCount,
		})
	}
}

func computeAutomationFingerprint(description, targetKind, targetConfig string) string {
	combined := strings.Join([]string{
		normalizeAutomationFingerprintText(description),
		normalizeAutomationFingerprintText(targetKind),
		canonicalizeJSON(targetConfig),
	}, "|")
	if strings.ReplaceAll(combined, "|", "") == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash)
}

func canonicalizeJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(canonical)
}

func normalizeAutomationFingerprintText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

func validateAutomationFrequency(kind string, config json.RawMessage) error {
	switch kind {
	case "every":
		var cfg struct {
			EveryMs int64 `json:"every_ms"`
		}
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("parse every config: %w", err)
		}
		if cfg.EveryMs < int64(minAutomationIntervalMs) {
			return fmt.Errorf("interval %dms is below minimum %dms", cfg.EveryMs, minAutomationIntervalMs)
		}
	case "cron":
		var cfg struct {
			Expr string `json:"expr"`
			TZ   string `json:"tz,omitempty"`
		}
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("parse cron config: %w", err)
		}
		sched, err := parseCronExpr(cfg.Expr, cfg.TZ)
		if err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", cfg.Expr, err)
		}
		now := time.Now()
		first := sched.Next(now)
		second := sched.Next(first)
		interval := second.Sub(first)
		if interval < 15*time.Minute {
			return fmt.Errorf("cron interval %v is below minimum 15 minutes", interval)
		}
	case "at":
	case "manual":
		// Manual automations never auto-fire; no frequency to validate.
	default:
		return fmt.Errorf("unknown schedule kind: %s", kind)
	}
	return nil
}

func (a *App) buildAutomationTriageSection() string {
	var sb strings.Builder
	sb.WriteString(`

## Automation (worthAutomation)

Evaluate whether this conversation suggests a RECURRING task worth automating.

When worthAutomation=true, include these additional fields in the JSON response:
- "worthAutomation": true
- "automationName": short descriptive name (e.g., "daily-standup-prep")
- "automationDescription": what the automation does and why
- "automationPrompt": the prompt an AI agent should execute on each run
- "automationScheduleKind": "every" (fixed interval) or "cron" (cron expression)
- "automationScheduleConfig": {"every_ms": 900000} or {"expr": "0 9 * * 1-5", "tz": "UTC"}

Schedule constraints:
- Minimum interval is 15 minutes (every_ms >= 900000, or cron with >= 15 min gap)
- Use "cron" for time-of-day patterns (e.g., daily at 9am: {"expr": "0 9 * * *", "tz": "UTC"})
- Use "every" for fixed intervals (e.g., every 30 min: {"every_ms": 1800000})

Only return worthAutomation=true when the task is not just recurring, but also has a STABLE REUSE SHAPE:
- There is a clear future input / trigger / source that will exist again on later runs
- The prompt will still mean one clear thing without the user re-explaining vague nouns
- The output will differ because the underlying input or external state can change between runs, not because the agent is re-answering the same one-off question

Reject prompts that sound automation-like but are not stable execution contracts:
- One-off analysis / diagnosis / recommendation requests with no clear future trigger
- Vague inputs like "the activity log", "expressions", "records", "items", "the system" unless the conversation concretely defines them
- Result/status pages or already-completed states that do not introduce fresh input on the next run
- Parameter-guessing or advisory tasks where the conversation does not establish what concrete input or external state will be analyzed next time

Return worthAutomation=false (default) if:
- The conversation is one-off or unlikely to recur
- The task is too complex or context-dependent for automated execution
- The task lacks a concrete future input source or uses undefined nouns the agent could not interpret reliably on the next run
- A similar automation already exists (check dedup list below if present)

Most conversations (>90%) should have worthAutomation=false.
`)
	sb.WriteString(a.buildAutomationDedupSection(""))
	return sb.String()
}

func (a *App) buildAutomationDedupSection(query string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var jobs []storage.CronJobRow
	if a.store != nil {
		var err error
		jobs, err = a.store.ListCronJobs(ctx)
		if err != nil {
			log.Printf("[initiative] failed to load cron jobs for dedup: %s", activityPrivateLogField("error", err.Error()))
		}
	}
	var active, reviewed, blocked []Initiative
	if a.activity != nil && a.activity.store != nil {
		active, _ = a.activity.store.ListActiveInitiatives(dedupListCap)
		reviewed, _ = a.activity.store.ListRecentlyReviewedInitiatives(8)
		blocked, _ = a.activity.store.ListRecentlyBlockedInitiatives(8)
	}
	if len(jobs) == 0 && len(active) == 0 && len(reviewed) == 0 && len(blocked) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Existing automations and initiatives (dedup and correction check)\n\n")
	sb.WriteString("Before creating a new shadow prediction or automation, review the existing work below.\n")
	sb.WriteString("Set the applicable worth flag false if the new prediction or automation would be redundant with\n")
	sb.WriteString("an existing automation or active initiative. Rejected initiatives are user corrections: do not recreate the same inference without materially new evidence.\n\n")
	if len(jobs) > 0 {
		sb.WriteString("Existing automations:\n")
		shown := rankAutomationDedupJobs(jobs, query)
		if len(shown) > dedupListCap {
			shown = shown[:dedupListCap]
		}
		for _, job := range shown {
			desc := job.Description
			if desc == "" {
				desc = "(no description)"
			}
			sb.WriteString(fmt.Sprintf("- %q: %s\n", job.Name, desc))
		}
		if len(jobs) > len(shown) {
			fmt.Fprintf(&sb, "... and %d more (not shown)\n", len(jobs)-len(shown))
		}
	}
	if len(active) > 0 {
		sb.WriteString("\nActive initiatives:\n")
		for _, item := range active {
			fmt.Fprintf(&sb, "- [id=%s status=%s] %q: %s\n", item.ID, item.Status, item.Title, item.PredictedAction)
			if outcome, err := a.activity.store.GetLatestInitiativeOutcome(item.ID); err == nil {
				fmt.Fprintf(&sb, "  Latest observed action [%s]: %s", outcome.Assessment, outcome.ObservedAction)
				if outcome.ObservedResult != "" {
					fmt.Fprintf(&sb, " Result: %s", outcome.ObservedResult)
				}
				sb.WriteString("\n")
			}
		}
	}
	if len(reviewed) > 0 {
		sb.WriteString("\nUser-confirmed or corrected shadow judgments (guidance, never authority):\n")
		for _, item := range reviewed {
			action := item.PredictedAction
			decision := "confirmed"
			if item.LatestFeedback != nil {
				decision = item.LatestFeedback.Decision
				if corrected := strings.TrimSpace(item.LatestFeedback.CorrectedAction); corrected != "" {
					action = corrected
				}
			}
			fmt.Fprintf(&sb, "- [id=%s decision=%s] %q: %s\n", item.ID, decision, item.Title, action)
		}
	}
	if len(blocked) > 0 {
		sb.WriteString("\nRejected initiative inferences:\n")
		for _, item := range blocked {
			reason := ""
			if item.LatestFeedback != nil && strings.TrimSpace(item.LatestFeedback.Reason) != "" {
				reason = " User correction: " + strings.TrimSpace(item.LatestFeedback.Reason)
			}
			fmt.Fprintf(&sb, "- %q: %s.%s\n", item.Title, strings.TrimSuffix(item.PredictedAction, "."), reason)
		}
	}
	return sb.String()
}

// rankAutomationDedupJobs orders existing automations by relevance to query (the
// activity summary) so the dedup section surfaces the automations most likely to
// be duplicated by a proposal derived from this activity.
//
// Scoring is IDF-weighted token overlap: a word shared across many automations
// ("daily"/"news"/"summary"/"user") carries almost no weight, while a rare
// topical word ("tennis"/"cmux") dominates. This fixes the prior plain-count
// scoring, where a verbose-but-unrelated automation outranked a genuinely
// related one purely on boilerplate word count. Tokens are CJK-aware (bigrams)
// so Chinese automations rank on the same footing as English ones rather than
// being silently sorted to the bottom for lacking ASCII tokens.
//
// It RANKS but does not drop while the list fits under automationDedupRankCap:
// every automation still reaches the model, so a short list never misses a
// duplicate. Only once automations exceed the cap is the least-relevant tail
// trimmed, to bound per-triage prompt cost as automations accumulate — and the
// IDF ranking keeps the duplicate-likely ones. Falls back to the original order
// when the query is empty or overlaps nothing topical, so the result is never
// worse than the prior full dump.
func rankAutomationDedupJobs(jobs []storage.CronJobRow, query string) []storage.CronJobRow {
	if len(jobs) <= 1 {
		return jobs
	}

	queryTokens := map[string]struct{}{}
	for _, t := range cjkAwareTokens(query) {
		queryTokens[t] = struct{}{}
	}
	if len(queryTokens) == 0 {
		return jobs
	}

	// Tokenize each automation once and build document frequency across the
	// automation corpus, so common boilerplate gets near-zero IDF weight.
	docTokens := make([]map[string]struct{}, len(jobs))
	df := map[string]int{}
	for i, job := range jobs {
		set := map[string]struct{}{}
		for _, t := range cjkAwareTokens(strings.Join([]string{
			job.Name,
			job.Description,
			job.ScheduleEvidence,
			job.TargetConfig,
		}, "\n")) {
			set[t] = struct{}{}
		}
		docTokens[i] = set
		for t := range set {
			df[t]++
		}
	}
	n := float64(len(jobs))

	type scoredJob struct {
		job   storage.CronJobRow
		score float64
		index int
	}
	scored := make([]scoredJob, 0, len(jobs))
	anyMatch := false
	for i, job := range jobs {
		var score float64
		for t := range queryTokens {
			if _, ok := docTokens[i][t]; ok {
				// Smoothed IDF: a token present in every automation contributes
				// ~0; a token unique to one automation contributes the most.
				score += math.Log((n + 1) / (float64(df[t]) + 1))
			}
		}
		if score > 0 {
			anyMatch = true
		}
		// Keep every job (even score 0) so nothing is dropped below the cap;
		// score-0 jobs sort to the bottom and are only trimmed at scale.
		scored = append(scored, scoredJob{job: job, score: score, index: i})
	}
	if !anyMatch {
		return jobs
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].index < scored[j].index
	})
	if len(scored) > automationDedupRankCap {
		scored = scored[:automationDedupRankCap]
	}

	ranked := make([]storage.CronJobRow, len(scored))
	for i, item := range scored {
		ranked[i] = item.job
	}
	return ranked
}

// cjkAwareTokens tokenizes text for automation dedup relevance scoring. Unlike
// ftsTokens (which merges a whole CJK run into one token, so Chinese text only
// matches by exact whole-run equality), it emits latin/digit words of >= 3 runes
// plus CJK character bigrams (and a unigram for a lone CJK char), so Chinese
// automation names/descriptions produce overlappable tokens.
func cjkAwareTokens(text string) []string {
	text = strings.ToLower(text)
	var out []string
	var latin []rune
	var cjk []rune
	flushLatin := func() {
		if len(latin) >= 3 {
			out = append(out, string(latin))
		}
		latin = latin[:0]
	}
	flushCJK := func() {
		switch len(cjk) {
		case 0:
		case 1:
			out = append(out, string(cjk))
		default:
			for i := 0; i+1 < len(cjk); i++ {
				out = append(out, string(cjk[i:i+2]))
			}
		}
		cjk = cjk[:0]
	}
	for _, r := range text {
		switch {
		case isCJKRune(r):
			flushLatin()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			flushCJK()
			latin = append(latin, r)
		default:
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return out
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
