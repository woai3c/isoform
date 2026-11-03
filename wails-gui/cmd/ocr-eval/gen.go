package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"

	"wails-gui/internal/ocr/eval"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type textBlock struct {
	text     string
	fontSize float64
}

type genSample struct {
	id       string
	lang     string
	category string
	blocks   []textBlock
	bg       color.RGBA
	fg       color.RGBA
	width    int
	height   int
	jpegQ    int
}

func defaultSamples() []genSample {
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{30, 30, 30, 255}
	darkBg := color.RGBA{40, 44, 52, 255}
	lightFg := color.RGBA{200, 200, 200, 255}

	return []genSample{
		{
			id: "zh_large_light", lang: "zh", category: "chat",
			blocks: []textBlock{
				{text: "你好世界", fontSize: 32},
				{text: "这是一段中文测试文本", fontSize: 32},
			},
			bg: white, fg: black, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "zh_medium_light", lang: "zh", category: "chat",
			blocks: []textBlock{
				{text: "飞书消息：项目进度更新", fontSize: 20},
				{text: "请查看最新的设计稿并提供反馈", fontSize: 20},
			},
			bg: white, fg: black, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "zh_small_light", lang: "zh", category: "ide",
			blocks: []textBlock{
				{text: "函数返回值类型不匹配", fontSize: 14},
				{text: "变量未使用请检查代码", fontSize: 14},
			},
			bg: white, fg: black, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "zh_small_dark", lang: "zh", category: "ide",
			blocks: []textBlock{
				{text: "函数返回值类型不匹配", fontSize: 14},
				{text: "变量未使用请检查代码", fontSize: 14},
			},
			bg: darkBg, fg: lightFg, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "zh_small_lowq", lang: "zh", category: "ide",
			blocks: []textBlock{
				{text: "函数返回值类型不匹配", fontSize: 14},
				{text: "变量未使用请检查代码", fontSize: 14},
			},
			bg: white, fg: black, width: 800, height: 200, jpegQ: 50,
		},
		{
			id: "en_large_light", lang: "en", category: "browser",
			blocks: []textBlock{
				{text: "Hello World", fontSize: 32},
				{text: "The quick brown fox jumps over the lazy dog", fontSize: 32},
			},
			bg: white, fg: black, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "en_small_light", lang: "en", category: "ide",
			blocks: []textBlock{
				{text: "func main() {", fontSize: 14},
				{text: "    fmt.Println(\"hello\")", fontSize: 14},
				{text: "}", fontSize: 14},
			},
			bg: white, fg: black, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "en_small_dark", lang: "en", category: "ide",
			blocks: []textBlock{
				{text: "func main() {", fontSize: 14},
				{text: "    fmt.Println(\"hello\")", fontSize: 14},
				{text: "}", fontSize: 14},
			},
			bg: darkBg, fg: lightFg, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "mixed_zh_en", lang: "zh-en", category: "ide",
			blocks: []textBlock{
				{text: "// 初始化数据库连接", fontSize: 16},
				{text: "db, err := sql.Open(\"postgres\", connStr)", fontSize: 16},
				{text: "// 检查错误并返回", fontSize: 16},
			},
			bg: white, fg: black, width: 800, height: 200, jpegQ: 90,
		},
		{
			id: "mixed_zh_en_dark", lang: "zh-en", category: "ide",
			blocks: []textBlock{
				{text: "// 初始化数据库连接", fontSize: 16},
				{text: "db, err := sql.Open(\"postgres\", connStr)", fontSize: 16},
				{text: "// 检查错误并返回", fontSize: 16},
			},
			bg: darkBg, fg: lightFg, width: 800, height: 200, jpegQ: 90,
		},
	}
}

func generateDataset(outDir string, fontData []byte) (*eval.Dataset, error) {
	if err := os.MkdirAll(filepath.Join(outDir, "images"), 0755); err != nil {
		return nil, err
	}

	f, err := opentype.Parse(fontData)
	if err != nil {
		return nil, fmt.Errorf("parse font: %w", err)
	}

	ds := &eval.Dataset{
		Name:        "ocr-eval-synthetic",
		Description: "Synthetic test images for OCR quality evaluation",
		Version:     "1.0",
	}

	for _, gs := range defaultSamples() {
		img := image.NewRGBA(image.Rect(0, 0, gs.width, gs.height))
		fillRect(img, gs.bg)

		groundTruth := ""
		y := 40.0
		for i, blk := range gs.blocks {
			face, err := opentype.NewFace(f, &opentype.FaceOptions{
				Size:    blk.fontSize,
				DPI:     144,
				Hinting: font.HintingFull,
			})
			if err != nil {
				return nil, fmt.Errorf("create face: %w", err)
			}

			drawText(img, face, 20, int(y), blk.text, gs.fg)
			face.Close()
			y += blk.fontSize*2.5 + 4

			if i > 0 {
				groundTruth += "\n"
			}
			groundTruth += blk.text
		}

		imgPath := filepath.Join(outDir, "images", gs.id+".jpg")
		if err := saveJPEG(img, imgPath, gs.jpegQ); err != nil {
			return nil, fmt.Errorf("save %s: %w", gs.id, err)
		}

		ds.Samples = append(ds.Samples, eval.Sample{
			ID:          gs.id,
			ImagePath:   imgPath,
			Language:    gs.lang,
			Category:    gs.category,
			GroundTruth: groundTruth,
		})
	}

	return ds, nil
}

func fillRect(img *image.RGBA, c color.RGBA) {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawText(img *image.RGBA, face font.Face, x, y int, text string, fg color.RGBA) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(fg),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(text)
}

func saveJPEG(img *image.RGBA, path string, quality int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: quality})
}
