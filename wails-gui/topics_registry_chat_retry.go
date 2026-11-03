package main

import (
	"context"
	"log"
	"time"
)

// One immutable source collection, retried only by the registry worker. A chat
// failure does not recollect activities or make a settled display refreshing.
type topicRegistryChatWork struct {
	result   topicRegistryResult
	in       topicRegistryData
	outcomes map[string]*ActivityThreadOutcome
	cache    *topicRegistryCache
	attempts uint
}

func (s *topicsService) retryRegistryChatLocked(parent context.Context) bool {
	work := s.registryDeferredChat
	if work == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	c := s.loadRegistryCache()
	if c == nil || c != work.cache {
		s.registryDeferredChat = nil
		return false
	}
	revision, err := s.registryRevisionWithSessionFiles(ctx, &c.revision.sessionFiles)
	revision.files = c.revision.files
	if err != nil || !topicRegistryCacheMatches(c, revision, s.store.load(), s.now()) {
		s.registryDeferredChat = nil
		return false
	}
	work.attempts++
	chatCtx, chatCancel := ctx, func() {}
	if s.registryChatContext != nil {
		chatCtx, chatCancel = s.registryChatContext(ctx)
	}
	result := work.result
	in := work.in
	in.now = s.now() // Retrying is new derivation, not an extension of the old cache clock.
	result.chat, err = s.deriveChatTopics(chatCtx, result, in, work.outcomes, work.cache.revision)
	chatCancel()
	if err == nil {
		cache := *c
		cache.result = result
		cache.chatComplete = true
		cache.databaseFiles, err = s.registryDatabaseFiles(ctx)
		if err == nil {
			if s.registryBeforePublish != nil {
				s.registryBeforePublish()
			}
			err = s.publishRegistrySnapshot(ctx, c.revision, c.state, result, &cache, nil)
		}
	}
	if err == nil {
		s.registryDeferredChat = nil
		if s.registryAfterPublish != nil {
			s.registryAfterPublish()
		}
		log.Printf("[topics] today level=info stage=chat_publish chat_count=%d attempt=%d", len(result.chat), work.attempts)
		return true
	}
	log.Printf("[topics] today level=info stage=chat_deferred kind=%s attempt=%d", topicRegistryErrorKind(err), work.attempts)
	if work.attempts >= 3 {
		s.registryDeferredChat = nil
	}
	return false
}
