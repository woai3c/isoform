package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/xuri/excelize/v2"
	"golang.org/x/image/draw"
)

const (
	maxExtractedTextLen   = 200000 // ~50K tokens, safe for Kimi's 256K context
	scannedPDFTextLimit   = 100    // below this, treat PDF as scanned/image-based
	maxPDFPageImages      = 20     // max pages to extract as images
	pageImageMaxDimension = 1600   // resize page images to this max dimension
	pageImageJPEGQuality  = 75
)

type FileCategory string

const (
	FileCategoryImage        FileCategory = "image"
	FileCategoryDocument     FileCategory = "document"
	FileCategorySpreadsheet  FileCategory = "spreadsheet"
	FileCategoryPresentation FileCategory = "presentation"
	FileCategoryCode         FileCategory = "code"
	FileCategoryText         FileCategory = "text"
	FileCategoryArchive      FileCategory = "archive"
)

func detectFileCategory(mimeType, fileName string) FileCategory {
	ext := strings.ToLower(filepath.Ext(fileName))

	if strings.HasPrefix(mimeType, "image/") {
		return FileCategoryImage
	}

	switch ext {
	case ".pdf":
		return FileCategoryDocument
	case ".doc", ".docx", ".rtf", ".odt":
		return FileCategoryDocument
	case ".xls", ".xlsx", ".ods":
		return FileCategorySpreadsheet
	case ".csv", ".tsv":
		return FileCategorySpreadsheet
	case ".ppt", ".pptx":
		return FileCategoryPresentation
	case ".zip", ".tar", ".gz", ".7z", ".rar":
		return FileCategoryArchive
	case ".js", ".ts", ".jsx", ".tsx", ".py", ".go", ".java", ".c", ".cpp", ".h", ".hpp",
		".rs", ".rb", ".php", ".swift", ".kt", ".scala", ".sql",
		".html", ".htm", ".css", ".scss", ".less",
		".sh", ".bash", ".zsh", ".ps1",
		".diff", ".patch":
		return FileCategoryCode
	case ".json", ".xml", ".yaml", ".yml", ".toml", ".ini", ".conf", ".cfg", ".env",
		".md", ".markdown", ".txt", ".log", ".gitignore", ".dockerignore":
		return FileCategoryText
	}

	if strings.HasPrefix(mimeType, "text/") {
		return FileCategoryText
	}
	if strings.Contains(mimeType, "json") || strings.Contains(mimeType, "xml") {
		return FileCategoryText
	}

	switch mimeType {
	case "application/pdf":
		return FileCategoryDocument
	case "application/msword",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/rtf":
		return FileCategoryDocument
	case "application/vnd.ms-excel",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return FileCategorySpreadsheet
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return FileCategoryPresentation
	}

	return FileCategoryText
}

type fileProcessResult struct {
	text     string
	category FileCategory
}

func processFileContent(dataBase64, fileName, mimeType, workDir string) (*fileProcessResult, error) {
	category := detectFileCategory(mimeType, fileName)

	if category == FileCategoryImage || category == FileCategoryArchive {
		return &fileProcessResult{category: category}, nil
	}

	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	var text string
	ext := strings.ToLower(filepath.Ext(fileName))

	switch {
	case ext == ".pdf" || mimeType == "application/pdf":
		text, err = extractPDFText(data)
		if err == nil && len(strings.TrimSpace(text)) < scannedPDFTextLimit {
			paths, imgErr := extractPDFPageImagesToDir(data, workDir, fileName, maxPDFPageImages)
			if imgErr == nil && len(paths) > 0 {
				log.Printf("[file-processor] scanned PDF detected: %s — saved %d page images to disk", fileName, len(paths))
				var listing strings.Builder
				listing.WriteString(fmt.Sprintf("This is a scanned/image-based PDF with %d page(s). The pages have been saved as image files that you can read:\n", len(paths)))
				for i, p := range paths {
					listing.WriteString(fmt.Sprintf("  Page %d: %s\n", i+1, p))
				}
				listing.WriteString("Use the Read tool to view each page image and analyze the content.")
				return &fileProcessResult{
					text:     listing.String(),
					category: category,
				}, nil
			}
		}
	case ext == ".docx" || mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		text, err = extractDocxText(data)
	case ext == ".xlsx" || mimeType == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		text, err = extractXlsxText(data)
	case ext == ".csv" || ext == ".tsv" || mimeType == "text/csv":
		text = extractPlainText(data)
	default:
		text = extractPlainText(data)
	}

	if err != nil {
		text = fmt.Sprintf("[Failed to extract text from %s: %v]", fileName, err)
	}

	return &fileProcessResult{
		text:     truncateFileText(text, maxExtractedTextLen),
		category: category,
	}, nil
}

