import Foundation
import SwiftUI
import UIKit
import XCTest
@testable import Yansu

/// The phone draws with the design system's Paper (light) and One Dark (dark) themes. These checks keep each role on
/// its token in docs/design-system/tokens.json, keep text readable, and keep the widget's copy in step.
final class ThemeTests: XCTestCase {
    private struct RGBA: Equatable {
        let red: Double
        let green: Double
        let blue: Double
        let alpha: Double
    }

    private static let roles: [(name: String, color: Color, token: String)] = [
        ("background", YansuTheme.background, "color-background"),
        ("surface", YansuTheme.surface, "color-panel"),
        ("surfaceWarm", YansuTheme.surfaceWarm, "color-panel-warm"),
        ("surfaceUser", YansuTheme.surfaceUser, "color-panel-user"),
        ("border", YansuTheme.border, "color-border"),
        ("borderStrong", YansuTheme.borderStrong, "color-border-strong"),
        ("borderSubtle", YansuTheme.borderSubtle, "color-border-subtle"),
        ("ink", YansuTheme.ink, "color-ink"),
        ("secondary", YansuTheme.secondary, "color-muted"),
        ("tertiary", YansuTheme.tertiary, "color-subtle"),
        ("faint", YansuTheme.faint, "color-faint"),
        ("accent", YansuTheme.accent, "color-accent"),
        ("accentWash", YansuTheme.accentWash, "color-accent-light"),
        ("solid", YansuTheme.solid, "color-solid-btn-bg"),
        ("onSolid", YansuTheme.onSolid, "color-solid-btn-fg"),
        ("sampleText", YansuTheme.sampleText, "color-sample-text"),
        ("sampleBorder", YansuTheme.sampleBorder, "color-sample-border"),
        ("chip", YansuTheme.chip, "color-chip-bg"),
        ("chipBorder", YansuTheme.chipBorder, "color-chip-border"),
        ("composer", YansuTheme.composer, "color-composer-bg"),
        ("composerBorder", YansuTheme.composerBorder, "color-composer-border"),
        ("error", YansuTheme.error, "color-error"),
        ("errorWash", YansuTheme.errorWash, "color-error-bg"),
        ("errorBorder", YansuTheme.errorBorder, "color-error-border"),
        ("success", YansuTheme.success, "color-success"),
        ("successWash", YansuTheme.successWash, "color-success-bg"),
        ("successBorder", YansuTheme.successBorder, "color-success-border"),
        ("warning", YansuTheme.warning, "color-warning"),
        ("warningWash", YansuTheme.warningWash, "color-warning-bg"),
        ("warningBorder", YansuTheme.warningBorder, "color-warning-border"),
        ("info", YansuTheme.info, "color-info"),
        ("infoWash", YansuTheme.infoWash, "color-info-bg"),
        ("infoBorder", YansuTheme.infoBorder, "color-info-border"),
    ]

    // ios/YansuTests/<file> -> repository root is three levels up.
    private static let root = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()

    func testEveryRoleTakesItsPaperAndOneDarkToken() throws {
        let data = try Data(contentsOf: Self.root.appendingPathComponent("docs/design-system/tokens.json"))
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let color = try XCTUnwrap(json["color"] as? [String: Any])
        let tokens = try XCTUnwrap(color["tokens"] as? [[String: Any]])
        var values: [String: [String: String]] = [:]
        for token in tokens {
            if let name = token["name"] as? String, let value = token["value"] as? [String: String] {
                values[name] = value
            }
        }
        for role in Self.roles {
            let value = try XCTUnwrap(values[role.token], "\(role.token) is not in tokens.json")
            for (theme, style) in [("paper", UIUserInterfaceStyle.light), ("onedark", .dark)] {
                let expected = try XCTUnwrap(Self.parse(value[theme] ?? ""), "\(role.token) has no \(theme) value")
                let actual = Self.resolve(role.color, style)
                XCTAssertTrue(Self.close(actual, expected),
                              "\(role.name) in \(theme) is \(actual), but \(role.token) is \(value[theme] ?? "")")
            }
        }
    }

    // Patina exempts tertiary metadata at 12pt+; faint is for disabled/decorative content only.
    func testTextHoldsFourPointFiveToOneOnItsGround() {
        let text: [(String, Color)] = [
            ("ink", YansuTheme.ink), ("secondary", YansuTheme.secondary), ("accent", YansuTheme.accent), ("sampleText", YansuTheme.sampleText),
            ("error", YansuTheme.error),
            ("success", YansuTheme.success), ("warning", YansuTheme.warning), ("info", YansuTheme.info),
        ]
        let grounds: [(String, Color)] = [("background", YansuTheme.background), ("surface", YansuTheme.surface)]
        // The dark half below means something only if dark mode resolves to One Dark, not to Paper again.
        XCTAssertNotEqual(Self.resolve(YansuTheme.background, .light), Self.resolve(YansuTheme.background, .dark))
        for style in [UIUserInterfaceStyle.light, .dark] {
            for (name, color) in text {
                for (groundName, ground) in grounds {
                    let ratio = Self.contrast(Self.resolve(color, style), Self.resolve(ground, style))
                    XCTAssertGreaterThanOrEqual(ratio, 4.5 - 0.001, "\(name) on \(groundName), \(style.rawValue)")
                }
            }
            let onSolid = Self.contrast(Self.resolve(YansuTheme.onSolid, style), Self.resolve(YansuTheme.solid, style))
            XCTAssertGreaterThanOrEqual(onSolid, 4.5, "primary button text, \(style.rawValue)")
            let userBubble = Self.contrast(Self.resolve(YansuTheme.ink, style),
                                           Self.resolve(YansuTheme.surfaceUser, style))
            XCTAssertGreaterThanOrEqual(userBubble, 4.5, "the user's chat bubble, \(style.rawValue)")
        }
    }

