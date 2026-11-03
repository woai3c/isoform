package pii

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
)

// Safety cap: a single per-region PII mask that covers more than this
// fraction of the image is almost always a detector bug (span bbox
// blowup, mis-mapped observation, future regression), and pixelating it
// would blank most of the screenshot. Skipping it would leave the PII it
// was meant to hide visible, so MaskRegions fails instead and the caller
// drops the frame. Path-A whole-window blurs (BlurRegion/BlurRegionsBatch/
// BlurEntireImage) intentionally cover large areas and bypass this cap.
const maxMaskAreaFraction = 0.25

// ErrMaskRegionTooLarge is returned by MaskRegions when a detection region
// covers more than maxMaskAreaFraction of the image.
var ErrMaskRegionTooLarge = errors.New("pii mask region too large")

// MaskRegions applies pixelation to the specified detection regions on a JPEG image.
// Returns the masked JPEG data, or ErrMaskRegionTooLarge (and no image) when
// any region exceeds the safety cap: a frame is better lost than written
// half-masked.
func MaskRegions(jpegData []byte, detections []DetectionResult, jpegQuality int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()

	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	padding := 0.005
	imgArea := imgW * imgH
	for _, d := range detections {
		x0 := int(math.Max(0, (d.X-padding)*float64(imgW)))
		y0 := int(math.Max(0, (d.Y-padding)*float64(imgH)))
		x1 := int(math.Min(float64(imgW), (d.X+d.Width+padding)*float64(imgW)))
		y1 := int(math.Min(float64(imgH), (d.Y+d.Height+padding)*float64(imgH)))

		if x1 <= x0 || y1 <= y0 {
			continue
		}

		regionArea := (x1 - x0) * (y1 - y0)
		if imgArea > 0 && float64(regionArea)/float64(imgArea) > maxMaskAreaFraction {
			log.Printf("[pii] oversized mask: %dx%d covers %.0f%% of %dx%d (category=%s); not writing the frame",
				x1-x0, y1-y0, 100*float64(regionArea)/float64(imgArea), imgW, imgH, d.Category)
			return nil, fmt.Errorf("%w: %s region covers %.0f%% of the frame",
				ErrMaskRegionTooLarge, d.Category, 100*float64(regionArea)/float64(imgArea))
		}

		pixelate(rgba, x0, y0, x1, y1, 12)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WindowRegion describes a normalized (0-1) rectangular area for batch blurring.
type WindowRegion struct {
	X, Y, W, H float64
}

// BlurRegionsBatch decodes the JPEG once, applies all region blurs, then
// re-encodes once. This avoids the O(n) decode+encode cycle that the old
// per-region BlurRegion path had when multiple sensitive windows are visible.
func BlurRegionsBatch(jpegData []byte, regions []WindowRegion, jpegQuality int) ([]byte, error) {
	if len(regions) == 0 {
		return jpegData, nil
	}

	img, _, err := image.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	imgW := float64(bounds.Dx())
	imgH := float64(bounds.Dy())

	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	for _, r := range regions {
		x0 := int(math.Max(0, r.X*imgW))
		y0 := int(math.Max(0, r.Y*imgH))
		x1 := int(math.Min(imgW, (r.X+r.W)*imgW))
		y1 := int(math.Min(imgH, (r.Y+r.H)*imgH))
		pixelate(rgba, x0, y0, x1, y1, 24)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BlurRegion pixelates a normalized (0-1) rectangular region of the image.
// Used to blur a specific window area (e.g. a password manager window).
func BlurRegion(jpegData []byte, nx, ny, nw, nh float64, jpegQuality int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	imgW := float64(bounds.Dx())
	imgH := float64(bounds.Dy())

	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	x0 := int(math.Max(0, nx*imgW))
	y0 := int(math.Max(0, ny*imgH))
	x1 := int(math.Min(imgW, (nx+nw)*imgW))
	y1 := int(math.Min(imgH, (ny+nh)*imgH))

	pixelate(rgba, x0, y0, x1, y1, 24)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BlurEntireImage pixelates the entire image with large blocks.
// Used when a sensitive app (password manager, etc.) is detected.
func BlurEntireImage(jpegData []byte, jpegQuality int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	pixelate(rgba, bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y, 24)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pixelate replaces a region with blockSize x blockSize averaged blocks.
func pixelate(img *image.RGBA, x0, y0, x1, y1, blockSize int) {
	if blockSize < 1 {
		blockSize = 1
	}
	stride := img.Stride

	for by := y0; by < y1; by += blockSize {
		for bx := x0; bx < x1; bx += blockSize {
			bx1 := bx + blockSize
			if bx1 > x1 {
				bx1 = x1
			}
			by1 := by + blockSize
			if by1 > y1 {
				by1 = y1
			}

			var rSum, gSum, bSum uint64
			count := uint64(0)
			for py := by; py < by1; py++ {
				off := py*stride + bx*4
				for px := bx; px < bx1; px++ {
					rSum += uint64(img.Pix[off])
					gSum += uint64(img.Pix[off+1])
					bSum += uint64(img.Pix[off+2])
					count++
					off += 4
				}
			}

			if count == 0 {
				continue
			}

			avgR := uint8(rSum / count)
			avgG := uint8(gSum / count)
			avgB := uint8(bSum / count)

			for py := by; py < by1; py++ {
				off := py*stride + bx*4
				for px := bx; px < bx1; px++ {
					img.Pix[off] = avgR
					img.Pix[off+1] = avgG
					img.Pix[off+2] = avgB
					img.Pix[off+3] = 0xFF
					off += 4
				}
			}
		}
	}
}
