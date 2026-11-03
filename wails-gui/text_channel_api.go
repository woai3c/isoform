package main

import (
	"context"
	"log"
	"sync"
	"time"
)

var (
	sharedTextChannelOnce sync.Once
	sharedTextChannelInst *textChannel
	textChannelConfigured sync.Once
)

// sharedTextChannel is the app's text channel, stored under ~/.yansu-agent.
// Without a home directory it still exists, and every call reports the storage
// error, so the channel is simply unavailable.
func sharedTextChannel() *textChannel {
	sharedTextChannelOnce.Do(func() {
		dir, err := textChannelDir()
		if err != nil {
			log.Printf("[text-channel] no home directory: %v", err)
		}
		sharedTextChannelInst = newTextChannel(dir)
	})
	return sharedTextChannelInst
}

func (a *App) textChannel() *textChannel {
	c := sharedTextChannel()
	textChannelConfigured.Do(func() { c.lang = a.currentUILanguage })
	return c
}

// GetTextChannelStatus reports the text message settings and recent sends.
func (a *App) GetTextChannelStatus() (perfValue TextChannelStatus, perfErr error) {
	defer logPageBinding("GetTextChannelStatus", time.Now(), &perfValue)
	return a.textChannel().Status()
}

// SetTextChannel saves the text message settings. Turning the channel on needs
// a number in E.164 form and accepted consent. AcceptConsent records the
// consent time for that number.
func (a *App) SetTextChannel(input TextChannelInput) (TextChannelStatus, error) {
	return a.textChannel().SetConfig(input)
}

// SendTestText sends one fixed test text to the saved number. It does not count
// toward the daily cap or the cooldown, and it allows one attempt a minute.
func (a *App) SendTestText() (TextChannelStatus, error) {
	return a.textChannel().SendTest(context.Background())
}

// StartTextVerification sends a code through Messages after consent and access.
func (a *App) StartTextVerification() (TextChannelStatus, error) {
	return a.textChannel().StartVerification(context.Background())
}

// ConfirmTextVerification verifies the saved number with the received code.
func (a *App) ConfirmTextVerification(code string) (TextChannelStatus, error) {
	return a.textChannel().ConfirmVerification(code)
}
