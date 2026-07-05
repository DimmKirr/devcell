package serve

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateIcon_ReturnsValidPNG(t *testing.T) {
	data, err := GenerateIcon("Dev Desktop 1", 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty PNG data")
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a valid PNG: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 32 || bounds.Dy() != 32 {
		t.Errorf("dimensions = %dx%d, want 32x32", bounds.Dx(), bounds.Dy())
	}
}

func TestGenerateIcon_HasWallpaperBackground(t *testing.T) {
	data, _ := GenerateIcon("Test", 32)
	img, _ := png.Decode(bytes.NewReader(data))

	// Top-left pixel should match wallpaper colors (not black or white)
	r, g, b, _ := img.At(0, 0).RGBA()
	r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)

	if r8 == 0 && g8 == 0 && b8 == 0 {
		t.Error("top-left pixel is black — wallpaper background not applied")
	}
	if r8 == 255 && g8 == 255 && b8 == 255 {
		t.Error("top-left pixel is white — wallpaper background not applied")
	}
}

func TestGenerateIcon_TextIsDrawnInLowerRight(t *testing.T) {
	data, _ := GenerateIcon("Dev Desktop 1", 64)
	img, _ := png.Decode(bytes.NewReader(data))

	// Sample the lower-right quadrant for text pixels (white on dark bg)
	hasText := false
	for y := 42; y < 62; y++ {
		for x := 4; x < 60; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
			if r8 > 200 && g8 > 200 && b8 > 200 {
				hasText = true
				break
			}
		}
		if hasText {
			break
		}
	}
	if !hasText {
		t.Error("expected white text pixels in lower-right area")
	}
}

func TestGenerateIcon_LargerSize(t *testing.T) {
	data, err := GenerateIcon("Test", 64)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(data))
	bounds := img.Bounds()
	if bounds.Dx() != 64 || bounds.Dy() != 64 {
		t.Errorf("dimensions = %dx%d, want 64x64", bounds.Dx(), bounds.Dy())
	}
}

func TestWorkspaceHandler_IconEndpoint(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/icons/mock-desktop-1.png", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}

	_, err := png.Decode(rec.Body)
	if err != nil {
		t.Fatalf("response not valid PNG: %v", err)
	}
}

func TestWorkspaceHandler_IconEndpoint_404ForUnknown(t *testing.T) {
	mux := workspaceMux("localhost:8484")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/icons/nonexistent.png", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
