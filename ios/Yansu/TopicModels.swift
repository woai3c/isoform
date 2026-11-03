import Foundation

// RC 1 phone topics (mobile-protocol/PROTOCOL.md section 15.8). Every field decodes tolerantly, as chat does: a
// missing value becomes empty, and a topic or row the phone cannot use is dropped rather than failing the page.

/// One of "Your topics": a work project or a followed topic, in the computer's order.
struct MobileTopic: Codable, Equatable, Sendable, Identifiable, Hashable {
    var id: String
    /// `project` or `following`.
    var kind: String
    var name: String
    /// `waiting_on_you`, `waiting_on_them`, `today`, `new_stories` or `moving`.
    var status: String
    var latest: MobileTopicMoment? = nil
    var next: MobileTopicMoment? = nil
    var peopleLine: MobileTopicPeople? = nil
    var why: MobileTopicWhy? = nil
    var newStories: Int = 0

    var isFollowing: Bool { kind == "following" }
}

extension MobileTopic {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        id = container.chatText("id")
        name = container.chatText("name").trimmingCharacters(in: .whitespacesAndNewlines)
        // The id goes into a route path; a topic the phone cannot open is left out.
        guard Identifiers.isSafePathID(id), !name.isEmpty else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "Unusable topic"))
        }
        kind = container.chatText("kind")
        status = container.chatText("status")
        latest = try? container.decodeIfPresent(MobileTopicMoment.self, forKey: ChatCodingKeys("latest"))
        next = try? container.decodeIfPresent(MobileTopicMoment.self, forKey: ChatCodingKeys("next"))
        peopleLine = try? container.decodeIfPresent(MobileTopicPeople.self, forKey: ChatCodingKeys("peopleLine"))
        why = try? container.decodeIfPresent(MobileTopicWhy.self, forKey: ChatCodingKeys("why"))
        newStories = max(0, container.chatInt("newStories"))
    }
}

/// The topic's latest or next moment: `{"text","at"}`, `at` optional.
struct MobileTopicMoment: Codable, Equatable, Sendable, Hashable {
    var text: String
    var at: String? = nil

    var atDate: Date? { at.flatMap(RelayTime.parse) }
}

extension MobileTopicMoment {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        text = container.chatText("text").trimmingCharacters(in: .whitespacesAndNewlines)
        at = container.chatOptionalText("at")
    }
}

/// At most three names and how many people in all.
struct MobileTopicPeople: Codable, Equatable, Sendable, Hashable {
    var names: [String]
    var count: Int
}

extension MobileTopicPeople {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        names = container.chatItems(String.self, "names").filter { !$0.isEmpty }
        count = max(container.chatInt("count"), names.count)
    }
}

/// A followed topic's existing reason.
struct MobileTopicWhy: Codable, Equatable, Sendable, Hashable {
    var lastSeenAt: String? = nil
    var interestWhy: String? = nil
}

extension MobileTopicWhy {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        lastSeenAt = container.chatOptionalText("lastSeenAt")
        interestWhy = container.chatOptionalText("interestWhy")
    }
}

struct MobileTopicList: Decodable, Equatable, Sendable {
    var topics: [MobileTopic]
    var refreshing = false
    var hasTopics = false
}

extension MobileTopicList {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        var seen = Set<String>()
        topics = container.chatItems(MobileTopic.self, "topics").filter { seen.insert($0.id).inserted }
        refreshing = (try? container.decodeIfPresent(Bool.self, forKey: ChatCodingKeys("refreshing"))) ?? false
        hasTopics = (try? container.decodeIfPresent(Bool.self, forKey: ChatCodingKeys("hasTopics"))) ?? !topics.isEmpty
    }
}

/// One row of a topic: an Activity event, or a main-chat message in the ordinary phone projection (15.4).
struct MobileTopicItem: Decodable, Equatable, Sendable, Identifiable {
    var id: String
    /// `event` or `message`.
    var kind: String
    var at: Date?
    /// A stable role code for an event; `user` is the person using the phone.
    var actor: String = ""
    var text: String = ""
    var message: ChatItemView? = nil
}

extension MobileTopicItem {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        id = container.chatText("id")
        guard !id.isEmpty else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "Missing row id"))
        }
        kind = container.chatText("kind")
        // Unix milliseconds on topic rows, unlike the RFC 3339 `at` of chat items.
        let milliseconds = try? container.decodeIfPresent(Double.self, forKey: ChatCodingKeys("at"))
        at = milliseconds.map { Date(timeIntervalSince1970: $0 / 1000) }
        actor = container.chatText("actor")
        text = container.chatText("text")
        message = try? container.decodeIfPresent(ChatItemView.self, forKey: ChatCodingKeys("message"))
    }
}

/// One page of a topic, oldest first. Older pages go before it; `nextCursor` continues strictly before this page.
struct MobileTopicPage: Decodable, Equatable, Sendable {
    var sessionId: String
    var topicId: String
    var items: [MobileTopicItem]
    var hasMore: Bool
    var nextCursor: String?
}

extension MobileTopicPage {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        sessionId = container.chatText("sessionId")
        topicId = container.chatText("topicId")
        items = container.chatItems(MobileTopicItem.self, "items")
        hasMore = (try? container.decodeIfPresent(Bool.self, forKey: ChatCodingKeys("hasMore"))) ?? false
        nextCursor = container.chatOptionalText("nextCursor").flatMap { $0.isEmpty ? nil : $0 }
    }
}

struct TopicMessageRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let text: String
}
