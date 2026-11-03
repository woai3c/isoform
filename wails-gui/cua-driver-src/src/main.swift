import AppKit
import Foundation

// Initialise AppKit so window-server-backed APIs (CoreGraphics,
// ScreenCaptureKit) stop asserting with "CGS_REQUIRE_INIT". We set activation
// policy to .prohibited so the helper has no dock icon and never steals
// focus even if its Info.plist is not loaded (e.g. when invoked directly
// from a CLI harness rather than via `open`).
_ = NSApplication.shared
NSApplication.shared.setActivationPolicy(.prohibited)

// AlmaComputerUse — Swift helper for desktop automation via the macOS
// Accessibility API. Design inspired by Codex's SkyComputerUseService, which
// drives UIs via `AXUIElementPerformAction` instead of synthesising HID events.
//
// Usage:
//   AlmaComputerUse daemon [--socket /path/to/sock] [--idle-seconds 600]
//   AlmaComputerUse ping                    # sanity check, talks to the running daemon

let args = Array(CommandLine.arguments.dropFirst())
let subcmd = args.first ?? "daemon"

switch subcmd {
case "daemon":
    var socketPath = defaultSocketPath()
    var idleSec: TimeInterval = 600
    var showLens = true
    var i = 1
    while i < args.count {
        let a = args[i]
        switch a {
        case "--socket":
            i += 1
            if i < args.count { socketPath = args[i] }
        case "--idle-seconds":
            i += 1
            if i < args.count, let v = TimeInterval(args[i]) { idleSec = v }
        case "--no-lens":
            showLens = false
        case "--help", "-h":
            printUsage(); exit(0)
        default:
            break
        }
        i += 1
    }
    LensOverlay.shared.setEnabled(showLens)
    do {
        try Daemon(socketPath: socketPath, idleTimeoutSec: idleSec).run()
    } catch let e as CUError {
        FileHandle.standardError.write(Data("error: \(e.message)\n".utf8))
        exit(1)
    } catch {
        FileHandle.standardError.write(Data("error: \(error.localizedDescription)\n".utf8))
        exit(1)
    }

case "version", "--version", "-v":
    print("alma-computer-use 0.1.0")

case "help", "--help", "-h":
    printUsage()

default:
    FileHandle.standardError.write(Data("unknown subcommand: \(subcmd)\n".utf8))
    printUsage()
    exit(2)
}

func printUsage() {
    let s = """
    AlmaComputerUse — macOS desktop automation helper (AX + ScreenCaptureKit)

    Subcommands:
      daemon [--socket PATH] [--idle-seconds N]
          Start the long-running daemon. Opens a Unix domain socket and serves
          NDJSON requests. Exits after N seconds of inactivity (default 600).

      version
          Print version and exit.

    The daemon is normally launched automatically by the Alma app. Users do
    not need to run it directly.
    """
    print(s)
}

func defaultSocketPath() -> String {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    return "\(home)/Library/Application Support/Alma/computer-use.sock"
}
