package main

import (
	"context"
	"encoding/json"
	"strings"
)

const mobileBriefThreadOutcomeSource = "thread_outcome"

func threadOutcomeSourceID(id string, outcome *ActivityThreadOutcome) string {
	raw, _ := json.Marshal(outcome)
	return threadOutcomeMomentPrefix + id + ":" + agencyPayloadHash(raw)
}

func (a *App) threadOutcomeSourceReadable(ctx context.Context, sourceID string) bool {
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() || ctx.Err() != nil {
		return false
	}
	if memo := mainChatReadMemoFrom(ctx); memo != nil {
		return memo.threadSourceReadable(sourceID, func() bool {
			return a.threadOutcomeSourceReadableUncached(ctx, sourceID)
		})
	}
	recordMainChatReadMemoLoad("threadSource")
	return a.threadOutcomeSourceReadableUncached(ctx, sourceID)
}

func (a *App) threadOutcomeSourceReadableUncached(ctx context.Context, sourceID string) bool {
	at := strings.LastIndex(sourceID, ":")
	if at < 1 {
		return false
	}
	row, err := a.activity.store.GetActivityByID(strings.TrimPrefix(sourceID[:at], threadOutcomeMomentPrefix))
	if err != nil || row == nil {
		return false
	}
	outcome := a.readableThreadOutcome(ctx, *row)
	return outcome != nil && threadOutcomeSourceID(row.ID, outcome) == sourceID
}

// Apply to both provider replies and deterministic copy; normalize typography
// so curly apostrophes and whitespace cannot bypass the output contract.
func mobileBriefBannedOutcomePhrase(value string) bool {
	folded := strings.ToLower(strings.ReplaceAll(value, "’", "'"))
	folded = strings.Join(strings.Fields(folded), " ")
	for _, phrase := range []string{"i saw", "i noticed", "i observed", "i did not see", "i haven't seen", "i have not seen", "i can't tell", "i can't see", "i cannot tell", "i cannot see", "i could not tell", "completion is unconfirmed", "completion remains unconfirmed", "unclear whether", "i'm not sure", "i cannot confirm", "i can't confirm", "我看见", "我发现", "尚不清楚", "不确定", "worth a quick look to see if it's settled", "我看到", "我注意到", "我没有看到", "无法判断", "不能确定", "完成情况尚未确认", "完成状态尚未确认"} {
		if strings.Contains(folded, phrase) {
			return true
		}
	}
	return false
}

func mobileBriefNarrativeWithinBudget(paragraphs []MobileBriefPara) bool {
	return mobileBriefNarrativeWords(paragraphs) <= mobileBriefMaxWords
}
