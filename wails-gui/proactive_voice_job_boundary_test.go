package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/proactivevoice"
)

func TestProactiveVoiceAssemblerClosesItemUnion(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ kind, itemType string }{
		{"waiting_on_reply", "mail_thread"},
		{"meeting_prep", "calendar_event"},
		{"commitment_slipping", "commitment"},
		{"brief_ready", "brief"},
		{"capture_paused", "capture_status"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			item := proactivevoice.Item{
				Ref: "private-storage-id", Type: tc.itemType,
				FromName: proactivevoice.Text("Dana Ruiz"), WaitingSince: &proactivevoice.Display{Display: "Thursday, Oct 15"},
				Title: proactivevoice.Text("Design review"), Start: &proactivevoice.Display{Display: "Friday, Oct 16"}, StartsIn: &proactivevoice.Display{Display: "15 minutes"},
				Subject: proactivevoice.Text("Private mail subject"), BodyExcerpt: proactivevoice.Text("Private mail contents"),
				Due: &proactivevoice.Display{Display: "Friday, Oct 16"}, Progress: proactivevoice.Text("none recorded"), NextStep: proactivevoice.Text("Open the source"),
				Slot: &proactivevoice.Display{Display: "morning"}, TopItems: []proactivevoice.TopItem{{Ref: "private-top-id", Text: "Design review"}},
				What: &proactivevoice.Display{Display: "Screen capture"}, OffFor: &proactivevoice.Display{Display: "20 minutes"},
				Text: "private arbitrary text", Label: "private admin label", Owner: "private owner", With: "private person id", Redacted: true,
				Followups: []proactivevoice.Followup{{Ref: "private-followup-id", Text: "private followup"}},
			}
			source := proactiveVoiceSource{Items: []proactivevoice.Item{item}}
			job, _, reason := assembleProactiveVoiceJob(tc.kind, "en", true, now, source)
			if reason != "" {
				t.Fatal(reason)
			}
			encoded, _ := json.Marshal(job)
			for _, forbidden := range []string{"Private mail", "private-storage-id", "private-top-id", "private arbitrary", "private admin", "private owner", "private person", "private-followup", `"redacted"`} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("unexpected field: %s", forbidden)
				}
			}
			got := job.Items[0]
			if tc.kind != "brief_ready" && len(got.TopItems) > 0 {
				t.Fatal("brief text crossed item union")
			}
			if tc.kind != "commitment_slipping" && (got.Progress != nil || got.NextStep != nil) {
				t.Fatal("commitment text crossed item union")
			}
			if tc.kind != "meeting_prep" && (got.Start != nil || got.StartsIn != nil) {
				t.Fatal("calendar facts crossed item union")
			}
		})
	}
}

func TestProactiveVoiceNamesOffRemovesAliasesAndTitlesBeforeDisclosure(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	source := proactiveVoiceSource{
		ContactNames: []string{"Dana Ruiz", "Élodie Martin", "林悦"},
		People:       []proactivevoice.Person{{Ref: "person", Name: proactivevoice.Text("Ravi Okafor"), RoleLabel: "a colleague"}},
		UserName:     "Sam",
		Items: []proactivevoice.Item{{Ref: "item", Type: "commitment", Progress: proactivevoice.Text("none recorded"),
			Title: proactivevoice.Text("Hidden project"), Place: proactivevoice.Text("Private room"),
			NextStep: proactivevoice.Text("Dana and Élodie met Ravi and Sam with 林悦 about Hidden project in Private room.")}},
	}
	job, _, reason := assembleProactiveVoiceJob("commitment_slipping", "en", false, now, source)
	if reason != "" {
		t.Fatal(reason)
	}
	encoded, _ := json.Marshal(job)
	for _, forbidden := range []string{"Dana", "Élodie", "Ravi", "Sam", "林悦", "Hidden project", "Private room"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("withheld name or title reached provider: %s", forbidden)
		}
	}
	if got := proactiveVoiceClean("Danaish and Élodiex", []string{"Dana", "Élodie"}, 100); got != "Danaish and Élodiex" {
		t.Fatalf("partial names removed: %s", got)
	}
}

func TestProactiveVoiceHealthNeverBypassedByMoneyFlag(t *testing.T) {
	now := time.Now()
	for _, text := range []string{"Please pay the hospital invoice", "Please send money for medication", "请支付医院账单", "Pay the dentist"} {
		source := voiceMailSource()
		source.RawMailBody = text
		if _, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, now, source); reason != "unsafe_content" {
			t.Fatalf("health mail passed: %s", reason)
		}
	}
}

func TestProactiveVoiceRestrictedDisplayAndProgressFields(t *testing.T) {
	now := time.Now()
	for _, field := range []string{"place", "progress", "display"} {
		t.Run(field, func(t *testing.T) {
			item := proactivevoice.Item{Ref: "item", Type: "calendar_event", Title: proactivevoice.Text("Review")}
			kind := "meeting_prep"
			switch field {
			case "place":
				item.Place = proactivevoice.Text("hospital room")
			case "progress":
				item.Type = "commitment"
				item.Progress = proactivevoice.Text("prescription ready")
				kind = "commitment_slipping"
			case "display":
				item.StartsIn = &proactivevoice.Display{Display: "USD 50"}
			}
			if _, _, reason := assembleProactiveVoiceJob(kind, "en", true, now, proactiveVoiceSource{Items: []proactivevoice.Item{item}}); reason != "unsafe_content" {
				t.Fatalf("restricted %s passed: %s", field, reason)
			}
		})
	}
}

