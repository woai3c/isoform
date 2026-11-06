package main

import (
	"testing"
	"time"
)

// A stale ranking is served at once and rebuilt in the background, once, while the
// rebuild is still running; the request does not wait for the sources (R24b).
func TestStaleTopicRankingIsServedAndRebuiltInTheBackground(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	svc := newTopicsService(&App{})
	now := topicTestNow
	svc.now = func() time.Time { return now }
	svc.collect = func(now time.Time) topicInput {
		return topicInput{Now: now, Sessions: topicChatsOnThreeDays("Fix wails drag regions", "Plan pricing review")}
	}
	if _, _ = svc.rebuildRankedPhrases(now); svc.cache == nil {
		t.Fatal("setup: no ranking cached")
	}
	// Ten minutes later the ranking is stale; the rebuild's sources are held open.
	now = now.Add(topicRefreshInterval + time.Minute)
	release := make(chan struct{})
	collects := make(chan struct{}, 4)
	svc.collect = func(now time.Time) topicInput {
		collects <- struct{}{}
		<-release
		return topicInput{Now: now, Sessions: topicChatsOnThreeDays("Fix wails drag regions", "Plan pricing review")}
	}
	done := make(chan []string, 1)
	go func() {
		topics, _ := svc.rankedPhrases(false)
		names := []string{}
		for _, topic := range topics {
			names = append(names, topic.Label)
		}
		done <- names
	}()
	select {
	case names := <-done:
		if len(names) == 0 {
			t.Fatal("a stale ranking was not served from the cache")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request waited for the background sources")
	}
	<-collects
	// A second stale read while the rebuild runs starts no second rebuild.
	svc.rankedPhrases(false)
	select {
	case <-collects:
		t.Fatal("a second rebuild started while one was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		fresh := svc.cacheAt.Equal(now) && !svc.rebuilding
		svc.mu.Unlock()
		if fresh {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the background rebuild did not publish a fresh ranking")
}
