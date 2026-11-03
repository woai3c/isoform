#if DEBUG && targetEnvironment(simulator)
import UIKit
import UIKit.UIGestureRecognizerSubclass

/// Shows each touch as a soft circle, for the launch demo recordings (YANSU_UITEST_SETTINGS_STATE=launch-demo). It
/// watches touches on the app's window without taking them, and draws on top of that window in a view that takes no
/// input.
@MainActor
enum LaunchDemoTouches {
    private static weak var window: UIWindow?

    static func install() {
        Task { @MainActor in
            for _ in 0..<50 {
                if let scene = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene }).first,
                   let window = scene.windows.first(where: \.isKeyWindow) ?? scene.windows.first {
                    attach(to: window)
                    return
                }
                try? await Task.sleep(for: .milliseconds(100))
            }
        }
    }

    private static func attach(to window: UIWindow) {
        self.window = window
        let watcher = TouchWatcher { phase, point in show(phase, at: point) }
        window.addGestureRecognizer(watcher)
    }

    private static var dot: UIView?

    private static func show(_ phase: UITouch.Phase, at point: CGPoint) {
        guard let view = window else { return }
        switch phase {
        case .began:
            dot?.removeFromSuperview()
            let circle = UIView(frame: CGRect(x: 0, y: 0, width: 46, height: 46))
            circle.isUserInteractionEnabled = false
            circle.layer.zPosition = 10_000
            circle.layer.cornerRadius = 23
            circle.backgroundColor = UIColor(white: 0.62, alpha: 0.55)
            circle.layer.borderColor = UIColor(white: 1, alpha: 0.95).cgColor
            circle.layer.borderWidth = 2
            circle.layer.shadowColor = UIColor.black.cgColor
            circle.layer.shadowOpacity = 0.35
            circle.layer.shadowRadius = 4
            circle.layer.shadowOffset = .zero
            circle.center = point
            circle.transform = CGAffineTransform(scaleX: 0.7, y: 0.7)
            // Above sheets and alerts, which the window adds later.
            view.addSubview(circle)
            view.bringSubviewToFront(circle)
            dot = circle
            UIView.animate(withDuration: 0.12) { circle.transform = .identity }
        case .moved:
            dot?.center = point
        default:
            guard let circle = dot else { return }
            dot = nil
            UIView.animate(withDuration: 0.3, animations: {
                circle.alpha = 0
                circle.transform = CGAffineTransform(scaleX: 1.25, y: 1.25)
            }, completion: { _ in circle.removeFromSuperview() })
        }
    }
}

/// Sees every touch and never claims one.
private final class TouchWatcher: UIGestureRecognizer, UIGestureRecognizerDelegate {
    private let report: @MainActor (UITouch.Phase, CGPoint) -> Void

    init(report: @escaping @MainActor (UITouch.Phase, CGPoint) -> Void) {
        self.report = report
        super.init(target: nil, action: nil)
        cancelsTouchesInView = false
        delaysTouchesBegan = false
        delaysTouchesEnded = false
        delegate = self
    }

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent) {
        touches.first.map { report(.began, $0.location(in: nil)) }
    }

    override func touchesMoved(_ touches: Set<UITouch>, with event: UIEvent) {
        touches.first.map { report(.moved, $0.location(in: nil)) }
    }

    override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent) {
        touches.first.map { report(.ended, $0.location(in: nil)) }
        state = .failed
    }

    override func touchesCancelled(_ touches: Set<UITouch>, with event: UIEvent) {
        touches.first.map { report(.cancelled, $0.location(in: nil)) }
        state = .failed
    }

    func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer,
                           shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool { true }
}
#endif
