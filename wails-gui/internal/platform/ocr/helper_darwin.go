//go:build darwin

package ocr

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	darwinHelperName       = "yansu-ocr-helper"
	darwinHelperChildEnv   = "YANSU_OCR_HELPER_CHILD"
	darwinHelperDisableEnv = "YANSU_OCR_HELPER_DISABLE"
	darwinHelperPathEnv    = "YANSU_OCR_HELPER_PATH"
)

var darwinHelper = newHelperProcess(func() *exec.Cmd {
	cmd := exec.Command(darwinOCRHelperPath())
	cmd.Env = append(os.Environ(), darwinHelperChildEnv+"=1")
	return cmd
}, 20*time.Second, 3*time.Minute)

// PrewarmHelper exercises accurate Vision recognition before capture begins.
// Call it asynchronously at launch; it never reads the screen or user files.
func PrewarmHelper() {
	if darwinOCRHelperPath() == "" {
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, 320, 80))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	drawer := font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: basicfont.Face7x13, Dot: fixed.P(20, 40)}
	drawer.DrawString("Isoform OCR warmup")
	var data bytes.Buffer
	if err := jpeg.Encode(&data, img, nil); err != nil {
		log.Printf("[ocr] helper prewarm image failed: %v", err)
		return
	}
	if _, err := darwinHelper.run(helperRequest{Mode: "boxes", JPEGData: data.Bytes(), Priority: int(PriorityLow)}, true); err != nil {
		log.Printf("[ocr] helper prewarm failed: %v", err)
	}
}

// CloseHelper stops the persistent subprocess and reports its session counters.
func CloseHelper() { darwinHelper.close() }

func darwinOCRHelperPath() string {
	if os.Getenv(darwinHelperChildEnv) != "" || os.Getenv(darwinHelperDisableEnv) != "" {
		return ""
	}
	if override := strings.TrimSpace(os.Getenv(darwinHelperPathEnv)); override != "" {
		if fileExists(override) {
			return override
		}
	}

	candidates := make([]string, 0, 6)
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "..", "Resources", darwinHelperName),
			filepath.Join(dir, darwinHelperName),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "build", "bin", darwinHelperName),
			filepath.Join(wd, "wails-gui", "build", "bin", darwinHelperName),
		)
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func runDarwinOCRHelper(req helperRequest) (*helperResponse, error) {
	if darwinOCRHelperPath() == "" {
		return nil, nil
	}
	return darwinHelper.run(req, false)
}

func recognizeJPEGViaDarwinHelper(jpegData []byte, langs []string, prio Priority) (*Result, bool, error) {
	resp, err := runDarwinOCRHelper(helperRequest{
		Mode:     "text",
		JPEGData: jpegData,
		Langs:    langs,
		Priority: int(prio),
	})
	if resp == nil && err == nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	return &Result{Text: resp.Text, Confidence: resp.Confidence}, true, nil
}

func recognizeJPEGWithBoxesViaDarwinHelper(jpegData []byte, langs []string, prio Priority) ([]TextObservation, bool, error) {
	resp, err := runDarwinOCRHelper(helperRequest{
		Mode:     "boxes",
		JPEGData: jpegData,
		Langs:    langs,
		Priority: int(prio),
	})
	if resp == nil && err == nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	return resp.Observations, true, nil
}

func logDarwinOCRHelperFallback() {
	if os.Getenv(darwinHelperChildEnv) != "" || os.Getenv(darwinHelperDisableEnv) != "" {
		return
	}
	log.Printf("[ocr] darwin helper unavailable; falling back to in-process Vision OCR")
}
