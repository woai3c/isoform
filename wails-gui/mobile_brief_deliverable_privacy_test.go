package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBriefDeliverablesRespectPrivateFoldersAtCollectionAndDelivery(t *testing.T) {
	app, _ := todayDeliverablePrivacyFixture(t)
	svc := newMobileBriefService(app)
	base, ok := svc.store.latest()
	if !ok {
		t.Fatal("stored brief missing")
	}
	if len(app.mobileBriefForDelivery(base).Sections.Deliverables) != 3 {
		t.Fatal("readable control missing")
	}
	if err := app.saveFilesPrivateFolders([]string{os.Getenv("HOME")}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	in := mobileBriefInput{AsOf: now.Add(time.Minute), WindowStart: now.Add(-time.Hour)}
	collectMobileBriefDeliverables(app, &in)
	if len(in.Deliverables) != 0 {
		t.Errorf("private deliverables collected: %+v", in.Deliverables)
	}
	check := func(brief MobileBriefV1) {
		t.Helper()
		wire, _ := json.Marshal(brief)
		if strings.Contains(string(wire), "Confidential Atlas outline") {
			t.Fatalf("private metadata delivered: %s", wire)
		}
	}
	check(app.mobileBriefForDelivery(base))
	bridge := newMobileBriefPrivacyBridge(t, app)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	if err := bridge.publishBrief(base, false); err != nil {
		t.Fatal(err)
	}
	queued := bridge.pendingBriefs[phone.deviceID]
	wire := phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, queued.Env)
	var pushed MobileBriefV1
	if err := json.Unmarshal(wire, &pushed); err != nil {
		t.Fatal(err)
	}
	check(pushed)
}
