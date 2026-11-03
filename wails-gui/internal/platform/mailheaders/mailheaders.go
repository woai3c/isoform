// Package mailheaders reads the headers of recent mail from Mail.app over
// Apple Events: for the inboxes and the sent mailbox, each message's
// message-id, sender, recipients, subject, date, and read, flagged and
// replied state. Never a body: the script asks for none of the content
// properties, and nothing here can.
//
// The script is JavaScript for Automation run by /usr/bin/osascript. It
// addresses the running Mail by its process id and asks whether it still
// runs before each mailbox, so a read never opens Mail, even when Mail quits
// in the middle of one. The caller checks the Automation permission for Mail
// without asking (AEDeterminePermissionToAutomateTarget, askUser false)
// before running it, so a read never shows the consent prompt either.
package mailheaders

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// BundleID is Mail's bundle identifier.
const BundleID = "com.apple.mail"

// Header is one message's headers as the script returned them.
type Header struct {
	MessageID string
	FromName  string
	FromEmail string
	Subject   string
	At        time.Time
	Read      bool
	Flagged   bool
	Replied   bool
	To        []string
	CC        []string
}

// Snapshot is what one read returned.
type Snapshot struct {
	// Running is false when Mail was not running; nothing else is set then.
	Running bool
	// Me are the user's own addresses, from Mail's accounts, lowercased.
	Me    []string
	Inbox []Header
	Sent  []Header
	// Dropped counts headers without an ID or date. Other consumers may use
	// the remaining rows, but an absence of a reply cannot be inferred from
	// a read that discarded messages.
	Dropped int
	// Complete requires the script's property-array checks and parseable
	// address fields. A partial read can still be useful to other consumers,
	// but cannot prove absence of a reply.
	Complete bool
}

type rawHeader struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	Subject string   `json:"subject"`
	At      float64  `json:"at"`
	Read    bool     `json:"read"`
	Flagged bool     `json:"flagged"`
	Replied bool     `json:"replied"`
	To      []string `json:"to"`
	CC      []string `json:"cc"`
}

type rawSnapshot struct {
	Running  bool        `json:"running"`
	Complete bool        `json:"complete"`
	Me       []string    `json:"me"`
	Inbox    []rawHeader `json:"inbox"`
	Sent     []rawHeader `json:"sent"`
}

// Parse maps the script's JSON output to a snapshot. A message without a
// message-id or a date is dropped; addresses are lowercased and
// de-duplicated; a sender that cannot be parsed keeps no address.
func Parse(output []byte) (Snapshot, error) {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return Snapshot{}, fmt.Errorf("mail headers: empty output")
	}
	var raw rawSnapshot
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return Snapshot{}, fmt.Errorf("mail headers: %w", err)
	}
	snap := Snapshot{Running: raw.Running}
	if !raw.Running {
		return snap, nil
	}
	snap.Me = cleanAddresses(raw.Me)
	snap.Inbox = headers(raw.Inbox)
	snap.Sent = headers(raw.Sent)
	snap.Complete = raw.Complete && raw.Inbox != nil && raw.Sent != nil &&
		validAddresses(raw.Me) && validHeaderAddresses(raw.Inbox, true) &&
		validHeaderAddresses(raw.Sent, len(snap.Me) == 0)
	snap.Dropped = len(raw.Inbox) - len(snap.Inbox) + len(raw.Sent) - len(snap.Sent)
	return snap, nil
}

// Keep usable headers for existing consumers, but do not let a silently
// discarded address establish source evidence for a future promise. Sent
// senders are omitted by the script when account addresses are available.
func validHeaderAddresses(headers []rawHeader, requireSender bool) bool {
	for _, header := range headers {
		if header.To == nil || header.CC == nil || !validAddresses(header.To) || !validAddresses(header.CC) {
			return false
		}
		if requireSender || header.From != "" {
			if _, address := ParseSender(header.From); address == "" {
				return false
			}
		}
	}
	return true
}

func validAddresses(addresses []string) bool {
	for _, raw := range addresses {
		if _, address := ParseSender(raw); address == "" {
			return false
		}
	}
	return true
}