    func testSecondaryTextOnEverySurface() throws {
        let grounds = [YansuTheme.background, YansuTheme.surface, YansuTheme.surfaceWarm, YansuTheme.surfaceUser]
        for style in [UIUserInterfaceStyle.light, .dark] {
            for ground in grounds {
                XCTAssertGreaterThanOrEqual(Self.contrast(Self.resolve(YansuTheme.secondary, style),
                                                         Self.resolve(ground, style)), 4.5)
            }
        }
    }

    @MainActor
    func testFloatingTabBarTitleColorsUseReadableThemeRoles() {
        for style in [UIUserInterfaceStyle.light, .dark] {
            let background = Self.resolve(YansuTheme.surface, style)
            for (selected, expected) in [(false, YansuTheme.secondary), (true, YansuTheme.accent)] {
                let color = Self.resolve(FloatingTabBar.titleColor(selected: selected), style)
                XCTAssertTrue(Self.close(color, Self.resolve(expected, style)))
                XCTAssertGreaterThanOrEqual(Self.contrast(color, background), 4.5,
                                            "tab title, \(style.rawValue)")
            }
        }
    }

    func testCaptionMetadataUsesSecondaryAtSmallerDynamicTypeSizes() throws {
        // caption1 shrinks to 11pt below Large; caption2 stays below 12pt even at Large.
        let categories: [UIContentSizeCategory] = [.extraSmall, .small, .medium, .large, .extraLarge]
        for category in categories {
            let traits = UITraitCollection(preferredContentSizeCategory: category)
            let size = UIFont.preferredFont(forTextStyle: .caption1, compatibleWith: traits).pointSize
            for allowedStyle in [UIFont.TextStyle.footnote, .subheadline] {
                XCTAssertGreaterThanOrEqual(UIFont.preferredFont(forTextStyle: allowedStyle, compatibleWith: traits).pointSize, 12)
            }
            if [.extraSmall, .small, .medium].contains(category) {
                XCTAssertLessThan(size, 12, "the regression must cover smaller Dynamic Type: \(category.rawValue)")
            }
        }
        try Self.assertReadableCaptionCallers(in: "ios/Yansu", theme: "YansuTheme")
    }

    func testWidgetCaptionMetadataUsesSecondaryAtSmallerDynamicTypeSizes() throws {
        try Self.assertReadableCaptionCallers(in: "ios/YansuWidget", theme: "WidgetTheme")
        // The widget target is separate; parse its copy, then resolve the matching app role in both schemes.
        let widget = try Self.roleValues(in: "ios/YansuWidget/WidgetTheme.swift")
        let app = try Self.roleValues(in: "ios/Yansu/Theme.swift")
        XCTAssertNotNil(widget["secondary"])
        XCTAssertEqual(widget["secondary"], app["secondary"])
        for style in [UIUserInterfaceStyle.light, .dark] {
            for ground in [YansuTheme.background, YansuTheme.surface] {
                XCTAssertGreaterThanOrEqual(Self.contrast(Self.resolve(YansuTheme.secondary, style),
                                                         Self.resolve(ground, style)), 4.5)
            }
        }
    }

