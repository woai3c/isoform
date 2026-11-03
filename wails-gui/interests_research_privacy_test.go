package main

import (
	"os"
	"testing"
	"wails-gui/internal/interests"
)

func TestStoredInterestResearchReadsWithdrawEvidence(t *testing.T) {
	for _, restriction := range []string{"read_blocked", "missing", "closed_eyes"} {
		t.Run(restriction, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			path := writeInterestReplayMemory(t, "research-source", memoryReadSource("manual"))
			it := testInterest("research", "optical camera modules", "Evidence-backed research", "optical camera module")
			it.Evidence = []interests.Ref{{Key: "about:research-source#0", Source: interests.SourceAbout, At: interestTestNow.UnixMilli()}}
			svc, _ := interestReplayService(t, app, "")
			svc.choices = nil
			svc.collect = nil
			svc.rememberNames(nil, interestTestNow)
			if _, err := svc.store.update(func(st *interestsStateFile) { st.Interests = []interests.Interest{it} }); err != nil {
				t.Fatal(err)
			}
			if got := svc.list(); len(got) != 1 {
				t.Fatal("readable interest missing")
			}
			switch restriction {
			case "read_blocked":
				writeInterestReplayMemory(t, "research-source", memoryReadProvenance{Version: 2, ReadBlocked: true})
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "closed_eyes":
				app.closedEyes.enabled.Store(true)
			}
			if got := svc.list(); len(got) != 0 {
				t.Fatalf("research received withdrawn interest: %+v", got)
			}
		})
	}
}
