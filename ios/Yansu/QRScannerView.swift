import AVFoundation
import SwiftUI

// Camera view for the pairing code. It reports the first Isoform or legacy
// pairing link once, then stops. It never stores frames or codes.
struct QRScannerView: UIViewControllerRepresentable {
    var onCode: (String) -> Void
    var onUnavailable: (String) -> Void

    func makeUIViewController(context: Context) -> ScannerViewController {
        let controller = ScannerViewController()
        controller.onCode = onCode
        controller.onUnavailable = onUnavailable
        return controller
    }

    func updateUIViewController(_ controller: ScannerViewController, context: Context) {}
}

final class ScannerViewController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    var onCode: ((String) -> Void)?
    var onUnavailable: ((String) -> Void)?

    private let session = AVCaptureSession()
    private let sessionQueue = DispatchQueue(label: "app.yansu.scanner")
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private var delivered = false

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        requestAccessAndConfigure()
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        previewLayer?.frame = view.bounds
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        stop()
    }

    private func requestAccessAndConfigure() {
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            configure()
        case .notDetermined:
            AVCaptureDevice.requestAccess(for: .video) { [weak self] granted in
                DispatchQueue.main.async {
                    if granted {
                        self?.configure()
                    } else {
                        self?.onUnavailable?("Isoform needs camera access to scan the code. Paste the link instead.")
                    }
                }
            }
        default:
            onUnavailable?("Camera access is off for Isoform. Allow it in Settings, or paste the link instead.")
        }
    }

    private func configure() {
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input) else {
            onUnavailable?("This phone has no camera available. Paste the link instead.")
            return
        }
        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else {
            onUnavailable?("The camera could not start. Paste the link instead.")
            return
        }
        session.beginConfiguration()
        session.addInput(input)
        session.addOutput(output)
        session.commitConfiguration()
        output.metadataObjectTypes = [.qr]
        output.setMetadataObjectsDelegate(self, queue: .main)

        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.videoGravity = .resizeAspectFill
        layer.frame = view.bounds
        view.layer.addSublayer(layer)
        previewLayer = layer
        start()
    }

    private func start() {
        let session = self.session
        sessionQueue.async {
            if !session.isRunning { session.startRunning() }
        }
    }

    private func stop() {
        let session = self.session
        sessionQueue.async {
            if session.isRunning { session.stopRunning() }
        }
    }

    func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput metadataObjects: [AVMetadataObject],
                        from connection: AVCaptureConnection) {
        guard !delivered,
              let object = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
              object.type == .qr,
              let value = object.stringValue,
              PairingInvitation.hasInvitationPrefix(value) else { return }
        delivered = true
        stop()
        onCode?(value)
    }
}
