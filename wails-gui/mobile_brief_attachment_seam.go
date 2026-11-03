package main

import (
	"context"
	"errors"
)

// errMobileAttachmentOffline reports that no paired phone can receive an
// attachment right now (the connection is off or no relay is configured).
// The audio then stays on this Mac, and the brief says so by carrying no
// attachment.
var errMobileAttachmentOffline = errors.New("no paired phone can receive attachments")

// mobileBriefAudioSealer uploads a brief's spoken version for paired phones.
// mobile_attachment.go installs the real one; without it the audio stays on
// this Mac.
var mobileBriefAudioSealer func(ctx context.Context, a *App, briefID, path string) (*MobileBriefAudioAttachment, error)

// sealMobileBriefAudio hands the stored audio to the relay transport. It
// reports errMobileAttachmentOffline when no phone can receive it.
func (a *App) sealMobileBriefAudio(ctx context.Context, briefID, path string) (*MobileBriefAudioAttachment, error) {
	if a == nil || mobileBriefAudioSealer == nil {
		return nil, errMobileAttachmentOffline
	}
	return mobileBriefAudioSealer(ctx, a, briefID, path)
}
