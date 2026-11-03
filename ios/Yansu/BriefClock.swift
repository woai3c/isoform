import Foundation

// The one form for a time in brief copy (handoff 2026-10-28, Copy table). In zh-Hans the period is written in
// words, as the design does: "早上 6:50", "晚上 6:00". Every other locale gets the system's short time.
enum BriefClock {
    static func text(_ date: Date, locale: Locale = .current, timeZone: TimeZone = .current) -> String {
        guard isChinese(locale) else {
            return date.formatted(Date.FormatStyle(date: .omitted, time: .shortened, locale: locale, timeZone: timeZone))
        }
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timeZone
        let parts = calendar.dateComponents([.hour, .minute], from: date)
        let hour = parts.hour ?? 0
        let minute = parts.minute ?? 0
        let hour12 = hour % 12 == 0 ? 12 : hour % 12
        return "\(period(hour: hour)) \(hour12):\(String(format: "%02d", minute))"
    }

    /// The period of the day for an hour from 0 to 23: 早上 from 5, 中午 for the noon hour, 下午 from 13, and 晚上
    /// from 18 and through the small hours, which the design has no separate word for.
    static func period(hour: Int) -> String {
        switch hour {
        case 5..<12: return "早上"
        case 12: return "中午"
        case 13..<18: return "下午"
        default: return "晚上"
        }
    }

    static func isChinese(_ locale: Locale) -> Bool {
        locale.language.languageCode?.identifier == "zh"
    }
}
