import CoreGraphics
import Foundation
import ImageIO
import Vision

/// Fallback indexing for AX-blind apps (Qt, custom-drawn games, some CEF
/// builds). Runs Apple Vision OCR over the screenshot, returning each
/// recognised text region as a numbered visual-ref (`v1`, `v2`, …) with a
/// bounding box in screenshot-pixel coordinates.
///
/// When `get_app_state` sees an empty/sparse AX tree, it attaches these
/// visual refs to the response. The `click` handler accepts `ref: "v3"`
/// the same way it accepts `"e3"` — resolves the bounds and clicks the
/// centre, translated through the P2 coord map.
///
/// Matches Codex's `fallbackIndexing` feature: they run a similar visual
/// segmentation so models can click by index even when the AX tree is
/// useless.
enum VisualIndex {

    struct Region {
        let ref: String
        let text: String
        let bounds: CGRect // screenshot pixel coords, origin top-left
        let confidence: Float
    }

    /// Run OCR synchronously on the image at `path`. Caps at `maxResults`
    /// to keep the returned payload small. Returns empty on any failure
    /// (Vision refusals, unreadable file, etc) so this call never breaks
    /// get_app_state.
    static func index(imagePath: String, maxResults: Int = 80) -> [Region] {
        let url = URL(fileURLWithPath: imagePath)
        guard let dataProvider = CGDataProvider(url: url as CFURL),
              let image = CGImageSourceCreateWithDataProvider(dataProvider, nil),
              let cgImage = CGImageSourceCreateImageAtIndex(image, 0, nil)
        else {
            return []
        }

        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .fast  // fast: ~50ms on typical screenshots
        request.usesLanguageCorrection = false
        request.recognitionLanguages = ["en-US", "zh-Hans"]

        let handler = VNImageRequestHandler(cgImage: cgImage, options: [:])
        do {
            try handler.perform([request])
        } catch {
            return []
        }

        let results = request.results ?? []
        let w = CGFloat(cgImage.width)
        let h = CGFloat(cgImage.height)
        var out: [Region] = []
        var counter = 0
        for obs in results {
            guard let top = obs.topCandidates(1).first, !top.string.isEmpty else { continue }
            // VNRectangleObservation uses normalised (0..1) coords with
            // origin at the BOTTOM-left. Flip Y to match our screenshot
            // convention (top-left origin).
            let nb = obs.boundingBox
            let px = CGRect(
                x: nb.origin.x * w,
                y: (1 - nb.origin.y - nb.size.height) * h,
                width: nb.size.width * w,
                height: nb.size.height * h
            )
            counter += 1
            out.append(Region(
                ref: "v\(counter)",
                text: top.string,
                bounds: px,
                confidence: top.confidence
            ))
            if out.count >= maxResults { break }
        }
        return out
    }
}
