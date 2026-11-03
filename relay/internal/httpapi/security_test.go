package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBrowserOriginsAreRefused(t *testing.T) {
	r := newRelay(t, "")
	origin := http.Header{"Origin": {"https://evil.example"}}
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/v1/health"},
		{http.MethodGet, "/v1/presence"},
		{http.MethodPost, "/v1/requests"},
		{http.MethodGet, "/v1/nothing-here"},
	} {
		resp := r.call(probe.method, probe.path, origin, nil)
		r.expectStatus(resp, http.StatusForbidden, "origin on "+probe.path)
	}
	if _, resp, err := r.dialHub(uuid.NewString(), randomToken(t), origin); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatal("hub socket accepted a browser origin")
	}
}

func TestJSONIsStrictAndBounded(t *testing.T) {
	r := newRelay(t, "")
	dev := r.pairDevice(r.connectHub(uuid.NewString(), randomToken(t)), allScopes)
	valid := requestEnvelope(t, dev, 1)
	raw := envelopeJSON(t, valid)
	withExtra := strings.TrimSuffix(string(raw), "}") + `,"extra":1}`
	post := func(body string, contentType string) response {
		header := bearer(dev.bearer)
		if contentType != "" {
			header.Set("Content-Type", contentType)
		}
		return r.call(http.MethodPost, "/v1/requests", header, []byte(body))
	}
	r.expectStatus(post(withExtra, ""), http.StatusBadRequest, "unknown field")
	r.expectStatus(post(string(raw)+" {}", ""), http.StatusBadRequest, "two JSON values")
	r.expectStatus(post("", ""), http.StatusBadRequest, "empty body")
	r.expectStatus(post(string(raw), "text/plain"), http.StatusUnsupportedMediaType, "text body")
	r.expectStatus(post(`{"ct":"`+strings.Repeat("a", 70<<10)+`"}`, ""), http.StatusRequestEntityTooLarge, "body over 64 KiB")
	r.expectStatus(r.call(http.MethodPost, "/v1/responses/ack", bearer(dev.bearer), []byte(`{"upTo":-1}`)), http.StatusBadRequest, "negative cursor")
	r.expectStatus(r.call(http.MethodPost, "/v1/responses/ack", bearer(dev.bearer), []byte(`{}`)), http.StatusBadRequest, "missing cursor")
	r.expectStatus(post(string(raw), ""), http.StatusAccepted, "valid request after refusals")
}

func TestRoutesAndMethodsAnswerWithEnvelopes(t *testing.T) {
	r := newRelay(t, "")
	r.expectStatus(r.call(http.MethodGet, "/v1/unknown", nil, nil), http.StatusNotFound, "unknown route")
	r.expectStatus(r.call(http.MethodPost, "/v1/health", nil, []byte(`{}`)), http.StatusMethodNotAllowed, "wrong method on health")
	r.expectStatus(r.call(http.MethodPut, "/v1/requests", nil, nil), http.StatusMethodNotAllowed, "wrong method on requests")
	r.expectStatus(r.call(http.MethodGet, "/v1/pairings/"+uuid.NewString()+"/claim", nil, nil), http.StatusMethodNotAllowed, "poll path used for claim")
	r.expectStatus(r.call(http.MethodGet, "/v1/pairings/x/y/z", nil, nil), http.StatusNotFound, "deep pairing path")
	r.expectStatus(r.call(http.MethodPost, "/v1/pairings/not-a-uuid/claim", nil, []byte(`{}`)), http.StatusNotFound, "malformed pairing id")
	health := r.call(http.MethodGet, "/v1/health", nil, nil)
	r.expectStatus(health, http.StatusOK, "health")
	if service := rawField(t, health.Data, "service"); string(service) != `"isoform-relay"` {
		t.Fatalf("health service %s", service)
	}
	if protocol := rawField(t, health.Data, "protocolVersion"); string(protocol) != "1" {
		t.Fatalf("health protocol %s", protocol)
	}
}