func headers(raws []rawHeader) []Header {
	out := make([]Header, 0, len(raws))
	for _, raw := range raws {
		id := strings.TrimSpace(raw.ID)
		if id == "" || raw.At <= 0 {
			continue
		}
		name, email := ParseSender(raw.From)
		out = append(out, Header{
			MessageID: id,
			FromName:  name,
			FromEmail: email,
			Subject:   strings.Join(strings.Fields(strings.ToValidUTF8(raw.Subject, "")), " "),
			At:        time.UnixMilli(int64(raw.At)),
			Read:      raw.Read,
			Flagged:   raw.Flagged,
			Replied:   raw.Replied,
			To:        cleanAddresses(raw.To),
			CC:        cleanAddresses(raw.CC),
		})
	}
	return out
}

// ParseSender splits Mail's sender text, "Sam Lee <sam@example.com>" or a
// bare address, into a name and a lowercased address.
func ParseSender(from string) (string, string) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", ""
	}
	if addr, err := mail.ParseAddress(from); err == nil {
		return strings.TrimSpace(addr.Name), strings.ToLower(addr.Address)
	}
	if lt := strings.LastIndex(from, "<"); lt >= 0 {
		if gt := strings.LastIndex(from, ">"); gt > lt {
			email := strings.ToLower(strings.TrimSpace(from[lt+1 : gt]))
			if validAddress(email) {
				return strings.Trim(strings.TrimSpace(from[:lt]), `"`), email
			}
		}
	}
	if validAddress(strings.ToLower(from)) {
		return "", strings.ToLower(from)
	}
	return "", ""
}

var addressPattern = regexp.MustCompile(`^[^\s@<>"]+@[^\s@<>"]+\.[^\s@<>"]+$`)

func validAddress(s string) bool {
	return addressPattern.MatchString(s)
}

func cleanAddresses(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		_, email := ParseSender(raw)
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		out = append(out, email)
	}
	return out
}

// subjectPrefix matches the reply and forward markers mail clients put in
// front of a subject, in English, Chinese and the common European forms.
var subjectPrefix = regexp.MustCompile(`(?i)^\s*(?:(?:re|fw|fwd|aw|wg|sv|vs|tr|rv|ref|antw|odp)\s*(?:\[\d+\])?\s*[:：]|回复\s*[:：]|答复\s*[:：]|转发\s*[:：]|\[[^\]]*\]\s*)\s*`)

// NormalizeSubject strips the reply and forward markers and the whitespace
// around them, so the messages of one conversation share a subject.
func NormalizeSubject(subject string) string {
	subject = strings.Join(strings.Fields(subject), " ")
	for {
		next := subjectPrefix.ReplaceAllString(subject, "")
		if next == subject {
			break
		}
		subject = next
	}
	return strings.TrimSpace(subject)
}

// automatedSubject matches the subjects a program writes on a person's
// behalf, in English and Chinese: calendar invitations and their answers
// and read receipts, which are followed by a colon, and automatic replies
// and delivery reports, which may stand alone.
var automatedSubject = regexp.MustCompile(`(?i)^\s*(?:` +
	`(?:accepted|declined|tentative|tentatively accepted|canceled|cancelled|` +
	`invitation|updated invitation|canceled event|cancelled event|new event|updated event|` +
	`auto|read|not read|delivered|` +
	`(?:已接受|已拒绝|暂定接受|暂定|已取消|已更新)(?:此)?(?:邀请)?|邀请|更新的邀请|已读|未读)` +
	`(?:\s+with note)?\s*[:：]` +
	`|(?:automatic reply|auto[- ]?reply|autoreply|out of (?:the )?office|read receipt|return receipt|` +
	`undeliverable|undelivered mail|delivery status notification|delivery failure|mail delivery failed|returned mail)\b` +
	`|自动回复|自动答复|无法送达|系统退信|退信)`)

// automatedSender matches the local part of an address no person reads.
var automatedSender = regexp.MustCompile(`(?i)^(?:.*[-_.+])?(?:no[-_.]?reply|do[-_.]?not[-_.]?reply|` +
	`notifications?|notify|mailer[-_.]?daemon|postmaster|bounces?)(?:[-_.+].*)?$`)

