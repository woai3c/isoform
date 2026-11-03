import AppKit
import CoreGraphics
import Foundation
import ScreenCaptureKit

/// Screenshots via ScreenCaptureKit.
///
/// We capture *a single window* by its CGWindowID — this means we don't need to
/// raise the window to the front and don't leak other desktop content. The
/// CGWindowID comes from `_AXUIElementGetWindow` on the AX window element.
enum Capture {

    struct Result {
        let path: String
        let width: Int
        let height: Int
    }

    static func captureWindow(windowId: CGWindowID, outputPath: String, maxWidth: Int?) async throws -> Result {
        let content: SCShareableContent
        do {
            content = try await SCShareableContent.current
        } catch {
            // The most common failure here is a missing Screen Recording
            // permission; surface it as our structured error.
            throw CUError.scDenied()
        }
        guard let target = content.windows.first(where: { $0.windowID == windowId }) else {
            throw CUError.internalError("No SCWindow with id \(windowId) (was the window closed?)")
        }

        let filter = SCContentFilter(desktopIndependentWindow: target)
        let config = SCStreamConfiguration()
        // SCWindow.frame is in points; SCStreamConfiguration wants pixels.
        // The display's backing scale gives us the pixel density — assume the
        // target display matches the window's owning screen (good enough for
        // v1; multi-display refinement can come later).
        let scale = NSScreen.main?.backingScaleFactor ?? 2.0
        let srcW = max(1, target.frame.width)
        let srcH = max(1, target.frame.height)
        var targetW = Int(srcW * scale)
        var targetH = Int(srcH * scale)
        if let cap = maxWidth, cap > 0, targetW > cap {
            let ratio = Double(cap) / Double(targetW)
            targetW = cap
            targetH = max(1, Int(Double(targetH) * ratio))
        }
        config.width = targetW
        config.height = targetH
        config.showsCursor = false
        config.capturesAudio = false
        if #available(macOS 14.0, *) {
            config.ignoreShadowsSingleWindow = true
        }
        // We want still frames, not a stream.
        config.minimumFrameInterval = CMTime(value: 1, timescale: 60)

        let image: CGImage
        if #available(macOS 14.0, *) {
            image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
        } else {
            image = try await captureImageFallback(filter: filter, config: config)
        }

        try writeJPEG(image, to: outputPath)
        return Result(path: outputPath, width: image.width, height: image.height)
    }

    static func captureWindowBackingStore(windowId: CGWindowID, outputPath: String, maxWidth: Int?) throws -> Result {
        let opts: CGWindowListOption = [.optionIncludingWindow]
        guard let rawImage = CGWindowListCreateImage(
            .null,
            opts,
            windowId,
            [.boundsIgnoreFraming, .nominalResolution]
        ) else {
            throw CUError.internalError("CGWindowListCreateImage returned nil for window \(windowId)")
        }
        let image = resizeIfNeeded(rawImage, maxWidth: maxWidth)
        try writeJPEG(image, to: outputPath)
        return Result(path: outputPath, width: image.width, height: image.height)
    }

    @available(macOS, deprecated: 14.0, message: "Use SCScreenshotManager.captureImage")
    private static func captureImageFallback(filter: SCContentFilter, config: SCStreamConfiguration) async throws -> CGImage {
        // Pre-14 fallback: spin up a one-shot stream, grab a single frame.
        // (Kept minimal — a v1 nicety for users on Sonoma betas.)
        throw CUError.internalError("SCScreenshotManager requires macOS 14+. Please update macOS.")
    }

    private static func writeJPEG(_ image: CGImage, to path: String) throws {
        let url = URL(fileURLWithPath: path)
        // Ensure the parent directory exists.
        try? FileManager.default.createDirectory(
            at: url.deletingLastPathComponent(), withIntermediateDirectories: true
        )
        let bitmap = NSBitmapImageRep(cgImage: image)
        guard let data = bitmap.representation(using: .jpeg, properties: [.compressionFactor: 0.85]) else {
            throw CUError.internalError("Failed to encode JPEG.")
        }
        try data.write(to: url, options: .atomic)
    }

    private static func resizeIfNeeded(_ image: CGImage, maxWidth: Int?) -> CGImage {
        guard let maxWidth, maxWidth > 0, image.width > maxWidth else { return image }
        let ratio = Double(maxWidth) / Double(image.width)
        let targetWidth = maxWidth
        let targetHeight = max(1, Int(Double(image.height) * ratio))
        guard let colorSpace = image.colorSpace ?? CGColorSpace(name: CGColorSpace.sRGB),
              let context = CGContext(
                data: nil,
                width: targetWidth,
                height: targetHeight,
                bitsPerComponent: image.bitsPerComponent,
                bytesPerRow: 0,
                space: colorSpace,
                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
              )
        else {
            return image
        }
        context.interpolationQuality = .high
        context.draw(image, in: CGRect(x: 0, y: 0, width: targetWidth, height: targetHeight))
        return context.makeImage() ?? image
    }
}