func extractPDFText(content []byte) (string, error) {
	reader := bytes.NewReader(content)
	pdfReader, err := pdf.NewReader(reader, int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("parse PDF: %w", err)
	}

	var result strings.Builder
	for i := 1; i <= pdfReader.NumPage(); i++ {
		page := pdfReader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		result.WriteString(text)
		result.WriteString("\n")
	}

	extracted := strings.TrimSpace(result.String())
	if len(extracted) < 10 {
		return "[PDF content - text extraction limited (may be image-based PDF)]", nil
	}
	return extracted, nil
}

func extractDocxText(content []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}

	var result strings.Builder
	for _, f := range r.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		extractDocxXML(rc, &result)
		rc.Close()
	}

	extracted := strings.TrimSpace(result.String())
	if len(extracted) < 10 {
		return "[Word document - no text content found]", nil
	}
	return extracted, nil
}

func extractDocxXML(r io.Reader, out *strings.Builder) {
	decoder := xml.NewDecoder(r)
	var inText bool
	for {
		if out.Len() >= maxExtractedTextLen {
			break
		}
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch se := tok.(type) {
		case xml.StartElement:
			inText = se.Name.Local == "t"
		case xml.EndElement:
			if se.Name.Local == "p" {
				out.WriteString("\n")
			}
			if se.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				out.Write(se)
			}
		}
	}
}

func extractXlsxText(content []byte) (string, error) {
	reader := bytes.NewReader(content)
	f, err := excelize.OpenReader(reader)
	if err != nil {
		return "", fmt.Errorf("parse XLSX: %w", err)
	}
	defer f.Close()

	var result strings.Builder
	for _, sheetName := range f.GetSheetList() {
		result.WriteString(fmt.Sprintf("=== Sheet: %s ===\n", sheetName))
		rows, err := f.Rows(sheetName)
		if err != nil {
			continue
		}
		for rows.Next() {
			row, err := rows.Columns()
			if err != nil {
				break
			}
			result.WriteString(strings.Join(row, "\t"))
			result.WriteString("\n")
			if result.Len() >= maxExtractedTextLen {
				rows.Close()
				break
			}
		}
		rows.Close()
		result.WriteString("\n")
		if result.Len() >= maxExtractedTextLen {
			break
		}
	}

	extracted := strings.TrimSpace(result.String())
	if len(extracted) < 10 {
		return "[Excel spreadsheet - no content found]", nil
	}
	return extracted, nil
}

func extractPlainText(content []byte) string {
	if !utf8.Valid(content) {
		return "[Binary file - cannot extract text]"
	}
	return strings.TrimSpace(string(content))
}

func truncateFileText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	for maxLen > 0 && !utf8.RuneStart(text[maxLen]) {
		maxLen--
	}
	truncated := text[:maxLen]
	if idx := strings.LastIndex(truncated, "\n"); idx > maxLen-1000 {
		truncated = truncated[:idx]
	}
	return truncated + "\n\n[... content truncated ...]"
}

func formatBytes(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.0f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func buildFileContextBlock(fileName, mimeType string, fileSize int64, extractedText string) string {
	if extractedText == "" {
		return ""
	}
	return fmt.Sprintf("<attached-file name=%q type=%q size=%q>\n%s\n</attached-file>",
		fileName, mimeType, formatBytes(fileSize), extractedText)
}

func extractPDFPageImagesToDir(content []byte, workDir, fileName string, maxPages int) ([]string, error) {
	tmpFile, err := os.CreateTemp("", "yansu-pdf-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	extractDir, err := os.MkdirTemp("", "yansu-pdf-extract-*")
	if err != nil {
		return nil, fmt.Errorf("create extract dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	selectedPages := []string{fmt.Sprintf("1-%d", maxPages)}
	if err := api.ExtractImagesFile(tmpPath, extractDir, selectedPages, nil); err != nil {
		return nil, fmt.Errorf("extract images: %w", err)
	}

	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return nil, fmt.Errorf("read extract dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	baseName := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	targetDir := filepath.Join(workDir, ".yansu-attachments")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create attachment dir: %w", err)
	}

	var savedPaths []string
	pageNum := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		srcPath := filepath.Join(extractDir, entry.Name())
		data, err := os.ReadFile(srcPath)
		if err != nil || len(data) < 1000 {
			continue
		}
		pageNum++

		resized, resizeErr := resizeAndCompressImage(data)
		ext := filepath.Ext(entry.Name())
		destName := fmt.Sprintf("%s_page_%d%s", baseName, pageNum, ext)
		if resizeErr == nil {
			destName = fmt.Sprintf("%s_page_%d.jpg", baseName, pageNum)
			data = resized
		}

		destPath := filepath.Join(targetDir, destName)
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			continue
		}
		savedPaths = append(savedPaths, destPath)
	}

	return savedPaths, nil
}

func resizeAndCompressImage(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	maxDim := pageImageMaxDimension
	if w <= maxDim && h <= maxDim {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: pageImageJPEGQuality}); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	var newW, newH int
	if w > h {
		newW = maxDim
		newH = h * maxDim / w
	} else {
		newH = maxDim
		newW = w * maxDim / h
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: pageImageJPEGQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
