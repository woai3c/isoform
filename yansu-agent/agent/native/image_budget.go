package native

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif" // register GIF decoder for image.Decode
	"image/jpeg"
	_ "image/png" // register PNG decoder for image.Decode

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder for image.Decode
)

// Some upstreams the agent talks to via sub2api (e.g. Kimi / Moonshot) enforce
// a HARD limit on the serialized request body — they reject any request whose
// total message size exceeds 2 MiB with:
//
//	HTTP 400 {"error":{"type":"invalid_request_error",
//	          "message":"total message size N exceeds limit 2097152"}}
//
// This is a BYTE limit on the wire payload, entirely independent of the
// token-based context window that the auto-compactor (compact.go) manages.
// The two budgets diverge sharply for images: a full-resolution base64
// screenshot is ~1-3 MB of bytes but only ~1-2k vision tokens, so a couple
// of cua_driver screenshots blow past the 2 MiB wire limit while the token
// count stays far below AutoCompactLimit — meaning token-based compaction
// never fires and the request 400s.
//
// EnforceRequestByteBudget closes that blind spot by shrinking/pruning heavy
// image payloads. The loop invokes it REACTIVELY — only after an upstream
// actually returns a request-too-large 400, then retries the turn once — NOT
// pre-emptively before every send. The byte ceiling is upstream/model-specific
// (a 6 MB image succeeds on a high-limit model but 400s on Kimi's 2 MiB cap),
// so pre-emptive downscaling would needlessly degrade images the upstream
// would have accepted. Reacting to the real rejection makes the exact limit
// value irrelevant.
const (
	// requestByteTarget is the size we drive the request DOWN to once an
	// upstream has rejected it for being too large. Set well under the
	// smallest ceiling we know we hit (Kimi/Moonshot's 2 MiB) so the system
	// prompt, tools manifest, and JSON envelope still fit alongside whatever
	// we keep. We never compare against the ceiling directly — the loop
	// reacts to the upstream's actual 400, so the precise limit is irrelevant.
	requestByteTarget = 1_500_000

	// maxImageEdge is the longest-edge pixel cap applied when downscaling
	// an oversized image. 1568 matches the dimension above which most
	// vision stacks (incl. Anthropic) downsample server-side anyway, so we
	// lose no model-visible detail by doing it client-side — and shrinking
	// pixels cuts BOTH bytes and vision tokens (tokens scale with pixel
	// area, not file size).
	maxImageEdge = 1568

	// imageRecompressQuality is the JPEG quality used when re-encoding.
	// 80 is visually near-lossless for screenshots while ~10x smaller than
	// the equivalent PNG.
	imageRecompressQuality = 80

	// imageMinRecompressBytes skips images already small enough not to
	// matter, avoiding pointless re-encode churn (and quality loss) on
	// icons / thumbnails.
	imageMinRecompressBytes = 200 * 1024

	// stringResultHeavyBytes is the size above which a string tool_result
	// (e.g. a cua_driver screenshot returned as base64 inside its JSON
	// envelope) is considered a droppable heavy block.
	stringResultHeavyBytes = 64 * 1024

	// droppedImagePlaceholder replaces an image content block that had to
	// be dropped to fit the request-size limit.
	droppedImagePlaceholder = "[image removed to fit the model's request-size limit — re-capture or re-read it with a tool call if you still need to see it]"

	// droppedResultPlaceholder replaces an oversized string tool_result
	// that had to be pruned. Keeps the tool_use/tool_result pairing intact
	// (only the body is replaced) so the next request still validates.
	droppedResultPlaceholder = "[tool result removed to fit the model's request-size limit — re-run the tool if you still need its output]"
)

// ImageBudgetStats reports what EnforceRequestByteBudget did, for logging
// and telemetry. All zero means the request was already within budget.
type ImageBudgetStats struct {
	DownsizedCount int
	DroppedCount   int
	BytesBefore    int
	BytesAfter     int
}

