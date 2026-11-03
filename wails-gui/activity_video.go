package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

// GetHostBridgePort returns the port of the localhost host bridge HTTP server.
// Used by the frontend to construct video URLs.
func (a *App) GetHostBridgePort() int {
	if a.bridge == nil {
		return 0
	}
	return a.bridge.port
}

// GetHostBridgeMediaToken returns the per-launch token the activity page
// adds to its screenshot and recording URLs (the "token" query parameter;
// <img> and <video> cannot send headers). It opens /api/activity/* only.
func (a *App) GetHostBridgeMediaToken() string {
	if a.bridge == nil {
		return ""
	}
	return a.bridge.mediaToken
}

// handleActivitySnapshot serves a JPEG snapshot file directly.
// GET /api/activity/snapshot?path=2026-03-04/1709500000000-abc12345.jpg
func (a *App) handleActivitySnapshot(w http.ResponseWriter, r *http.Request) {

	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		a.bridge.writeError(w, http.StatusBadRequest, "path parameter required")
		return
	}

	if strings.Contains(relPath, "..") || !strings.HasSuffix(relPath, ".jpg") {
		a.bridge.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	if a.activity == nil || a.activity.store == nil {
		a.bridge.writeError(w, http.StatusServiceUnavailable, "activity monitor not initialized")
		return
	}

	snapshotsDir := a.activity.store.SnapshotsDir()
	fullPath := filepath.Join(snapshotsDir, relPath)

	absPath, err := filepath.Abs(fullPath)
	if err != nil || !strings.HasPrefix(absPath, snapshotsDir) {
		a.bridge.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		a.bridge.writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, fullPath)
}

