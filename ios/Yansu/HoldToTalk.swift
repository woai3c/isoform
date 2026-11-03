import AVFoundation
import Speech
import SwiftUI

@MainActor
private final class Dictation: ObservableObject {
    @Published var listening = false
    @Published var unavailable = false
    private var held = false
    private var engine: AVAudioEngine?
    private var request: SFSpeechAudioBufferRecognitionRequest?
    private var recognition: SFSpeechRecognitionTask?
    private var base = ""
    private var generation = UUID()

    func begin(text: String, update: @escaping (String) -> Void) {
        guard !held else { return }
        held = true
        generation = UUID()
        base = text
        unavailable = false
        SFSpeechRecognizer.requestAuthorization { [weak self] status in
            AVAudioSession.sharedInstance().requestRecordPermission { allowed in
                Task { @MainActor in
                    guard let self, self.held else { return }
                    guard status == .authorized, allowed else { self.unavailable = true; return }
                    self.start(update: update)
                }
            }
        }
    }

    private func start(update: @escaping (String) -> Void) {
        guard let recognizer = SFSpeechRecognizer(), recognizer.isAvailable else { unavailable = true; return }
        let audio = AVAudioEngine()
        let bufferRequest = SFSpeechAudioBufferRecognitionRequest()
        let currentGeneration = generation
        bufferRequest.shouldReportPartialResults = true
        do {
            try AVAudioSession.sharedInstance().setCategory(.record, mode: .measurement, options: .duckOthers)
            try AVAudioSession.sharedInstance().setActive(true, options: .notifyOthersOnDeactivation)
            let input = audio.inputNode
            input.installTap(onBus: 0, bufferSize: 1024, format: input.outputFormat(forBus: 0)) { buffer, _ in
                bufferRequest.append(buffer)
            }
            recognition = recognizer.recognitionTask(with: bufferRequest) { [weak self] result, error in
                let words = result?.bestTranscription.formattedString
                Task { @MainActor in
                    guard let self, self.generation == currentGeneration else { return }
                    if let words {
                        let separator = self.base.isEmpty || self.base.hasSuffix("\n") ? "" : " "
                        update(self.base + separator + words)
                    }
                    if error != nil { self.end() }
                }
            }
            engine = audio; request = bufferRequest
            audio.prepare()
            try audio.start()
            listening = true
        } catch {
            unavailable = true
            end()
        }
    }

    func end() {
        held = false
        guard engine != nil || request != nil || listening else { return }
        engine?.stop()
        engine?.inputNode.removeTap(onBus: 0)
        request?.endAudio()
        engine = nil; request = nil; listening = false
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }
}

struct HoldToTalk: View {
    @Binding var text: String
    var showsLabel = false
    @StateObject private var dictation = Dictation()

    var body: some View {
        HStack(spacing: 7) {
            Image(systemName: dictation.listening ? "waveform" : "mic")
            if showsLabel { Text(String(localized: "Hold to talk")) }
        }
        .font(.subheadline.weight(.medium)).foregroundStyle(YansuTheme.secondary)
        .padding(.horizontal, showsLabel ? 14 : 10).frame(minHeight: 40)
        .background(YansuTheme.surface, in: Capsule())
        .overlay { Capsule().strokeBorder(YansuTheme.border) }
        .contentShape(Capsule())
        .gesture(DragGesture(minimumDistance: 0)
            .onChanged { _ in dictation.begin(text: text) { text = $0 } }
            .onEnded { _ in dictation.end() })
        .accessibilityLabel(String(localized: "Hold to talk"))
        .accessibilityAddTraits(.isButton)
        .accessibilityAction {
            if dictation.listening { dictation.end() }
            else { dictation.begin(text: text) { text = $0 } }
        }
        .onDisappear { dictation.end() }
        .alert(String(localized: "Dictation is unavailable"), isPresented: $dictation.unavailable) {
            Button(String(localized: "OK"), role: .cancel) {}
        }
    }
}
