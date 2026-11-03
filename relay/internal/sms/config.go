package sms

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Defaults for the caps. The plan is about 30 texts a month at the Normal
// notification level; the caps leave room and bound the cost of one number.
const (
	DefaultDailyCap   = 10
	DefaultMonthlyCap = 200
	// DefaultHourlyCap bounds the texts the relay sends in an hour across all
	// hubs (hub texts and keyword answers), so a fault or abuse cannot run up
	// an unbounded bill.
	DefaultHourlyCap = 500
	// DefaultCodesPerHour bounds verification texts across all hubs, apart
	// from the sends, so code requests can neither starve sends nor be used
	// to pump texts at scale.
	DefaultCodesPerHour = 30
	// DefaultKeywordAnswersPerHour bounds the HELP and START answers across
	// all numbers, apart from the sends, so texting keywords from many
	// numbers can neither starve the hubs' texts nor pump answers at scale.
	DefaultKeywordAnswersPerHour = 20
	// DefaultCountries are the calling codes numbers may have. The US and
	// Canada share +1, which is where A2P 10DLC registration covers sending.
	// Only +1 numbers are checked further (nanpAllowed); any other calling
	// code allows every number under it, premium and satellite ranges
	// included, so adding one leans on the provider's geographic permissions.
	DefaultCountries = "+1"
)

var countryPattern = regexp.MustCompile(`^\+[1-9][0-9]{0,3}$`)

// Config is the relay's text message settings. Provider "" leaves the channel
// off: the hub routes answer 503 and the webhook 404.
//
// Key seals numbers and replies at rest. PreviousKey is set only during a
// rotation, so values sealed under the old key still open until Reseal moves
// them. HashKey keys the lookup hashes (opt-outs among them) and is never
// rotated; it defaults to Key, so a relay that sets it to its first Key
// before rotating keeps every hash.
type Config struct {
	Provider              string
	Twilio                TwilioConfig
	Key                   []byte
	PreviousKey           []byte
	HashKey               []byte
	DailyCap              int
	MonthlyCap            int
	HourlyCap             int
	CodesPerHour          int
	KeywordAnswersPerHour int
	Countries             []string
	HelpContact           string
}

// VaultKeys are the keys the vault is built from.
func (c Config) VaultKeys() VaultKeys {
	return VaultKeys{Seal: c.Key, Previous: c.PreviousKey, Hash: c.HashKey}
}

// Enabled reports whether a provider is configured.
func (c Config) Enabled() bool { return c.Provider != "" }

// LoadConfig reads ISOFORM_RELAY_SMS_* and ISOFORM_RELAY_TWILIO_* settings.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		Provider:              strings.TrimSpace(getenv("ISOFORM_RELAY_SMS_PROVIDER")),
		DailyCap:              DefaultDailyCap,
		MonthlyCap:            DefaultMonthlyCap,
		HourlyCap:             DefaultHourlyCap,
		CodesPerHour:          DefaultCodesPerHour,
		KeywordAnswersPerHour: DefaultKeywordAnswersPerHour,
		HelpContact:           strings.TrimSpace(getenv("ISOFORM_RELAY_SMS_HELP_CONTACT")),
	}
	switch c.Provider {
	case "":
		return c, nil
	case "twilio":
		c.Twilio = TwilioConfig{
			AccountSID:          strings.TrimSpace(getenv("ISOFORM_RELAY_TWILIO_ACCOUNT_SID")),
			AuthToken:           strings.TrimSpace(getenv("ISOFORM_RELAY_TWILIO_AUTH_TOKEN")),
			MessagingServiceSID: strings.TrimSpace(getenv("ISOFORM_RELAY_TWILIO_MESSAGING_SERVICE_SID")),
			WebhookURL:          strings.TrimSpace(getenv("ISOFORM_RELAY_SMS_WEBHOOK_URL")),
		}
		if err := c.Twilio.validate(); err != nil {
			return c, err
		}
	default:
		return c, errors.New("ISOFORM_RELAY_SMS_PROVIDER must be empty or twilio")
	}
	key, err := decodeKey("ISOFORM_RELAY_SMS_KEY", getenv("ISOFORM_RELAY_SMS_KEY"))
	if err != nil {
		return c, err
	}
	c.Key = key
	for _, optional := range []struct {
		name   string
		target *[]byte
	}{
		{"ISOFORM_RELAY_SMS_KEY_PREVIOUS", &c.PreviousKey},
		{"ISOFORM_RELAY_SMS_HASH_KEY", &c.HashKey},
	} {
		raw := strings.TrimSpace(getenv(optional.name))
		if raw == "" {
			continue
		}
		if *optional.target, err = decodeKey(optional.name, raw); err != nil {
			return c, err
		}
	}
	if c.PreviousKey != nil && KeyID(c.PreviousKey) == KeyID(c.Key) {
		return c, errors.New("ISOFORM_RELAY_SMS_KEY_PREVIOUS must differ from ISOFORM_RELAY_SMS_KEY")
	}
	if !validContact(c.HelpContact) {
		return c, errors.New("ISOFORM_RELAY_SMS_HELP_CONTACT must be a support email or web address of at most 60 characters")
	}
	caps := []struct {
		name     string
		target   *int
		min, max int
	}{
		{"ISOFORM_RELAY_SMS_DAILY_CAP", &c.DailyCap, 1, 100},
		{"ISOFORM_RELAY_SMS_MONTHLY_CAP", &c.MonthlyCap, 1, 3000},
		{"ISOFORM_RELAY_SMS_HOURLY_CAP", &c.HourlyCap, 1, 100000},
		{"ISOFORM_RELAY_SMS_CODES_PER_HOUR", &c.CodesPerHour, 1, 10000},
		{"ISOFORM_RELAY_SMS_KEYWORD_ANSWERS_PER_HOUR", &c.KeywordAnswersPerHour, 1, 1000},
	}
	for _, limit := range caps {
		raw := strings.TrimSpace(getenv(limit.name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < limit.min || value > limit.max {
			return c, fmt.Errorf("%s must be a whole number from %d to %d", limit.name, limit.min, limit.max)
		}
		*limit.target = value
	}
	countries := strings.TrimSpace(getenv("ISOFORM_RELAY_SMS_COUNTRIES"))
	if countries == "" {
		countries = DefaultCountries
	}
	for _, code := range strings.Split(countries, ",") {
		code = strings.TrimSpace(code)
		if !countryPattern.MatchString(code) {
			return c, errors.New("ISOFORM_RELAY_SMS_COUNTRIES must list calling codes such as +1,+44")
		}
		c.Countries = append(c.Countries, code)
	}
	return c, nil
}

// decodeKey accepts 32 bytes as standard or URL base64, padded or not.
func decodeKey(name, raw string) ([]byte, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "=")
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if key, err := encoding.DecodeString(raw); err == nil && len(key) == KeySize {
			return key, nil
		}
	}
	return nil, errors.New(name + " must be 32 random bytes in base64 (openssl rand -base64 32)")
}

