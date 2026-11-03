package appparser

import "strings"

// AX coverage tags written into ParsedContent.Metadata["axCoverage"]
// by app parsers when they know they only captured part of the
// rendered content. Consumers gate OCR fusion on these values via
// IsPartialAX so the OCR splice happens only where it adds signal.
//
// The contract: a parser SHOULD set axCoverage when its strategy is
// known-partial (e.g. Numbers exposes headers + first column only,
// PowerPoint exposes the slide-navigator outline only). Parsers MAY
// omit the tag when coverage is nominally complete; consumers treat
// "missing" the same as "full".
const (
	// AXCoverageFullGrid: every visible cell was captured (Excel-style
	// AXRow > AXCell shape). No OCR splice needed.
	AXCoverageFullGrid = "full_grid"

	// AXCoverageFirstColumnOnly: only column headers + first-column
	// values were reachable (Numbers — Apple blocks per-cell AXValue).
	AXCoverageFirstColumnOnly = "first_column_only"

	// AXCoverageOutlineOnly: only per-slide titles + active-slide
	// marker were reachable (PowerPoint — Microsoft does not expose
	// AXTextArea for canvas content).
	AXCoverageOutlineOnly = "outline_only"

	// AXCoverageMetadataOnly: AX surfaced only window-level metadata
	// (filename, workspace, title) with NO body content. Used by the
	// code-editor parser for VSCode / Cursor — Monaco's editor pane
	// is AX-gated behind a setting most users don't flip, so we only
	// ever see chrome (sidebar, status bar). The OCR snapshot of the
	// same frame is the load-bearing source of the actual code text.
	AXCoverageMetadataOnly = "metadata_only"

	// AXCoverageChatListOnly: AX surfaced the chat-list previews +
	// current open channel name, but the active-conversation pane
	// content could not be located. Used by the Feishu / Lark parser
	// in the FALLBACK path only — when the parser successfully scopes
	// the messenger-chat WebView (compose textarea + channel-heading
	// dual signal both present) the conversation text is folded into
	// the AX body and axCoverage is dropped to "" (full). This flag
	// stays set when the user has no chat open, or when the
	// dual-signal walk fails for an unrecognized Lark UI variant.
	//
	// Historical note: the original Lark parser assumed the
	// messenger-chat WebArea was always a canvas-rendered stub and
	// hard-coded this flag. A 2026-05 audit (Codex Appshots
	// get_app_state dump comparison) showed the conversation pane is
	// actually a full DOM-backed WebView with all message text
	// reachable through AXStaticText nodes; the parser was just not
	// looking inside it.
	AXCoverageChatListOnly = "chat_list_only"
)

// IsPartialAX reports whether the parser flagged this content as
// known-incomplete and the caller should splice in OCR text.
//
// Returns false when p is nil — that case is "no parser matched at
// all" and the consumer must handle it via a different branch (there
// is no ParsedContent to merge into). Returns false for "full_grid"
// or empty axCoverage so OCR is not redundantly appended where AX
// already captured everything.
func IsPartialAX(p *ParsedContent) bool {
	if p == nil {
		return false
	}
	switch p.Metadata["axCoverage"] {
	case AXCoverageFirstColumnOnly, AXCoverageOutlineOnly,
		AXCoverageMetadataOnly, AXCoverageChatListOnly:
		return true
	}
	return false
}

// MergeWithOCR fuses the AX-parsed body with OCR text using clearly
// labeled sections so downstream LLMs can tell the two signals apart
// (AX is structured + accurate, OCR can have read errors).
//
// Behavior:
//   - ocrText empty / whitespace-only → returns the original body
//     unchanged, so callers can call this unconditionally without
//     gating on OCR availability.
//   - p nil or p.Body empty → returns "[OCR: …]\n<ocr>" so the OCR
//     payload still gets surfaced when no AX content exists (e.g.
//     WeChat, where AX yields nothing useful).
//   - Both present → "[AX: …]\n<body>\n\n[OCR: …]\n<ocr>" with
//     content-aware labels keyed off axCoverage.
//
// The function does NOT de-duplicate overlapping content; OCR will
// often re-read the same first-column cells the AX parser already
// captured. Consumers that care can post-process; for the LLM-context
// case the redundancy is cheap and keeps the merge logic simple.
func MergeWithOCR(p *ParsedContent, ocrText string) string {
	ocr := strings.TrimSpace(ocrText)
	body := ""
	coverage := ""
	if p != nil {
		body = p.Body
		coverage = p.Metadata["axCoverage"]
	}

	if ocr == "" {
		return body
	}

	axLabel, ocrLabel := axOCRLabels(coverage)

	if strings.TrimSpace(body) == "" {
		return "[" + ocrLabel + "]\n" + ocr
	}

	var b strings.Builder
	b.Grow(len(body) + len(ocr) + 64)
	b.WriteString("[")
	b.WriteString(axLabel)
	b.WriteString("]\n")
	b.WriteString(body)
	b.WriteString("\n\n[")
	b.WriteString(ocrLabel)
	b.WriteString("]\n")
	b.WriteString(ocr)
	return b.String()
}

// axOCRLabels returns the section headers used by MergeWithOCR.
//
// AX labels are content-specific because the parser really did scope
// itself to that subtree — "slide outline" or "headers + first column"
// accurately describes what's in the AX body.
//
// The OCR label is INTENTIONALLY generic. captureAndOCR screenshots
// and OCRs the entire focused window (see activity_monitor.go); it
// does NOT crop to a specific pane. So "OCR: visible slide content"
// or "OCR: visible conversation" would be lying — those would imply
// we cropped the OCR to that region, which we don't. The honest
// description is "the whole visible window", and the LLM is expected
// to infer from the preceding AX section which parts of the OCR are
// the value-add vs. which parts overlap with what AX already gave it.
//
// If per-parser OCR cropping ever lands (e.g. parsers returning an
// OCRHintRect), the labels can become specific again — but until then
// the runtime contract must match the runtime behaviour.
func axOCRLabels(coverage string) (axLabel, ocrLabel string) {
	const ocrFullWindow = "OCR: visible window content"
	switch coverage {
	case AXCoverageOutlineOnly:
		return "AX: slide outline", ocrFullWindow
	case AXCoverageFirstColumnOnly:
		return "AX: headers + first column", ocrFullWindow
	case AXCoverageMetadataOnly:
		return "AX: file + workspace metadata", ocrFullWindow
	case AXCoverageChatListOnly:
		return "AX: chat list + open channel", ocrFullWindow
	}
	return "AX", ocrFullWindow
}
