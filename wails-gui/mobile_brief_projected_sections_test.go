package main

import "testing"

func TestMobileBriefParagraphChipPrefersTypedSectionsOverProjections(t *testing.T) {
	t.Run("commitment projected into plan", func(t *testing.T) {
		commitment := MobileBriefItem{ID: "commitment", Kind: "commitment", Status: "active", Title: "Q4 plan", Refs: []string{"e1"}}
		projection := commitment
		projection.ID = "moved-commitment"
		brief := MobileBriefV1{
			AsOf:     "2026-10-23T15:00:00Z",
			Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "purpose", SourceID: "q4-plan"}},
			Narrative: []MobileBriefPara{{
				Text: "The Q4 plan needs time.", Refs: []string{"e1"},
			}},
			Sections: MobileBriefSections{
				Commitments: []MobileBriefItem{commitment},
				Moved:       []MobileBriefItem{projection},
			},
		}
		brief.Sections = mobileBriefChiefSections(brief)
		if len(brief.Sections.Plan) != 1 || brief.Sections.Plan[0].Refs[0] != "e1" {
			t.Fatalf("fixture did not project the commitment into Plan: %+v", brief.Sections.Plan)
		}
		cards := mobileBriefParagraphCards(brief, "en", nil, 1)
		if len(cards) != 1 || cards[0].Label != "Find time for Q4 plan" {
			t.Fatalf("cards = %+v, want the commitment chip", cards)
		}
		subject, ok := mobileBriefCardSubject(brief, []string{"e1"})
		if !ok || subject.ID != brief.Sections.Plan[0].ID {
			t.Fatalf("first-match item subject changed: %+v, %v", subject, ok)
		}
	})

	t.Run("future meeting projected into review", func(t *testing.T) {
		meeting := MobileBriefItem{
			ID: "meeting", Kind: "meeting", Status: "upcoming", Title: "Planning",
			At: "2026-10-23T18:00:00Z", Detail: "11:00 AM; Room 4; Dan Smith", Refs: []string{"e2"},
		}
		projection := meeting
		projection.ID = "review-meeting"
		brief := MobileBriefV1{
			AsOf: "2026-10-23T15:00:00Z", Timezone: "America/Los_Angeles",
			Evidence:  []MobileBriefRef{{Ref: "e2", SourceKind: mobileBriefCalendarSource, SourceID: "planning"}},
			Narrative: []MobileBriefPara{{Text: "Planning is coming up.", Refs: []string{"e2"}}},
			Sections: MobileBriefSections{
				Meetings: []MobileBriefItem{meeting},
				Review:   []MobileBriefItem{projection},
			},
		}
		cards := mobileBriefParagraphCards(brief, "en", nil, 1)
		if len(cards) != 1 || cards[0].Label != "Prep for 11:00 AM with Dan Smith" {
			t.Fatalf("cards = %+v, want the meeting prep chip", cards)
		}
	})
}