func validContact(contact string) bool {
	if contact == "" || len(contact) > 60 {
		return false
	}
	for _, r := range contact {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return strings.Contains(contact, "@") || strings.Contains(contact, ".")
}

// NewProvider builds the configured provider.
func NewProvider(c Config) (Provider, error) {
	switch c.Provider {
	case "twilio":
		return NewTwilio(c.Twilio)
	default:
		return nil, errors.New("no SMS provider is configured")
	}
}

// allowedNumber reports whether the number's calling code is configured. A
// +1 number must also be a well-formed US or Canadian number (see nanpAllowed).
// Other calling codes are a plain prefix match with no premium-rate filter.
func (c Config) allowedNumber(number string) bool {
	for _, code := range c.Countries {
		if !strings.HasPrefix(number, code) {
			continue
		}
		if strings.HasPrefix(number, "+1") {
			return nanpAllowed(number)
		}
		return true
	}
	return false
}

// nanpPattern is +1, an area code and an exchange that start with 2 to 9, and
// four more digits.
var nanpPattern = regexp.MustCompile(`^\+1[2-9][0-9]{2}[2-9][0-9]{6}$`)

// nanpBlocked are +1 area codes outside the US and Canada (Caribbean and
// Atlantic countries, where texts cost more and are a common target of SMS
// pumping) and non-geographic codes (toll-free, personal, premium, carrier
// and government services, and 456 for inbound international traffic) that a
// person's phone does not have. US territories stay allowed. The provider's geographic permissions are the
// backstop for anything this list misses.
var nanpBlocked = map[string]bool{
	"242": true, "246": true, "264": true, "268": true, "284": true, "345": true, "441": true, "473": true,
	"649": true, "658": true, "664": true, "721": true, "758": true, "767": true, "784": true, "809": true,
	"829": true, "849": true, "868": true, "869": true, "876": true, "456": true,
	"500": true, "521": true, "522": true, "523": true, "524": true, "525": true, "526": true, "527": true,
	"528": true, "529": true, "532": true, "533": true, "535": true, "538": true, "542": true, "543": true,
	"544": true, "545": true, "546": true, "547": true, "549": true, "550": true, "552": true, "553": true,
	"554": true, "556": true, "558": true, "566": true, "569": true, "577": true, "578": true, "588": true,
	"589": true, "600": true, "622": true, "700": true, "710": true, "800": true, "822": true, "833": true,
	"844": true, "855": true, "866": true, "877": true, "880": true, "881": true, "882": true, "883": true,
	"884": true, "885": true, "886": true, "887": true, "888": true, "889": true, "900": true,
}

func nanpAllowed(number string) bool {
	if !nanpPattern.MatchString(number) {
		return false
	}
	area := number[2:5]
	// N11 codes are service codes and N9X codes are reserved; neither is an area code.
	if area[1:] == "11" || area[1] == '9' {
		return false
	}
	return !nanpBlocked[area]
}
