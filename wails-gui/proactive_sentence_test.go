package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/pii"
	"wails-gui/internal/proactivevoice"
	"wails-gui/storage"
)

func proactiveCommitmentSentenceFixture(t *testing.T, generated bool, title string) (*App, storage.ReminderRow, engagement.Moment, time.Time, string) {
	t.Helper()
	app := mainChatTestApp(t)
	app.activity = &activityMonitorState{store: newTestStore(t)}
	now := time.Now().Truncate(time.Second)
	due := now.Add(time.Hour)
	row := storage.ReminderRow{UID: "sentence-reminder", Title: title, DueAt: due.UnixMilli(), List: "Work"}
	ctx := t.Context()
	if err := app.store.UpsertReminders(ctx, []storage.ReminderRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	purpose := NewPurposeMapStore(app.activity.store.db)
	created, _, err := purpose.Create(reminderCreateInput(reminderFromRow(row), now.Add(-time.Hour).UnixMilli(), time.Local))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := purpose.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	moment, eligible := engagementCommitmentSlippingMoment(detail, now)
	if !eligible {
		t.Fatal("fixture commitment is not due")
	}
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	app.engagement.now = func() time.Time { return now }
	app.engagement.prepareSource, app.engagement.sourceAllowsRead = app.prepareEngagementRead, app.engagementDeliveryAllowsRead
	storedMoment, err := app.engagement.Submit(ctx, moment)
	if err != nil {
		t.Fatal(err)
	}
	moment = momentFromRow(storedMoment)
	catalog, _ := engagement.Copy(moment.Kind, "en")
	card := mainChatProactive{MomentID: moment.ID, Kind: string(moment.Kind), Headline: catalog.PushBody, Body: catalog.PushBody,
		ActionIDs: []string{"open"}, ActionLabels: map[string]string{"open": "Open"}, State: "pending", Target: mainChatTarget{Kind: mainChatTargetToday},
		Evidence: []mainChatEvidenceRef{{Kind: "commitment", ID: moment.SubjectID, Target: mainChatTarget{Kind: mainChatTargetToday}}}}
	populateMainChatProactiveCopy(&card, nil)
	if generated {
		card.VoiceVersion = proactivevoice.PromptVersion
		source, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en")
		job, addresses, assembly := assembleProactiveVoiceJob(card.Kind, "en", true, now, source)
		if reason != "" || assembly != "" || !app.saveProactiveVoiceReceipt(ctx, moment, now, job, addresses, source.Provenance, &card) {
			t.Fatalf("generated receipt: source=%s assembly=%s", reason, assembly)
		}
	}
	id := app.postProactiveItem(card, "sentence-card")
	if id == "" {
		t.Fatal("card was not persisted")
	}
	return app, row, moment, now, id
}

// Real product stores and delivery projection; no provider or personal data.
func TestProactiveCommitmentSentenceDelivery(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	for _, generated := range []bool{false, true} {
		for _, transition := range []string{"readable", "protected", "closed-eyes", "revised", "missing-receipt"} {
			name := "catalog/" + transition
			if generated {
				name = "generated/" + transition
			}
			t.Run(name, func(t *testing.T) {
				app, row, moment, now, id := proactiveCommitmentSentenceFixture(t, generated, "Countersign the Northwind SOW")
				due := now.Add(time.Hour)
				ctx := t.Context()
				stored, _ := app.store.LoadStreamItem(ctx, id)
				read := func() mainChatProactive {
					t.Helper()
					main, _ := app.ensureMainChat(ctx)
					page, err := app.LoadStreamItemsPage(main.LocalID, -1, 50)
					if err != nil || len(page.Items) != 1 {
						t.Fatalf("desktop read: %+v %v", page, err)
					}
					var delivered mainChatProactive
					if err := json.Unmarshal([]byte(page.Items[0].ToolInput), &delivered); err != nil {
						t.Fatal(err)
					}
					return delivered
				}
				before := read()
				for _, lang := range engagement.Languages {
					want := proactiveCommitmentSentence(row.Title, due, now, lang)
					if before.Localized[lang].Body != want {
						t.Fatalf("%s: fallback=%q want %q", lang, before.Localized[lang].Body, want)
					}
				}
				switch transition {
				case "protected":
					old := pii.GetUserSensitiveApps()
					t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
					pii.SetUserSensitiveApps(map[string]string{"com.apple.reminders": "Reminders"})
				case "closed-eyes":
					app.closedEyes.enabled.Store(true)
				case "revised":
					row.Title = "A replacement private reminder"
					if err := app.store.UpsertReminders(ctx, []storage.ReminderRow{row}, now.UnixMilli()); err != nil {
						t.Fatal(err)
					}
				case "missing-receipt":
					var original mainChatProactive
					_ = json.Unmarshal([]byte(stored.ToolInput), &original)
					original.VoiceReceipt, original.ReadReceipt = "", ""
					payload, _ := json.Marshal(original)
					stored.ToolInput = string(payload)
					if err := app.store.UpdateStreamItemToolInput(ctx, id, stored.ToolInput); err != nil {
						t.Fatal(err)
					}
				}
				if transition != "readable" {
					after := read()
					raw, _ := json.Marshal(after)
					if strings.Contains(string(raw), "Northwind") || strings.Contains(string(raw), "replacement private") {
						t.Fatalf("withdrawn source resurfaced: %s", raw)
					}
					for _, lang := range engagement.Languages {
						copy, _ := engagement.Copy(moment.Kind, lang)
						if after.Localized[lang].Headline != copy.PushBody || after.Localized[lang].Body != copy.PushBody {
							t.Fatalf("%s withdrawal is not generic: %+v", lang, after)
						}
					}
				}
				if transition != "missing-receipt" {
					unchanged, _ := app.store.LoadStreamItem(ctx, id)
					if unchanged.ToolInput != stored.ToolInput {
						t.Fatal("delivery rewrote the stored card")
					}
				}
			})
		}
	}
}

func TestProactiveCommitmentSentenceClock(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		lang string
		due  time.Time
		want string
	}{
		{"en", time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local), "Countersign the Northwind SOW · due today 12:00 PM"},
		{"zh-CN", time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local), "Countersign the Northwind SOW · 今天 12:00 到期"},
		{"en", time.Date(2026, 10, 24, 14, 47, 0, 0, time.Local), "Countersign the Northwind SOW · due Oct 24 at 2:47 PM"},
		{"zh-CN", time.Date(2026, 10, 24, 14, 47, 0, 0, time.Local), "Countersign the Northwind SOW · 10月24日 14:47 到期"},
	} {
		if got := proactiveCommitmentSentence("Countersign the Northwind SOW", tc.due, now, tc.lang); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.lang, got, tc.want)
		}
	}
}

