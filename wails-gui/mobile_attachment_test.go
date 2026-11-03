package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMobileAttachmentSealRoundTripAndBinding(t *testing.T) {
	key, nonce, err := newMobileAttachmentSecret()
	if err != nil {
		t.Fatal(err)
	}
	hubID, id := uuid.NewString(), uuid.NewString()
	plaintext := bytes.Repeat([]byte("audio "), 1000)
	sealed, err := sealMobileAttachment(key, nonce, hubID, id, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("audio audio")) {
		t.Fatal("the sealed bytes must not carry the plaintext")
	}
	opened, err := openMobileAttachment(key, nonce, hubID, id, sealed)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := openMobileAttachment(key, nonce, hubID, uuid.NewString(), sealed); !errors.Is(err, errMobileAttachmentRejected) {
		t.Fatal("another attachment id must not open the file")
	}
	if _, err := openMobileAttachment(key, nonce, uuid.NewString(), id, sealed); !errors.Is(err, errMobileAttachmentRejected) {
		t.Fatal("another hub must not open the file")
	}
	tampered := append([]byte(nil), sealed...)
	tampered[10] ^= 1
	if _, err := openMobileAttachment(key, nonce, hubID, id, tampered); !errors.Is(err, errMobileAttachmentRejected) {
		t.Fatal("a changed byte must be refused")
	}
	if _, err := sealMobileAttachment(key[:16], nonce, hubID, id, plaintext); !errors.Is(err, errMobileAttachmentKey) {
		t.Fatal("a short key must be refused")
	}
	if _, err := sealMobileAttachment(key, nonce, hubID, id, nil); !errors.Is(err, errMobileAttachmentTooLarge) {
		t.Fatal("an empty file must be refused")
	}
}

type stubAttachmentUploader struct {
	mu       sync.Mutex
	uploaded map[string][]byte
	err      error
}

func (s *stubAttachmentUploader) uploadAttachment(_ context.Context, id string, sealed []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.uploaded == nil {
		s.uploaded = map[string][]byte{}
	}
	s.uploaded[id] = append([]byte(nil), sealed...)
	return nil
}

func TestSealAndUploadAttachmentProducesARecordThePhoneCanOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-10-14-morning.wav")
	plaintext := testWAV(1)
	if err := os.WriteFile(path, plaintext, 0600); err != nil {
		t.Fatal(err)
	}
	hubID := uuid.NewString()
	uploader := &stubAttachmentUploader{}
	record, err := sealAndUploadAttachment(context.Background(), uploader, hubID, path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.RawURLEncoding.DecodeString(record.Key)
	if err != nil || len(key) != 32 {
		t.Fatalf("key = %q %v", record.Key, err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(record.Nonce)
	if err != nil || len(nonce) != 12 {
		t.Fatalf("nonce = %q %v", record.Nonce, err)
	}
	sealed, ok := uploader.uploaded[record.ID]
	if !ok {
		t.Fatalf("nothing uploaded under %s", record.ID)
	}
	opened, err := openMobileAttachment(key, nonce, hubID, record.ID, sealed)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("the phone could not open the upload: %v", err)
	}

	failing := &stubAttachmentUploader{err: errors.New("relay down")}
	if _, err := sealAndUploadAttachment(context.Background(), failing, hubID, path); err == nil {
		t.Fatal("an upload error must surface")
	}
	if _, err := sealAndUploadAttachment(context.Background(), uploader, hubID, filepath.Join(dir, "missing.wav")); err == nil {
		t.Fatal("a missing file must surface")
	}
}

func TestRelayClientUploadsAttachmentsWithTheHubCredential(t *testing.T) {
	var got struct {
		method, path, auth, hub, contentType string
		body                                 []byte
	}
	status := http.StatusCreated
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.auth, got.hub, got.contentType = r.Header.Get("Authorization"), r.Header.Get("X-Yansu-Hub"), r.Header.Get("Content-Type")
		got.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true,"data":{"id":"x","bytes":3}}`))
	}))
	defer server.Close()

	client := &mobileRelayClient{origin: server.URL, hubID: uuid.NewString(), credential: "secret-credential"}
	id := uuid.NewString()
	if err := client.uploadAttachment(context.Background(), id, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPut || got.path != "/v1/hub/attachments/"+id {
		t.Fatalf("request %s %s", got.method, got.path)
	}
	if got.auth != "Bearer secret-credential" || got.hub != client.hubID || got.contentType != mobileAttachmentContentType {
		t.Fatalf("headers auth=%q hub=%q type=%q", got.auth, got.hub, got.contentType)
	}
	if !bytes.Equal(got.body, []byte{1, 2, 3}) {
		t.Fatalf("body = %v", got.body)
	}

	status = http.StatusUnauthorized
	if err := client.uploadAttachment(context.Background(), id, []byte{1}); !errors.Is(err, errMobileAttachmentRejected) {
		t.Fatalf("a refusal must surface, got %v", err)
	}
	if err := client.uploadAttachment(context.Background(), id, nil); !errors.Is(err, errMobileAttachmentTooLarge) {
		t.Fatal("an empty upload must be refused before the network")
	}
}

func TestUploadMobileBriefAudioStaysLocalWithoutAPhone(t *testing.T) {
	app := &App{}
	if _, err := uploadMobileBriefAudio(context.Background(), app, "2026-10-14-morning", "/nonexistent"); !errors.Is(err, errMobileAttachmentOffline) {
		t.Fatalf("without a bridge the audio must stay local, got %v", err)
	}
}

func TestRelayClientAttachmentUploadHonorsEarlierRequestDeadline(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client := &mobileRelayClient{origin: server.URL, hubID: uuid.NewString(), credential: "secret-credential"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := client.uploadAttachment(ctx, uuid.NewString(), []byte{1, 2, 3}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("upload ignored the earlier request deadline: %v", err)
	}
}
