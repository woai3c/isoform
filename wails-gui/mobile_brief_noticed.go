package main

import (
	"strings"
	"time"
)

const (
	mobileBriefNoticedSource = "activity_noticed"
	mobileBriefNoticedLimit  = 2
)

// MobileBriefActivityTarget opens an editable suggestion in Activity. It is
// navigation only: neither the card nor its evidence authorizes saving work.
type MobileBriefActivityTarget struct {
	Date         string `json:"date"`
	SuggestionID string `json:"suggestionId"`
	Action       string `json:"action"` // follow_up or routine
}

func mobileBriefNoticedTarget(id, action string) (*MobileBriefActivityTarget, bool) {
	date, suggestion, ok := strings.Cut(id, "/")
	if !ok || suggestion == "" || strings.Contains(suggestion, "/") {
		return nil, false
	}
	if day, err := time.Parse("2006-01-02", date); err != nil || day.Format("2006-01-02") != date {
		return nil, false
	}
	if action != noticedKindFollowUp && action != noticedKindRoutine {
		return nil, false
	}
	return &MobileBriefActivityTarget{Date: date, SuggestionID: suggestion, Action: action}, true
}

// Every pass reads through GetDayNoticed's current gate. A stored ref is
// membership, never authority: privacy changes and withdrawals apply on the
// next pass. Only repeated reads of one date within this pass are shared.
type mobileBriefNoticedLookup struct {
	read func(string) ([]NoticedSuggestion, error)
	days map[string]map[string]NoticedSuggestion
}

func newMobileBriefNoticedLookup(a *App) *mobileBriefNoticedLookup {
	lookup := &mobileBriefNoticedLookup{days: make(map[string]map[string]NoticedSuggestion)}
	if a != nil {
		lookup.read = a.GetDayNoticed
	}
	return lookup
}

func (lookup *mobileBriefNoticedLookup) suggestion(id string) (NoticedSuggestion, bool) {
	target, ok := mobileBriefNoticedTarget(id, noticedKindFollowUp)
	if !ok || lookup == nil || lookup.read == nil {
		return NoticedSuggestion{}, false
	}
	items, seen := lookup.days[target.Date]
	if !seen {
		items = make(map[string]NoticedSuggestion)
		day, err := lookup.read(target.Date)
		if err == nil {
			for _, item := range day {
				if _, exists := items[item.ID]; !exists {
					items[item.ID] = item
				}
			}
		}
		lookup.days[target.Date] = items
	}
	item, ok := items[target.SuggestionID]
	return item, ok
}

func (a *App) mobileBriefNoticedSuggestion(id string) (NoticedSuggestion, bool) {
	return newMobileBriefNoticedLookup(a).suggestion(id)
}

func collectMobileBriefNoticed(a *App, in *mobileBriefInput) {
	// A failed read withdraws old suggestions too. Never retain the stored
	// section as a substitute for the current Activity gate.
	in.Noticed = nil
	if a == nil {
		return
	}
	day := in.AsOf.In(mobileBriefInputLocation(*in))
	for offset := 0; offset >= -1; offset-- {
		date := day.AddDate(0, 0, offset).Format("2006-01-02")
		items, err := a.GetDayNoticed(date)
		if err != nil {
			continue
		}
		for _, item := range items {
			if len(in.Noticed) == mobileBriefNoticedLimit {
				return
			}
			in.Noticed = append(in.Noticed, mobileBriefCandidate{
				SourceKind: mobileBriefNoticedSource, SourceID: date + "/" + item.ID,
				Kind: "noticed", Status: item.Kind, Title: item.Text, Detail: item.Why,
				At: mobileBriefMillis(item.MomentStartMs),
			})
		}
	}
}

func mobileBriefNoticedOffer(brief MobileBriefV1, item MobileBriefItem, lang string) (mobileBriefOfferDraft, bool) {
	if len(item.Refs) == 0 {
		return mobileBriefOfferDraft{}, false
	}
	ref := mobileBriefEvidenceByRef(brief)[item.Refs[0]]
	target, ok := mobileBriefNoticedTarget(ref.SourceID, item.Status)
	if ref.SourceKind != mobileBriefNoticedSource || !ok {
		return mobileBriefOfferDraft{}, false
	}
	label := pick(lang, "Add follow-up", "添加跟进")
	phrase := pick(lang, "open the follow-up field in Activity for ", "在活动中打开跟进输入框：")
	if target.Action == noticedKindRoutine {
		label = pick(lang, "Make a routine", "设为例行事务")
		phrase = pick(lang, "open the routine draft in Activity for ", "在活动中打开例行事务草稿：")
	}
	return mobileBriefOfferDraft{
		offer:  MobileBriefOffer{Action: mobileBriefCardOpenActivity, Kind: target.Action, Label: label, Target: target},
		phrase: phrase + mobileBriefCardTitle(item), subject: mobileBriefCardTitle(item),
	}, true
}

// The phone cannot open Activity on the computer. Apply this projection to
// latest, by-ID, push and queued re-delivery, after the shared read gates and
// card reconstruction. The brief's text and revision stay the same.
func (a *App) mobileBriefForPhone(brief MobileBriefV1) MobileBriefV1 {
	phone, _ := a.mobileBriefPhoneProjection(brief)
	return a.mobileBriefWithDayShape(phone)
}

// Retain the original offers only in the local queued frame: revalidation
// must reconstruct the same desktop revision before stripping navigation.
func (a *App) mobileBriefPhoneProjection(brief MobileBriefV1) (MobileBriefV1, []MobileBriefOffer) {
	brief = a.mobileBriefWithCards(a.mobileBriefForDelivery(brief))
	var desktopOffers []MobileBriefOffer
	if brief.Offers != nil {
		offers := make([]MobileBriefOffer, 0, len(brief.Offers))
		for _, offer := range brief.Offers {
			if offer.Action != mobileBriefCardOpenActivity {
				offers = append(offers, offer)
			} else if desktopOffers == nil {
				desktopOffers = append([]MobileBriefOffer{}, brief.Offers...)
			}
		}
		brief.Offers = offers
	}
	if brief.Cards != nil {
		cards := make([]MobileBriefCard, 0, len(brief.Cards))
		for _, card := range brief.Cards {
			if card.Mode != mobileBriefCardOpenActivity {
				cards = append(cards, card)
			}
		}
		brief.Cards = cards
	}
	// Older phones infer the Talk action from the revision alone. A
	// noticed-only brief has no chat opener, so it must remain text only.
	if desktopOffers != nil {
		if _, ok := mobileBriefFindCard(brief, mobileBriefOpenerCardID); !ok {
			brief.ConversationKey = ""
		}
	}
	return brief, desktopOffers
}
