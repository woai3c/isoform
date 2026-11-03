//go:build !bindings && ((darwin && (amd64 || arm64) && !ios) || (windows && (amd64 || 386)))

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"wails-gui/internal/asr"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/models"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Qwen3-ASR 0.6B (open weights, int8, from the sherpa-onnx releases)
// transcribes a call again once it has ended. Measured on the user's own
// recordings against SenseVoice-Small, the live engine: 93% vs 87.5%
// agreement with reference lines, 9% vs 11% error on Mandarin, names and
// mixed English-Mandarin terms right far more often; about 5x slower and
// 2.4 GB of memory while it runs, which is fine after a call but not live.
// Setup confirms the model download into the shared models folder. Until
// it is installed, the live engine does the after-call pass.
// Settings > Observation shows it, with its size, status and last error,
// and can turn it off or remove it (after_call_speech_model.go).
var qwen3ASRMirrorURLs = []string{
	"https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25.tar.bz2",
}

const (
	envQwen3ASRURL = "YANSU_SHERPA_QWEN3_ASR_URL"
	// SHA-256 of the upstream tarball (GitHub release asset digest,
	// 2026-04-07). Always verified, a URL override included: the files are
	// loaded into native code, and a wrong one aborts the process.
	sha256Qwen3ASRArchive = "393f8a14e2f5fb96746aaab342997a40641001fbd5bf9592a080a8329178ee96"
	qwen3ASRDirName       = "sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25"

	// Sizes for the Settings row and the disk check.
	qwen3ASRDownloadBytes = 878_702_423
	qwen3ASRDiskBytes     = 970_000_000
	// qwen3ASRDiskMargin stays free after the download and extraction.
	qwen3ASRDiskMargin = 1_000_000_000

	// qwen3ASRVerifiedMarker is written into the model folder once a
	// SHA-verified archive has been extracted completely. Without it the
	// folder is not a model (a full disk can stop an extraction halfway).
	qwen3ASRVerifiedMarker = ".isoform-verified"

	// qwen3ASRMaxDecode: the exported decoder holds 512 positions, about
	// 20 s of audio; longer audio loses its end. Segments are cut below this,
	// edge padding included.
	qwen3ASRMaxDecode = 18 * time.Second

	// afterCallEngineWait bounds how long a finished call waits for the
	// model while another call is being transcribed with it; after that it
	// uses the live engine.
	afterCallEngineWait = 10 * time.Minute

	// afterCallMinBatteryPercent: on battery below this the large model is
	// not loaded.
	afterCallMinBatteryPercent = 30
)

type qwen3ASRPaths struct {
	root         string // <sherpa>/qwen3-asr
	modelDir     string
	convFrontend string
	encoder      string
	decoder      string
	tokenizer    string
}

func defaultQwen3ASRPaths() qwen3ASRPaths {
	dir := filepath.Dir(models.Path(configDirOrFallback(), "sherpa/qwen3-asr/"+qwen3ASRDirName+"/encoder.int8.onnx"))
	return qwen3ASRPathsForDir(filepath.Dir(dir), dir)
}

// qwen3ASRPathsUnder lays the model out under root.
func qwen3ASRPathsUnder(root string) qwen3ASRPaths {
	return qwen3ASRPathsForDir(root, filepath.Join(root, qwen3ASRDirName))
}

func qwen3ASRPathsForDir(root, dir string) qwen3ASRPaths {
	return qwen3ASRPaths{
		root:         root,
		modelDir:     dir,
		convFrontend: filepath.Join(dir, "conv_frontend.onnx"),
		encoder:      filepath.Join(dir, "encoder.int8.onnx"),
		decoder:      filepath.Join(dir, "decoder.int8.onnx"),
		tokenizer:    filepath.Join(dir, "tokenizer"),
	}
}

