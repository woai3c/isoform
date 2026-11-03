package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

type fallbackChecksumTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (x fallbackChecksumTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "api.github.com" {
		req = req.Clone(req.Context())
		u := *req.URL
		u.Scheme = x.target.Scheme
		u.Host = x.target.Host
		req.URL = &u
	}
	return x.base.RoundTrip(req)
}
func TestUpdaterFallbackRequiresPublishedChecksum(t *testing.T) {
	for _, mode := range []string{"missing", "valid", "tampered"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte("published archive")
			sum := fmt.Sprintf("%x", sha256.Sum256(payload))
			name := "Isoform-2.0.0" + platformAssetSuffix()
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/" + desktopReleasesRepo + "/releases/latest":
					assets := []githubAsset{{Name: name, BrowserDownloadURL: server.URL + "/archive"}}
					if mode != "missing" {
						assets = append(assets, githubAsset{Name: "SHA256SUMS", BrowserDownloadURL: server.URL + "/SHA256SUMS"})
					}
					_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v2.0.0", Assets: assets})
				case "/SHA256SUMS":
					fmt.Fprintf(w, "%s  %s\n", sum, name)
				case "/archive":
					if mode == "tampered" {
						_, _ = w.Write([]byte("altered archive"))
					} else {
						_, _ = w.Write(payload)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			previous := http.DefaultTransport
			target, _ := url.Parse(server.URL)
			http.DefaultTransport = fallbackChecksumTransport{base: previous, target: target}
			defer func() { http.DefaultTransport = previous }()
			app := &App{updater: &updater{}}
			info := app.fetchFromGitHub()
			if mode == "missing" {
				if info.Available || info.Error == "" {
					t.Fatalf("checksum-free fallback offered download: %+v", info)
				}
				return
			}
			if !info.Available || info.Error != "" || app.updater.expectedChecksum != sum {
				t.Fatalf("fallback lost published checksum: %+v checksum=%q", info, app.updater.expectedChecksum)
			}
			path := filepath.Join(t.TempDir(), "archive")
			err := app.downloadAttempt(server.Client(), info.DownloadURL, path, 1)
			if mode == "valid" && err != nil {
				t.Fatal(err)
			}
			if mode == "tampered" {
				if err == nil {
					t.Fatal("tampered fallback accepted")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("tampered staging file retained")
				}
			}
		})
	}
}
func TestUpdaterDownloadRefusesMissingChecksum(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requested = true; _, _ = w.Write([]byte("unverified")) }))
	defer server.Close()
	app := &App{updater: &updater{}}
	if err := app.downloadAttempt(server.Client(), server.URL, filepath.Join(t.TempDir(), "archive"), 1); err == nil {
		t.Fatal("checksum-free download accepted")
	}
	if requested {
		t.Fatal("checksum-free update fetched before validation")
	}
}
