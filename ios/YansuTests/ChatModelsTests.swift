import Foundation
import SwiftUI
import XCTest
@testable import Yansu

private func decode<T: Decodable>(_ type: T.Type, _ text: String) throws -> T {
    try JSONDecoder().decode(type, from: Data(text.utf8))
}

private func expectedMessageSendTime(_ date: Date, locale: Locale) -> String {
    date.formatted(.dateTime.hour().minute().locale(locale))
}

final class ChatModelsTests: XCTestCase {
    private let proactiveJSON = #"""
    {"seq":7,"id":"row-moment","role":"system","kind":"proactive","text":"Your brief is ready.",
     "proactive":{"momentId":"moment-brief","kind":"brief_ready","headline":"Your brief is ready.",
       "body":"Open it when you have a moment.","draft":false,"actionIds":["open","future_action","open"],
       "actionLabels":{"open":"Open brief","future_action":"Do something new"},"target":{"kind":"today"},
       "evidence":[{"kind":"brief","id":"brief-today","at":"2026-10-16T18:00:00Z"}],"state":"pending",
       "localized":{"zh-CN":{"headline":"早报已准备好。","body":"有空时查看。","actionLabels":{"open":"打开早报"}}}}}
    """#

    func testProactiveCardDecodesCopyActionsSourcesAndState() throws {
        let item = try decode(ChatItemView.self, proactiveJSON)
        let card = try XCTUnwrap(item.proactive)

        XCTAssertEqual(item.kind, .proactive)
        XCTAssertEqual(card.id, "moment-brief")
        XCTAssertEqual(card.copy(language: "en").headline, "Your brief is ready.")
        XCTAssertEqual(card.actions(language: "en"), [ChatProactiveAction(id: "open", label: "Open brief")])
        XCTAssertEqual(card.actions(language: "zh-CN").first?.label, "打开早报")
        XCTAssertEqual(card.copy(language: "fr").body, card.body)
        XCTAssertEqual(card.evidence.first?.sourceLabel(language: "en"), "Brief")
        XCTAssertEqual(card.target.kind, "today")
        XCTAssertFalse(card.draft)
        XCTAssertTrue(card.canAct)
        XCTAssertEqual(card.statusLabel(language: "en"), "Waiting on you")
        XCTAssertEqual(card.statusLabel(language: "zh-CN"), "等待你回应")
        XCTAssertEqual(card.evidence.first?.sourceLabel(language: "zh-CN"), "简报")
    }

    /// The computer withdraws an update whose source is gone: routine, nothing to answer, never "Waiting on you".
    func testWithdrawnThreadUpdateIsARoutineLineNotAWaitingCard() throws {
        let item = try decode(ChatItemView.self, #"""
        {"seq":9,"id":"row-withdrawn","role":"system","kind":"proactive","text":"",
         "proactive":{"momentId":"moment-withdrawn","kind":"thread_update_unavailable",
           "headline":"Thread update unavailable.","body":"This thread is no longer available.",
           "draft":false,"actionIds":[],"actionLabels":{},"target":{"kind":"none"},"evidence":[],
           "state":"expired","localized":{}}}
        """#)
        let card = try XCTUnwrap(item.proactive)

        XCTAssertTrue(card.isWithdrawnThreadUpdate)
        XCTAssertFalse(card.canAct)
        XCTAssertTrue(card.actions(language: "en").isEmpty)
        XCTAssertEqual(card.statusLabel(language: "en"), "No longer waiting")
        XCTAssertEqual(card.headline, "Thread update unavailable.")
    }

    func testChatCardButtonSpecificationMapsEveryKind() throws {
        let scenarios: [(String, [String], String, String?, String?, String?, String?)] = [
            ("meeting_prep", ["open"], "open_prep", "open_calendar", "today", nil, "2026-10-16T10:00:00Z"),
            ("calendar_conflict", ["open"], "show_both", "open_calendar", "today", nil, "2026-10-16T10:00:00Z"),
            ("decision_due", ["open"], "show_thread", nil, "thread", "com.example.editor", nil),
            ("decision_due", ["open"], "show_thread", nil, "thread", nil, nil),
            ("waiting_on_reply", ["open", "not_now"], "review", "not_now", "today", nil, nil),
            ("decision_due", ["approve", "decline"], "review", nil, "handoff", nil, nil),
            ("work_finished", ["review"], "review", nil, "today", nil, nil),
            ("playbook_result", ["review"], "review", nil, "today", nil, nil),
            ("work_blocked", ["review", "stop", "dismiss"], "answer", "stop", "today", nil, nil),
            ("commitment_slipping", ["open", "not_now"], "find_time", "not_now", "today", nil, nil),
            ("person_reappeared", ["open_person"], "show_person", nil, "person", nil, nil),
            ("topic_finds", ["open"], "show_whats_new", nil, "today", nil, nil),
            ("playbook_suggested", ["see", "not_now"], "review", "not_now", "today", nil, nil),
            ("suggestion", ["do_once", "not_now"], "do_once", "not_now", "today", nil, nil),
            ("brief_ready", ["open"], "play", "read_it", "today", nil, "brief-1"),
            ("weekly_recap", ["open"], "read_week", "play", "today", nil, "brief-1"),
            ("first_week", ["open"], "read_it", nil, "today", nil, nil),
            ("brief_missed", ["make_it_now"], "make_it_now", nil, "today", nil, nil),
            ("schedule_missed", ["run_now", "open"], "run_now", "show_why", "today", nil, nil),
            ("learned_to_confirm", ["keep", "correct"], "keep", nil, "today", nil, nil),
            ("goal_check_in", ["update", "snooze", "dismiss"], "update", "snooze", "today", nil, nil),
            ("capture_paused", ["resume_capture"], "resume_capture", nil, "today", nil, nil),
            ("reminder", ["done", "not_now"], "done", "not_now", "today", nil, nil),
        ]
        for (index, scenario) in scenarios.enumerated() {
            let (kind, actions, main, second, target, bundleID, detail) = scenario
            var extra: [String: Any] = [:]
            if let bundleID { extra["place"] = ["bundleId": bundleID] }
            if let detail, kind == "brief_ready" || kind == "weekly_recap" { extra["briefId"] = detail }
            if let detail, kind == "meeting_prep" || kind == "calendar_conflict" {
                extra["event"] = ["start": detail]
            }
            if kind == "work_finished" || kind == "playbook_result" {
                extra["work"] = ["handoffId": "work-1", "status": "completed", "summary": "Ready.",
                                 "result": "Result.", "deliverables": [["label": "Report", "url": "/tmp/report.md"]]]
            }
            let card = try proactiveCard(kind, actions: actions, targetKind: target ?? "today", extra: extra)
            let slots = ChatCardButtons.make(for: card, availableBriefIDs: ["brief-1"])
            XCTAssertEqual(slots.main?.id, main, "\(index): \(kind)")
            XCTAssertEqual(slots.second?.id, second, "\(index): \(kind)")
        }
    }

    func testChatCardButtonGatingHidesUnsupportedActionsAndPromotesSecond() throws {
        let noEvent = ChatCardButtons.make(for: try proactiveCard("meeting_prep", actions: ["open"]))
        XCTAssertEqual(noEvent.main?.id, "open_prep")
        XCTAssertNil(noEvent.second)

        let invalidDate = ChatCardButtons.make(for: try proactiveCard(
            "meeting_prep", actions: ["open"], extra: ["event": ["start": "not a date"]]))
        XCTAssertNil(invalidDate.second)

        let missingBrief = ChatCardButtons.make(for: try proactiveCard("brief_ready", actions: ["open"]))
        XCTAssertEqual(missingBrief.main?.id, "read_it")
        XCTAssertNil(missingBrief.second)

        let thread = ChatCardButtons.make(for: try proactiveCard(
            "decision_due", actions: ["open"], targetKind: "thread",
            extra: ["place": ["bundleId": "com.example.editor"]]))
        XCTAssertEqual(thread.main?.id, "show_thread")
        XCTAssertEqual(thread.main?.behavior, .panel)
        XCTAssertNil(thread.second)

        let learned = ChatCardButtons.make(for: try proactiveCard("learned_to_confirm", actions: ["keep", "correct"]))
        XCTAssertEqual(learned.main?.id, "keep")
        XCTAssertNil(learned.second)
    }

    func testChatCardItemSheetHidesPanelButtonsAndPromotesSecond() throws {
        let prep = try proactiveCard(
            "meeting_prep", actions: ["open"], extra: ["event": ["start": "2026-10-16T10:00:00Z"]])
        let cardSlots = ChatCardButtons.make(for: prep)
        XCTAssertEqual(cardSlots.main?.id, "open_prep")
        XCTAssertEqual(cardSlots.second?.id, "open_calendar")

        let sheetSlots = ChatCardButtons.make(for: prep, excludingPanelButtons: true)
        XCTAssertEqual(sheetSlots.main?.id, "open_calendar")
        XCTAssertEqual(sheetSlots.main?.behavior, .calendar)
        XCTAssertNil(sheetSlots.second)
    }

    func testChatProactiveOptionalDetailsDecodeTolerantly() throws {
        let card = try decode(ChatProactiveView.self, #"""
        {"momentId":"details","kind":"meeting_prep","event":{"title":"Review","start":"2026-10-16T10:00:00Z",
         "end":"2026-10-16T10:30:00Z","place":"Room 2","people":"Sam","private":false,"minutes":30},
         "events":[{"title":"Private","private":true,"minutes":15}],
         "place":{"app":"Editor","window":"Launch plan","bundleId":"com.example.editor"},
         "personName":"Dana","briefId":"brief-1","kindMuted":true}
        """#)
        XCTAssertEqual(card.event?.title, "Review")
        XCTAssertEqual(card.event?.start, "2026-10-16T10:00:00Z")
        XCTAssertEqual(card.event?.end, "2026-10-16T10:30:00Z")
        XCTAssertEqual(card.event?.place, "Room 2")
        XCTAssertEqual(card.event?.people, "Sam")
        XCTAssertEqual(card.event?.isPrivate, false)
        XCTAssertEqual(card.event?.minutes, 30)
        XCTAssertEqual(card.events.first?.isPrivate, true)
        XCTAssertEqual(card.place?.bundleID, "com.example.editor")
        XCTAssertEqual(card.personName, "Dana")
        XCTAssertEqual(card.briefID, "brief-1")
        XCTAssertEqual(card.kindMuted, true)

        let malformed = try decode(ChatProactiveView.self, #"""
        {"momentId":"malformed","event":4,"events":["bad"],"place":false,"personName":9,"briefId":{},
         "kindMuted":"yes"}
        """#)
        XCTAssertNil(malformed.event)
        XCTAssertTrue(malformed.events.isEmpty)
        XCTAssertNil(malformed.place)
        XCTAssertNil(malformed.personName)
        XCTAssertNil(malformed.briefID)
        XCTAssertNil(malformed.kindMuted)
    }

    func testMomentNoticeOpensChatAndLooksUpOnlyTheMatchingItem() throws {
        let item = try decode(ChatItemView.self, proactiveJSON)
        let foundRoute = NoticeRouting.route(kind: "meeting_prep", momentID: "moment-brief")
        XCTAssertEqual(foundRoute, .chat(momentID: "moment-brief"))
        XCTAssertEqual(ChatProactiveLookup.find(momentID: "moment-brief", in: [item])?.momentID, "moment-brief")

        let missingRoute = NoticeRouting.route(kind: "meeting_prep", momentID: "not-in-chat")
        XCTAssertEqual(missingRoute, .chat(momentID: "not-in-chat"))
        XCTAssertNil(ChatProactiveLookup.find(momentID: "not-in-chat", in: [item]))
        XCTAssertEqual(NoticeRouting.route(kind: "brief_ready", momentID: "moment-brief"), .brief)
    }

    private func proactiveCard(_ kind: String, actions: [String], targetKind: String = "today",
                               extra: [String: Any] = [:]) throws -> ChatProactiveView {
        var payload: [String: Any] = ["momentId": "moment-\(kind)", "kind": kind,
                                      "actionIds": actions, "target": ["kind": targetKind]]
        payload.merge(extra) { _, new in new }
        let data = try JSONSerialization.data(withJSONObject: payload)
        return try JSONDecoder().decode(ChatProactiveView.self, from: data)
    }

    func testDraftContentDecodesMetadataAndOffersLocalizedSend() throws {
        let item = try decode(ChatItemView.self, #"""
        {"seq":8,"id":"draft-row","role":"system","kind":"proactive","text":"Reply drafted",
         "proactive":{"momentId":"draft-1","kind":"waiting_on_reply","headline":"Reply to Sam",
          "body":"The reply is ready.","draft":true,"actionIds":["send","not_now"],
          "actionLabels":{"send":"Send it","not_now":"Not now"},"state":"pending",
          "draftContent":{"to":"sam@example.com","subject":"Q3 deck","text":"Thanks for the notes.\n\nThursday works.",
                          "channel":"mail","recipient":"Sam","principalId":"person-sam","revision":"rev-3"}}}
        """#)
        let card = try XCTUnwrap(item.proactive)

        XCTAssertEqual(card.readableDraft?.to, "sam@example.com")
        XCTAssertEqual(card.readableDraft?.subject, "Q3 deck")
        XCTAssertEqual(card.readableDraft?.text, "Thanks for the notes.\n\nThursday works.")
        XCTAssertEqual(card.draftContent?.channel, "mail")
        XCTAssertEqual(card.draftContent?.recipient, "Sam")
        XCTAssertEqual(card.draftContent?.principalId, "person-sam")
        XCTAssertEqual(card.draftContent?.revision, "rev-3")
        XCTAssertEqual(card.actions(language: "en").map(\.id), ["send", "not_now"])
        XCTAssertEqual(card.actions(language: "en").first?.label, "Send the reply")
        XCTAssertEqual(card.actions(language: "zh-CN").first?.label, "发送回复")
        XCTAssertEqual(CachedChatItem(item).view.proactive?.draftContent, card.draftContent)
    }

    func testIncompleteOrMalformedDraftContentNeverOffersSend() throws {
        for content in [
            #"{"to":"","subject":"Q3","text":"Thursday works","channel":"mail","recipient":"Sam","principalId":"p1","revision":"r1"}"#,
            #"{"to":"sam@example.com","subject":"Q3","text":"  ","channel":"mail","recipient":"Sam","principalId":"p1","revision":"r1"}"#,
            #"{"to":"sam@example.com","subject":"Q3","text":"Thursday works","recipient":"Sam","principalId":"p1","revision":"r1"}"#,
            #"{"to":"sam@example.com","subject":"Q3","text":"Thursday works","channel":"teams","recipient":"Sam","principalId":"p1","revision":"r1"}"#,
            #"{"to":"sam@example.com","subject":"Q3","text":"Thursday works","channel":"mail","principalId":"p1","revision":"r1"}"#,
            #"{"to":"sam@example.com","subject":"Q3","text":"Thursday works","channel":"mail","recipient":"Sam","revision":"r1"}"#,
            #"{"to":"sam@example.com","subject":"Q3","text":"Thursday works","channel":"mail","recipient":"Sam","principalId":"p1"}"#,
            #"{"to":"sam@example.com","subject":" ","text":"Thursday works","channel":"mail","recipient":"Sam","principalId":"p1","revision":"r1"}"#,
            #""not a draft""#,
        ] {
            let card = try decode(ChatProactiveView.self, """
            {"momentId":"draft-1","kind":"waiting_on_reply","draft":true,"state":"pending",
             "actionIds":["send","not_now"],"actionLabels":{"send":"Send","not_now":"Not now"},
             "draftContent":\(content)}
            """)

            XCTAssertNil(card.readableDraft)
            XCTAssertEqual(card.actions(language: "en").map(\.id), ["not_now"])
        }
    }

    func testSlackDraftAllowsAnEmptySubjectButStillRequiresMetadata() throws {
        let card = try decode(ChatProactiveView.self, #"""
        {"momentId":"slack-draft","draft":true,"state":"pending","actionIds":["send"],
         "draftContent":{"to":"#launch","subject":"","text":"Thursday works","channel":"slack",
                         "recipient":"Launch channel","principalId":"channel-launch","revision":"rev-1"}}
        """#)

        XCTAssertNotNil(card.readableDraft)
        XCTAssertEqual(card.actions(language: "en").map(\.id), ["send"])
    }

    func testMessageSendReportDecodesPersistsAndPresentsLocalizedChannelAndTime() throws {
        let date = Calendar.current.date(from: DateComponents(year: 2026, month: 10, day: 24, hour: 14, minute: 10))!
        let milliseconds = Int64(date.timeIntervalSince1970 * 1000)
        let english = Locale(identifier: "en_US")
        let chinese = Locale(identifier: "zh-Hans")
        let item = try decode(ChatItemView.self, """
        {"seq":9,"role":"system","kind":"text","text":"Sent via Mail, 2:10 PM",
         "report":{"source":"message_send","channel":"mail","sentAt":\(milliseconds),"principalId":"person-sam"}}
        """)
        let report = try XCTUnwrap(item.report)

        XCTAssertEqual(report.source, "message_send")
        XCTAssertEqual(report.channel, "mail")
        XCTAssertEqual(report.sentAt, milliseconds)
        XCTAssertEqual(report.principalId, "person-sam")
        XCTAssertEqual(report.presentation(locale: english),
                       "Sent via Mail, \(expectedMessageSendTime(date, locale: english))")
        XCTAssertEqual(report.presentation(locale: chinese),
                       "已通过邮件发送，\(expectedMessageSendTime(date, locale: chinese))")
        XCTAssertEqual(CachedChatItem(item).view.report, report)
    }

    func testSlackMessageSendReportUsesSlackInBothLanguages() throws {
        let date = Calendar.current.date(from: DateComponents(year: 2026, month: 10, day: 24, hour: 14, minute: 10))!
        let milliseconds = Int64(date.timeIntervalSince1970 * 1000)
        let english = Locale(identifier: "en_US")
        let chinese = Locale(identifier: "zh-Hans")
        let item = try decode(ChatItemView.self, """
        {"seq":9,"role":"system","kind":"text","text":"English fallback",
         "report":{"source":"message_send","channel":"slack","sentAt":\(milliseconds)}}
        """)

        XCTAssertEqual(item.report?.presentation(locale: english),
                       "Sent via Slack, \(expectedMessageSendTime(date, locale: english))")
        XCTAssertEqual(item.report?.presentation(locale: chinese),
                       "已通过 Slack 发送，\(expectedMessageSendTime(date, locale: chinese))")
    }

    func testInvalidMessageSendReportsKeepTheTextFallback() throws {
        for report in [
            #"{"source":"other","channel":"mail","sentAt":1761120060000}"#,
            #"{"source":"message_send","channel":"teams","sentAt":1761120060000}"#,
            #"{"source":"message_send","channel":"mail","sentAt":0}"#,
            #"{"source":"message_send","channel":"mail","sentAt":"1761120060000"}"#,
        ] {
            let item = try decode(ChatItemView.self, """
            {"seq":9,"role":"system","kind":"text","text":"Original fallback","report":\(report)}
            """)

            XCTAssertNil(item.report)
            XCTAssertEqual(item.text, "Original fallback")
        }
    }

    func testLegacyDraftWithoutContentKeepsOtherActions() throws {
        let card = try decode(ChatProactiveView.self, #"""
        {"momentId":"legacy-draft","kind":"waiting_on_reply","draft":true,"state":"pending",
         "actionIds":["open","send","not_now"],"actionLabels":{"open":"Open Mail","send":"Send","not_now":"Not now"}}
        """#)

        XCTAssertNil(card.readableDraft)
        XCTAssertEqual(card.actions(language: "en").map(\.id), ["open", "not_now"])
    }

    func testCompletedAndUnknownProactiveStatesDoNotOfferWrites() throws {
        var card = try XCTUnwrap(decode(ChatItemView.self, proactiveJSON).proactive)
        for (state, label) in [("acted", "Done"), ("dismissed", "Dismissed"),
                               ("deferred", "Saved for later"), ("expired", "No longer waiting"),
                               ("future_state", "Status unavailable")] {
            card.state = state
            XCTAssertFalse(card.canAct, state)
            XCTAssertEqual(card.statusLabel(language: "en"), label)
        }
        card.state = "opened"
        XCTAssertTrue(card.canAct, "opening a destination does not remove the ability to return there")
    }

    func testProactiveLifecycleChromeUsesTheCardLanguage() throws {
        var card = try XCTUnwrap(decode(ChatItemView.self, proactiveJSON).proactive)
        for (state, label) in [("pending", "等待你回应"), ("opened", "已打开"), ("acted", "已完成"),
                               ("dismissed", "已忽略"), ("deferred", "已留待稍后处理"),
                               ("expired", "无需再回应"), ("future_state", "状态不可用")] {
            card.state = state
            XCTAssertEqual(card.statusLabel(language: "zh-CN"), label)
            XCTAssertEqual(card.statusLabel(language: "fr"), card.statusLabel(language: "en"))
        }
        XCTAssertEqual(ChatPresentationLanguage(locale: Locale(identifier: "zh-Hans-CN")).code, "zh-CN")
        XCTAssertEqual(ChatPresentationLanguage(locale: Locale(identifier: "en-US")).code, "en")
    }

    func testMalformedPresentationKeepsThePlainTextFallback() throws {
        for payload in [#"{"headline":"No identity"}"#, #"{"momentId":""}"#, #""wrong type""#] {
            let item = try decode(ChatItemView.self, """
            {"seq":1,"role":"system","kind":"proactive","text":"Read this update.","proactive":\(payload)}
            """)
            XCTAssertNil(item.proactive)
            XCTAssertEqual(item.text, "Read this update.")
        }
        let item = try decode(ChatItemView.self, #"{"kind":"status","text":"One update","status":false}"#)
        XCTAssertNil(item.status)
        XCTAssertEqual(item.text, "One update")
    }

    func testStatusModelKeepsUniqueEntriesAndLocalizedText() throws {
        let item = try decode(ChatItemView.self, #"""
        {"id":"status-morning","seq":9,"kind":"status","role":"system","text":"Two updates",
         "status":{"period":"morning","entries":[
          {"id":"brief","kind":"brief_missed","text":"Your brief is waiting.","localized":{"zh-CN":"早报等待查看。"}},
          "malformed",{"id":"brief","kind":"brief_missed","text":"Duplicate"},
          {"id":"capture","kind":"capture_paused","text":"Capture is paused."}]}}
        """#)
        let status = try XCTUnwrap(item.status)
        XCTAssertEqual(status.entries.map(\.id), ["brief", "capture"])
        XCTAssertEqual(status.summary(language: "en", fallback: item.text), "2 updates from Isoform")
        XCTAssertEqual(status.summary(language: "zh-CN", fallback: item.text), "来自 Isoform 的 2 条更新")
        var singleEntry = status
        singleEntry.entries = [status.entries[0]]
        XCTAssertEqual(singleEntry.summary(language: "zh-CN", fallback: item.text), "早报等待查看。")
        XCTAssertEqual(status.entries[0].displayText(language: "zh-CN"), "早报等待查看。")
        XCTAssertEqual(status.entries[1].displayText(language: "zh-CN"), "Capture is paused.")
        XCTAssertEqual(try JSONDecoder().decode(CachedChatItem.self,
                                                from: JSONEncoder().encode(CachedChatItem(item))).view, item)
    }

    func testProactiveCardSurvivesCacheRelaunchWithoutLosingItsActions() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let item = try decode(ChatItemView.self, proactiveJSON)
        try ChatCache(directory: directory).save(ChatHeaderView(id: "main", title: "Isoform", state: .idle), items: [item])
        let restored = try XCTUnwrap(ChatCache(directory: directory).load(chatID: "main")?.items.first?.view)
        XCTAssertEqual(restored, item)
        XCTAssertEqual(restored.proactive?.actions(language: "en").first?.id, "open")
    }

    func testFirstReadIsNotRestoredWithoutCheckingTheCurrentPairingAndPortrait() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let item = try decode(ChatItemView.self, #"""
        {"seq":1,"role":"system","kind":"proactive","text":"A checked sentence.",
         "proactive":{"momentId":"first-read:1","kind":"first_read","headline":"What I see so far",
          "body":"A checked sentence.","sentences":["A checked sentence."],"state":"pending"}}
        """#)
        XCTAssertEqual(item.proactive?.sentences, ["A checked sentence."])
        let cache = ChatCache(directory: directory)
        try cache.save(ChatHeaderView(id: "main", title: "Isoform", state: .idle), items: [item])
        XCTAssertEqual(cache.load(chatID: "main")?.items.count, 0)
    }

    func testDigestKeepsUniqueChildrenAndBoundsTheirNumber() throws {
        let children = (1...8).map { #"{"momentId":"child-\#($0)","kind":"brief_ready","state":"pending"}"# }
        let card = try decode(ChatProactiveView.self, """
        {"momentId":"digest","kind":"digest","state":"pending","items":[\(([children[0]] + children).joined(separator: ","))]}
        """)
        XCTAssertEqual(card.items.map(\.id), ["child-1", "child-2", "child-3", "child-4", "child-5"])
    }

    func testWorkPermissionActionsRequireTheirRequestDetails() throws {
        let item = try decode(ChatItemView.self, #"""
        {"seq":3,"kind":"proactive","role":"system","text":"Read the launch plan?",
         "proactive":{"momentId":"work-permission:123","kind":"decision_due","state":"pending",
          "headline":"Isoform needs your permission","body":"Read the launch plan?",
          "actionIds":["approve","decline"],"actionLabels":{"approve":"Approve","decline":"Decline"},
          "permission":{"requestId":"work-permission:123","toolName":"read_file","details":"/work/plan.md",
                        "approveOptionId":"allow_once","declineOptionId":"deny"}}}
        """#)
        var card = try XCTUnwrap(item.proactive)
        XCTAssertEqual(card.actions(language: "en").map(\.id), ["approve", "decline"])
        XCTAssertEqual(card.permission?.details, "/work/plan.md")
        XCTAssertEqual(CachedChatItem(item).view.proactive?.permission, card.permission)
        card.permission = nil
        XCTAssertTrue(card.actions(language: "en").isEmpty)
    }

    func testWorkReportIdentitiesAreNotRestrictedToPathSegments() throws {
        let card = try decode(ChatProactiveView.self,
            #"{"momentId":"handoff:work-1:blocked:Needs access to /Users/me/项目:123","kind":"work_blocked","state":"pending"}"#)
        XCTAssertEqual(card.momentID, "handoff:work-1:blocked:Needs access to /Users/me/项目:123")
    }

    func testMessageWireRetainsOfferReferencesAlongsideEditedText() throws {
        let reference = ChatOfferReference(id: "find.research", version: String(repeating: "b", count: 64))
        let request = SendChatMessageRequest(idempotencyKey: "same-retry-key", text: "Edited source text", offerReferences: [reference])
        let data = try JSONEncoder().encode(request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["text"] as? String, "Edited source text")
        XCTAssertEqual(object["idempotencyKey"] as? String, "same-retry-key")
        let references = try XCTUnwrap(object["offerReferences"] as? [[String: String]])
        XCTAssertEqual(references, [["id": reference.id, "version": reference.version]])
    }

    func testSourceDraftCopyIsAvailableInBothPhoneLanguages() {
        XCTAssertEqual(ChatProvenanceCopy.text("Discard draft", "放弃草稿", locale: Locale(identifier: "en_US")), "Discard draft")
        XCTAssertEqual(ChatProvenanceCopy.text("Discard draft", "放弃草稿", locale: Locale(identifier: "zh_CN")), "放弃草稿")
        XCTAssertEqual(ChatProvenanceCopy.sourceUnavailable(locale: Locale(identifier: "en_US")),
                       "This idea changed or its source is no longer available. Discard this draft and refresh Ideas.")
        XCTAssertEqual(ChatProvenanceCopy.sourceUnavailable(locale: Locale(identifier: "zh_CN")),
                       "这个想法已更改，或其来源已不可用。请放弃这份草稿，然后刷新想法。")
    }

    func testComposerSubmissionIsAtomicWhenProtectedCacheWriteFails() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        var rejectWrites = false
        let cache = ChatCache(directory: directory, writeData: { data, url in
            if rejectWrites { throw CocoaError(.fileWriteNoPermission) }
            try data.write(to: url, options: [.atomic, .completeFileProtection])
        })
        let reference = ChatOfferReference(id: "suggestion.notes", version: String(repeating: "a", count: 64))
        let composer = ChatComposerDraft(text: "Idea", offerReferences: [reference])
        try cache.saveComposer(composer, chatID: "main-chat")
        let queued = ChatOutgoingMessage(id: "message", text: "Idea", after: nil, sentAt: Date(), state: .sending,
                                         offerReferences: [reference])
        rejectWrites = true
        XCTAssertThrowsError(try cache.saveSubmission([queued], chatID: "main-chat", composer: ChatComposerDraft()))
        let reopened = ChatCache(directory: directory)
        XCTAssertEqual(reopened.loadComposer(chatID: "main-chat"), composer)
        XCTAssertEqual(reopened.loadOutgoing(chatID: "main-chat"), [])
        rejectWrites = false
        try cache.saveSubmission([queued], chatID: "main-chat", composer: ChatComposerDraft())
        XCTAssertEqual(reopened.loadComposer(chatID: "main-chat"), ChatComposerDraft())
        XCTAssertEqual(reopened.loadOutgoing(chatID: "main-chat")?.first?.offerReferences, [reference])
    }

    func testOrdinaryOutgoingUpdatesDoNotOverwriteSourceBoundComposer() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        let reference = ChatOfferReference(id: "find.research", version: String(repeating: "b", count: 64))
        let composer = ChatComposerDraft(text: "Edited idea", offerReferences: [reference])
        try cache.saveComposer(composer, chatID: "main-chat")
        let queued = ChatOutgoingMessage(id: "earlier", text: "Earlier message", after: nil, sentAt: Date(), state: .sending)
        try cache.saveOutgoing([queued], chatID: "main-chat")
        XCTAssertEqual(cache.loadComposer(chatID: "main-chat"), composer)
        try cache.saveOutgoing([], chatID: "main-chat")
        XCTAssertEqual(ChatCache(directory: directory).loadComposer(chatID: "main-chat"), composer)
    }

    func testTargetFormatComposerMigratesTextAndRecoveryIDsWithoutInventingProvenance() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        try cache.saveComposer(ChatComposerDraft(), chatID: "main-chat")
        let file = try XCTUnwrap(FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
            .first { $0.lastPathComponent.hasSuffix(".outgoing.json") })
        let targetFixture = #"{"chatId":"main-chat","messages":[],"composerText":"Draft saved by the companion target","recoveredDraftIDs":["previously-recovered"]}"#
        try Data(targetFixture.utf8).write(to: file, options: [.atomic, .completeFileProtection])

        let migrated = try XCTUnwrap(ChatCache(directory: directory).loadComposer(chatID: "main-chat"))
        XCTAssertEqual(migrated.text, "Draft saved by the companion target")
        XCTAssertEqual(migrated.recoveredDraftIDs, ["previously-recovered"])
        XCTAssertEqual(migrated.offerReferences, [.unverifiedLegacy], "Old text must not silently become user-authored")
        try cache.saveOutgoing([], chatID: "main-chat")
        XCTAssertEqual(ChatCache(directory: directory).loadComposer(chatID: "main-chat"), migrated)
        let rewritten = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any])
        XCTAssertNotNil(rewritten["composer"])
        XCTAssertNil(rewritten["composerText"])
    }

    func testAStoredItemKeepsItsIdentityWhenItsSequenceMoves() throws {
        let original = try decode(ChatItemView.self,
            #"{"id":"status-morning","seq":1,"role":"assistant","kind":"status","text":"One update"}"#)
        let moved = try decode(ChatItemView.self,
            #"{"id":"status-morning","seq":3,"role":"assistant","kind":"status","text":"Two updates"}"#)
        let other = try decode(ChatItemView.self,
            #"{"id":"other-row","seq":3,"role":"assistant","kind":"text","text":"Reply"}"#)

        XCTAssertEqual(original.id, moved.id, "seq is a cursor position, not the identity of a stored row")
        XCTAssertNotEqual(moved.id, other.id)
    }

    func testOlderHubsAndTransientItemsKeepSequenceIdentity() throws {
        let legacy = try decode(ChatItemView.self, #"{"seq":7,"text":"Reply"}"#)
        let empty = try decode(ChatItemView.self, #"{"id":"","seq":7,"text":"Updated reply"}"#)
        let other = try decode(ChatItemView.self, #"{"seq":8,"text":"Another reply"}"#)
        let stored = try decode(ChatItemView.self, #"{"id":"7","seq":7,"text":"Stored reply"}"#)

        XCTAssertEqual(legacy.id, empty.id)
        XCTAssertNotEqual(legacy.id, other.id)
        XCTAssertNotEqual(legacy.id, stored.id, "a wire id must not collide with a legacy sequence")
    }

    func testTheChatCachePreservesStableRowIdentity() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let original = try decode(ChatItemView.self,
            #"{"id":"status-morning","seq":1,"role":"assistant","kind":"status","text":"One update"}"#)
        let moved = try decode(ChatItemView.self,
            #"{"id":"status-morning","seq":3,"role":"assistant","kind":"status","text":"Two updates"}"#)
        let cache = ChatCache(directory: directory)

        try cache.save(ChatHeaderView(id: "main-chat", title: "Isoform", state: .idle), items: [original])

        let reopened = ChatCache(directory: directory)
        let restored = try XCTUnwrap(reopened.load(chatID: "main-chat")?.items.first?.view)
        XCTAssertEqual(restored.id, moved.id)
        XCTAssertEqual(restored.seq, 1)
    }

    func testTheChatListDecodesStatesAndKindsAndKeepsUnknownOnesAsUnknown() throws {
        let payload = try decode(ChatListPayload.self, """
        {"chats": [
          {"id": "chat-1", "title": "Pricing sync", "updatedAt": "2026-10-10T09:00:00Z", "state": "running", "kind": "chat"},
          {"id": "chat-2", "title": "Expense export", "updatedAt": "2026-10-10T08:00:00Z", "state": "waiting", "kind": "handoff"},
          {"id": "chat-3", "state": "paused-by-a-future-mac", "kind": "something-new"}
        ]}
        """)

        XCTAssertEqual(payload.chats.map(\.id), ["chat-1", "chat-2", "chat-3"])
        XCTAssertEqual(payload.chats.map(\.state), [.running, .waiting, .unknown])
        XCTAssertEqual(payload.chats.map(\.kind), [.chat, .handoff, .unknown])
        XCTAssertEqual(payload.chats[2].displayTitle, "Untitled chat")
        XCTAssertEqual(payload.chats[1].kind.label, "One-time work")
        XCTAssertNotNil(payload.chats[0].updatedDate)
    }

    func testTheChatListDecodesTheMainAndSideKinds() throws {
        let payload = try decode(ChatListPayload.self, """
        {"chats": [
          {"id": "main-1", "title": "Isoform", "updatedAt": "2026-10-14T09:00:00Z", "state": "idle", "kind": "main"},
          {"id": "side-1", "title": "Pricing tiers", "updatedAt": "2026-10-14T08:00:00Z", "state": "running", "kind": "side"}
        ]}
        """)

        XCTAssertEqual(payload.chats.map(\.kind), [.main, .side])
        XCTAssertEqual(payload.chats[0].kind.label, "Chat")
        XCTAssertEqual(payload.chats[1].kind.label, "Side chat")
    }

    func testTheTranscriptDecodesItemsInSeqOrderAndDropsAMalformedItem() throws {
        let detail = try decode(ChatDetail.self, """
        {
          "chat": {"id": "chat-1", "title": "Pricing sync", "state": "waiting"},
          "items": [
            {"seq": 3, "role": "assistant", "kind": "text", "text": "Which deck?", "at": "2026-10-10T09:01:00Z"},
            {"seq": 1, "role": "user", "kind": "text", "text": "Prepare the deck", "at": "2026-10-10T09:00:00Z"},
            "not an item",
            {"seq": 2, "role": "tool", "kind": "tool_use", "toolName": "read_file", "text": "notes.md"}
          ]
        }
        """)

        XCTAssertEqual(detail.chat?.state, .waiting)
        XCTAssertEqual(detail.items.map(\.seq), [1, 2, 3])
        XCTAssertEqual(detail.items.map(\.role), [.user, .tool, .assistant])
        XCTAssertEqual(detail.items[1].kind, .toolUse)
        XCTAssertEqual(detail.items[1].toolName, "read_file")
    }

    func testAShortenedReplyShowsANoteWithoutChangingItsWireOrCachedText() throws {
        let body = String(repeating: "a", count: 4084)
        let item = try decode(ChatItemView.self, """
        {"seq": 3, "role": "assistant", "kind": "text", "text": "\(body) [truncated]"}
        """)

        let reply = ChatReplyPresentation(item.text)

        XCTAssertEqual(item.text.utf8.count, 4096)
        XCTAssertEqual(reply.text, body)
        XCTAssertEqual(reply.truncationNote, "This reply was shortened for this phone. Read the full reply in Isoform on your computer.")
        XCTAssertEqual(item.text, body + " [truncated]")
        let cached = CachedChatItem(item).view
        XCTAssertEqual(cached.text, item.text)
        XCTAssertEqual(ChatReplyPresentation(cached.text).truncationNote, reply.truncationNote)
    }

    func testAReplyWithoutTheExactTrailingMarkerStaysUnchanged() {
        let body = String(repeating: "a", count: 4084)
        for text in ["", "A complete reply", "The [truncated] marker is in the middle.",
                     body + "[truncated]", body + " [Truncated]", body + " [truncated] "] {
            let reply = ChatReplyPresentation(text)

            XCTAssertEqual(reply.text, text)
            XCTAssertNil(reply.truncationNote)
        }
    }

    func testAShortenedReplyPreservesUnicodeAndOnlyRemovesTheFinalMarker() {
        let body = String(repeating: "明", count: 1357) + " [truncated]"
        let wire = body + " [truncated]"
        let reply = ChatReplyPresentation(wire)

        XCTAssertEqual(wire.utf8.count, 4095)
        XCTAssertLessThan(wire.count, 4093, "the cap counts UTF-8 bytes, not Swift characters")
        XCTAssertEqual(reply.text, body)
        XCTAssertNotNil(reply.truncationNote)
    }

    func testRealCutsAtEveryUTF8BoundaryShowTheNote() throws {
        for bytesBackedUp in 0...3 {
            // A four-byte scalar can cross any of the last three bytes of the Mac's payload budget.
            let prefix = String(repeating: "a", count: 4084 - bytesBackedUp)
            let source = prefix + String(repeating: "\u{10400}", count: 8)
            var payloadBytes = Data(source.utf8.prefix(4084))
            while String(data: payloadBytes, encoding: .utf8) == nil {
                payloadBytes.removeLast()
            }
            let body = try XCTUnwrap(String(data: payloadBytes, encoding: .utf8))
            let wire = body + " [truncated]"

            XCTAssertGreaterThan(source.utf8.count, 4096)
            XCTAssertEqual(wire.utf8.count, 4096 - bytesBackedUp)
            for kind in ChatReplyPresentation.Kind.allCases where kind != .toolSummary {
                XCTAssertEqual(ChatReplyPresentation(wire, kind: kind).text, prefix)
                XCTAssertNotNil(ChatReplyPresentation(wire, kind: kind).truncationNote)
            }
        }
    }

    func testRealToolSummaryCutsAtEveryUTF8BoundaryShowTheNote() throws {
        for bytesBackedUp in 0...3 {
            let prefix = String(repeating: "a", count: 228 - bytesBackedUp)
            let source = prefix + String(repeating: "\u{10400}", count: 8)
            var payloadBytes = Data(source.utf8.prefix(228))
            while String(data: payloadBytes, encoding: .utf8) == nil {
                payloadBytes.removeLast()
            }
            let body = try XCTUnwrap(String(data: payloadBytes, encoding: .utf8))
            let wire = body + " [truncated]"
            let summary = ChatReplyPresentation(wire, kind: .toolSummary)

            XCTAssertGreaterThan(source.utf8.count, 240)
            XCTAssertEqual(wire.utf8.count, 240 - bytesBackedUp)
            XCTAssertEqual(summary.text, prefix)
            XCTAssertEqual(summary.truncationNote, "This tool summary was shortened for this phone. Read the full details in Isoform on your computer.")
            XCTAssertEqual(ChatReplyPresentation(wire, kind: .toolOutput).text, wire,
                           "tool results use the 4096-byte text cap, not the tool summary cap")
            XCTAssertNil(ChatReplyPresentation(wire, kind: .toolOutput).truncationNote)
        }
    }

    func testToolSummariesPreserveLiteralMarkersOutsideTheirWireByteLimit() {
        for text in ["Literal tool input [truncated]",
                     String(repeating: "a", count: 224) + " [truncated]",
                     String(repeating: "a", count: 229) + " [truncated]",
                     String(repeating: "a", count: 4084) + " [truncated]"] {
            let summary = ChatReplyPresentation(text, kind: .toolSummary)

            XCTAssertEqual(summary.text, text)
            XCTAssertNil(summary.truncationNote)
        }
    }

    func testLiteralMarkersOutsideTheWireByteLimitStayUnchangedInEveryContext() {
        for kind in ChatReplyPresentation.Kind.allCases {
            for text in ["Here is the plan [truncated]",
                         String(repeating: "a", count: 4080) + " [truncated]",
                         String(repeating: "a", count: 4085) + " [truncated]"] {
                let presentation = ChatReplyPresentation(text, kind: kind)

                XCTAssertEqual(presentation.text, text)
                XCTAssertNil(presentation.truncationNote)
            }
        }
    }

    func testLongUserMessagesAndPermissionPromptsShowContextualNotes() {
        let body = String(repeating: "明", count: 1361)
        let wire = body + " [truncated]"
        let message = ChatReplyPresentation(wire, kind: .message)
        let permission = ChatReplyPresentation(wire, kind: .permissionPrompt)

        XCTAssertEqual(wire.utf8.count, 4095)
        XCTAssertEqual(message.text, body)
        XCTAssertEqual(message.truncationNote, "This message was shortened for this phone. Read the full message in Isoform on your computer.")
        XCTAssertEqual(permission.text, body)
        XCTAssertEqual(permission.truncationNote, "This permission request was shortened for this phone. Read the full request in Isoform on your computer.")
    }

    func testLongSystemErrorAndToolRowsShowContextualNotes() {
        let body = String(repeating: "a", count: 4084)
        let wire = body + " [truncated]"
        let cases: [(ChatReplyPresentation.Kind, String)] = [
            (.systemMessage, "This system message was shortened for this phone. Read the full message in Isoform on your computer."),
            (.error, "This error was shortened for this phone. Read the full error in Isoform on your computer."),
            (.toolOutput, "This tool output was shortened for this phone. Read the full output in Isoform on your computer."),
        ]
        for (kind, note) in cases {
            let message = ChatReplyPresentation(wire, kind: kind)

            XCTAssertEqual(message.text, body)
            XCTAssertEqual(message.truncationNote, note)
        }
    }

    @MainActor
    func testAShortenedReplyRendersItsNote() throws {
        let body = String(String(repeating: "Here are the next steps. ", count: 200).prefix(4084))
        let wire = body + " [truncated]"
        XCTAssertEqual(wire.utf8.count, 4096)
        XCTAssertNotNil(ChatReplyPresentation(wire).truncationNote)
        let renderer = ImageRenderer(content: ChatAssistantReply(text: wire)
            .padding(YansuTheme.gutter)
            .frame(width: 393)
            .background(YansuTheme.background)
            .environment(\.colorScheme, .light))
        renderer.scale = 2
        let image = try XCTUnwrap(renderer.uiImage)
        XCTAssertGreaterThan(image.size.height, 0)

        let attachment = XCTAttachment(image: image)
        attachment.name = "Chat shortened reply"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    func testAPermissionRequestCarriesItsOptionsAndIsActionable() throws {
        let detail = try decode(ChatDetail.self, """
        {"chat": {"id": "chat-1", "title": "T", "state": "waiting"}, "items": [
          {"seq": 4, "role": "system", "kind": "permission_request", "text": "Read the design notes?",
           "permission": {"requestId": "req-9", "options": [{"id": "allow", "label": "Allow"}, {"id": "deny", "label": "Deny"}]}},
          {"seq": 5, "role": "system", "kind": "permission_request", "text": "No options sent"}
        ]}
        """)

        let prompt = try XCTUnwrap(detail.pendingPermission)
        XCTAssertTrue(prompt.isPermissionPrompt)
        XCTAssertEqual(prompt.permission?.requestID, "req-9")
        XCTAssertEqual(prompt.permission?.options.map(\.id), ["allow", "deny"])
        XCTAssertTrue(detail.items.isEmpty, "a permission prompt is never a stored row")
    }

    func testAPageCarriesItsCursorHasMoreAndItsPendingPermissionApart() throws {
        let detail = try decode(ChatDetail.self, """
        {
          "chat": {"id": "chat-1", "title": "T", "state": "waiting"},
          "items": [
            {"seq": 2, "role": "assistant", "kind": "text", "text": "Which deck?"},
            {"seq": 3, "role": "system", "kind": "permission_request", "text": "Read the notes?",
             "permission": {"requestId": "req-7", "options": [{"id": "allow", "label": "Allow"}]}}
          ],
          "cursor": 2,
          "hasMore": false
        }
        """)

        XCTAssertEqual(detail.items.map(\.seq), [2])
        XCTAssertEqual(detail.cursor, 2, "the cursor is the Mac's, and the transient prompt does not move it")
        XCTAssertFalse(detail.hasMore)
        XCTAssertEqual(detail.pendingPermission?.seq, 3)
        XCTAssertEqual(detail.pendingPermission?.permission?.requestID, "req-7")
    }

    func testACursorThatPassesHiddenRowsStillMovesThePage() throws {
        let detail = try decode(ChatDetail.self, #"{"chat": {"id": "chat-1", "title": "T", "state": "running"}, "items": [], "cursor": 9, "hasMore": true}"#)

        XCTAssertTrue(detail.items.isEmpty)
        XCTAssertEqual(detail.cursor, 9)
        XCTAssertTrue(detail.hasMore)
        XCTAssertNil(detail.pendingPermission)
    }

    func testAPermissionRowWithoutOptionsIsNeitherStoredNorShownAsAPrompt() throws {
        let detail = try decode(ChatDetail.self, #"{"chat": {"id": "chat-1", "title": "T", "state": "waiting"}, "items": [{"seq": 4, "role": "system", "kind": "permission_request", "text": "No options"}], "cursor": 4, "hasMore": false}"#)

        XCTAssertTrue(detail.items.isEmpty)
        XCTAssertNil(detail.pendingPermission)
    }

    func testAMissingCursorFallsBackToTheLastStoredSeq() throws {
        let detail = try decode(ChatDetail.self, #"{"chat": {"id": "chat-1", "title": "T", "state": "idle"}, "items": [{"seq": 5, "role": "user", "kind": "text", "text": "Hi"}]}"#)

        XCTAssertEqual(detail.cursor, 5)
        XCTAssertFalse(detail.hasMore)
    }

    func testAnAfterReadWithNoItemsIsAnEmptyPage() throws {
        let detail = try decode(ChatDetail.self, #"{"chat": {"id": "chat-1", "title": "T", "state": "idle"}, "items": []}"#)

        XCTAssertTrue(detail.items.isEmpty)
        XCTAssertEqual(detail.chat?.state, .idle)
    }

    func testATurnEventDecodesWithItsChatAndState() throws {
        let event = try decode(MobileChatTurnEvent.self, """
        {"event": "chat_turn", "chatId": "chat-1", "turnId": "turn-4", "state": "permission", "seq": 12, "at": "2026-10-10T09:05:00Z"}
        """)

        XCTAssertEqual(event.event, "chat_turn")
        XCTAssertEqual(event.chatId, "chat-1")
        XCTAssertEqual(event.turnId, "turn-4")
        XCTAssertEqual(event.state, "permission")
        XCTAssertEqual(event.seq, 12)
    }

    func testAPollEventThatIsNotAChatTurnDecodesWithItsOwnName() throws {
        let event = try decode(MobileChatTurnEvent.self, #"{"event": "needs_you", "count": 2, "at": "2026-10-10T09:05:00Z"}"#)

        XCTAssertNotEqual(event.event, "chat_turn")
    }

    func testChatStatesHaveCalmLabels() {
        XCTAssertEqual(ChatState.running.label, "Working")
        XCTAssertEqual(ChatState.waiting.label, "Waiting on you")
        XCTAssertEqual(ChatState.idle.label, "Ready")
        XCTAssertTrue(ChatState.running.isBusy)
        XCTAssertFalse(ChatState.waiting.isBusy)
    }

    func testTheChatRoutesBuildTheExpectedPathsAndQueries() {
        XCTAssertEqual(HubRoute.chats(limit: 20).path, "/chats")
        XCTAssertEqual(HubRoute.chats(limit: 20).query, "limit=20")
        XCTAssertEqual(HubRoute.chats(limit: 900).query, "limit=50", "the limit is clamped to the Mac's maximum page")
        XCTAssertEqual(HubRoute.chat(id: "chat-1", after: nil, limit: 50).path, "/chats/chat-1")
        XCTAssertEqual(HubRoute.chat(id: "chat-1", after: nil, limit: 50).query, "limit=50&firstRead=1")
        XCTAssertEqual(HubRoute.chat(id: "chat-1", after: 12, limit: 50).query, "after=12&limit=50&firstRead=1")
        XCTAssertEqual(HubRoute.chat(id: "chat-1", after: 0, limit: 50).query, "after=0&limit=50&firstRead=1", "after=0 is the start of the chat, not the newest page")
        XCTAssertEqual(HubRoute.chat(id: "chat-1", after: 12, limit: 50).method, "GET")
    }

    func testTheCachedTranscriptKeepsOnlyTheNewestFiftyItemsAndRoundTrips() throws {
        let directory = try makeTemporaryDirectory()
        let cache = ChatCache(directory: directory.appendingPathComponent("chats", isDirectory: true),
                              now: { Date(timeIntervalSince1970: 1_800_000_000) })
        let items = (1...60).map { seq in
            ChatItemView(seq: seq, role: seq % 2 == 0 ? .assistant : .user, kind: .text, text: "Message \(seq)",
                         toolName: "", at: "", permission: nil)
        }

        try cache.save(ChatHeaderView(id: "chat-1", title: "Pricing sync", state: .running), items: items.shuffled())

        let loaded = try XCTUnwrap(cache.load(chatID: "chat-1"))
        XCTAssertEqual(loaded.items.count, ChatCache.itemLimit)
        XCTAssertEqual(loaded.items.first?.seq, 11)
        XCTAssertEqual(loaded.items.last?.seq, 60)
        XCTAssertEqual(loaded.state, .running)
        XCTAssertEqual(loaded.items.last?.view.text, "Message 60")
        XCTAssertEqual(loaded.items.last?.view.role, .assistant)
    }

    func testACachedItemCannotRestorePermissionAuthority() throws {
        let original = ChatItemView(seq: 7, role: .system, kind: .permissionRequest, text: "Allow?", toolName: "",
                                    at: "", permission: ChatPermissionView(requestID: "req-2",
                                                                           options: [ChatPermissionOption(id: "allow", label: "Allow")]))

        let restored = CachedChatItem(original).view

        XCTAssertNil(restored.permission)
        XCTAssertEqual(restored.kind, .permissionRequest)
    }

    func testMainChatIdentitySurvivesRelaunchWithoutInventingAnEmptyTranscript() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        let summary = ChatSummaryView(id: "main-1", title: "Isoform", updatedAt: "2026-10-16T09:00:00Z",
                                      state: .idle, kind: .main)

        try cache.rememberMainChat(summary)

        let reopened = ChatCache(directory: directory)
        XCTAssertEqual(reopened.loadMainChatSummary(), summary)
        XCTAssertNil(reopened.loadMainChat(), "a remembered identity must keep the loading skeleton until a transcript exists")

        try cache.save(ChatHeaderView(id: summary.id, title: summary.title, state: .idle), items: [])
        XCTAssertEqual(reopened.loadMainChat()?.items, [], "a successfully cached empty chat is distinct from a missing transcript")
    }

    func testMainChatSnapshotIsDiscoverableBoundedAndProtectedAfterRelaunch() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        try cache.rememberMainChat(ChatSummaryView(id: "main-1", title: "Isoform", updatedAt: "",
                                                   state: .idle, kind: .main))
        var items = (1...65).map { seq in
            ChatItemView(seq: seq, role: .assistant, kind: .text, text: "Message \(seq)",
                         toolName: "", at: "", permission: nil)
        }
        items.append(ChatItemView(seq: 66, role: .system, kind: .permissionRequest, text: "Private permission",
                                  toolName: "", at: "", permission: ChatPermissionView(requestID: "private-request",
                                  options: [ChatPermissionOption(id: "allow", label: "Allow")])))

        try cache.save(ChatHeaderView(id: "main-1", title: "Updated main", state: .waiting), items: items)
        let reopened = ChatCache(directory: directory)
        let snapshot = try XCTUnwrap(reopened.loadMainChat())

        XCTAssertEqual(snapshot.title, "Updated main")
        XCTAssertEqual(snapshot.items.map(\.seq), Array(16...65))
        XCTAssertFalse(snapshot.items.contains { $0.kind == .permissionRequest })
        let files = try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
        XCTAssertEqual(files.count, 2, "only the bounded transcript and main-chat marker are stored")
        for file in files {
            let data = try Data(contentsOf: file)
            XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("private-request"))
            XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("Private permission"))
            // Simulator does not expose device file protection attributes; the write uses complete protection.
            #if !targetEnvironment(simulator)
            let attributes = try FileManager.default.attributesOfItem(atPath: file.path)
            XCTAssertEqual(attributes[.protectionKey] as? FileProtectionType, .complete)
            #endif
        }
    }

    func testSideChatsAndUnsafeIDsCannotReplaceTheRememberedMainChat() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        let main = ChatSummaryView(id: "main-1", title: "Isoform", updatedAt: "", state: .idle, kind: .main)
        try cache.rememberMainChat(main)
        try cache.rememberMainChat(ChatSummaryView(id: "side-1", title: "Side chat", updatedAt: "",
                                                   state: .idle, kind: .side))
        try cache.rememberMainChat(ChatSummaryView(id: "../other", title: "Invalid", updatedAt: "",
                                                   state: .idle, kind: .main))

        XCTAssertEqual(cache.loadMainChatSummary(), main)
    }

    func testLegacyCachePermissionsAreDiscardedAndProtectionUpgradedOnRead() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        let header = ChatHeaderView(id: "main-1", title: "Isoform", state: .waiting)
        try cache.save(header, items: [])
        let file = try XCTUnwrap(FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil).first)
        let legacy = """
        {"id":"main-1","title":"Isoform","state":"waiting","storedAt":"2026-10-16T09:00:00Z","items":[
          {"seq":1,"role":"assistant","kind":"text","text":"Previous reply","toolName":"","at":"",
           "permissionRequestID":"old-request","permissionOptions":[{"id":"allow","label":"Allow"}]},
          {"seq":2,"role":"system","kind":"permission_request","text":"Old permission","toolName":"","at":"",
           "permissionRequestID":"old-request","permissionOptions":[{"id":"allow","label":"Allow"}]}
        ]}
        """
        try Data(legacy.utf8).write(to: file, options: .completeFileProtectionUntilFirstUserAuthentication)

        let snapshot = try XCTUnwrap(cache.load(chatID: header.id))

        XCTAssertEqual(snapshot.items.map(\.seq), [1])
        XCTAssertNil(snapshot.items[0].permissionRequestID)
        XCTAssertTrue(snapshot.items[0].permissionOptions.isEmpty)
        XCTAssertNil(snapshot.items[0].view.permission)
        XCTAssertEqual(snapshot.storedAt, RelayTime.parse("2026-10-16T09:00:00Z"), "upgrading protection must not make an old snapshot look fresh")
        // Simulator does not expose device file protection attributes; loading requests complete protection.
        #if !targetEnvironment(simulator)
        let attributes = try FileManager.default.attributesOfItem(atPath: file.path)
        XCTAssertEqual(attributes[.protectionKey] as? FileProtectionType, .complete)
        #endif
    }

    func testAnUnknownChatIdInTheCacheIsNotReturnedForAnotherChat() throws {
        let directory = try makeTemporaryDirectory()
        let cache = ChatCache(directory: directory.appendingPathComponent("chats", isDirectory: true))
        try cache.save(ChatHeaderView(id: "chat-1", title: "One", state: .idle), items: [])

        XCTAssertNil(cache.load(chatID: "chat-2"))
        XCTAssertNotNil(cache.load(chatID: "chat-1"))
    }

    func testLegacyOutgoingIsDiscoveredAndMigratedBeforeItsChatOpens() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        let message = ChatOutgoingMessage(id: "legacy-message", text: "Keep this queued message", after: 4,
                                          sentAt: Date(timeIntervalSince1970: 1_800_000_000), state: .sending)
        try cache.save(ChatHeaderView(id: "old-side-chat", title: "Earlier chat", state: .idle), items: [])
        let file = try writeLegacyOutgoing([message], chatID: "old-side-chat", cache: cache, directory: directory)

        let relaunched = ChatCache(directory: directory)
        XCTAssertEqual(relaunched.outgoingChatIDs(), ["old-side-chat"])
        XCTAssertEqual(relaunched.loadOutgoing(chatID: "old-side-chat"), [message])
        let migrated = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any])
        XCTAssertEqual(migrated["chatId"] as? String, "old-side-chat")

        for other in try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
            where other != file {
            try FileManager.default.removeItem(at: other)
        }
        XCTAssertEqual(ChatCache(directory: directory).outgoingChatIDs(), ["old-side-chat"],
                       "the migration must survive another launch without its transcript")
    }

    func testLegacyOutgoingWithoutATranscriptUsesItsPendingJournalTarget() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        let message = ChatOutgoingMessage(id: "legacy-message", text: "The chat has not loaded", after: nil,
                                          sentAt: Date(timeIntervalSince1970: 1_800_000_000), state: .sending)
        let file = try writeLegacyOutgoing([message], chatID: "unread-chat", cache: cache, directory: directory)
        let before = try Data(contentsOf: file)

        XCTAssertTrue(cache.outgoingChatIDs(knownChatIDs: ["other-chat", "../unsafe"]).isEmpty)
        XCTAssertEqual(try Data(contentsOf: file), before, "an unmatched legacy file must stay intact")
        XCTAssertEqual(cache.outgoingChatIDs(knownChatIDs: ["unread-chat"]), ["unread-chat"])
        XCTAssertEqual(ChatCache(directory: directory).outgoingChatIDs(), ["unread-chat"])
        XCTAssertEqual(ChatCache(directory: directory).loadOutgoing(chatID: "unread-chat"), [message])
    }

    func testLegacyOutgoingCanUseTheMainChatMarkerWithoutATranscript() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        try cache.rememberMainChat(ChatSummaryView(id: "main-chat", title: "Isoform", updatedAt: "",
                                                   state: .idle, kind: .main))
        let message = ChatOutgoingMessage(id: "legacy-message", text: "Waiting for the Mac", after: nil,
                                          sentAt: Date(timeIntervalSince1970: 1_800_000_000), state: .sending)
        _ = try writeLegacyOutgoing([message], chatID: "main-chat", cache: cache, directory: directory)

        XCTAssertEqual(ChatCache(directory: directory).outgoingChatIDs(), ["main-chat"])
        XCTAssertEqual(cache.loadOutgoing(chatID: "main-chat"), [message])
    }

    func testLegacyOutgoingDoesNotTrustATranscriptStoredUnderAnotherChatsFilename() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = ChatCache(directory: directory)
        try cache.save(ChatHeaderView(id: "other-chat", title: "Other", state: .idle), items: [])
        let transcript = try XCTUnwrap(FileManager.default.contentsOfDirectory(at: directory,
                                                                               includingPropertiesForKeys: nil).first)
        try Data("{\"id\":\"unread-chat\"}".utf8).write(to: transcript)
        let message = ChatOutgoingMessage(id: "legacy-message", text: "Keep the original chat", after: nil,
                                          sentAt: Date(timeIntervalSince1970: 1_800_000_000), state: .sending)
        let file = try writeLegacyOutgoing([message], chatID: "unread-chat", cache: cache, directory: directory)
        let before = try Data(contentsOf: file)

        XCTAssertTrue(cache.outgoingChatIDs().isEmpty)
        XCTAssertEqual(try Data(contentsOf: file), before)
    }

    private func writeLegacyOutgoing(_ messages: [ChatOutgoingMessage], chatID: String, cache: ChatCache,
                                     directory: URL) throws -> URL {
        try cache.saveOutgoing(messages, chatID: chatID)
        let file = try XCTUnwrap(FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
            .first { $0.lastPathComponent.hasSuffix(".outgoing.json") })
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        try encoder.encode(messages).write(to: file, options: [.atomic, .completeFileProtection])
        return file
    }

    func testClearingTheCacheRemovesEveryChat() throws {
        let directory = try makeTemporaryDirectory()
        let cache = ChatCache(directory: directory.appendingPathComponent("chats", isDirectory: true))
        try cache.save(ChatHeaderView(id: "chat-1", title: "One", state: .idle), items: [])
        try cache.rememberMainChat(ChatSummaryView(id: "chat-1", title: "One", updatedAt: "", state: .idle, kind: .main))

        try cache.removeAll()

        XCTAssertNil(cache.load(chatID: "chat-1"))
        XCTAssertNil(cache.loadMainChatSummary())
        XCTAssertNil(cache.loadMainChat())
    }

    func testFailedRemovalHidesChatsAndRefusesWritesUntilCleanupSucceeds() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        var blocked = false
        let cache = ChatCache(directory: directory, removeItem: { url in
            if blocked { throw YansuError.message("Injected removal failure") }
            try FileManager.default.removeItem(at: url)
        })
        let old = ChatHeaderView(id: "old-chat", title: "Old Mac", state: .idle)
        let new = ChatHeaderView(id: "new-chat", title: "New Mac", state: .idle)
        try cache.save(old, items: [])
        try cache.rememberMainChat(ChatSummaryView(id: old.id, title: old.title, updatedAt: "", state: .idle, kind: .main))
        blocked = true

        XCTAssertThrowsError(try cache.removeAll())
        XCTAssertNil(cache.load(chatID: old.id))
        XCTAssertNil(cache.loadMainChatSummary())
        XCTAssertNil(cache.loadMainChat())
        XCTAssertThrowsError(try cache.save(new, items: []))
        XCTAssertThrowsError(try cache.rememberMainChat(ChatSummaryView(id: new.id, title: new.title, updatedAt: "", state: .idle, kind: .main)))

        blocked = false
        try cache.save(new, items: [])
        XCTAssertNil(cache.load(chatID: old.id))
        XCTAssertEqual(cache.load(chatID: new.id)?.title, "New Mac")
    }
}
