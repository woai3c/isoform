// Standalone public-image benchmark. Does not capture screens or launch Isoform.
import Foundation
import Vision
import ImageIO

struct Input: Decodable {
    let path: String
    let mode: String
}
struct Output: Encodable {
    var text: String = ""
    var latency_ms: Double = 0
    var error: String? = nil
}

@main
struct VisionEval {
    static func main() async {
        while let line = readLine() {
            var output = Output()
            do {
                let input = try JSONDecoder().decode(Input.self, from: Data(line.utf8))
                let url = URL(fileURLWithPath: input.path)
                let start = ContinuousClock.now
                if input.mode == "document-languages" {
                    if #available(macOS 26.0, *) {
                        output.text = RecognizeDocumentsRequest().supportedRecognitionLanguages.map { $0.minimalIdentifier }.joined(separator: ",")
                    }
                } else if input.mode == "documents" {
                    if #available(macOS 26.0, *) {
                        var request = RecognizeDocumentsRequest()
                        request.textRecognitionOptions.recognitionLanguages = [Locale.Language(identifier: "zh-Hans"), Locale.Language(identifier: "zh-Hant"), Locale.Language(identifier: "en-US")]
                        request.textRecognitionOptions.useLanguageCorrection = true
                        let results = try await request.perform(on: url)
                        output.text = results.map { $0.document.text.transcript }.joined(separator: "\n")
                    } else {
                        throw NSError(domain: "survey", code: 1, userInfo: [NSLocalizedDescriptionKey: "Requires macOS 26"])
                    }
                } else {
                    let request = VNRecognizeTextRequest()
                    request.recognitionLevel = input.mode == "fast" ? .fast : .accurate
                    request.usesLanguageCorrection = input.mode != "fast"
                    if input.mode == "languages" {
                        output.text = try request.supportedRecognitionLanguages().joined(separator: ",")
                    } else if input.mode == "multilingual" {
                        request.automaticallyDetectsLanguage = true
                        request.recognitionLanguages = try request.supportedRecognitionLanguages()
                    } else if input.mode.hasPrefix("language:") {
                        request.recognitionLanguages = [String(input.mode.dropFirst("language:".count))]
                    } else if input.mode == "app" {
                        let supported = try request.supportedRecognitionLanguages()
                        let preferred = ["zh-Hans", "zh-Hant", "en-US"]
                        request.recognitionLanguages = preferred.filter { supported.contains($0) }
                        request.recognitionLanguages += supported.filter { lang in
                            !request.recognitionLanguages.contains(lang) && ["zh-", "ja-", "ko-"].contains(where: { lang.hasPrefix($0) })
                        }
                    } else {
                        request.recognitionLanguages = input.mode == "accurate-zh-first" ? ["zh-Hans", "en-US"] : ["en-US", "zh-Hans"]
                    }
                    if input.mode != "languages" {
                        try VNImageRequestHandler(url: url).perform([request])
                        output.text = (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
                    }
                }
                let elapsed = start.duration(to: .now).components
                output.latency_ms = Double(elapsed.seconds) * 1000 + Double(elapsed.attoseconds) / 1e15
            } catch {
                output.error = String(describing: error)
            }
            if let data = try? JSONEncoder().encode(output), let text = String(data: data, encoding: .utf8) {
                print(text)
                fflush(stdout)
            }
        }
    }
}