// Automated reports a message a program wrote rather than a person, by its
// subject (an invitation or its answer, an automatic reply, a receipt, a
// delivery report) or its sender (no-reply, notifications, mailer-daemon,
// postmaster). Nobody is waiting on a reply to one. Reply and forward
// markers in front of the subject are ignored.
func Automated(subject, fromEmail string) bool {
	if automatedSubject.MatchString(NormalizeSubject(subject)) {
		return true
	}
	local, _, ok := strings.Cut(strings.TrimSpace(fromEmail), "@")
	return ok && automatedSender.MatchString(local)
}

// Script is the JavaScript for Automation that reads the headers of the
// messages received (inboxes) and sent (sent mailbox) in the last days from
// the Mail process with this id. It reads Mail's accounts' addresses so the
// caller can tell which recipients are the user. It never touches a
// message's content.
//
// Mail is addressed by process id, never by name or bundle id: once that
// process has quit, every Apple event fails with procNotFound (-600)
// instead of starting Mail again. The script also asks whether Mail still
// runs before each mailbox and answers "not running" when it does not.
//
// Each property it asks for makes Mail go over the whole mailbox again, so
// the sent mailbox asks only for what it is used for: no read, flagged or
// replied state, and its sender only when Mail gave no account addresses.
func Script(days, pid int) string {
	if pid <= 0 {
		return `function run() { return JSON.stringify({running: false}); }`
	}
	if days <= 0 {
		days = 7
	}
	return fmt.Sprintf(scriptTemplate, pid, days)
}

const scriptTemplate = `function run() {
	var mail = Application(%d);
	if (!mail.running()) {
		return JSON.stringify({running: false});
	}
	var since = new Date(Date.now() - %d * 86400000);
	var out = {running: true, complete: true, me: [], inbox: [], sent: []};
	try {
		var addresses = mail.accounts.emailAddresses();
		for (var i = 0; i < addresses.length; i++) {
			out.me = out.me.concat(addresses[i] || []);
		}
	} catch (e) { out.complete = false; }
	function read(box, dateProp, received) {
		if (!mail.running()) {
			return null;
		}
		var filter = {};
		filter[dateProp] = {_greaterThan: since};
		var msgs = box.messages.whose(filter);
		var ids = msgs.messageId();
		if (!Array.isArray(ids)) {
			out.complete = false;
			return [];
		}
		if (ids.length === 0) {
			return [];
		}
		var subjects = msgs.subject();
		var dates = msgs[dateProp]();
		var to = msgs.toRecipients.address();
		var cc = msgs.ccRecipients.address();
		var senders = received || out.me.length === 0 ? msgs.sender() : [];
		var reads = received ? msgs.readStatus() : [];
		var flags = received ? msgs.flaggedStatus() : [];
		var replied = received ? msgs.wasRepliedTo() : [];
		var properties = [subjects, dates, to, cc];
		if (received || out.me.length === 0) properties.push(senders);
		if (received) properties.push(reads, flags, replied);
		for (var p = 0; p < properties.length; p++) {
			if (!Array.isArray(properties[p]) || properties[p].length !== ids.length) {
				out.complete = false;
			}
		}
		var list = [];
		for (var i = 0; i < ids.length; i++) {
			if (!Array.isArray(to[i]) || !Array.isArray(cc[i])) out.complete = false;
			list.push({
				id: ids[i] || '',
				from: senders[i] || '',
				subject: subjects[i] || '',
				at: dates[i] ? dates[i].getTime() : 0,
				read: !!reads[i],
				flagged: !!flags[i],
				replied: !!replied[i],
				to: to[i] || [],
				cc: cc[i] || []
			});
		}
		return list;
	}
	out.inbox = read(mail.inbox, 'dateReceived', true);
	if (out.inbox === null) {
		return JSON.stringify({running: false});
	}
	out.sent = read(mail.sentMailbox, 'dateSent', false);
	if (out.sent === null) {
		return JSON.stringify({running: false});
	}
	return JSON.stringify(out);
}`
