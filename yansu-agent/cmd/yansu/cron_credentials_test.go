package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func cronCredentialHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "icc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", home)
	return home
}

func TestLoadCronCredentialsIgnoresPortEnvironment(t *testing.T) {
	home := cronCredentialHome(t)
	writeCronPort(t, home, "12346")
	t.Setenv("YANSU_CRON_PORT", "12345")
	t.Setenv("YANSU_CRON_SECRET", "environment-secret")
	port, secret, err := loadCronCredentials()
	if err != nil || port != 12346 || secret != "environment-secret" {
		t.Fatalf("published port and environment secret not used: port=%d err=%v", port, err)
	}
}

func TestLoadCronCredentialsReadsPortFile(t *testing.T) {
	home := cronCredentialHome(t)
	t.Setenv("YANSU_CRON_PORT", "")
	t.Setenv("YANSU_CRON_SECRET", "environment-secret")
	if err := os.WriteFile(filepath.Join(home, "cron-port"), []byte("12346\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	port, secret, err := loadCronCredentials()
	if err != nil || port != 12346 || secret != "environment-secret" {
		t.Fatalf("port file not used: port=%d err=%v", port, err)
	}
}

func writeCronPort(t *testing.T, home, port string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "cron-port"), []byte(port+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCronCredentialsRequiresValidPublishedPort(t *testing.T) {
	home := cronCredentialHome(t)
	t.Setenv("YANSU_CRON_PORT", "12345")
	t.Setenv("YANSU_CRON_SECRET", "environment-secret")
	if _, _, err := loadCronCredentials(); err == nil {
		t.Fatal("environment port must not replace the missing desktop port file")
	}
	for _, port := range []string{"", "0", "-1", "65536", "12345/path", "localhost:12345"} {
		t.Run(port, func(t *testing.T) {
			writeCronPort(t, home, port)
			if _, _, err := loadCronCredentials(); err == nil {
				t.Fatal("invalid desktop port must not fall back to the environment")
			}
		})
	}
}

func TestCronRequestNeverSendsSecretToOverrideListener(t *testing.T) {
	home := cronCredentialHome(t)
	t.Setenv("YANSU_CRON_SECRET", "loopback-test-secret")
	var otherRequests atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	otherURL, _ := url.Parse(other.URL)
	t.Setenv("YANSU_CRON_PORT", otherURL.Port())

	var publishedRequests atomic.Int32
	published := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publishedRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer loopback-test-secret" {
			t.Error("published endpoint did not receive the expected bearer credential")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer published.Close()
	publishedURL, _ := url.Parse(published.URL)
	writeCronPort(t, home, publishedURL.Port())

	resp, err := doCronRequest(http.MethodGet, "/cron/jobs", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || publishedRequests.Load() != 1 {
		t.Fatalf("published endpoint was not used: status=%d requests=%d", resp.StatusCode, publishedRequests.Load())
	}
	if otherRequests.Load() != 0 {
		t.Fatal("the override listener received an authenticated request")
	}
}

func TestCronRequestNeverRedirectsSecretToAnotherPort(t *testing.T) {
	home := cronCredentialHome(t)
	t.Setenv("YANSU_CRON_PORT", "")
	t.Setenv("YANSU_CRON_SECRET", "redirect-test-secret")
	var otherRequests atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			published := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer redirect-test-secret" {
					t.Error("published endpoint did not receive the expected bearer credential")
				}
				http.Redirect(w, r, other.URL+"/capture", status)
			}))
			defer published.Close()
			publishedURL, _ := url.Parse(published.URL)
			writeCronPort(t, home, publishedURL.Port())
			resp, err := doCronRequest(http.MethodGet, "/cron/jobs", nil, "")
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "redirects are not allowed") {
				t.Fatalf("expected redirect refusal, got %v", err)
			}
			if otherRequests.Load() != 0 {
				t.Fatal("a redirect leaked the credential to another listener")
			}
		})
	}
}
