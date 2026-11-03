package main

import (
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"wails-gui/storage"
)

var errOpenAnswerMailBinding = errors.New("mail reply cannot be bound to these headers")

// openAnswerMailBinding is a preliminary match against one observer snapshot.
// The anchor and correspondent must be established independently from the
// user's turn. Subject hashes are not Mail thread IDs, and a matching reply
// may be from an unrelated conversation with the same subject. This result
// neither authorizes admission nor proves that an unseen reply does not exist.
type openAnswerMailBinding struct {
	anchor        storage.MailMessageRow
	reply         *storage.MailMessageRow
	person        storage.PrincipalRow
	correspondent string
}

// bindExplicitReplyReference selects a sent header only when the user's own
// question contains both the complete recipient address and the exact subject
// in double quotes. It is a narrow evidence primitive, not an interpretation
// of the question or authorization to create a promise. In particular, the
// subject-derived thread key cannot establish a unique Mail conversation.
// The caller must separately bind the unchanged persisted turn, validate its
// intent and privacy, and provide a durable check and message-opening action.
func (snapshot openAnswerMailSnapshot) bindExplicitReplyReference(question, correspondent string, now time.Time) (openAnswerMailBinding, error) {
	address, err := mail.ParseAddress(correspondent)
	if err != nil || address.Name != "" || address.Address != strings.TrimSpace(correspondent) || !mailReferenceMentioned(question, address.Address) {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	return snapshot.bindQuotedSubject(question, address.Address, now)
}

// bindNamedReplyReference supports name-only questions. The caller must
// independently resolve a unique person and verify that correspondent is an
// address belonging to that person; a model-provided name or address is not
// identity evidence. A literal name match alone does not establish intent.
func (snapshot openAnswerMailSnapshot) bindNamedReplyReference(question, name, correspondent string, now time.Time) (openAnswerMailBinding, error) {
	address, err := mail.ParseAddress(correspondent)
	if err != nil || address.Name != "" || address.Address != strings.TrimSpace(correspondent) ||
		strings.TrimSpace(name) != name || name == "" || !mailReferenceMentioned(question, name) {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	return snapshot.bindQuotedSubject(question, address.Address, now)
}

func (snapshot openAnswerMailSnapshot) bindQuotedSubject(question, correspondent string, now time.Time) (openAnswerMailBinding, error) {
	correspondent = strings.ToLower(correspondent)
	anchorID := ""
	for _, row := range snapshot.rows {
		if row.Mailbox != storage.MailboxSent || row.Subject == "" || strings.ContainsAny(row.Subject, "\"\r\n") ||
			!strings.Contains(question, "\""+row.Subject+"\"") {
			continue
		}
		for _, recipient := range strings.Split(row.To+"\n"+row.CC, "\n") {
			if recipient != correspondent {
				continue
			}
			if anchorID != "" {
				return openAnswerMailBinding{}, errOpenAnswerMailBinding
			}
			anchorID = row.ID
			break
		}
	}
	if anchorID == "" {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	return snapshot.bindReply(anchorID, correspondent, now)
}

// A name or address embedded in a longer identifier is not a reference to
// that correspondent. Literal text alone is not evidence of intent.
func mailReferenceMentioned(question, reference string) bool {
	question, reference = strings.ToLower(question), strings.ToLower(reference)
	for offset := 0; offset <= len(question)-len(reference); {
		index := strings.Index(question[offset:], reference)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(reference)
		before := []rune(question[:start])
		after := []rune(question[end:])
		if strings.Count(question[:start], `"`)%2 == 0 &&
			(len(before) == 0 || !mailAddressBoundaryChar(before[len(before)-1])) &&
			(len(after) == 0 || !mailAddressBoundaryChar(after[0])) {
			return true
		}
		offset = end
	}
	return false
}

func mailAddressBoundaryChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._%+-@", r)
}

func (snapshot openAnswerMailSnapshot) bindReply(anchorID, correspondent string, now time.Time) (openAnswerMailBinding, error) {
	address, err := mail.ParseAddress(correspondent)
	if err != nil || address.Name != "" || address.Address != strings.TrimSpace(correspondent) || now.IsZero() {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	correspondent = strings.ToLower(address.Address)
	var anchor storage.MailMessageRow
	seen := make(map[string]bool, len(snapshot.rows))
	for _, row := range snapshot.rows {
		if row.ID == "" || seen[row.ID] {
			return openAnswerMailBinding{}, errOpenAnswerMailBinding
		}
		seen[row.ID] = true
		if row.ID == anchorID {
			anchor = row
		}
	}
	if anchor.ID == "" || anchor.Mailbox != storage.MailboxSent || anchor.ThreadKey == "" ||
		strings.TrimSpace(anchor.Subject) == "" || anchor.At <= 0 || anchor.At > now.UnixMilli() {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	recipient := false
	for _, value := range strings.Split(anchor.To+"\n"+anchor.CC, "\n") {
		if value == correspondent {
			recipient = true
		}
	}
	if !recipient {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	result := openAnswerMailBinding{anchor: anchor}
	for _, row := range snapshot.rows {
		if row.Mailbox != storage.MailboxInbox || row.ThreadKey != anchor.ThreadKey || row.SenderEmail != correspondent || row.At < anchor.At {
			continue
		}
		// Millisecond ties do not establish which message came first, and a
		// future date does not establish that the reply has happened yet.
		if row.At == anchor.At || row.At > now.UnixMilli() {
			return openAnswerMailBinding{}, errOpenAnswerMailBinding
		}
		if result.reply == nil || row.At < result.reply.At || (row.At == result.reply.At && row.ID < result.reply.ID) {
			reply := row
			result.reply = &reply
		}
	}
	return result, nil
}
