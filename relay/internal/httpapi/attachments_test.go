package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/store"
)

// getRaw fetches a path and returns the status, headers and raw body. Attachment
// downloads are bytes, not the JSON envelope the shared call helper decodes.
func (r *relay) getRaw(path, token string) (int, http.Header, []byte) {
	r.t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.srv.URL+path, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.srv.Client().Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		r.t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, raw
}

// expectRawStatus checks a raw download's status.
func (r *relay) expectRawStatus(path, token string, want int, context string) {
	r.t.Helper()
	status, _, _ := r.getRaw(path, token)
	if status != want {
		r.t.Fatalf("%s: status %d want %d", context, status, want)
	}
}

// putAttachment uploads a body as the hub.
func (r *relay) putAttachment(hubID, credential, id string, body []byte, contentType string) response {
	r.t.Helper()
	header := bearer(credential)
	header.Set("X-Yansu-Hub", hubID)
	header.Set("Content-Type", contentType)
	return r.call(http.MethodPut, "/v1/hub/attachments/"+id, header, body)
}

// lowerAttachmentQuotas shrinks the store quotas for one test.
func lowerAttachmentQuotas(t *testing.T, perHub, total int64) {
	t.Helper()
	oldHub, oldTotal := store.MaxAttachmentBytesPerHub, store.MaxAttachmentStoreBytes
	store.MaxAttachmentBytesPerHub, store.MaxAttachmentStoreBytes = perHub, total
	t.Cleanup(func() { store.MaxAttachmentBytesPerHub, store.MaxAttachmentStoreBytes = oldHub, oldTotal })
}

const octetStream = "application/octet-stream"

func TestAttachmentRoundTripBetweenHubAndItsDevice(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	dev := r.pairDevice(hc, []string{"brief:read", "push"})
	id := uuid.NewString()
	body := randomBytes(t, 4096)

	resp := r.putAttachment(hubID, credential, id, body, octetStream)
	r.expectStatus(resp, http.StatusCreated, "put attachment")
	var data struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil || data.ID != id {
		t.Fatalf("put answer %s %v", resp.Data, err)
	}
	stored := filepath.Join(r.dir, "attachments", hubID, id)
	if info, err := os.Stat(stored); err != nil || info.Size() != int64(len(body)) || info.Mode().Perm() != 0600 {
		t.Fatalf("attachment body on disk: %v %v", info, err)
	}

	status, header, raw := r.getRaw("/v1/attachments/"+id, dev.bearer)
	if status != http.StatusOK || !bytes.Equal(raw, body) {
		t.Fatalf("get attachment: status %d, %d bytes (want %d)", status, len(raw), len(body))
	}
	if header.Get("Content-Type") != octetStream || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("attachment headers %v", header)
	}

	// A re-upload under the same id replaces the body.
	replaced := randomBytes(t, 2048)
	r.expectStatus(r.putAttachment(hubID, credential, id, replaced, octetStream), http.StatusCreated, "replace attachment")
	if _, _, again := r.getRaw("/v1/attachments/"+id, dev.bearer); !bytes.Equal(again, replaced) {
		t.Fatal("the replaced body was not served")
	}
}

func TestAttachmentIsScopedToTheOwningHubAndTheBriefScope(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	reader := r.pairDevice(hc, []string{"brief:read"})
	noScope := r.pairDevice(hc, []string{"push"})
	otherHub := r.connectHub(uuid.NewString(), randomToken(t))
	stranger := r.pairDevice(otherHub, []string{"brief:read"})
	id := uuid.NewString()
	r.expectStatus(r.putAttachment(hubID, credential, id, randomBytes(t, 64), octetStream), http.StatusCreated, "put")

	r.expectRawStatus("/v1/attachments/"+id, reader.bearer, http.StatusOK, "owner device")
	r.expectStatus(r.getWith("/v1/attachments/"+id, noScope.bearer), http.StatusForbidden, "device without brief:read")
	r.expectStatus(r.getWith("/v1/attachments/"+id, stranger.bearer), http.StatusNotFound, "device of another hub")
	r.expectStatus(r.getWith("/v1/attachments/"+uuid.NewString(), reader.bearer), http.StatusNotFound, "unknown id")
	r.expectStatus(r.getWith("/v1/attachments/not-a-uuid", reader.bearer), http.StatusNotFound, "malformed id")
	r.expectStatus(r.call(http.MethodGet, "/v1/attachments/"+id, nil, nil), http.StatusUnauthorized, "no bearer")
	r.expectStatus(r.call(http.MethodPost, "/v1/attachments/"+id, bearer(reader.bearer), nil), http.StatusMethodNotAllowed, "wrong method")
}