    private static func assertReadableCaptionCallers(in directory: String, theme: String) throws {
        let pattern = try NSRegularExpression(
            pattern: #"\.font\(\.caption(?:2)?\b[^\n]*\)[\s\S]{0,180}?\.foregroundStyle\([^\n]*"# + theme + #"\.tertiary"#)
        let invalid = ".font(.caption.weight(.medium))\n.foregroundStyle(selected ? \(theme).ink : \(theme).tertiary)"
        XCTAssertEqual(pattern.numberOfMatches(in: invalid, range: NSRange(invalid.startIndex..., in: invalid)), 1)
        let valid = ".font(.caption)\n.foregroundStyle(\(theme).secondary)"
        XCTAssertEqual(pattern.numberOfMatches(in: valid, range: NSRange(valid.startIndex..., in: valid)), 0)
        let files = try FileManager.default.contentsOfDirectory(at: root.appendingPathComponent(directory),
                                                              includingPropertiesForKeys: nil)
        for file in files where file.pathExtension == "swift" {
            let source = try String(contentsOf: file, encoding: .utf8)
            XCTAssertTrue(pattern.matches(in: source, range: NSRange(source.startIndex..., in: source)).isEmpty,
                          "caption can render below 12pt and must use secondary: \(file.lastPathComponent)")
            XCTAssertFalse(source.contains(".foregroundStyle(\(theme).faint)"),
                           "faint must not be used for readable text: \(file.lastPathComponent)")
        }
    }

    /// The widget cannot import the app target, so it keeps its own copy of the shared roles.
    func testWidgetThemeMirrorsTheAppTheme() throws {
        let app = try Self.roleValues(in: "ios/Yansu/Theme.swift")
        let widget = try Self.roleValues(in: "ios/YansuWidget/WidgetTheme.swift")
        let source = try String(contentsOf: Self.root.appendingPathComponent("ios/YansuWidget/WidgetTheme.swift"),
                                encoding: .utf8)
        XCTAssertEqual(widget.count, source.components(separatedBy: "= dynamic(").count - 1,
                       "every widget role is read, none skipped by the pattern")
        for (role, value) in widget {
            XCTAssertEqual(app[role], value, "WidgetTheme.\(role) differs from YansuTheme.\(role)")
        }
    }

    private static func roleValues(in path: String) throws -> [String: String] {
        let source = try String(contentsOf: root.appendingPathComponent(path), encoding: .utf8)
        let pattern = try NSRegularExpression(
            pattern: #"static let (\w+) = dynamic\((light: 0x[0-9a-fA-F]{6}, dark: 0x[0-9a-fA-F]{6})\)"#)
        var values: [String: String] = [:]
        for match in pattern.matches(in: source, range: NSRange(source.startIndex..., in: source)) {
            guard let name = Range(match.range(at: 1), in: source), let value = Range(match.range(at: 2), in: source)
            else { continue }
            values[String(source[name])] = source[value].lowercased()
        }
        return values
    }

    private static func resolve(_ color: UIColor, _ traits: UITraitCollection) -> RGBA {
        var red: CGFloat = 0, green: CGFloat = 0, blue: CGFloat = 0, alpha: CGFloat = 0
        XCTAssertTrue(color.resolvedColor(with: traits).getRed(&red, green: &green, blue: &blue, alpha: &alpha))
        return RGBA(red: Double(red), green: Double(green), blue: Double(blue), alpha: Double(alpha))
    }

    /// Resolves through SwiftUI's environment, as a view does; `UIColor(color)` would drop the light and dark provider.
    private static func resolve(_ color: Color, _ style: UIUserInterfaceStyle) -> RGBA {
        var environment = EnvironmentValues()
        environment.colorScheme = style == .dark ? .dark : .light
        let resolved = color.resolve(in: environment)
        return RGBA(red: Double(resolved.red), green: Double(resolved.green), blue: Double(resolved.blue),
                    alpha: Double(resolved.opacity))
    }

    /// "#rrggbb", "#rrggbbaa" or "rgba(r, g, b, a)" as tokens.json writes them.
    private static func parse(_ value: String) -> RGBA? {
        if value.hasPrefix("#"), value.count == 7, let hex = UInt32(value.dropFirst(), radix: 16) {
            return RGBA(red: Double((hex >> 16) & 0xff) / 255, green: Double((hex >> 8) & 0xff) / 255,
                        blue: Double(hex & 0xff) / 255, alpha: 1)
        }
        if value.hasPrefix("#"), value.count == 9, let hex = UInt32(value.dropFirst(), radix: 16) {
            return RGBA(red: Double((hex >> 24) & 0xff) / 255, green: Double((hex >> 16) & 0xff) / 255,
                        blue: Double((hex >> 8) & 0xff) / 255, alpha: Double(hex & 0xff) / 255)
        }
        guard value.hasPrefix("rgba("), value.hasSuffix(")") else { return nil }
        let parts = value.dropFirst(5).dropLast().split(separator: ",")
            .compactMap { Double($0.trimmingCharacters(in: .whitespaces)) }
        guard parts.count == 4 else { return nil }
        return RGBA(red: parts[0] / 255, green: parts[1] / 255, blue: parts[2] / 255, alpha: parts[3])
    }

    private static func close(_ a: RGBA, _ b: RGBA) -> Bool {
        let tolerance = 0.5 / 255
        return abs(a.red - b.red) <= tolerance && abs(a.green - b.green) <= tolerance
            && abs(a.blue - b.blue) <= tolerance && abs(a.alpha - b.alpha) <= 0.001
    }

    private static func contrast(_ a: RGBA, _ b: RGBA) -> Double {
        func channel(_ c: Double) -> Double { c <= 0.03928 ? c / 12.92 : pow((c + 0.055) / 1.055, 2.4) }
        func luminance(_ c: RGBA) -> Double {
            0.2126 * channel(c.red) + 0.7152 * channel(c.green) + 0.0722 * channel(c.blue)
        }
        let (high, low) = (max(luminance(a), luminance(b)), min(luminance(a), luminance(b)))
        return (high + 0.05) / (low + 0.05)
    }
}
