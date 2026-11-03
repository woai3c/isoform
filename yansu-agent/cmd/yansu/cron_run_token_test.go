package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"yansu-agent/runtoken"
)

func TestCronRequestUsesIssuedRunCredentialAndListener(t *testing.T) {
	cronCredentialHome(t)
	t.Setenv("YANSU_CRON_SECRET", "master-must-not-be-used")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer run-issued" {
			t.Error("master or missing credential reached listener")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	t.Setenv(runtoken.TokenEnv, "run-issued")
	t.Setenv(runtoken.PortEnv, endpoint.Port())
	t.Setenv(runtoken.ScopedEnv, "1")
	response, err := doCronRequest(http.MethodGet, "/cron/jobs", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatal("scoped CLI request failed")
	}
}

func TestRunCredentialPrecedesMasterWithoutStoreOrPortFile(t *testing.T) {
	cronCredentialHome(t)
	t.Setenv("YANSU_CRON_SECRET", "master-must-not-be-used")
	t.Setenv(runtoken.ScopedEnv, "1")
	t.Setenv(runtoken.TokenEnv, "run-issued")
	t.Setenv(runtoken.PortEnv, "12345")
	port, token, err := loadCronCredentials()
	if err != nil || port != 12345 || token != "run-issued" {
		t.Fatal("run credential was not selected")
	}
	for _, test := range []struct{ token, port string }{{"", "12345"}, {"run-issued", "0"}, {"run-issued", "65536"}, {"run-issued", "12345/path"}} {
		t.Setenv(runtoken.TokenEnv, test.token)
		t.Setenv(runtoken.PortEnv, test.port)
		if _, _, err := loadCronCredentials(); err == nil {
			t.Fatal("invalid scoped credential fell back to master")
		}
	}
}