// Exercise the persisted desktop and phone delivery boundaries, including both
// localized bodies, without a provider call or a separate sanitizer in the test.
func TestProactiveCommitmentSentenceSanitizedDelivery(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	for _, generated := range []bool{false, true} {
		name := "catalog"
		if generated {
			name = "generated"
		}
		t.Run(name, func(t *testing.T) {
			app, _, _, _, id := proactiveCommitmentSentenceFixture(t, generated, "Follow up with code 482913 at https://private.example/token")
			stored, err := app.store.LoadStreamItem(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, surface := range []string{"desktop", "phone"} {
				t.Run(surface, func(t *testing.T) {
					var card mainChatProactive
					var wire string
					switch surface {
					case "desktop":
						page, err := app.LoadStreamItemsPage(main.LocalID, -1, 50)
						if err != nil || len(page.Items) != 1 {
							t.Fatalf("desktop read: %+v %v", page, err)
						}
						if err := json.Unmarshal([]byte(page.Items[0].ToolInput), &card); err != nil {
							t.Fatal(err)
						}
						wire = page.Items[0].Content + page.Items[0].ToolInput
					case "phone":
						detail, raw := proactiveVoicePhoneRead(t, app, main.LocalID)
						if len(detail.Items) != 1 || detail.Items[0].Proactive == nil {
							t.Fatalf("phone read: %s", raw)
						}
						card, wire = *detail.Items[0].Proactive, raw
					}
					for _, forbidden := range []string{"482913", "https://", "private.example", "/token"} {
						if strings.Contains(wire, forbidden) {
							t.Errorf("private source text %q escaped %s delivery", forbidden, surface)
						}
					}
					for _, lang := range engagement.Languages {
						t.Run(lang, func(t *testing.T) {
							body := card.Localized[lang].Body
							if !strings.Contains(body, "Follow up with code") || body == card.Localized[lang].Headline {
								t.Errorf("sanitization lost the specific sentence: %q", body)
							}
							for _, forbidden := range []string{"482913", "https://", "private.example", "/token"} {
								if strings.Contains(body, forbidden) {
									t.Errorf("private source text %q escaped localized body: %q", forbidden, body)
								}
							}
						})
					}
				})
			}
			after, _ := app.store.LoadStreamItem(t.Context(), id)
			if after.ToolInput != stored.ToolInput || after.Content != stored.Content {
				t.Fatal("delivery rewrote persisted source copy")
			}
		})
	}
}

func TestProactiveCommitmentSentenceRejectsRestrictedTitle(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	app, _, moment, _, _ := proactiveCommitmentSentenceFixture(t, false, "Follow up on the medical diagnosis")
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page, err := app.LoadStreamItemsPage(main.LocalID, -1, 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("desktop read: %+v %v", page, err)
	}
	var card mainChatProactive
	if err := json.Unmarshal([]byte(page.Items[0].ToolInput), &card); err != nil {
		t.Fatal(err)
	}
	phone, wire := proactiveVoicePhoneRead(t, app, main.LocalID)
	if len(phone.Items) != 1 || phone.Items[0].Proactive == nil {
		t.Fatalf("phone read: %s", wire)
	}
	for _, delivered := range []mainChatProactive{card, *phone.Items[0].Proactive} {
		for _, lang := range engagement.Languages {
			copy, _ := engagement.Copy(moment.Kind, lang)
			if delivered.Localized[lang].Headline != copy.PushBody || delivered.Localized[lang].Body != copy.PushBody {
				t.Errorf("%s: restricted source did not stay generic: %+v", lang, delivered.Localized[lang])
			}
		}
	}
}

func TestProactiveCommitmentSentenceShortNameKeepsReceiptReadable(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	for _, generated := range []bool{false, true} {
		app, _, _, _, id := proactiveCommitmentSentenceFixture(t, generated, "Make sure I do some sort of calf race or exercise for AFO like every day")
		stored, err := app.store.LoadStreamItem(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		main, err := app.ensureMainChat(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		page, err := app.LoadStreamItemsPage(main.LocalID, -1, 50)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("read: %+v %v", page, err)
		}
		var card mainChatProactive
		if err := json.Unmarshal([]byte(page.Items[0].ToolInput), &card); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(card.Localized["en"].Body, "AFO calf raises, every day") || strings.Contains(card.Localized["en"].Body, "Make sure") {
			t.Fatalf("wrong display: %+v", card)
		}
		after, err := app.store.LoadStreamItem(t.Context(), id)
		if err != nil || stored.ToolInput != after.ToolInput {
			t.Fatal("display projection changed stored receipt")
		}
	}
}
