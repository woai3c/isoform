//go:build darwin

package main

import (
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
)

// startAssetServer serves the embedded frontend/dist over a localhost
// HTTP server and returns the bound port. The native WKWebView loads
// http://127.0.0.1:<port>/ from it — the native replacement for Wails'
// built-in asset server. Isoform's host bridge accepts calls from exactly
// this Origin, so the port is printed on stdout as SPOTLIGHT_ASSET_PORT=
// for the supervisor before the webview loads
// (wails-gui/host_bridge_guard.go).
func startAssetServer() (int, error) {
	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return 0, fmt.Errorf("sub frontend/dist: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listen asset server: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: http.FileServer(http.FS(dist))}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[spotlight] asset server error: %v", err)
		}
	}()
	log.Printf("[spotlight] asset server listening on 127.0.0.1:%d", port)
	return port, nil
}
