import Foundation
import SwiftUI

/// What Today shows while the phone has no brief to read (walk 1.0.0, P125): Today is never blank on a paired phone.
/// The times come from the computer's own schedule, so they are real times in the phone's time zone.
enum TodayAwaitingBrief {
    /// The first scheduled brief after `now`, from the snapshot's schedule.
    static func nextBriefDate(in snapshot: MobileTodaySnapshot?, after now: Date) -> Date? {
        snapshot?.briefSchedule?.compactMap(\.date).filter { $0 > now }.min()
    }

    /// True while a brief is on its way: no snapshot has arrived yet, or the snapshot names a brief whose text the
    /// phone has not saved. Otherwise the computer has no brief for the phone yet.
    static func isFetching(_ snapshot: MobileTodaySnapshot?) -> Bool {
        guard let snapshot else { return true }
        return snapshot.currentBrief != nil || snapshot.latestBrief != nil || !snapshot.briefs.isEmpty
    }

    /// "Next brief at 6:50 AM", or nil when the computer's schedule is not known yet.
    static func nextBriefLine(in snapshot: MobileTodaySnapshot?, now: Date, locale: Locale) -> String? {
        guard let date = nextBriefDate(in: snapshot, after: now) else { return nil }
        return String.localizedStringWithFormat(String(localized: "Next brief at %@"), timeText(date, locale: locale))
    }
}

extension TodayAwaitingBrief {
    /// "Yesterday", the weekday for the last six days, or the month and day before that; nil for a brief from today.
    static func dayName(for date: Date, now: Date, locale: Locale) -> String? {
        let calendar = Calendar.current
        if calendar.isDate(date, inSameDayAs: now) { return nil }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: yesterday) {
            return String(localized: "Yesterday")
        }
        let formatter = DateFormatter()
        formatter.locale = locale
        let days = calendar.dateComponents([.day], from: date, to: now).day ?? Int.max
        formatter.setLocalizedDateFormatFromTemplate(days >= 0 && days < 7 ? "EEEE" : "MMMd")
        return formatter.string(from: date)
    }

    /// "6:50 AM", or "早上 6:50" in Chinese: the period word gets a space before the time, as Chinese copy writes it.
    static func timeText(_ date: Date, locale: Locale) -> String {
        let text = BriefClock.text(date, locale: locale)
        guard locale.language.languageCode?.identifier == "zh" else { return text }
        return text.replacingOccurrences(of: "上午", with: "早上")
            .replacingOccurrences(of: #"([\u4e00-\u9fff])(\d)"#, with: "$1 $2", options: .regularExpression)
    }

    /// The card's title with the day it was made on, when it is not from today: "Yesterday · Morning brief · 7:40 AM".
    static func cardTitle(_ brief: MobileBriefV1, now: Date, locale: Locale) -> String {
        let title = BriefTitle.title(BriefTitleInput(brief: brief))
        guard let date = brief.asOfDate, let day = dayName(for: date, now: now, locale: locale) else { return title }
        return "\(day) · \(title)"
    }
}

/// The card Today shows when the phone has no brief to read yet. It names the computer while the brief is fetched,
/// and the first brief's time once the computer reports that none is due yet.
struct TodayAwaitingBriefCard: View {
    let computerName: String
    let snapshot: MobileTodaySnapshot?
    let now: Date
    @Environment(\.locale) private var locale

    var body: some View {
        YansuCard {
            VStack(alignment: .leading, spacing: 6) {
                if TodayAwaitingBrief.isFetching(snapshot) {
                    Text(String.localizedStringWithFormat(String(localized: "Getting your brief from %@…"), computerName))
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("today.awaiting.fetching")
                } else if let date = TodayAwaitingBrief.nextBriefDate(in: snapshot, after: now) {
                    Text(String.localizedStringWithFormat(String(localized: "Your first brief comes at %@."),
                                                          TodayAwaitingBrief.timeText(date, locale: locale)))
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("today.awaiting.firstBrief")
                } else {
                    Text("Your first brief comes soon.")
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("today.awaiting.firstBrief")
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("today.awaiting")
    }
}