func qwen3ASRFilesPresent(p qwen3ASRPaths) bool {
	return fileExistsAndNonEmpty(p.convFrontend) &&
		fileExistsAndNonEmpty(p.encoder) &&
		fileExistsAndNonEmpty(p.decoder) &&
		fileExistsAndNonEmpty(filepath.Join(p.tokenizer, "vocab.json")) &&
		fileExistsAndNonEmpty(filepath.Join(p.tokenizer, "merges.txt"))
}

// qwen3ASRModelPresent reports a complete, verified model.
func qwen3ASRModelPresent(p qwen3ASRPaths) bool {
	if p.modelDir == defaultQwen3ASRPaths().modelDir {
		return models.Present(configDirOrFallback(), "qwen3-asr-0.6b")
	}
	marker, err := os.ReadFile(filepath.Join(p.modelDir, qwen3ASRVerifiedMarker))
	return err == nil && strings.TrimSpace(string(marker)) == sha256Qwen3ASRArchive && qwen3ASRFilesPresent(p)
}

// freeDiskSpace is freeDiskBytes, replaceable in tests.
var freeDiskSpace = freeDiskBytes

// errQwen3ASRLowDisk: the download was skipped for lack of free space.
var errQwen3ASRLowDisk = errors.New("not enough free disk space")

// ensureQwen3ASRAssets downloads and installs the model when it is missing.
func ensureQwen3ASRAssets(ctx context.Context, p qwen3ASRPaths, progress sherpaDownloadProgress) error {
	return models.Require(configDirOrFallback(), "qwen3-asr-0.6b")
}

// ensureQwen3ASRAssetsFrom installs the model from urls. The archive is
// verified against expectSHA before anything is extracted; extraction goes
// into a staging folder that is renamed into place only once complete and
// marked verified, so an interrupted install leaves no model behind.
func ensureQwen3ASRAssetsFrom(ctx context.Context, p qwen3ASRPaths, urls []string, expectSHA string, progress sherpaDownloadProgress) error {
	if qwen3ASRModelPresent(p) {
		return nil
	}
	if err := os.MkdirAll(p.root, 0o755); err != nil {
		return fmt.Errorf("create qwen3-asr dir: %w", err)
	}
	archivePath := filepath.Join(p.root, qwen3ASRDirName+".tar.bz2")
	var partial int64
	if st, err := os.Stat(archivePath + ".partial"); err == nil {
		partial = st.Size()
	}
	if free, err := freeDiskSpace(p.root); err == nil && free < uint64(qwen3ASRDownloadBytes-partial+qwen3ASRDiskBytes+qwen3ASRDiskMargin) {
		return fmt.Errorf("%w: %d MB free, about %d MB needed", errQwen3ASRLowDisk, free/1_000_000, (qwen3ASRDownloadBytes+qwen3ASRDiskBytes+qwen3ASRDiskMargin)/1_000_000)
	}

	var lastErr error
	downloaded := false
	for _, url := range urls {
		if err := downloadSherpaAsset(ctx, "qwen3-asr/"+qwen3ASRDirName+".tar.bz2", url, archivePath, expectSHA, progress); err != nil {
			lastErr = fmt.Errorf("%s: %w", url, err)
			log.Printf("[asr] qwen3-asr download from %s failed: %v", url, err)
			continue
		}
		downloaded = true
		break
	}
	if !downloaded {
		return fmt.Errorf("download qwen3-asr: %w", lastErr)
	}
	keepArchive := false
	defer func() {
		if !keepArchive {
			os.Remove(archivePath)
		}
	}()

	staging, err := os.MkdirTemp(p.root, ".extract-")
	if err != nil {
		return fmt.Errorf("staging folder: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := extractTarBz2(archivePath, staging); err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			// The verified archive stays for the next try once space is freed.
			keepArchive = true
			return fmt.Errorf("%w: extract qwen3-asr: %v", errQwen3ASRLowDisk, err)
		}
		return fmt.Errorf("extract qwen3-asr: %w", err)
	}
	// Turned off or removed meanwhile: nothing more is installed.
	if err := ctx.Err(); err != nil {
		return err
	}
	staged := qwen3ASRPathsForDir(staging, filepath.Join(staging, qwen3ASRDirName))
	if !qwen3ASRFilesPresent(staged) {
		return errors.New("qwen3-asr: model files missing after extraction")
	}
	if err := os.WriteFile(filepath.Join(staged.modelDir, qwen3ASRVerifiedMarker), []byte(expectSHA+"\n"), 0o644); err != nil {
		return fmt.Errorf("mark qwen3-asr verified: %w", err)
	}
	if err := os.RemoveAll(p.modelDir); err != nil {
		return fmt.Errorf("clear old qwen3-asr: %w", err)
	}
	if err := os.Rename(staged.modelDir, p.modelDir); err != nil {
		return fmt.Errorf("install qwen3-asr: %w", err)
	}
	log.Printf("[asr] Qwen3-ASR model installed in %s", p.modelDir)
	return nil
}

