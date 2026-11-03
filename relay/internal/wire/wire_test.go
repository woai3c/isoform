package wire

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sealed(t *testing.T, n int) string {
	t.Helper()
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestEnvelopeKindsDirectionsAndSizes(t *testing.T) {
	hub, dev := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name string
		env  Envelope
		ok   bool
	}{
		{"request", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 1, Kind: "req", CT: sealed(t, 64)}, true},
		{"response", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "h2d", Seq: 2, Kind: "res", CT: sealed(t, 64)}, true},
		{"event", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "h2d", Seq: 3, Kind: "event", CT: sealed(t, 64)}, true},
		{"brief near limit", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "h2d", Seq: 4, Kind: "brief", CT: sealed(t, MaxBriefPlaintext+AEADTag-1)}, true},
		{"request at 64 KiB", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 5, Kind: "req", CT: sealed(t, MaxMessagePlaintext+AEADTag)}, true},
		{"request above 64 KiB", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 10, Kind: "req", CT: sealed(t, MaxMessagePlaintext+AEADTag+1)}, false},
		{"brief above 256 KiB", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "h2d", Seq: 6, Kind: "brief", CT: sealed(t, MaxBriefPlaintext+AEADTag+3)}, false},
		{"request sent h2d", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "h2d", Seq: 7, Kind: "req", CT: sealed(t, 64)}, false},
		{"response sent d2h", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 8, Kind: "res", CT: sealed(t, 64)}, false},
		{"brief sent d2h", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 9, Kind: "brief", CT: sealed(t, 64)}, false},
		{"zero sequence", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 0, Kind: "req", CT: sealed(t, 64)}, false},
		{"sequence beyond JSON-safe range", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: MaxSeq + 1, Kind: "req", CT: sealed(t, 64)}, false},
		{"unknown version", Envelope{V: 2, Hub: hub, Dev: dev, Dir: "d2h", Seq: 1, Kind: "req", CT: sealed(t, 64)}, false},
		{"upper-case hub", Envelope{V: 1, Hub: strings.ToUpper(hub), Dev: dev, Dir: "d2h", Seq: 1, Kind: "req", CT: sealed(t, 64)}, false},
		{"padded ciphertext", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 1, Kind: "req", CT: sealed(t, 64) + "="}, false},
		{"ciphertext of the tag only", Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 1, Kind: "req", CT: sealed(t, AEADTag)}, false},
	} {
		err := tc.env.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: valid=%t, want %t (%v)", tc.name, err == nil, tc.ok, err)
		}
	}
}

func TestParseEnvelopeIsStrict(t *testing.T) {
	hub, dev := uuid.NewString(), uuid.NewString()
	valid := Envelope{V: 1, Hub: hub, Dev: dev, Dir: "d2h", Seq: 1, Kind: "req", CT: sealed(t, 64)}
	raw, err := valid.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseEnvelope(raw); err != nil {
		t.Fatalf("canonical envelope rejected: %v", err)
	}
	withExtra := append(bytes.TrimSuffix(raw, []byte("}")), []byte(`,"extra":true}`)...)
	for _, bad := range [][]byte{withExtra, append(raw, []byte(" {}")...), []byte("[]"), []byte("null"), []byte("")} {
		if _, err = ParseEnvelope(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestTimesAreStrictRFC3339Seconds(t *testing.T) {
	for _, good := range []string{"2026-10-09T07:00:00Z", "2027-01-07T07:00:00Z"} {
		if _, err := ParseTime(good); err != nil {
			t.Errorf("rejected %s: %v", good, err)
		}
	}
	for _, bad := range []string{"2026-10-09T07:00:00.000Z", "2026-10-09T07:00:00+00:00", "2026-10-09 07:00:00Z", "2026-10-09T07:00Z", "yesterday", ""} {
		if _, err := ParseTime(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	when := time.Date(2026, 10, 9, 7, 0, 0, 900_000_000, time.FixedZone("PDT", -7*3600))
	if got := FormatTime(when); got != "2026-10-09T14:00:00Z" {
		t.Fatalf("format %s", got)
	}
}

func TestTokensDigestsAndScopes(t *testing.T) {
	good := sealed(t, 32)
	if len(good) != 43 || !ValidToken(good) {
		t.Fatalf("32-byte token %q not accepted", good)
	}
	for _, bad := range []string{good + "A", good[:42], good[:42] + "+", good[:42] + "="} {
		if ValidToken(bad) {
			t.Errorf("accepted token %q", bad)
		}
	}
	if !ValidDigest(Digest("x")) || ValidDigest(strings.ToUpper(Digest("x"))) {
		t.Fatal("digest format wrong")
	}
	if !ValidScopes([]string{"brief:read", "push"}) || ValidScopes([]string{"push", "push"}) || ValidScopes([]string{"Root"}) || ValidScopes(make([]string, 17)) {
		t.Fatal("scope validation wrong")
	}
	if token, ok := ValidPushToken(strings.Repeat("AB", 32)); !ok || token != strings.Repeat("ab", 32) {
		t.Fatal("push token not normalized")
	}
	for _, bad := range []string{"", strings.Repeat("a", 63), strings.Repeat("g", 64), strings.Repeat("a", 402)} {
		if _, ok := ValidPushToken(bad); ok {
			t.Errorf("accepted push token %q", bad)
		}
	}
}

func TestDecodeStrictRejectsTrailingAndUnknown(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}
	if err := DecodeStrict(strings.NewReader(`{"a":1}`), &v); err != nil || v.A != 1 {
		t.Fatalf("valid object: %v", err)
	}
	for _, bad := range []string{`{"a":1} {}`, `{"a":1,"b":2}`, `{"a":1}x`, ``} {
		if err := DecodeStrict(strings.NewReader(bad), &v); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
