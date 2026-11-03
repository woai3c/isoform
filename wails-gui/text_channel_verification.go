package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const textVerificationTTL = 10 * time.Minute

var (
	errTextVerificationWrong = errors.New("the text verification code is not right")
	errTextVerificationSpent = errors.New("the text verification code expired or was tried too often")
	errTextVerificationSoon  = errors.New("wait a minute before requesting another text verification code")
)

// StartVerification is an explicit Settings action. Consent and Automation
// precede this one send; neither a status read nor enabling texts sends a code.
// Only a salted hash is saved, and attempts and resends remain bounded on restart.
func (c *textChannel) StartVerification(ctx context.Context) (TextChannelStatus, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	cfg, err := loadTextChannelConfig(c.dir)
	if err != nil {
		return c.statusWith(err)
	}
	if cfg.Number == "" {
		return c.statusWith(errTextChannelNoNumber)
	}
	if cfg.ConsentAcceptedAt.IsZero() {
		return c.statusWith(errTextChannelNoConsent)
	}
	if !c.supported() {
		return c.statusWith(errTextChannelUnsupported)
	}
	if !c.automationAllowed(false) {
		return c.statusWith(errTextMessagesNotAllowed)
	}
	state, err := c.readState()
	if err != nil {
		return c.statusWith(err)
	}
	now := c.now()
	if !state.LastVerificationAt.IsZero() && now.Sub(state.LastVerificationAt) < time.Minute {
		return c.statusWith(errTextVerificationSoon)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return c.statusWith(errTextChannelStorage)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return c.statusWith(errTextChannelStorage)
	}
	hash := textVerificationHash(salt, cfg.Number, code)
	// Reserve before sending: a failed or ambiguous send is never retried here.
	if err := c.mutateState(func(s *textChannelState) bool {
		s.LastVerificationAt = now
		s.VerificationNumber = cfg.Number
		s.VerificationSalt, s.VerificationHash = salt, hash
		s.VerificationExpiresAt = now.Add(textVerificationTTL)
		s.VerificationAttempts = 0
		s.LastError = ""
		return true
	}); err != nil {
		return c.statusWith(err)
	}
	body := "Isoform: your verification code is " + code + ". Enter it in Settings on your computer."
	if c.chinese() {
		body = "Isoform：你的验证码是 " + code + "。请在电脑上的设置中输入。"
	}
	if err := c.sender.Send(ctx, cfg.Number, body); err != nil {
		c.recordFailure(err)
		_ = c.mutateState(func(s *textChannelState) bool { s.VerificationHash = nil; return true })
		return c.statusWith(err)
	}
	return c.statusWith(nil)
}

func textVerificationHash(salt []byte, number, code string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(number + "\x00" + code))
	return h.Sum(nil)
}

// ConfirmVerification binds the code to the saved number, and consumes the
// challenge. A number edit cannot inherit either verification or consent.
func (c *textChannel) ConfirmVerification(code string) (TextChannelStatus, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	cfg, err := loadTextChannelConfig(c.dir)
	if err != nil {
		return c.statusWith(err)
	}
	var refusal error
	err = c.mutateState(func(s *textChannelState) bool {
		if cfg.Number == "" || cfg.ConsentAcceptedAt.IsZero() || s.VerificationNumber != cfg.Number ||
			!s.VerificationExpiresAt.After(c.now()) || len(s.VerificationHash) == 0 || s.VerificationAttempts >= 5 {
			refusal = errTextVerificationSpent
			return false
		}
		s.VerificationAttempts++
		if subtle.ConstantTimeCompare(textVerificationHash(s.VerificationSalt, cfg.Number, strings.TrimSpace(code)), s.VerificationHash) != 1 {
			refusal = errTextVerificationWrong
			return true
		}
		s.VerificationHash = nil
		s.SendBlocked = false
		s.LastError = ""
		return true
	})
	if err != nil {
		return c.statusWith(err)
	}
	if refusal != nil {
		return c.statusWith(refusal)
	}
	cfg.VerifiedAt = c.now()
	if err := saveTextChannelConfig(c.dir, cfg); err != nil {
		return c.statusWith(err)
	}
	return c.statusWith(nil)
}
