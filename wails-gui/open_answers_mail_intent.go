package main

import (
	"context"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"wails-gui/internal/openanswer"
	"wails-gui/storage"
)

// Only these explicit questions are interpreted here. Other wording may be
// handled by a future intent mechanism, but model output is not intent proof.
var openAnswerMailReplyQuestion = regexp.MustCompile(`(?i)^(?:has (.+?) replied|did (.+?) reply) to "([^"\r\n]+)"\?$`)

// bindMailReplyTurn resolves a persisted question against known identities and
// the observer's current headers. The revision must have been captured before
// the snapshot. This is still not permission to publish: admission must repeat
// the turn, privacy and revision checks and establish an actionable check.
func bindMailReplyTurn(ctx context.Context, store *storage.Store, snapshot openAnswerMailSnapshot, sessionID, userID, runID string, candidate openanswer.Candidate, now time.Time) (openAnswerMailBinding, error) {
	if store == nil || sessionID == "" || userID == "" || runID == "" || candidate.Settles != "reply" || !strings.EqualFold(candidate.Source, "Mail") {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	row, err := store.LoadStreamItem(ctx, userID)
	if err != nil {
		return openAnswerMailBinding{}, err
	}
	if row == nil || row.Kind != "user" || row.ToolName != "" || row.SessionLocalID != sessionID || row.RunID != runID || strings.TrimSpace(row.Content) != candidate.Question {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	parts := openAnswerMailReplyQuestion.FindStringSubmatch(candidate.Question)
	if parts == nil {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	name := parts[1]
	if name == "" {
		name = parts[2]
	}
	if name != strings.TrimSpace(name) || name == "" {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	principals, err := store.ListPrincipals(ctx)
	if err != nil {
		return openAnswerMailBinding{}, err
	}
	personID := ""
	var principal storage.PrincipalRow
	owners := map[string]map[string]bool{}
	for _, person := range principals {
		for _, known := range append([]string{person.DisplayName}, person.Aliases...) {
			if strings.EqualFold(strings.TrimSpace(known), name) && known != "" {
				if personID != "" && personID != person.ID {
					return openAnswerMailBinding{}, errOpenAnswerMailBinding
				}
				personID = person.ID
				principal = person
			}
		}
		handles, err := store.ListAppHandlesByPrincipal(ctx, person.ID)
		if err != nil {
			return openAnswerMailBinding{}, err
		}
		for _, handle := range handles {
			if handle.App != "email" {
				continue
			}
			for _, value := range []string{handle.HandleID, handle.DisplayHandle} {
				parsed, err := mail.ParseAddress(value)
				if err != nil || parsed.Name != "" || parsed.Address != value {
					continue
				}
				address := strings.ToLower(parsed.Address)
				if owners[address] == nil {
					owners[address] = map[string]bool{}
				}
				owners[address][person.ID] = true
			}
		}
	}
	if personID == "" {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	var binding openAnswerMailBinding
	matches := 0
	for _, message := range snapshot.rows {
		if message.Mailbox != storage.MailboxSent || message.Subject != parts[3] {
			continue
		}
		for _, address := range strings.Split(message.To+"\n"+message.CC, "\n") {
			if !owners[address][personID] {
				continue
			}
			if len(owners[address]) != 1 {
				return openAnswerMailBinding{}, errOpenAnswerMailBinding
			}
			matches++
			if matches != 1 {
				return openAnswerMailBinding{}, errOpenAnswerMailBinding
			}
			binding, err = snapshot.bindReply(message.ID, address, now)
			if err != nil {
				return openAnswerMailBinding{}, err
			}
			binding.correspondent = address
		}
	}
	if matches != 1 {
		return openAnswerMailBinding{}, errOpenAnswerMailBinding
	}
	binding.person = principal
	release, err := store.LockReadRevision(ctx, snapshot.revision)
	if err != nil {
		return openAnswerMailBinding{}, err
	}
	release()
	return binding, nil
}