// removeQwen3ASRModel deletes the model and any download in progress.
// qwen3ASRFilesOnDisk reports whether anything of the model is on disk: the
// model, a partial download or extraction leftovers.
func qwen3ASRFilesOnDisk(p qwen3ASRPaths) bool {
	if _, err := os.Stat(p.modelDir); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(p.root, qwen3ASRDirName+".tar.bz2.partial")); err == nil {
		return true
	}
	staging, _ := filepath.Glob(filepath.Join(p.root, ".extract-*"))
	return len(staging) > 0
}

func removeQwen3ASRModel(p qwen3ASRPaths) error {
	if err := os.RemoveAll(p.modelDir); err != nil {
		return err
	}
	archivePath := filepath.Join(p.root, qwen3ASRDirName+".tar.bz2")
	os.Remove(archivePath)
	os.Remove(archivePath + ".partial")
	os.Remove(archivePath + ".partial" + sherpaResumeValidatorSuffix)
	// Extraction folders a crash left behind (about 1 GB each).
	staging, _ := filepath.Glob(filepath.Join(p.root, ".extract-*"))
	for _, dir := range staging {
		os.RemoveAll(dir)
	}
	return nil
}

// newQwen3ASRTranscriber loads Qwen3-ASR behind the same Silero VAD as the
// live engine. It accepts every language the model reports.
func newQwen3ASRTranscriber(p qwen3ASRPaths, silero string) (*sherpaTranscriber, error) {
	if !qwen3ASRFilesPresent(p) || !fileExistsAndNonEmpty(silero) {
		return nil, errors.New("qwen3-asr model or silero vad missing")
	}
	cfg := &sherpa.OfflineRecognizerConfig{}
	cfg.FeatConfig.SampleRate = sherpaSampleRate
	cfg.FeatConfig.FeatureDim = 80
	cfg.ModelConfig.Qwen3ASR.ConvFrontend = p.convFrontend
	cfg.ModelConfig.Qwen3ASR.Encoder = p.encoder
	cfg.ModelConfig.Qwen3ASR.Decoder = p.decoder
	cfg.ModelConfig.Qwen3ASR.Tokenizer = p.tokenizer
	cfg.ModelConfig.Qwen3ASR.MaxTotalLen = 512
	cfg.ModelConfig.Qwen3ASR.MaxNewTokens = 256
	// Near-greedy decoding: transcripts must be repeatable.
	cfg.ModelConfig.Qwen3ASR.Temperature = 1e-6
	cfg.ModelConfig.Qwen3ASR.TopP = 0.8
	cfg.ModelConfig.Qwen3ASR.Seed = 42
	cfg.ModelConfig.NumThreads = 2
	cfg.DecodingMethod = "greedy_search"
	t, err := newSherpaTranscriberWithRecognizer(cfg, sherpaPaths{silero: silero}, nil)
	if err != nil {
		return nil, err
	}
	t.maxDecodeSamples = int(qwen3ASRMaxDecode / time.Second * sherpaSampleRate)
	return t, nil
}