// handleActivityFrame extracts a single JPEG frame from a video segment (legacy HEVC)
// or finds the closest snapshot for snapshot-based sessions.
// GET /api/activity/frame?path=2026-03-03/xxx.hevc&ts=1709500005123     (legacy HEVC)
// GET /api/activity/frame?path=2026-03-03/xxx.hevc&t=12.5               (legacy HEVC)
// GET /api/activity/frame?session=<id>&ts=1709500005123                  (snapshot lookup)
func (a *App) handleActivityFrame(w http.ResponseWriter, r *http.Request) {

	if a.activity == nil || a.activity.store == nil {
		a.bridge.writeError(w, http.StatusServiceUnavailable, "activity monitor not initialized")
		return
	}

	// New snapshot-based lookup: ?session=<id>&ts=<ms>
	sessionID := r.URL.Query().Get("session")
	tsParam := r.URL.Query().Get("ts")

	if sessionID != "" && tsParam != "" {
		ts, err := strconv.ParseInt(tsParam, 10, 64)
		if err != nil {
			a.bridge.writeError(w, http.StatusBadRequest, "invalid ts parameter")
			return
		}
		snap, err := a.activity.store.FindClosestSnapshot(sessionID, ts)
		if err != nil {
			a.bridge.writeError(w, http.StatusNotFound, "no snapshot found")
			return
		}
		fullPath := filepath.Join(a.activity.store.SnapshotsDir(), snap.FilePath)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, fullPath)
		return
	}

	// Legacy HEVC frame extraction below
	relPath := r.URL.Query().Get("path")
	tParam := r.URL.Query().Get("t")
	if relPath == "" || (tParam == "" && tsParam == "") {
		a.bridge.writeError(w, http.StatusBadRequest, "path and (t or ts) parameters required")
		return
	}
	if strings.Contains(relPath, "..") || !strings.HasSuffix(relPath, ".hevc") {
		a.bridge.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	segmentsDir := a.activity.store.SegmentsDir()
	hevcPath := filepath.Join(segmentsDir, relPath)
	absHevc, err := filepath.Abs(hevcPath)
	if err != nil || !strings.HasPrefix(absHevc, segmentsDir) {
		a.bridge.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	if _, err := os.Stat(hevcPath); os.IsNotExist(err) {
		a.bridge.writeError(w, http.StatusNotFound, "segment not found")
		return
	}

	ffmpegPath := installedFFmpegPath()
	if ffmpegPath == "" {
		a.bridge.writeError(w, http.StatusNotImplemented, "ffmpeg not found")
		return
	}

	var output []byte

	// Prefer ts (absolute timestamp) over t (relative seconds) for precision
	if tsParam != "" {
		ts, err := strconv.ParseInt(tsParam, 10, 64)
		if err != nil {
			a.bridge.writeError(w, http.StatusBadRequest, "invalid ts parameter")
			return
		}
		// Try to resolve frame index from sidecar timestamps file
		tsFilePath := strings.TrimSuffix(hevcPath, ".hevc") + ".timestamps"
		frameIdx := resolveFrameIndex(tsFilePath, ts)
		if frameIdx >= 0 {
			output = extractFrameByIndex(ffmpegPath, hevcPath, frameIdx)
		}
		// Fallback to time-based seek if no sidecar or extraction failed
		if len(output) == 0 {
			baseName := filepath.Base(relPath)
			if m := strings.SplitN(baseName, "-", 2); len(m) >= 1 {
				if segStart, err := strconv.ParseInt(m[0], 10, 64); err == nil {
					tSec := float64(ts-segStart) / 1000.0
					if tSec < 0 {
						tSec = 0
					}
					output = extractFrame(ffmpegPath, hevcPath, fmt.Sprintf("%.1f", tSec))
				}
			}
		}
	} else {
		output = extractFrame(ffmpegPath, hevcPath, tParam)
	}

	if len(output) == 0 {
		log.Printf("[activity-frame] no frame for %s, falling back to first frame", relPath)
		output = extractFrameByIndex(ffmpegPath, hevcPath, 0)
	}
	if len(output) == 0 {
		output = extractFrame(ffmpegPath, hevcPath, "0")
	}
	if len(output) == 0 {
		a.bridge.writeError(w, http.StatusInternalServerError, "no frame at requested time")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(output)
}

// handleActivityVideo serves HEVC video segments as MP4 (H.264) via ffmpeg.
// GET /api/activity/video?path=2026-03-03/xxx.hevc
// Legacy endpoint kept for backward compatibility with old HEVC sessions.
func (a *App) handleActivityVideo(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		a.bridge.writeError(w, http.StatusBadRequest, "path parameter required")
		return
	}

	// Validate path: no traversal, must end with .hevc
	if strings.Contains(relPath, "..") || !strings.HasSuffix(relPath, ".hevc") {
		a.bridge.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	if a.activity == nil || a.activity.store == nil {
		a.bridge.writeError(w, http.StatusServiceUnavailable, "activity monitor not initialized")
		return
	}

	segmentsDir := a.activity.store.SegmentsDir()
	hevcPath := filepath.Join(segmentsDir, relPath)

	// Verify the resolved path is still under segmentsDir
	absHevc, err := filepath.Abs(hevcPath)
	if err != nil || !strings.HasPrefix(absHevc, segmentsDir) {
		a.bridge.writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	fi, err := os.Stat(hevcPath)
	if os.IsNotExist(err) {
		a.bridge.writeError(w, http.StatusNotFound, "segment not found")
		return
	}
	if fi.Size() == 0 {
		a.bridge.writeError(w, http.StatusUnprocessableEntity, "segment is empty (still recording?)")
		return
	}

	// Check MP4 cache
	dataRoot, err := flavorDataDir()
	if err != nil {
		a.bridge.writeError(w, http.StatusInternalServerError, "cannot determine home directory")
		return
	}
	cacheDir := filepath.Join(dataRoot, "cache", "mp4")
	mp4Name := strings.TrimSuffix(relPath, ".hevc") + ".mp4"
	mp4Path := filepath.Join(cacheDir, mp4Name)

	if _, err := os.Stat(mp4Path); os.IsNotExist(err) {
		ffmpegPath := installedFFmpegPath()
		if ffmpegPath == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			fmt.Fprintf(w, `{"ok":false,"error":"ffmpeg not found — install ffmpeg to play activity recordings"}`)
			return
		}

		if err := os.MkdirAll(filepath.Dir(mp4Path), 0700); err != nil {
			a.bridge.writeError(w, http.StatusInternalServerError, "cannot create cache directory")
			return
		}

		tmpPath := mp4Path + ".tmp"
		cmd := cmdutil.Command(ffmpegPath,
			"-probesize", "50M",
			"-analyzeduration", "100M",
			"-f", "hevc",
			"-r", "4",
			"-i", hevcPath,
			"-c:v", "libx264",
			"-preset", "fast",
			"-crf", "28",
			"-tune", "stillimage",
			"-pix_fmt", "yuv420p",
			"-movflags", "+faststart",
			"-f", "mp4",
			"-y",
			tmpPath,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			os.Remove(tmpPath)
			log.Printf("[activity-video] ffmpeg failed: %v\n%s", err, output)
			a.bridge.writeError(w, http.StatusInternalServerError, "ffmpeg conversion failed")
			return
		}

		if err := os.Rename(tmpPath, mp4Path); err != nil {
			os.Remove(tmpPath)
			a.bridge.writeError(w, http.StatusInternalServerError, "failed to finalize mp4")
			return
		}

		log.Printf("[activity-video] converted %s → %s", relPath, mp4Path)
	}

	// http.ServeFile handles Range requests, Content-Type, etc.
	http.ServeFile(w, r, mp4Path)
}

// resolveFrameIndex reads the sidecar .timestamps file and returns the frame index
// whose wall-clock timestamp is closest to the target ts (unix ms).
// Returns -1 if the sidecar file doesn't exist or is empty.
func resolveFrameIndex(tsFilePath string, targetTS int64) int {
	data, err := os.ReadFile(tsFilePath)
	if err != nil || len(data) < 8 {
		return -1
	}

	frameCount := len(data) / 8
	bestIdx := 0
	bestDist := int64(math.MaxInt64)

	for i := range frameCount {
		ts := int64(binary.LittleEndian.Uint64(data[i*8 : i*8+8]))
		dist := targetTS - ts
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			bestIdx = i
		}
	}

	return bestIdx
}

// extractFrameByIndex extracts a specific frame by its 0-based index using ffmpeg select filter.
func extractFrameByIndex(ffmpegPath, hevcPath string, frameIdx int) []byte {
	selectExpr := fmt.Sprintf("eq(n\\,%d)", frameIdx)
	cmd := cmdutil.Command(ffmpegPath,
		"-probesize", "50M",
		"-analyzeduration", "100M",
		"-f", "hevc",
		"-r", "4",
		"-i", hevcPath,
		"-vf", fmt.Sprintf("select=%s", selectExpr),
		"-vsync", "vfr",
		"-frames:v", "1",
		"-pix_fmt", "yuvj420p",
		"-q:v", "2",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"pipe:1",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[activity-frame] ffmpeg select n=%d failed: %v\nstderr: %s", frameIdx, err, stderr.String())
		return nil
	}
	return output
}

// extractFrame runs ffmpeg to extract a single JPEG frame from an HEVC file
// at the given time offset. Returns the JPEG bytes, or nil on failure.
func extractFrame(ffmpegPath, hevcPath, t string) []byte {
	cmd := cmdutil.Command(ffmpegPath,
		"-probesize", "50M",
		"-analyzeduration", "100M",
		"-f", "hevc",
		"-r", "4",
		"-i", hevcPath,
		"-ss", t,
		"-frames:v", "1",
		"-pix_fmt", "yuvj420p",
		"-q:v", "2",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"pipe:1",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[activity-frame] ffmpeg failed for t=%s: %v\nstderr: %s", t, err, stderr.String())
		return nil
	}
	return output
}
