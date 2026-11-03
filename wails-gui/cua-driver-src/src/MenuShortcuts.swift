import AppKit
import ApplicationServices
import Foundation

/// Enumerate the menu bar of a target app and extract every item that
/// exposes a keyboard shortcut via the `AXMenuItemCmd*` attributes.
///
/// Why: Codex gives the model a table of "File > New Note = cmd+n" pairs
/// as part of get_app_state, so the model can prefer `press_key cmd+n`
/// over navigating the menu by clicks. Same mechanism here.
///
/// AX model:
///   Application → AXMenuBar → list of AXMenuBarItem (File, Edit, ...) →
///   each has a child AXMenu → items are AXMenuItem (or AXMenuBarItem for
///   submenus). Each item may expose:
///     - AXMenuItemCmdChar        (String, e.g. "s")
///     - AXMenuItemCmdVirtualKey  (Number — for F1, arrows, etc.)
///     - AXMenuItemCmdModifiers   (Number — bitmask, see decode below)
///     - AXMenuItemCmdGlyph       (Number — special glyphs, ignored for v1)
enum MenuShortcuts {

    struct Shortcut {
        let path: [String]          // ["File", "New Note"]
        let combo: String           // "cmd+n"
    }

    static func collect(for app: NSRunningApplication, max: Int = 200) -> [Shortcut] {
        let appEl = AXUIElementCreateApplication(app.processIdentifier)
        var menuBar: AXUIElement?
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(appEl, kAXMenuBarAttribute as CFString, &raw)
        if err == .success, let v = raw {
            menuBar = (v as! AXUIElement)
        }
        guard let bar = menuBar else { return [] }

        var out: [Shortcut] = []
        walk(bar, path: [], budget: max, out: &out)
        return out
    }

    private static func walk(_ el: AXUIElement, path: [String], budget: Int, out: inout [Shortcut]) {
        guard out.count < budget else { return }

        // Extract title for this element (if any) — used as path component.
        let title = stringAttr(el, kAXTitleAttribute) ?? ""
        let nextPath = title.isEmpty ? path : path + [title]

        // If this node has a shortcut, record it. Both AXMenuBarItem
        // (top-level) and AXMenuItem nodes can carry shortcuts.
        if let combo = shortcut(of: el) {
            out.append(Shortcut(path: nextPath, combo: combo))
        }

        // Recurse into children. Menu bar items have an AXMenu child;
        // menu items can have a submenu or be leaves.
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(el, kAXChildrenAttribute as CFString, &raw)
        if err == .success, let kids = raw as? [AXUIElement] {
            for k in kids {
                walk(k, path: nextPath, budget: budget, out: &out)
            }
        }
    }

    private static func shortcut(of el: AXUIElement) -> String? {
        let cmdChar = stringAttr(el, "AXMenuItemCmdChar")
        let cmdVK = numberAttr(el, "AXMenuItemCmdVirtualKey")
        guard (cmdChar != nil && !cmdChar!.isEmpty) || cmdVK != nil else {
            return nil
        }
        let modifiersRaw = numberAttr(el, "AXMenuItemCmdModifiers") ?? 0

        // Decode the AXMenuItemCmdModifiers bitmask. Semantics (from macOS
        // Menu Manager constants):
        //   0 = Command implied
        //   bit 0 (1) = Shift
        //   bit 1 (2) = Option/Alt
        //   bit 2 (4) = Control
        //   bit 3 (8) = suppress Command (i.e. "no cmd")
        var parts: [String] = []
        let m = Int(truncatingIfNeeded: modifiersRaw)
        if (m & 8) == 0 { parts.append("cmd") }
        if (m & 4) != 0 { parts.append("ctrl") }
        if (m & 2) != 0 { parts.append("opt") }
        if (m & 1) != 0 { parts.append("shift") }

        // Prefer character if present (it's layout-agnostic and matches
        // xdotool syntax directly). Otherwise translate the virtual key.
        let keyPart: String
        if let c = cmdChar, !c.isEmpty {
            keyPart = c.lowercased()
        } else if let vk = cmdVK {
            keyPart = virtualKeyName(UInt32(truncatingIfNeeded: vk))
        } else {
            return nil
        }
        parts.append(keyPart)
        return parts.joined(separator: "+")
    }

    // MARK: - Attribute helpers

    private static func stringAttr(_ el: AXUIElement, _ name: String) -> String? {
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(el, name as CFString, &raw)
        if err == .success, let v = raw as? String { return v }
        return nil
    }

    private static func numberAttr(_ el: AXUIElement, _ name: String) -> Int64? {
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(el, name as CFString, &raw)
        if err == .success, let n = raw as? NSNumber { return n.int64Value }
        return nil
    }

    /// Translate a CGKeyCode to an xdotool-compatible name. Mirrors the
    /// reverse map in EventDispatch.swift's KeyParser.
    private static func virtualKeyName(_ code: UInt32) -> String {
        switch code {
        case 0x24: return "Return"
        case 0x30: return "Tab"
        case 0x31: return "Space"
        case 0x33: return "Backspace"
        case 0x75: return "Delete"
        case 0x35: return "Escape"
        case 0x7B: return "Left"
        case 0x7C: return "Right"
        case 0x7D: return "Down"
        case 0x7E: return "Up"
        case 0x73: return "Home"
        case 0x77: return "End"
        case 0x74: return "PageUp"
        case 0x79: return "PageDown"
        case 0x7A: return "F1"
        case 0x78: return "F2"
        case 0x63: return "F3"
        case 0x76: return "F4"
        case 0x60: return "F5"
        case 0x61: return "F6"
        case 0x62: return "F7"
        case 0x64: return "F8"
        case 0x65: return "F9"
        case 0x6D: return "F10"
        case 0x67: return "F11"
        case 0x6F: return "F12"
        default: return "vk\(String(format: "%02X", code))"
        }
    }
}