// Touched reports whether any intervention happened.
func (s ImageBudgetStats) Touched() bool {
	return s.DownsizedCount > 0 || s.DroppedCount > 0
}

// heavyBlock is a pointer to a content block that contributes significant
// bytes to the request, recorded in conversation order (oldest first).
type heavyBlock struct {
	block      *ContentBlock
	isImageSrc bool // true: image block with Source; false: string tool_result
}

// EnforceRequestByteBudget shrinks the serialized request below
// requestByteTarget after an upstream rejected it for size. The loop calls
// this on a request-too-large 400 and retries the turn once (see the
// byteBudgetRetryUsed path in loop.go) — it is NOT a pre-emptive pass, so
// requests the upstream accepts are never touched. Two passes, escalating
// only as needed:
//
//  1. Recompress oversized decodable image blocks (Read-image results, user
//     attachments) — downscale to maxImageEdge and re-encode as JPEG. This
//     preserves the image as vision while cutting bytes and tokens.
//  2. If still over budget, prune the OLDEST heavy blocks (large base64
//     screenshots that arrived as string tool_results, then image blocks)
//     to placeholders, newest-preserved, until under requestByteTarget.
//
// Returns zeroed stats (Touched()==false) when the request is already under
// target or has no image/large-result payload to trim — the caller then
// surfaces the original error instead of spinning on an identical retry.
//
// Mutations are done in place on the messages slice and persist across the
// remainder of the run (so a screenshot is shrunk once, not every turn).
// Pairing is never broken: string tool_results keep their Type/ToolUseID
// (only the body is replaced); image blocks live inside content arrays and
// are swapped for a text block.
func EnforceRequestByteBudget(messages []Message) ImageBudgetStats {
	var stats ImageBudgetStats
	stats.BytesBefore = approxMessageBytes(messages)
	if stats.BytesBefore <= requestByteTarget {
		stats.BytesAfter = stats.BytesBefore
		return stats
	}

	heavy := collectHeavyBlocks(messages)
	if len(heavy) == 0 {
		// Over budget but no image/large-result payload to trim — text
		// history dominates, which is the auto-compactor's job, not ours.
		stats.BytesAfter = stats.BytesBefore
		return stats
	}

	// Pass 1: recompress decodable image blocks in place.
	for _, h := range heavy {
		if !h.isImageSrc || h.block.Source == nil {
			continue
		}
		if len(h.block.Source.Data) < imageMinRecompressBytes {
			continue
		}
		if mt, data, ok := shrinkImageData(h.block.Source.Data); ok {
			h.block.Source.MediaType = mt
			h.block.Source.Data = data
			stats.DownsizedCount++
		}
	}

	// Pass 2: if still over, prune oldest heavy blocks to placeholders.
	total := approxMessageBytes(messages)
	for _, h := range heavy {
		if total <= requestByteTarget {
			break
		}
		freed := neutralizeHeavyBlock(h)
		if freed <= 0 {
			continue
		}
		total -= freed
		stats.DroppedCount++
	}

	stats.BytesAfter = total
	return stats
}

// collectHeavyBlocks walks the messages in conversation order and returns
// pointers to every byte-heavy block: image blocks (top-level attachments
// or nested inside a tool_result) and string tool_results above
// stringResultHeavyBytes. Pointers index into the backing arrays, so
// callers mutate the live messages.
func collectHeavyBlocks(messages []Message) []heavyBlock {
	var out []heavyBlock
	for i := range messages {
		blocks, ok := messages[i].Content.([]ContentBlock)
		if !ok {
			continue
		}
		for j := range blocks {
			b := &blocks[j]
			switch b.Type {
			case "image":
				if b.Source != nil {
					out = append(out, heavyBlock{block: b, isImageSrc: true})
				}
			case "tool_result":
				switch c := b.Content.(type) {
				case string:
					if len(c) > stringResultHeavyBytes {
						out = append(out, heavyBlock{block: b, isImageSrc: false})
					}
				case []ContentBlock:
					for k := range c {
						sb := &c[k]
						if sb.Type == "image" && sb.Source != nil {
							out = append(out, heavyBlock{block: sb, isImageSrc: true})
						}
					}
				}
			}
		}
	}
	return out
}

