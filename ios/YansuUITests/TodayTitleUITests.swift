import UIKit
import XCTest

// P160: the top of Today on every iOS version the app supports, in light and dark.
// At rest the large "Today" title is painted, and the band above it is the page's color. Scrolled so a line of
// text sits under the bar's bottom edge, nothing of that text reads through the bar (P137), and the bar is the
// page's color. The screenshots and the sampled colors are printed as P160 lines.
final class TodayTitleUITests: XCTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    func testLargeTodayTitleIsDrawnAtRestAndNothingUnderTheBarReadsWhenScrolled() throws {
        for appearance in ["light", "dark"] {
            let app = XCUIApplication()
            app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "feed-words"
            app.launchEnvironment["YANSU_UITEST_TAB"] = "feed"
            app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
            app.launch()
            XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 15))
            XCTAssertTrue(app.descendants(matching: .any)["feed.brief.title"].firstMatch.waitForExistence(timeout: 15))
            let version = ProcessInfo.processInfo.operatingSystemVersionString
            let page = appearance == "light" ? Page.light : Page.dark

            // At rest.
            let large = app.staticTexts.allElementsBoundByIndex.first { $0.label == "Today" && $0.frame.minY < 260 && $0.frame.height >= 30 }
            let title = try XCTUnwrap(large, "the large Today title is in the accessibility tree on \(version)")
            let top = app.screenshot()
            attach(top, named: "P160-REST-\(appearance)-\(version)")
            let image = try Pixels(top.image)
            let ink = image.countInk(in: title.frame, against: page)
            let aboveSamples = image.samples(y: [4, 20, 40, title.frame.minY - 2], xs: [4, 195, 386])
            let belowSamples = image.samples(y: [title.frame.maxY + 1], xs: [4, 195, 386])
            print("P160 REST \(appearance) \(version) title=\(title.frame) inkPixels=\(ink) page=\(page.hex)")
            print("P160 REST \(appearance) \(version) above=\(aboveSamples.map(\.hex)) below=\(belowSamples.map(\.hex))")
            XCTAssertGreaterThan(ink, 400, "the large Today title is painted at rest on \(version)")
            for color in aboveSamples where !color.matches(page, tolerance: 3) {
                XCTFail("the band above the title is \(color.hex), not the page \(page.hex), on \(version) \(appearance)")
            }

            // Scrolled until a line of the page sits under the bar's bottom edge.
            let bar = app.navigationBars.firstMatch
            XCTAssertTrue(bar.waitForExistence(timeout: 5))
            var straddler: XCUIElement?
            // Small drags, so the scroll stops with a line of text under the bar.
            let window = app.windows.firstMatch
            for _ in 0..<60 {
                let barBottom = bar.frame.maxY
                let barTop = bar.frame.minY
                straddler = app.staticTexts.allElementsBoundByIndex.first { text in
                    text.label != "Today" && text.frame.minY > barTop + 4 && text.frame.minY < barBottom - 4
                }
                if straddler != nil { break }
                let from = window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.62))
                let to = window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.59))
                from.press(forDuration: 0.05, thenDragTo: to)
            }
            let text = try XCTUnwrap(straddler, "a line of text sits under the bar's edge on \(version) \(appearance)")
            let barBottom = bar.frame.maxY
            let scrolled = app.screenshot()
            attach(scrolled, named: "P160-SCROLLED-\(appearance)-\(version)")
            let scrolledPixels = try Pixels(scrolled.image)
            // The bar's own inline title is centered, near x 160 to 230, so the check reads the line's left part.
            let leftEnd = min(text.frame.maxX, 150)
            let underBar = CGRect(x: text.frame.minX, y: text.frame.minY, width: leftEnd - text.frame.minX, height: barBottom - text.frame.minY - 2)
            let underInk = scrolledPixels.countInk(in: underBar, against: page)
            // The inline title sits in the middle of the bar, so the samples stay at the edges.
            let barSamples = scrolledPixels.samples(y: [barBottom - 3, barBottom - 12], xs: [4, 60, 330, 386])
            print("P160 SCROLLED \(appearance) \(version) barBottom=\(barBottom) text=\(text.label) under=\(underBar) inkUnderBar=\(underInk)")
            print("P160 SCROLLED \(appearance) \(version) barSamples=\(barSamples.map(\.hex)) page=\(page.hex)")
            XCTAssertLessThan(underInk, 40, "no text reads under the bar on \(version) \(appearance)")
            for color in barSamples where !color.matches(page, tolerance: 3) {
                XCTFail("the bar is \(color.hex), not the page \(page.hex), when scrolled on \(version) \(appearance)")
            }
            app.terminate()
        }
    }

    private func attach(_ screenshot: XCUIScreenshot, named name: String) {
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}

/// The page colors from YansuTheme.background (Paper and One Dark).
private enum Page {
    static let light = RGB(r: 246, g: 246, b: 249)
    static let dark = RGB(r: 18, g: 18, b: 23)
}

private struct RGB {
    let r: Int
    let g: Int
    let b: Int

    var luma: Int { (r + g + b) / 3 }
    var hex: String { String(format: "#%02x%02x%02x", r, g, b) }

    func matches(_ other: RGB, tolerance: Int) -> Bool {
        abs(r - other.r) <= tolerance && abs(g - other.g) <= tolerance && abs(b - other.b) <= tolerance
    }
}

/// The screenshot's pixels. Points map to the image's pixels by its scale.
private struct Pixels {
    let cgImage: CGImage
    let scale: CGFloat
    let bytes: [UInt8]

    init(_ image: UIImage) throws {
        let cg = try XCTUnwrap(image.cgImage)
        cgImage = cg
        scale = CGFloat(cg.width) / image.size.width
        var data = [UInt8](repeating: 0, count: cg.width * cg.height * 4)
        let context = CGContext(data: &data, width: cg.width, height: cg.height, bitsPerComponent: 8,
                                bytesPerRow: cg.width * 4, space: CGColorSpaceCreateDeviceRGB(),
                                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        context?.draw(cg, in: CGRect(x: 0, y: 0, width: cg.width, height: cg.height))
        bytes = data
    }

    /// Color of the pixel at a point in the screenshot.
    func color(x: CGFloat, y: CGFloat) -> RGB {
        let px = min(max(Int(x * scale), 0), cgImage.width - 1)
        let py = min(max(Int(y * scale), 0), cgImage.height - 1)
        let index = (py * cgImage.width + px) * 4
        return RGB(r: Int(bytes[index]), g: Int(bytes[index + 1]), b: Int(bytes[index + 2]))
    }

    func samples(y: [CGFloat], xs: [CGFloat]) -> [RGB] {
        y.flatMap { row in xs.map { color(x: $0, y: row) } }
    }

    /// Pixels inside the frame (in points) that differ from the page color by more than a threshold.
    func countInk(in frame: CGRect, against page: RGB) -> Int {
        let rect = CGRect(x: frame.minX * scale, y: frame.minY * scale,
                          width: frame.width * scale, height: frame.height * scale)
            .intersection(CGRect(x: 0, y: 0, width: cgImage.width, height: cgImage.height))
        guard !rect.isNull, rect.width > 0, rect.height > 0 else { return 0 }
        var count = 0
        for py in Int(rect.minY)..<Int(rect.maxY) {
            for px in Int(rect.minX)..<Int(rect.maxX) {
                let index = (py * cgImage.width + px) * 4
                let pixel = RGB(r: Int(bytes[index]), g: Int(bytes[index + 1]), b: Int(bytes[index + 2]))
                if abs(pixel.luma - page.luma) > 60 { count += 1 }
            }
        }
        return count
    }
}
