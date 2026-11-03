package main

import (
	"strings"

	"wails-gui/storage"
)

// The chip path compares redacted text and can therefore accept two distinct
// prompts with identical redactions. Bind candidates to the original user
// prompt, before attachment context is added, rather than guessing from a
// wrapper that can also occur in user-supplied text.
func openAnswerTurnQuestion(rows []storage.StreamItemRow, userID, prompt string) string {
	if userID == "" || len(rows) == 0 {
		return ""
	}
	row := rows[len(rows)-1]
	question := strings.TrimSpace(row.Content)
	prompt = strings.TrimSpace(prompt)
	if row.ID != userID || row.Kind != "user" || question == "" {
		return ""
	}
	if prompt != question {
		return ""
	}
	return question
}

// The candidate is only a request for backend evaluation. Until the backend
// can create a durable check, it must not be presented as a promise.
const openAnswerCandidateDirective = `<open-answer-protocol>
Answer the user's question normally. Do not promise in your visible answer to check again, watch for a change, or return later. Do not create a schedule or take action to monitor it as part of this protocol.

If the answer is not yet final and a result, reply, or status could settle within seven days, you may append one <open_answer> JSON object after your final answer for the app to evaluate. This is a candidate, not a promise: the app independently checks source access, privacy, and whether a durable check can actually be made. Do not mention the block in your answer.

Use this schema: {"question":"the user's exact question","topic":"short topic","settles":"result","source":"source name","notYet":true,"windowStart":"RFC3339 timestamp","windowEnd":"RFC3339 timestamp"}. Set "settles" to exactly one of "result", "reply", or "status". For an observable event instead of a named time, omit both window fields and use "watchedEvent":"event identifier". Use exactly one of a window or an event. Do not invent a source, event, time, or outcome. If the information is unavailable, omit the block. Omit it for complete answers, unbounded drift, and standing interests. An event or time more than seven days away is not a candidate; a reminder may be appropriate instead.
</open-answer-protocol>

`