var afterCallEngineSlot = make(chan struct{}, 1)

// afterCallTranscriber returns the engine for transcribing a finished call
// and a release func the caller must run when done. It is Qwen3-ASR when its
// model is installed, turned on, and the Mac can afford it right now; else
// the live engine. Only an explicitly confirmed setup job downloads assets.
func (a *App) afterCallTranscriber() (asr.LongFormTranscriber, func()) {
	live, _ := a.speechTranscriber().(asr.LongFormTranscriber)
	if live == nil {
		// No live engine: speech is not set up here (or this is a test).
		return nil, func() {}
	}
	if !a.afterCallSpeechModelEnabled() {
		return live, func() {}
	}
	paths := defaultQwen3ASRPaths()
	if !qwen3ASRModelPresent(paths) {
		if reason := afterCallModelDeferReason(); reason != "" {
			log.Printf("[asr] qwen3-asr download put off: %s", reason)
		} else {
			// Setup owns the download; keep the live transcript until it is ready.
		}
		return live, func() {}
	}
	if reason := afterCallModelDeferReason(); reason != "" {
		log.Printf("[asr] after-call pass with the live engine: %s", reason)
		return live, func() {}
	}
	select {
	case afterCallEngineSlot <- struct{}{}:
	case <-time.After(afterCallEngineWait):
		log.Printf("[asr] after-call pass with the live engine: Qwen3-ASR busy for %s", afterCallEngineWait)
		return live, func() {}
	}
	t, err := newQwen3ASRTranscriber(paths, defaultSherpaPaths().silero)
	if err != nil {
		<-afterCallEngineSlot
		log.Printf("[asr] qwen3-asr unavailable, using the live engine: %v", err)
		return live, func() {}
	}
	log.Printf("[asr] after-call pass with Qwen3-ASR")
	return t, func() {
		t.Stop()
		<-afterCallEngineSlot
	}
}

// afterCallModelDeferReason says why the large model should not be loaded
// right now: Low Power Mode, a low battery, or memory pressure.
func afterCallModelDeferReason() string {
	if lowPowerModeEnabled() {
		return "Low Power Mode is on"
	}
	if power := activitymon.PowerSource(); power.Known && power.OnBattery && power.Percent >= 0 && power.Percent < afterCallMinBatteryPercent {
		return fmt.Sprintf("on battery at %d%%", power.Percent)
	}
	if memoryPressureHigh() {
		return "memory is under pressure"
	}
	return ""
}

// afterCallModelView describes the model for the Settings row.
func (a *App) afterCallModelView() AfterCallSpeechModelView {
	paths := defaultQwen3ASRPaths()
	view := AfterCallSpeechModelView{
		Supported:     true,
		Name:          "Qwen3-ASR 0.6B",
		DownloadBytes: qwen3ASRDownloadBytes,
		DiskBytes:     qwen3ASRDiskBytes,
		Enabled:       a.afterCallSpeechModelEnabled(),
		Folder:        paths.root,
	}
	view.Removable = qwen3ASRFilesOnDisk(paths)
	view.Status = "absent"
	if qwen3ASRModelPresent(paths) {
		view.Status = "ready"
	}
	if manager, err := a.setupModelManager(); err == nil {
		for _, job := range manager.Downloads() {
			if job.ID != "qwen3-asr-0.6b" {
				continue
			}
			view.Downloaded, view.Total = job.BytesDone, job.BytesTotal
			view.LastError = job.Error
			switch job.State {
			case "queued", "downloading":
				view.Status = "downloading"
				view.Removable = false
			case "failed":
				view.Status = "failed"
			}
		}
	}
	return view
}

// removeAfterCallModel uses the same jobs and storage as setup and Settings.
func (a *App) removeAfterCallModel() error {
	manager, err := a.setupModelManager()
	if err != nil {
		return err
	}
	manager.Cancel("qwen3-asr-0.6b")
	return manager.Remove("qwen3-asr-0.6b")
}
