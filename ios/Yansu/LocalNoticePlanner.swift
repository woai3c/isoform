import Foundation

/// A notice that the hub has confirmed is still open at this refresh.
struct LocalOpenNotice: Equatable, Sendable {
    let id: String
    let kind: String
    let title: String
    let body: String
    let moment: Date
    let expires: Date?
    let priority: Double
    let timeSensitive: Bool
    let countsBudget: Bool
    var destinationKind: String? = nil
    var destinationID: String? = nil
}

struct LocalNoticePlan: Equatable {
    let deliver: [LocalOpenNotice]
    let deferred: Set<String>
    let batch: Bool
}

/// Selection is separate from delivery so held notices can be rechecked against the Mac before posting.
enum LocalNoticePlanner {
    static func batchTitle(count: Int, locale: Locale = .current) -> String {
        let language = locale.language.languageCode?.identifier == "zh" ? "zh-Hans" : "en"
        let bundle = Bundle.main.path(forResource: language, ofType: "lproj").flatMap(Bundle.init(path:)) ?? .main
        return String(format: String(localized: "%lld things need you", bundle: bundle, locale: locale), locale: locale, Int64(count))
    }

    static func plan(current: [LocalOpenNotice], previouslySeen: Set<String>, held: Set<String>,
                     delivered: Set<String>, settings: NotificationSettings, now: Date,
                     activeDeliveredToday: Int, lastQuietBreak: Date?, calendar: Calendar = .current) -> LocalNoticePlan {
        let kinds = Dictionary(settings.kinds.map { ($0.kind, $0) }, uniquingKeysWith: { first, _ in first })
        let candidates = current.filter { notice in
            guard (!previouslySeen.contains(notice.id) || held.contains(notice.id)), !delivered.contains(notice.id),
                  notice.expires.map({ $0 > now }) ?? true, let kind = kinds[notice.kind],
                  kind.enabled, !kind.paused else { return false }
            return !(kind.mutedUntil.flatMap(RelayTime.parse).map { $0 > now } ?? false)
        }.sorted { left, right in
            if left.priority != right.priority { return left.priority > right.priority }
            if left.moment != right.moment { return left.moment < right.moment }
            return left.id < right.id
        }
        let isQuiet = LocalBriefNotices.deliveryDate(scheduled: now, now: now, quietStart: settings.quietStart,
                                                       quietEnd: settings.quietEnd, calendar: calendar).map { $0 > now } ?? false
        let canBreakQuiet = lastQuietBreak.map { now.timeIntervalSince($0) >= 2 * 60 * 60 } ?? true
        var eligible: [LocalOpenNotice] = []
        var deferred: Set<String> = []
        var quietBreakUsed = false
        for notice in candidates {
            if isQuiet {
                guard notice.kind == "work_blocked", notice.timeSensitive, canBreakQuiet, !quietBreakUsed else {
                    deferred.insert(notice.id)
                    continue
                }
                quietBreakUsed = true
            }
            eligible.append(notice)
        }
        if settings.dailyInterruptBudget > 0 {
            var remaining = max(0, settings.dailyInterruptBudget - activeDeliveredToday)
            if eligible.count >= 3, eligible.contains(where: { $0.countsBudget }), remaining > 0 {
                remaining = 1 // One batched active notification uses one budget slot.
            }
            var budgetAllowed: [LocalOpenNotice] = []
            let isBatch = eligible.count >= 3
            for notice in eligible {
                if notice.countsBudget && remaining == 0 {
                    deferred.insert(notice.id)
                } else {
                    budgetAllowed.append(notice)
                    if notice.countsBudget && !isBatch { remaining -= 1 }
                }
            }
            eligible = budgetAllowed
        }
        return LocalNoticePlan(deliver: eligible, deferred: deferred, batch: eligible.count >= 3)
    }

    static func lateSuffix(moment: Date, now: Date, locale: Locale, calendar: Calendar = .current) -> String? {
        guard now.timeIntervalSince(moment) > 15 * 60 else { return nil }
        let formatter = DateFormatter()
        formatter.locale = locale
        formatter.calendar = calendar
        formatter.timeZone = calendar.timeZone
        formatter.dateStyle = .none
        if locale.language.languageCode?.identifier == "zh" {
            formatter.dateFormat = "a h:mm"
        } else {
            formatter.timeStyle = .short
        }
        return "· " + formatter.string(from: moment).replacingOccurrences(of: "\u{202f}", with: " ")
            .replacingOccurrences(of: "\u{00a0}", with: " ")
    }
}