func TestProactiveVoiceExactEvidenceJoin(t *testing.T) {
	for _, tc := range []struct {
		text, value string
		want        bool
	}{
		{"Dana Ruiz agreed", "Dana Ruiz", true}, {"NotDana Ruiz agreed", "Dana Ruiz", false},
		{"Dana Ruizish agreed", "Dana Ruiz", false}, {"dana Ruiz agreed", "Dana Ruiz", false},
		{"Élodie agreed", "Élodie", true}, {"xÉlodie agreed", "Élodie", false},
		{"请联系林悦确认", "林悦", true}, {"Discuss Design review.", "Design review", true},
		{"Discuss Design reviews.", "Design review", false},
	} {
		if got := proactiveVoiceExactContains(tc.text, tc.value); got != tc.want {
			t.Errorf("match %q in %q=%v", tc.value, tc.text, got)
		}
	}
	source := voiceMailSource()
	source.UserWords = []proactiveVoiceUserWordSource{{Source: "chat", Text: "NotDana Ruiz has the answer.", When: time.Now()}}
	job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, time.Now(), source)
	if reason != "" || len(job.UserWords) != 0 {
		t.Fatalf("partial name joined: %s %+v", reason, job.UserWords)
	}
}

func TestProactiveVoiceDropsUnresolvedRelativeDays(t *testing.T) {
	for _, day := range []string{"next month", "last week", "10/20", "下个月", "明年", "后天"} {
		t.Run(day, func(t *testing.T) {
			source := voiceMailSource()
			source.UserWords = []proactiveVoiceUserWordSource{{Source: "chat", Text: "I will answer Dana Ruiz " + day + ".", When: time.Now()}}
			job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, time.Now(), source)
			if reason != "" || len(job.UserWords) != 0 {
				t.Fatalf("unresolved day reached job: %s %+v", reason, job.UserWords)
			}
		})
	}
}

func TestProactiveVoiceLocalizesResolvedISOUserWords(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	promised := now.AddDate(0, 0, 1)
	for _, lang := range []string{"en", "zh-CN"} {
		for _, date := range []string{"2026-10-17", "2026/10/17"} {
			t.Run(lang+date, func(t *testing.T) {
				source := voiceMailSource()
				source.UserWords = []proactiveVoiceUserWordSource{{Source: "chat", Text: "I will answer Dana Ruiz by " + date + ".", When: now, PromisedDay: &promised}}
				job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", lang, true, now, source)
				if reason != "" || len(job.UserWords) != 1 {
					t.Fatalf("resolved date omitted: %s %+v", reason, job.UserWords)
				}
				if !strings.Contains(job.UserWords[0].Text, proactiveVoiceDate(promised, lang)) || strings.Contains(job.UserWords[0].Text, date) {
					t.Fatalf("raw date not localized: %s", job.UserWords[0].Text)
				}
				encoded, _ := json.Marshal(job)
				if proactiveVoiceRawDate.Match(encoded) {
					t.Fatal("ISO date reached provider JSON")
				}
			})
		}
	}
	for _, date := range []string{"2026-10-18", "2026-02-30", "2026-10-17T09:00:00Z", "2026-10-17 09:00:00"} {
		t.Run(date, func(t *testing.T) {
			source := voiceMailSource()
			source.UserWords = []proactiveVoiceUserWordSource{{Source: "chat", Text: "I will answer Dana Ruiz by " + date + ".", When: now, PromisedDay: &promised}}
			job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, now, source)
			if reason != "" || len(job.UserWords) != 0 {
				t.Fatalf("ambiguous date retained: %s %+v", reason, job.UserWords)
			}
		})
	}
}

func TestProactiveVoiceRejectsRawISOTitlesAndDisplays(t *testing.T) {
	for _, field := range []string{"title", "display", "top_item"} {
		t.Run(field, func(t *testing.T) {
			kind := "meeting_prep"
			item := proactivevoice.Item{Ref: "c1", Type: "calendar_event", Title: proactivevoice.Text("Design review"), Start: &proactivevoice.Display{Display: "Saturday, Oct 17"}}
			switch field {
			case "title":
				item.Title = proactivevoice.Text("Design review 2026-10-17")
			case "display":
				item.Start = &proactivevoice.Display{Display: "2026-10-17T09:00:00Z"}
			case "top_item":
				kind = "brief_ready"
				item = proactivevoice.Item{Ref: "b1", Type: "brief", Slot: &proactivevoice.Display{Display: "morning"}, TopItems: []proactivevoice.TopItem{{Ref: "b2", Text: "Design review 2026/10/17"}}}
			}
			if _, _, reason := assembleProactiveVoiceJob(kind, "en", true, time.Now(), proactiveVoiceSource{Items: []proactivevoice.Item{item}}); reason != "would_need_guess" {
				t.Fatalf("raw date crossed assembler: %s", reason)
			}
		})
	}
}
