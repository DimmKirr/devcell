package qemu

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
)

// ConvertPPMtoPNG reads a PPM (P6 binary) file and writes a PNG.
// Returns the PNG path (same base, .png extension).
func ConvertPPMtoPNG(ppmPath, pngPath string) error {
	f, err := os.Open(ppmPath)
	if err != nil {
		return fmt.Errorf("open PPM: %w", err)
	}
	defer f.Close()

	img, err := decodePPM(f)
	if err != nil {
		return fmt.Errorf("decode PPM: %w", err)
	}

	out, err := os.Create(pngPath)
	if err != nil {
		return fmt.Errorf("create PNG: %w", err)
	}
	defer out.Close()

	if err := png.Encode(out, img); err != nil {
		return fmt.Errorf("encode PNG: %w", err)
	}
	return nil
}

// decodePPM decodes a PPM P6 (binary) image.
func decodePPM(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)

	magic, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if magic != "P6" {
		return nil, fmt.Errorf("unsupported PPM format: %s (only P6 supported)", magic)
	}

	widthStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading width: %w", err)
	}
	heightStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading height: %w", err)
	}
	maxvalStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading maxval: %w", err)
	}

	var width, height, maxval int
	if _, err := fmt.Sscanf(widthStr, "%d", &width); err != nil {
		return nil, fmt.Errorf("parse width %q: %w", widthStr, err)
	}
	if _, err := fmt.Sscanf(heightStr, "%d", &height); err != nil {
		return nil, fmt.Errorf("parse height %q: %w", heightStr, err)
	}
	if _, err := fmt.Sscanf(maxvalStr, "%d", &maxval); err != nil {
		return nil, fmt.Errorf("parse maxval %q: %w", maxvalStr, err)
	}

	if width <= 0 || height <= 0 || maxval <= 0 || maxval > 65535 {
		return nil, fmt.Errorf("invalid PPM dimensions: %dx%d maxval=%d", width, height, maxval)
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	if maxval <= 255 {
		row := make([]byte, width*3)
		for y := 0; y < height; y++ {
			if _, err := io.ReadFull(br, row); err != nil {
				return nil, fmt.Errorf("reading pixel row %d: %w", y, err)
			}
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, color.RGBA{
					R: row[x*3],
					G: row[x*3+1],
					B: row[x*3+2],
					A: 255,
				})
			}
		}
	} else {
		row := make([]byte, width*6)
		for y := 0; y < height; y++ {
			if _, err := io.ReadFull(br, row); err != nil {
				return nil, fmt.Errorf("reading pixel row %d: %w", y, err)
			}
			for x := 0; x < width; x++ {
				r16 := uint16(row[x*6])<<8 | uint16(row[x*6+1])
				g16 := uint16(row[x*6+2])<<8 | uint16(row[x*6+3])
				b16 := uint16(row[x*6+4])<<8 | uint16(row[x*6+5])
				img.SetRGBA(x, y, color.RGBA{
					R: uint8(r16 >> 8),
					G: uint8(g16 >> 8),
					B: uint8(b16 >> 8),
					A: 255,
				})
			}
		}
	}

	return img, nil
}

// BluePixelRatio reads a PPM (P6) file and returns the fraction of pixels that
// are "blue" — i.e. B >= 120, B > R+40, B > G+30. This detects the Windows
// Setup installer's characteristic blue background.
func BluePixelRatio(ppmPath string) (float64, error) {
	f, err := os.Open(ppmPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	img, err := decodePPM(f)
	if err != nil {
		return 0, err
	}

	bounds := img.Bounds()
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return 0, nil
	}

	blue := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			ri, gi, bi := int(r>>8), int(g>>8), int(b>>8)
			if bi >= 120 && bi > ri+40 && bi > gi+30 {
				blue++
			}
		}
	}
	return float64(blue) / float64(total), nil
}

// readPPMToken reads the next whitespace-delimited token from a PPM header,
// skipping comments (lines starting with #).
func readPPMToken(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		for {
			ch, err := r.ReadByte()
			if err != nil {
				return "", err
			}
			if ch == '#' {
				// skip comment line
				for {
					c, err := r.ReadByte()
					if err != nil {
						return "", err
					}
					if c == '\n' {
						break
					}
				}
				continue
			}
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
				if b.Len() > 0 {
					return b.String(), nil
				}
				continue
			}
			b.WriteByte(ch)
		}
	}
}
