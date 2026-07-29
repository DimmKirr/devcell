package qemu

import (
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePPMP6(t *testing.T, path string, width, height int, fill color.RGBA) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	// P6 header
	_, err = fmt.Fprintf(f, "P6\n%d %d\n255\n", width, height)
	require.NoError(t, err)

	// RGB pixel data
	row := make([]byte, width*3)
	for x := 0; x < width; x++ {
		row[x*3] = fill.R
		row[x*3+1] = fill.G
		row[x*3+2] = fill.B
	}
	for y := 0; y < height; y++ {
		_, err := f.Write(row)
		require.NoError(t, err)
	}
}

func TestConvertPPMtoPNG_BasicConversion(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "test.ppm")
	pngPath := filepath.Join(dir, "test.png")

	writePPMP6(t, ppmPath, 4, 3, color.RGBA{R: 255, G: 0, B: 128, A: 255})

	err := ConvertPPMtoPNG(ppmPath, pngPath)
	require.NoError(t, err)

	// Verify PNG is valid and has correct dimensions
	f, err := os.Open(pngPath)
	require.NoError(t, err)
	defer f.Close()

	img, err := png.Decode(f)
	require.NoError(t, err)
	assert.Equal(t, 4, img.Bounds().Dx())
	assert.Equal(t, 3, img.Bounds().Dy())

	// Verify pixel color
	r, g, b, a := img.At(0, 0).RGBA()
	assert.Equal(t, uint32(0xffff), r)
	assert.Equal(t, uint32(0), g)
	assert.Equal(t, uint32(0x8080), b)
	assert.Equal(t, uint32(0xffff), a)
}

func TestConvertPPMtoPNG_PreservesPixels(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "multi.ppm")
	pngPath := filepath.Join(dir, "multi.png")

	// Write a 2x2 PPM with different colors per pixel
	f, err := os.Create(ppmPath)
	require.NoError(t, err)
	_, err = f.Write([]byte("P6\n2 2\n255\n"))
	require.NoError(t, err)
	pixels := []byte{
		255, 0, 0, 0, 255, 0, // row 0: red, green
		0, 0, 255, 255, 255, 255, // row 1: blue, white
	}
	_, err = f.Write(pixels)
	require.NoError(t, err)
	f.Close()

	err = ConvertPPMtoPNG(ppmPath, pngPath)
	require.NoError(t, err)

	pf, err := os.Open(pngPath)
	require.NoError(t, err)
	defer pf.Close()
	img, err := png.Decode(pf)
	require.NoError(t, err)

	assertPixel := func(x, y int, expected color.RGBA) {
		t.Helper()
		r, g, b, _ := img.At(x, y).RGBA()
		assert.Equal(t, uint32(expected.R)<<8|uint32(expected.R), r, "R at (%d,%d)", x, y)
		assert.Equal(t, uint32(expected.G)<<8|uint32(expected.G), g, "G at (%d,%d)", x, y)
		assert.Equal(t, uint32(expected.B)<<8|uint32(expected.B), b, "B at (%d,%d)", x, y)
	}

	assertPixel(0, 0, color.RGBA{R: 255, G: 0, B: 0})
	assertPixel(1, 0, color.RGBA{R: 0, G: 255, B: 0})
	assertPixel(0, 1, color.RGBA{R: 0, G: 0, B: 255})
	assertPixel(1, 1, color.RGBA{R: 255, G: 255, B: 255})
}

func TestConvertPPMtoPNG_WithComments(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "comment.ppm")
	pngPath := filepath.Join(dir, "comment.png")

	f, err := os.Create(ppmPath)
	require.NoError(t, err)
	_, err = f.Write([]byte("P6\n# QEMU screendump\n1 1\n255\n"))
	require.NoError(t, err)
	_, err = f.Write([]byte{42, 84, 126})
	require.NoError(t, err)
	f.Close()

	err = ConvertPPMtoPNG(ppmPath, pngPath)
	require.NoError(t, err)

	pf, err := os.Open(pngPath)
	require.NoError(t, err)
	defer pf.Close()
	img, err := png.Decode(pf)
	require.NoError(t, err)
	assert.Equal(t, 1, img.Bounds().Dx())
	assert.Equal(t, 1, img.Bounds().Dy())
}

func TestConvertPPMtoPNG_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "bad.ppm")
	pngPath := filepath.Join(dir, "bad.png")

	os.WriteFile(ppmPath, []byte("P3\n1 1\n255\n255 0 0"), 0644)

	err := ConvertPPMtoPNG(ppmPath, pngPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "P3")
}

func TestConvertPPMtoPNG_MissingFile(t *testing.T) {
	err := ConvertPPMtoPNG("/nonexistent/path.ppm", "/tmp/out.png")
	assert.Error(t, err)
}

func TestConvertPPMtoPNG_LargerImage(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "large.ppm")
	pngPath := filepath.Join(dir, "large.png")

	writePPMP6(t, ppmPath, 1920, 1080, color.RGBA{R: 100, G: 150, B: 200, A: 255})

	err := ConvertPPMtoPNG(ppmPath, pngPath)
	require.NoError(t, err)

	info, err := os.Stat(pngPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	pf, err := os.Open(pngPath)
	require.NoError(t, err)
	defer pf.Close()
	cfg, err := png.DecodeConfig(pf)
	require.NoError(t, err)
	assert.Equal(t, 1920, cfg.Width)
	assert.Equal(t, 1080, cfg.Height)
}
