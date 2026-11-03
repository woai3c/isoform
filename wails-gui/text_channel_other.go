//go:build !darwin

package main

import "context"

// The text channel sends through Messages.app, which only macOS has. On other
// platforms the sender reports itself unavailable, so the channel is never
// offered to the Engagement router and the settings card shows it as
// unsupported.

type unavailableTextSender struct{}

func (unavailableTextSender) Send(context.Context, string, string) error {
	return errTextChannelUnsupported
}

func defaultTextSender() textSender {
	return unavailableTextSender{}
}

func textSenderSupported() bool {
	return false
}

func textMessagesAutomation(bool) textAutomationState {
	return textAutomationNotAllowed
}

func prepareMessagesSend() error { return errTextChannelUnsupported }
