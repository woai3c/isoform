package main

import (
	"context"
	"sync"

	"wails-gui/internal/handoff"
)

type mainChatReadMemo struct {
	mu sync.Mutex

	handoffsLoaded bool
	handoffs       []handoff.Entry
	handoffsErr    error
	pausesLoaded   bool
	pauses         []observationPause
	pausesErr      error
	mailLoaded     bool
	mail           []string
	mailErr        error
	threadSource   map[string]bool
	cardSource     map[engagementCardReadReceipt]bool
}

type mainChatReadMemoKey struct{}

var mainChatReadMemoLoad func(kind string)

func recordMainChatReadMemoLoad(kind string) {
	if mainChatReadMemoLoad != nil {
		mainChatReadMemoLoad(kind)
	}
}

func withMainChatReadMemo(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if mainChatReadMemoFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, mainChatReadMemoKey{}, &mainChatReadMemo{
		threadSource: make(map[string]bool),
		cardSource:   make(map[engagementCardReadReceipt]bool),
	})
}

func mainChatReadMemoFrom(ctx context.Context) *mainChatReadMemo {
	if ctx == nil {
		return nil
	}
	memo, _ := ctx.Value(mainChatReadMemoKey{}).(*mainChatReadMemo)
	return memo
}

func (a *App) handoffEntries(ctx context.Context) ([]handoff.Entry, error) {
	memo := mainChatReadMemoFrom(ctx)
	if memo == nil {
		entries, err := a.handoffStore.List()
		recordMainChatReadMemoLoad("handoffs")
		return entries, err
	}
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if !memo.handoffsLoaded {
		memo.handoffs, memo.handoffsErr = a.handoffStore.List()
		memo.handoffsLoaded = true
		recordMainChatReadMemoLoad("handoffs")
	}
	return memo.handoffs, memo.handoffsErr
}

func mobileBriefMailSubjectsContext(ctx context.Context, a *App) ([]string, error) {
	memo := mainChatReadMemoFrom(ctx)
	if memo == nil {
		subjects, err := mobileBriefMailSubjects(a)
		recordMainChatReadMemoLoad("mail")
		return subjects, err
	}
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if !memo.mailLoaded {
		memo.mail, memo.mailErr = mobileBriefMailSubjects(a)
		memo.mailLoaded = true
		recordMainChatReadMemoLoad("mail")
	}
	return memo.mail, memo.mailErr
}

func (memo *mainChatReadMemo) threadSourceReadable(sourceID string, load func() bool) bool {
	memo.mu.Lock()
	readable, ok := memo.threadSource[sourceID]
	memo.mu.Unlock()
	if ok {
		return readable
	}
	recordMainChatReadMemoLoad("threadSource")
	readable = load()
	memo.mu.Lock()
	if memo.threadSource == nil {
		memo.threadSource = make(map[string]bool)
	}
	if cached, exists := memo.threadSource[sourceID]; exists {
		readable = cached
	} else {
		memo.threadSource[sourceID] = readable
	}
	memo.mu.Unlock()
	return readable
}

func (memo *mainChatReadMemo) cardSourceReadable(record engagementCardReadReceipt, load func() bool) bool {
	memo.mu.Lock()
	readable, ok := memo.cardSource[record]
	memo.mu.Unlock()
	if ok {
		return readable
	}
	recordMainChatReadMemoLoad("cardSource")
	readable = load()
	memo.mu.Lock()
	if memo.cardSource == nil {
		memo.cardSource = make(map[engagementCardReadReceipt]bool)
	}
	if cached, exists := memo.cardSource[record]; exists {
		readable = cached
	} else {
		memo.cardSource[record] = readable
	}
	memo.mu.Unlock()
	return readable
}