func TestAttachmentUploadRequiresTheHubCredentialAndBounds(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	r.pairDevice(hc, []string{"brief:read"})
	id := uuid.NewString()

	r.expectStatus(r.putAttachment(hubID, randomToken(t), id, randomBytes(t, 16), octetStream), http.StatusUnauthorized, "wrong credential")
	r.expectStatus(r.putAttachment("not-a-hub", credential, id, randomBytes(t, 16), octetStream), http.StatusUnauthorized, "bad hub id")
	r.expectStatus(r.putAttachment(hubID, credential, "bad-id", randomBytes(t, 16), octetStream), http.StatusNotFound, "bad attachment id")
	r.expectStatus(r.putAttachment(hubID, credential, id, randomBytes(t, 16), "application/json"), http.StatusUnsupportedMediaType, "wrong content type")
	r.expectStatus(r.putAttachment(hubID, credential, id, nil, octetStream), http.StatusRequestEntityTooLarge, "empty body")
	r.expectStatus(r.putAttachment(hubID, credential, id, make([]byte, store.MaxAttachmentBytes+1), octetStream), http.StatusRequestEntityTooLarge, "oversized body")
	if entries, _ := os.ReadDir(filepath.Join(r.dir, "attachments", hubID)); len(entries) != 0 {
		t.Fatalf("a refused upload must leave no file behind, found %d", len(entries))
	}
	header := bearer(credential)
	header.Set("X-Yansu-Hub", hubID)
	r.expectStatus(r.call(http.MethodGet, "/v1/hub/attachments/"+id, header, nil), http.StatusMethodNotAllowed, "wrong method")

	// Another hub cannot take over an id.
	r.expectStatus(r.putAttachment(hubID, credential, id, randomBytes(t, 16), octetStream), http.StatusCreated, "put")
	otherHub, otherCredential := uuid.NewString(), randomToken(t)
	otherClient := r.connectHub(otherHub, otherCredential)
	r.pairDevice(otherClient, []string{"brief:read"})
	r.expectStatus(r.putAttachment(otherHub, otherCredential, id, randomBytes(t, 16), octetStream), http.StatusConflict, "foreign id")
}

func TestAttachmentUploadNeedsAKnownHubWithAReader(t *testing.T) {
	r := newRelay(t, "")

	// A hub the relay has never seen is not created by an upload.
	unknown, unknownCredential := uuid.NewString(), randomToken(t)
	r.expectStatus(r.putAttachment(unknown, unknownCredential, uuid.NewString(), randomBytes(t, 16), octetStream), http.StatusUnauthorized, "unknown hub")
	if _, err := r.store.HubLastSeen(context.Background(), unknown); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the upload must not create the hub: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "attachments")); !os.IsNotExist(err) {
		t.Fatal("a refused upload must not create the attachments directory")
	}

	// A known hub without a device that reads briefs cannot store anything.
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	r.expectStatus(r.putAttachment(hubID, credential, uuid.NewString(), randomBytes(t, 16), octetStream), http.StatusForbidden, "hub without devices")
	r.pairDevice(hc, []string{"push"})
	r.expectStatus(r.putAttachment(hubID, credential, uuid.NewString(), randomBytes(t, 16), octetStream), http.StatusForbidden, "device without brief:read")
	r.pairDevice(hc, []string{"brief:read"})
	r.expectStatus(r.putAttachment(hubID, credential, uuid.NewString(), randomBytes(t, 16), octetStream), http.StatusCreated, "hub with a reader")
}

func TestAttachmentUploadsAreRateLimitedPerHub(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	r.pairDevice(hc, []string{"brief:read"})
	for i := 0; i < attachmentUploadsPerHub; i++ {
		r.expectStatus(r.putAttachment(hubID, credential, uuid.NewString(), randomBytes(t, 8), octetStream), http.StatusCreated, "upload within the limit")
	}
	limited := r.putAttachment(hubID, credential, uuid.NewString(), randomBytes(t, 8), octetStream)
	r.expectStatus(limited, http.StatusTooManyRequests, "upload over the limit")
	if limited.Header.Get("Retry-After") == "" {
		t.Fatal("a limited upload must say when to retry")
	}
	r.advance(attachmentUploadWindow + time.Second)
	r.expectStatus(r.putAttachment(hubID, credential, uuid.NewString(), randomBytes(t, 8), octetStream), http.StatusCreated, "upload after the window")
}