// neutralizeHeavyBlock replaces a heavy block's payload with a placeholder
// and returns the approximate bytes freed. Image blocks become text blocks;
// string tool_results keep their tool_result Type/ToolUseID (only the body
// is swapped) so tool_use/tool_result pairing stays valid.
func neutralizeHeavyBlock(h heavyBlock) int {
	if h.isImageSrc {
		if h.block.Source == nil {
			return 0
		}
		freed := len(h.block.Source.Data) + len(h.block.Source.MediaType)
		*h.block = ContentBlock{Type: "text", Text: droppedImagePlaceholder}
		return freed - len(droppedImagePlaceholder)
	}
	s, ok := h.block.Content.(string)
	if !ok {
		return 0
	}
	freed := len(s) - len(droppedResultPlaceholder)
	h.block.Content = droppedResultPlaceholder
	return freed
}

// shrinkImageData decodes a base64 image, downscales it to maxImageEdge if
// oversized, and re-encodes it as JPEG. Returns the new media type and
// base64 only when the result is actually smaller; otherwise ok=false and
// the caller leaves the original untouched (re-encoding an already-small or
// already-compressed image can grow it). Undecodable inputs (unknown format
// / corrupt) return ok=false.
func shrinkImageData(b64 string) (mediaType, data string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return "", "", false
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", "", false
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	// Guard against degenerate dimensions from a corrupt image or a decoder
	// edge case. Today the maxImageEdge check below means scaledDims is only
	// reached with a divisor > maxImageEdge > 0, so the division is safe — but
	// this processes untrusted bytes (attachments / screenshots), so a cheap
	// up-front check keeps us panic-proof against pathological w/h and any
	// future change to the call sites.
	if w <= 0 || h <= 0 {
		return "", "", false
	}
	dst := src
	if w > maxImageEdge || h > maxImageEdge {
		nw, nh := scaledDims(w, h, maxImageEdge)
		rgba := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(rgba, rgba.Bounds(), src, b, draw.Over, nil)
		dst = rgba
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: imageRecompressQuality}); err != nil {
		return "", "", false
	}
	newB64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	if len(newB64) >= len(b64) {
		return "", "", false
	}
	return "image/jpeg", newB64, true
}

// scaledDims returns the dimensions that fit (w, h) within a maxEdge longest
// side while preserving aspect ratio. Never returns a zero dimension.
func scaledDims(w, h, maxEdge int) (int, int) {
	if w >= h {
		nh := h * maxEdge / w
		if nh < 1 {
			nh = 1
		}
		return maxEdge, nh
	}
	nw := w * maxEdge / h
	if nw < 1 {
		nw = 1
	}
	return nw, maxEdge
}

// approxMessageBytes estimates the serialized request size by summing the
// content bytes of every message. It deliberately ignores fixed JSON
// punctuation overhead — the dominant terms (base64 image data, tool_result
// text) are counted exactly, which is what matters for the 2 MiB gate.
func approxMessageBytes(messages []Message) int {
	total := 0
	for i := range messages {
		total += len(messages[i].Role) + 8
		total += approxContentBytes(messages[i].Content)
	}
	return total
}

func approxContentBytes(c any) int {
	switch v := c.(type) {
	case string:
		return len(v)
	case []ContentBlock:
		n := 0
		for i := range v {
			n += approxBlockBytes(&v[i])
		}
		return n
	}
	return 0
}

func approxBlockBytes(b *ContentBlock) int {
	n := 32 // per-block JSON envelope overhead
	n += len(b.Type) + len(b.Text) + len(b.Thinking) + len(b.Name) + len(b.ID) + len(b.ToolUseID) + len(b.Signature)
	if b.Source != nil {
		n += len(b.Source.Data) + len(b.Source.MediaType)
	}
	n += approxContentBytes(b.Content)
	return n
}
