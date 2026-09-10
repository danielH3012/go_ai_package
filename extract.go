package goaipackage

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	//"golang.org/x/image/webp"
)

// HPF1SharpenKernel is configured with High-Boost filtering (sum = 1) for significantly stronger edge sharpening.
// By doubling the Laplacian high-frequency weight (center = 17, 8 neighbors = -2), character boundaries
// and fine text details are accentuated with maximum sharpness across each RGB channel.
var HPF1SharpenKernel = [3][3]int{
	{-1, -1, -1},
	{-1, 9, -1},
	{-1, -1, -1},
}

// ApplyHighPassFilter1RGB applies High Pass Filtering 1 independently across each color channel (Red, Green, Blue).
// It defaults to HPF1SharpenKernel to ensure character contrast is maximized without inverting text to black background.
func ApplyHighPassFilter1RGB(content []byte) ([]byte, error) {
	return ApplyHighPassFilter1RGBWithKernel(content, HPF1SharpenKernel)
}

// ApplyHighPassFilter1RGBWithKernel applies a 3x3 convolution kernel independently across R, G, B channels of an image.
// If decoding fails, it returns the original content gracefully so processing is never blocked.
func ApplyHighPassFilter1RGBWithKernel(content []byte, kernel [3][3]int) ([]byte, error) {
	srcImg, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		// Fallback to original content on decode failure (e.g. unsupported or corrupted format)
		return content, nil
	}

	bounds := srcImg.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return content, nil
	}

	// Normalize input to contiguous RGBA in memory
	srcRGBA := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(srcRGBA, srcRGBA.Bounds(), srcImg, bounds.Min, draw.Src)

	dstRGBA := image.NewRGBA(image.Rect(0, 0, width, height))

	// Perform 2D convolution for each color channel (R, G, B) independently
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var sumR, sumG, sumB int

			for ky := -1; ky <= 1; ky++ {
				ny := y + ky
				if ny < 0 {
					ny = 0
				} else if ny >= height {
					ny = height - 1
				}
				wRow := kernel[ky+1]
				rowOffset := ny * srcRGBA.Stride

				for kx := -1; kx <= 1; kx++ {
					weight := wRow[kx+1]
					if weight == 0 {
						continue
					}
					nx := x + kx
					if nx < 0 {
						nx = 0
					} else if nx >= width {
						nx = width - 1
					}

					pixOffset := rowOffset + nx*4
					sumR += int(srcRGBA.Pix[pixOffset]) * weight
					sumG += int(srcRGBA.Pix[pixOffset+1]) * weight
					sumB += int(srcRGBA.Pix[pixOffset+2]) * weight
				}
			}

			dstOffset := y*dstRGBA.Stride + x*4
			dstRGBA.Pix[dstOffset] = clampUint8(sumR)
			dstRGBA.Pix[dstOffset+1] = clampUint8(sumG)
			dstRGBA.Pix[dstOffset+2] = clampUint8(sumB)
			dstRGBA.Pix[dstOffset+3] = 255 // Opaque alpha
		}
	}

	var outBuf bytes.Buffer
	if err := png.Encode(&outBuf, dstRGBA); err != nil {
		return content, nil
	}
	return outBuf.Bytes(), nil
}

func clampUint8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func ExtractText(content []byte, filename string) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return extractPDF(content)
	case strings.HasSuffix(lower, ".xlsx"), strings.HasSuffix(lower, ".xls"):
		return ExtractExcelText(content)
	case strings.HasSuffix(lower, ".csv"):
		return ExtractCSVText(content)
	case isImage(filename):
		return ExtractImageOCR(content, filename)
	default:
		return string(content), nil
	}
}

func ExtractImageOCR(content []byte, filename string) (string, error) {
	// 1. Locate tesseract binary
	tessBin := "tesseract"
	if _, err := exec.LookPath("tesseract"); err != nil {
		candidates := []string{
			`C:\Program Files\Tesseract-OCR\tesseract.exe`,
			`C:\Program Files (x86)\Tesseract-OCR\tesseract.exe`,
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Tesseract-OCR", "tesseract.exe"),
		}
		found := false
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				tessBin = c
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("Tesseract OCR binary not found in PATH or standard directories. Please install Tesseract (e.g. winget install UB-Mannheim.TesseractOCR)")
		}
	}

	// 2. Preprocess image: Apply High Pass Filtering 1 for each color channel (R, G, B)
	filtered, err := ApplyHighPassFilter1RGB(content)
	isProcessedPNG := false
	if err == nil && len(filtered) > 0 {
		content = filtered
		isProcessedPNG = true
	}

	// 3. Write preprocessed image to temporary file
	ext := strings.ToLower(filepath.Ext(filename))
	if isProcessedPNG || ext == "" {
		ext = ".png"
	}
	pattern := fmt.Sprintf("ocr-*%s", ext)
	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(content); err != nil {
		return "", err
	}
	tmpFile.Close()

	// 4. Try with multilingual (eng+ind), fallback to default if language pack missing
	cmd := exec.Command(tessBin, tmpFile.Name(), "stdout", "-l", "eng+ind")
	out, err := cmd.Output()
	if err != nil {
		// Fallback to default language
		cmd = exec.Command(tessBin, tmpFile.Name(), "stdout")
		out, err = cmd.Output()
		if err != nil {
			return "", fmt.Errorf("tesseract OCR failed: %w", err)
		}
	}
	return string(out), nil
}

func isImage(filename string) bool {
	f := strings.ToLower(filename)
	return strings.HasSuffix(f, ".png") || strings.HasSuffix(f, ".jpg") || strings.HasSuffix(f, ".jpeg") || strings.HasSuffix(f, ".webp")
}

func extractPDF(content []byte) (string, error) {
	reader := bytes.NewReader(content)
	r, err := pdf.NewReader(reader, int64(len(content)))
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	b, err := r.GetPlainText()
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(&buf, b); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func ExtractExcelText(content []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	defer f.Close()

	var sb strings.Builder

	for _, sheetName := range f.GetSheetList() {
		sb.WriteString(fmt.Sprintf("=== Sheet: %s ===\n", sheetName))

		rows, err := f.GetRows(sheetName)
		if err != nil {
			continue
		}

		for i, row := range rows {
			if i == 0 {
				sb.WriteString(fmt.Sprintf("[Headers]: %s\n", strings.Join(row, " | ")))
			} else {
				sb.WriteString(fmt.Sprintf("Row %d: %s\n", i, strings.Join(row, " | ")))
			}
		}
	}

	return sb.String(), nil
}

func ExtractCSVText(content []byte) (string, error) {
	reader := csv.NewReader(bytes.NewReader(content))
	rows, err := reader.ReadAll()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for i, row := range rows {
		if i == 0 {
			sb.WriteString(fmt.Sprintf("[Headers]: %s\n", strings.Join(row, " | ")))
		} else {
			sb.WriteString(fmt.Sprintf("Row %d: %s\n", i, strings.Join(row, " | ")))
		}
	}
	return sb.String(), nil
}
