package main

import (
	"context"
	"time"
)

// GetSMSChannelStatus reports the text subscription. It asks the relay when
// the connection is on; otherwise, or when the relay cannot be reached, it
// returns the saved status with the reason in lastError.
func (a *App) GetSMSChannelStatus() (perfValue SMSChannelStatus, perfErr error) {
	defer logPageBinding("GetSMSChannelStatus", time.Now(), &perfValue)
	return a.smsChannel().Refresh(context.Background())
}

// StartSMSVerification asks the relay to text a code to the number (E.164;
// spaces, dashes, dots and parentheses are dropped). A refusal is one error
// code, such as invalid_number or opted_out.
func (a *App) StartSMSVerification(number string) (SMSChannelStatus, error) {
	return a.smsChannel().Start(context.Background(), number)
}

// ConfirmSMSVerification sends the 6-digit code the user received.
func (a *App) ConfirmSMSVerification(code string) (SMSChannelStatus, error) {
	return a.smsChannel().Confirm(context.Background(), code)
}

// StopSMSChannel removes the text subscription and forgets the number.
func (a *App) StopSMSChannel() (SMSChannelStatus, error) {
	return a.smsChannel().Stop(context.Background())
}
