package main

import (
	"testing"
	"time"
	"wails-gui/internal/topics"
)

func TestInterestHintsDoNotReplayCachedPhrases(t *testing.T) {
	app := newTopicFindsTestApp(t)
	svc := app.topicsSvc()
	svc.cache = []topics.Topic{{Label: "Restricted cached source"}}
	svc.cacheAt = svc.now()
	for _, hint := range app.interestsUserState().Hints {
		if hint == "Restricted cached source" {
			t.Fatal("model hints replayed stale cache")
		}
	}
}
func TestTopicCollectionResolvesMemoryReadProvenance(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	writeInterestReplayMemory(t, "topic-source", memoryReadSource("manual"))
	if len(collectTopicInput(app, time.Now()).Memories) != 1 {
		t.Fatal("readable memory missing")
	}
	writeInterestReplayMemory(t, "topic-source", memoryReadProvenance{Version: 2, ReadBlocked: true})
	if len(collectTopicInput(app, time.Now()).Memories) != 0 {
		t.Fatal("topic collector admitted blocked flattened provenance")
	}
}
