package qemu

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// pixelRatio returns the fraction of pixels in a PPM (P6) file for which
// match returns true.
func pixelRatio(ppmPath string, match func(r, g, b int) bool) (float64, error) {
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

	n := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if match(int(r>>8), int(g>>8), int(b>>8)) {
				n++
			}
		}
	}
	return float64(n) / float64(total), nil
}

// WhitePixelRatio returns the fraction of near-white pixels. The Windows 11
// Setup wizard is a large white window (~73% of the frame on the real UI).
func WhitePixelRatio(ppmPath string) (float64, error) {
	return pixelRatio(ppmPath, func(r, g, b int) bool {
		return r >= 230 && g >= 230 && b >= 230
	})
}

// WindowsPurpleRatio returns the fraction of pixels matching the Windows 11
// boot/setup backdrop (RGB 24,0,82 observed via QMP screendump). A mostly
// purple frame means the NT kernel has taken over the display; purple around
// a large white region means the Setup wizard is up.
func WindowsPurpleRatio(ppmPath string) (float64, error) {
	return pixelRatio(ppmPath, func(r, g, b int) bool {
		return b >= 60 && b <= 140 && r <= 70 && g <= 40 && b > r+20
	})
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

// ---------------------------------------------------------------------------
// Screen classification
// ---------------------------------------------------------------------------

// Thresholds for recognising a running Windows installer in a framebuffer dump.
//
// Two criteria, because the two generations of Setup look nothing alike:
// legacy media is a large flat blue field, while Windows 11 Setup is a mostly
// white wizard window on a purple backdrop and measures only ~1.2% blue. A
// Windows 11 frame therefore CANNOT be recognised by the blue criterion, and
// must never be labelled by it either — see screenshotName.
const (
	blueThreshold  = 0.20 // legacy Setup: flat blue field
	win11WhiteMin  = 0.30 // Win11 wizard window (real UI measures ~73%)
	win11PurpleMin = 0.05 // Win11 backdrop behind it (~16%)
)

// screenVerdict names the criterion that recognised a frame, or verdictNone.
type screenVerdict string

const (
	verdictNone        screenVerdict = "none"
	verdictClassicBlue screenVerdict = "blue"
	verdictWin11UI     screenVerdict = "win11"
)

// classifyScreen reports whether a frame shows a running Windows installer, and
// by which criterion. Ratios are 0..1, as returned by BluePixelRatio,
// WhitePixelRatio and WindowsPurpleRatio.
func classifyScreen(blue, white, purple float64) screenVerdict {
	if blue >= blueThreshold {
		return verdictClassicBlue
	}
	if white >= win11WhiteMin && purple >= win11PurpleMin {
		return verdictWin11UI
	}
	return verdictNone
}

// screenshotName is `<datetimeISO>-<screenName>-<id>.png`: the capture instant
// (UTC, ISO 8601 basic — no colons, filename-safe everywhere), the verdict with
// every measured ratio, then the poll number. Time first makes a directory
// listing sort chronologically and lets a frame be correlated with guest-side
// logs by wall clock, which a bare poll number cannot.
//
// The verdict and ratios stay in the name for triage: an earlier form encoded
// ScreenSource names how a frame was acquired. Frames from different
// sources see different surfaces — a QMP screendump reads the emulated
// framebuffer, an RDP capture reads what the guest renders into a session —
// so each gets its own directory and its own sequence.
type ScreenSource string

const (
	ScreenSourceQMP ScreenSource = "qmp"
	ScreenSourceRDP ScreenSource = "rdp"
)

// ScreenshotPath returns where a captured frame belongs:
//
//	<resultsDir>/screenshots/<source>/<ISO>-<screen>-<screenSeq>-<globalSeq>.<ext>
//
// screenSeq counts frames of the current screen (how long it has persisted),
// globalSeq counts every frame of the run — together they answer "what was
// on screen, for how long, and where in the run" from a directory listing.
// The caller creates the directory (see EnsureScreenshotDir).
func ScreenshotPath(resultsDir string, source ScreenSource, now time.Time,
	screen string, screenSeq, globalSeq int, ext string) string {
	name := fmt.Sprintf("%s-%s-%03d-%03d.%s",
		now.UTC().Format("20060102T150405Z"), screen, screenSeq, globalSeq, ext)
	return filepath.Join(resultsDir, "screenshots", string(source), name)
}

// EnsureScreenshotDir creates the directory ScreenshotPath writes into.
func EnsureScreenshotDir(resultsDir string, source ScreenSource) error {
	return os.MkdirAll(filepath.Join(resultsDir, "screenshots", string(source)), 0o755)
}

// only the blue ratio, so runs that PASSED on the white/purple criterion were
// written as `screen-007-blue1.png` and read as failures. Naming a frame after
// the number that did not decide it is worse than not naming it.
func screenshotName(now time.Time, attempt int, v screenVerdict, blue, white, purple float64) string {
	// qmp- prefix: these frames come from a QMP screendump of the emulated
	// framebuffer. Session captures (rdp-, vnc-) see a different surface —
	// the prefix keeps the two from being conflated during triage.
	return fmt.Sprintf("qmp-%s-%s-b%02.0f-w%02.0f-p%02.0f-%03d.png",
		now.UTC().Format("20060102T150405Z"), v, blue*100, white*100, purple*100, attempt)
}
