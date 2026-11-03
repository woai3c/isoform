//go:build updateqa

package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// These capabilities do not exist in production builds. No verification bypass
// is offered: QA requires the same Developer ID + notarization checks.
func startUpdateQAHook(a *App) bool {
	dir, feed := os.Getenv("YANSU_UPDATE_QA_DIR"), os.Getenv("YANSU_UPDATE_QA_FEED")
	if dir == "" && feed == "" {
		return false
	}
	parsed, err := url.Parse(feed)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || !filepath.IsAbs(dir) {
		log.Print("[update-qa] requires an absolute evidence directory and a loopback HTTP feed")
		return true
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Printf("[update-qa] %v", err)
		return true
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("[update-qa] %v", err)
		return true
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		_ = listener.Close()
		return true
	}
	token := hex.EncodeToString(tokenBytes)
	R2ManifestURL = feed
	handler := updateQAHandler(a, token)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	go func() { <-a.ctx.Done(); _ = server.Close() }()
	endpoint := map[string]any{"url": "http://" + listener.Addr().String(), "token": token, "pid": os.Getpid()}
	if err := writeUpdateJSON(filepath.Join(dir, "endpoint.json"), endpoint); err != nil {
		log.Printf("[update-qa] %v", err)
		_ = server.Close()
	}
	return true // deterministic runner drives all checks; no background feed requests
}

func updateQAHandler(a *App, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/status" && r.Method == http.MethodGet {
			home, _ := flavorDataDir()
			_ = json.NewEncoder(w).Encode(map[string]any{"version": Version, "pid": os.Getpid(), "bundle": findAppBundle(), "data_home": home, "temp_dir": os.TempDir(), "status": a.GetUpdateStatus()})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/check":
			_ = json.NewEncoder(w).Encode(a.CheckForUpdates())
		case "/download":
			if err := a.DownloadUpdate(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(a.GetUpdateStatus())
		case "/install":
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintln(w, `{ "installing": true }`)
			go func() {
				if err := a.QuitAndInstall(); err != nil {
					log.Printf("[update-qa] install: %v", err)
				}
			}()
		case "/quit":
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintln(w, `{ "quitting": true }`)
			go performRealQuit()
		default:
			http.NotFound(w, r)
		}
	})
}
