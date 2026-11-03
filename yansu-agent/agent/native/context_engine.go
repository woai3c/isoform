package native

// ContextEngineSnapshot is the per-step bundle of structural metrics (L1)
// and tool schemas (L2) that the loop attaches
// to step.llm.* telemetry events when a ContextEngineCollector is wired
// in. It is the "Context Engine" runtime view inspired by lobehub's
// StepSnapshot.contextEngine — recorded once per LLM call so dashboards
// and the Phase 2/3 pattern checker can correlate harness behavior with
// the surrounding context state.
//
// All fields are optional. A collector populates whichever the user's
// privacy tier authorizes (see tmp/yansu-monitoring-impl-plan.md §11):
//   - off:       returns nil (no snapshot)
//   - basic:     L1 fields only (structural counts)
//   - detailed:  L1 + L2 fields (tool schemas, template name)
//
// Wire/serialization shape: the loop translates the snapshot into a
// map[string]any property bag at emission time; only non-zero fields
// are attached. This struct deliberately uses plain Go types (no
// pointers, no nested optionals) to keep the collector implementations
// trivial — emit and forget.
type ContextEngineSnapshot struct {
	// Tier is the user's effective telemetry tier at snapshot time.
	// "basic" or "detailed". Attached as a property so dashboards can
	// separate populations and pattern checkers know what fields they
	// can rely on. The loop also uses Tier to skip emitting L2 fields
	// when the collector returned a basic snapshot — defense in depth
	// against a collector that accidentally fills L2 fields under
	// basic.
	Tier string

	// --- L1 (structural metrics, ~always-on) -------------------------

	// SystemPromptChars is len() of the assembled system prompt text
	// at this step (post plan-mode / workdir augmentation). Zero =
	// not collected.
	SystemPromptChars int

	// MessagesByRole counts messages in the per-turn slice grouped by
	// Role ("user", "assistant", "tool"). Nil = not collected.
	MessagesByRole map[string]int

	// Tools is the tool-name list the collector observed the loop was
	// about to send. Order matches LoopConfig.Registry filtering.
	// Nil = not collected.
	Tools []string

	// UserMemoryCounts is the per-category count of user-memory
	// entries the collector observed (e.g. persona / identity /
	// context). Category names are caller-defined — the loop just
	// passes them through. Nil = not collected.
	UserMemoryCounts map[string]int

	// KnowledgeFileCount is the number of knowledge-base files the
	// collector observed. -1 = not collected (0 is a legitimate
	// observation for users without any KB).
	KnowledgeFileCount int

	// --- L2 (richer, opt-in) -----------------------------------------

	// SystemPromptTemplate is a human-readable template identifier
	// (e.g. "react_default", "plan_mode") if the collector can map
	// the assembled prompt back to a known template. Empty = not
	// collected.
	SystemPromptTemplate string

	// ToolSchemas maps tool name → JSON schema object. Populated only
	// on the first step of a run (step_index == 0) to avoid bloating
	// every event with the same payload — schemas are static for the
	// run unless the model self-discovers MCP tools mid-flight, which
	// is rare. Nil = not collected (or not the first step).
	ToolSchemas map[string]any
}

// ContextEngineRequest is the per-step state the loop hands to the
// collector. Fields are loop-internal slices/strings; the collector
// must NOT mutate them — read-only snapshot computations only.
//
// The shape is intentionally narrow: only what's needed to compute
// L1+L2 fields. Adding more requires updating the loop's call site,
// which keeps the contract grep-able.
type ContextEngineRequest struct {
	// StepIndex matches the loop's turn counter (0-based) and the
	// step_index field on step.llm.* events.
	StepIndex int

	// SystemText is the fully assembled system prompt text the loop is
	// about to send (post plan-mode prefix and workdir suffix). Empty
	// string when LoopConfig.NoSystem is set. Collectors use len() for
	// the SystemPromptChars L1 field.
	SystemText string

	// Messages is the message slice the loop is about to send. Collector
	// iterates and counts by .Role to populate MessagesByRole. DO NOT
	// retain a reference past the call — the loop reuses the slice.
	Messages []Message

	// Tools is the active tool-definition slice for this step (after
	// AllowedTools / DisabledTools / PlanMode filtering). Collector
	// reads .Name for the L1 Tools field and .InputSchema for the L2
	// ToolSchemas field. nil/empty when LoopConfig.NoTools is set.
	Tools []ToolDef
}

// ContextEngineCollector is the callback the loop invokes at each
// step boundary to build a snapshot. The collector should be cheap
// (it runs on the loop goroutine and blocks the next model call) —
// read counters / references, do not deep-copy.
//
// Returning nil is allowed and means "no snapshot for this step";
// the loop will then emit step events without CE fields. Collectors
// implementing privacy tiers typically return nil when the tier is
// "off".
type ContextEngineCollector func(req ContextEngineRequest) *ContextEngineSnapshot

// telemetryTierBasic and telemetryTierDetailed are the canonical tier
// string values the loop checks before attaching L2 fields. Kept here
// (not in a public consts file) so the loop and any in-process
// collector agree on the spelling without an import cycle through
// wails-gui.
const (
	telemetryTierBasic    = "basic"
	telemetryTierDetailed = "detailed"
)

// applySnapshotToProps writes the snapshot's L1 fields into the
// supplied property bag, and (when tier == detailed) additionally
// writes L2 fields. Returns the same map for chaining. Nil snapshot
// is a no-op. The "step 0 only" rule for ToolSchemas is honored
// here so callers don't have to special-case it at every emission
// site.
func applySnapshotToProps(props map[string]any, snap *ContextEngineSnapshot, stepIndex int) map[string]any {
	if snap == nil || props == nil {
		return props
	}

	props["ce_tier"] = snap.Tier

	// L1 fields — only set when the collector populated them. Zero
	// values are ambiguous between "not collected" and "really zero"
	// for scalars, so we guard on >0 (and KnowledgeFileCount >= 0
	// since 0 is a meaningful observation there).
	if snap.SystemPromptChars > 0 {
		props["ce_system_prompt_chars"] = snap.SystemPromptChars
	}
	if len(snap.MessagesByRole) > 0 {
		props["ce_messages_by_role"] = snap.MessagesByRole
	}
	if len(snap.Tools) > 0 {
		props["ce_tools"] = snap.Tools
	}
	if len(snap.UserMemoryCounts) > 0 {
		props["ce_user_memory_counts"] = snap.UserMemoryCounts
	}
	if snap.KnowledgeFileCount >= 0 {
		props["ce_knowledge_file_count"] = snap.KnowledgeFileCount
	}

	// L2 fields — gated on detailed tier AND populated. The tier
	// check defends against a collector that filled L2 fields under
	// basic by accident; we'd rather drop the data than over-share.
	if snap.Tier == telemetryTierDetailed {
		if snap.SystemPromptTemplate != "" {
			props["ce_system_prompt_template"] = snap.SystemPromptTemplate
		}
		if stepIndex == 0 && len(snap.ToolSchemas) > 0 {
			props["ce_tool_schemas"] = snap.ToolSchemas
		}
	}

	return props
}