func TestAttachmentQuotasEvictTheHubsOwnFilesAndRefuseAtTheStoreCap(t *testing.T) {
	lowerAttachmentQuotas(t, 3*1024, 5*1024)
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	dev := r.pairDevice(hc, []string{"brief:read"})
	first, second, third := uuid.NewString(), uuid.NewString(), uuid.NewString()
	r.expectStatus(r.putAttachment(hubID, credential, first, randomBytes(t, 1024), octetStream), http.StatusCreated, "first")
	r.advance(time.Second)
	r.expectStatus(r.putAttachment(hubID, credential, second, randomBytes(t, 1024), octetStream), http.StatusCreated, "second")
	r.advance(time.Second)
	// 1 KiB + 1 KiB + 2 KiB is over the 3 KiB hub quota: the oldest goes.
	r.expectStatus(r.putAttachment(hubID, credential, third, randomBytes(t, 2048), octetStream), http.StatusCreated, "third")
	r.expectRawStatus("/v1/attachments/"+first, dev.bearer, http.StatusNotFound, "evicted by the hub quota")
	r.expectRawStatus("/v1/attachments/"+second, dev.bearer, http.StatusOK, "kept")
	if _, err := os.Stat(filepath.Join(r.dir, "attachments", hubID, first)); !os.IsNotExist(err) {
		t.Fatal("an evicted attachment's file must be removed")
	}
	// A body over the hub quota by itself is refused.
	r.expectStatus(r.putAttachment(hubID, credential, uuid.NewString(), randomBytes(t, 4096), octetStream), http.StatusRequestEntityTooLarge, "over the hub quota alone")

	// Another hub cannot push the store past its cap, and never evicts this hub.
	otherHub, otherCredential := uuid.NewString(), randomToken(t)
	otherClient := r.connectHub(otherHub, otherCredential)
	r.pairDevice(otherClient, []string{"brief:read"})
	r.expectStatus(r.putAttachment(otherHub, otherCredential, uuid.NewString(), randomBytes(t, 3*1024), octetStream), http.StatusInsufficientStorage, "store cap")
	r.expectRawStatus("/v1/attachments/"+second, dev.bearer, http.StatusOK, "untouched by the other hub")
	r.expectStatus(r.putAttachment(otherHub, otherCredential, uuid.NewString(), randomBytes(t, 1024), octetStream), http.StatusCreated, "within the cap")
}

func TestAttachmentsExpireAndAreCappedPerHub(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	dev := r.pairDevice(hc, []string{"brief:read"})

	ids := make([]string, 0, store.KeptAttachments+2)
	for i := 0; i < store.KeptAttachments+2; i++ {
		id := uuid.NewString()
		ids = append(ids, id)
		r.expectStatus(r.putAttachment(hubID, credential, id, randomBytes(t, 32), octetStream), http.StatusCreated, "put")
		// Spread the uploads so the per-hub rate limit does not apply.
		r.advance(attachmentUploadWindow / attachmentUploadsPerHub)
	}
	r.expectRawStatus("/v1/attachments/"+ids[0], dev.bearer, http.StatusNotFound, "oldest beyond the cap")
	r.expectRawStatus("/v1/attachments/"+ids[1], dev.bearer, http.StatusNotFound, "second oldest beyond the cap")
	r.expectRawStatus("/v1/attachments/"+ids[2], dev.bearer, http.StatusOK, "newest kept")

	r.advance(store.AttachmentTTL + time.Minute)
	r.expectRawStatus("/v1/attachments/"+ids[len(ids)-1], dev.bearer, http.StatusNotFound, "expired")
	swept, err := r.store.Sweep(context.Background(), r.now())
	if err != nil || swept.Attachments != int64(store.KeptAttachments) {
		t.Fatalf("sweep removed %d attachments (%v), want %d", swept.Attachments, err, store.KeptAttachments)
	}
	if entries, _ := os.ReadDir(filepath.Join(r.dir, "attachments", hubID)); len(entries) != 0 {
		t.Fatalf("the sweep must remove expired files, %d left", len(entries))
	}
}
