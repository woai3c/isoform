package email

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Defaults for the caps. A hub sends a few results a day; the caps bound what
// one hub, or a fault, can cost.
const (
	DefaultDailyCap     = 20
	DefaultHourlyCap    = 300
	DefaultCodesPerHour = 30
	// DefaultCodesPerDay bounds verification emails across all hubs in a
	// day, so free hub ids cannot turn the relay into a mailer of codes.
	DefaultCodesPerDay = 200
	// KeySize is the length of the relay's email key.
	KeySize = 32
)

// Config is the relay's email settings. Provider "" leaves the channel off:
// the hub routes answer 503 not_configured.
type Config struct {
	Provider string
	SMTP     SMTPConfig
	// Key seals addresses at rest and keys their lookup hashes.
	Key          []byte
	DailyCap     int
	HourlyCap    int
	CodesPerHour int
	CodesPerDay  int
}

// Enabled reports whether a provider is configured.
func (c Config) Enabled() bool { return c.Provider != "" }

// LoadConfig reads the email settings from the environment
// (docs/email-channel.md lists them).
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		Provider:     strings.ToLower(strings.TrimSpace(getenv("ISOFORM_RELAY_EMAIL_PROVIDER"))),
		DailyCap:     DefaultDailyCap,
		HourlyCap:    DefaultHourlyCap,
		CodesPerHour: DefaultCodesPerHour,
		CodesPerDay:  DefaultCodesPerDay,
	}
	for name, target := range map[string]*int{
		"ISOFORM_RELAY_EMAIL_DAILY_CAP":      &c.DailyCap,
		"ISOFORM_RELAY_EMAIL_HOURLY_CAP":     &c.HourlyCap,
		"ISOFORM_RELAY_EMAIL_CODES_PER_HOUR": &c.CodesPerHour,
		"ISOFORM_RELAY_EMAIL_CODES_PER_DAY":  &c.CodesPerDay,
	} {
		if raw := strings.TrimSpace(getenv(name)); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return Config{}, fmt.Errorf("%s must be a positive whole number", name)
			}
			*target = n
		}
	}
	switch c.Provider {
	case "":
		return c, nil
	case "smtp":
	default:
		return Config{}, errors.New("ISOFORM_RELAY_EMAIL_PROVIDER must be smtp or empty")
	}
	key, err := decodeKey(getenv("ISOFORM_RELAY_EMAIL_KEY"))
	if err != nil {
		return Config{}, err
	}
	c.Key = key
	port := 587
	if raw := strings.TrimSpace(getenv("ISOFORM_RELAY_SMTP_PORT")); raw != "" {
		if port, err = strconv.Atoi(raw); err != nil {
			return Config{}, errors.New("ISOFORM_RELAY_SMTP_PORT must be a port number")
		}
	}
	c.SMTP = SMTPConfig{
		Host:     strings.TrimSpace(getenv("ISOFORM_RELAY_SMTP_HOST")),
		Port:     port,
		Username: strings.TrimSpace(getenv("ISOFORM_RELAY_SMTP_USERNAME")),
		Password: getenv("ISOFORM_RELAY_SMTP_PASSWORD"),
		From:     strings.TrimSpace(getenv("ISOFORM_RELAY_EMAIL_FROM")),
		Mode:     strings.ToLower(strings.TrimSpace(getenv("ISOFORM_RELAY_SMTP_MODE"))),
	}
	if _, err := NewSMTP(c.SMTP); err != nil {
		return Config{}, err
	}
	return c, nil
}

// decodeKey accepts 32 bytes as standard or URL base64, padded or not.
func decodeKey(raw string) ([]byte, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "=")
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if key, err := encoding.DecodeString(raw); err == nil && len(key) == KeySize {
			return key, nil
		}
	}
	return nil, errors.New("ISOFORM_RELAY_EMAIL_KEY must be 32 random bytes in base64 (openssl rand -base64 32)")
}

// NewProvider builds the configured provider.
func NewProvider(c Config) (Provider, error) {
	switch c.Provider {
	case "smtp":
		return NewSMTP(c.SMTP)
	default:
		return nil, errors.New("no email provider is configured")
	}
}
