package tart

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"time"
)

// RFB client→server message types for framebuffer capture.
const (
	rfbFramebufferUpdateRequest = 3
)

// RFB server→client message types.
const (
	rfbServerFramebufferUpdate = 0
)

// RFB encoding types.
const (
	rfbEncodingRaw = 0
)

// CaptureScreenshot requests a full framebuffer update from the VNC server
// and saves it as a PNG file. The VNC client must already be connected.
//
// Uses the server's default pixel format (from ServerInit) — does NOT send
// SetPixelFormat or SetEncodings, because Apple's _VZVNCServer crashes (SIGTRAP)
// when it receives a SetEncodings with only RAW encoding.
func (c *VNCClient) CaptureScreenshot(path string) error {
	// Drain any stale server messages that accumulated while we weren't reading
	// (e.g. during a long wait directive). Without this, the read loop hits
	// queued framebuffer updates from earlier and times out.
	c.drainPending()

	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer c.conn.SetDeadline(time.Time{})

	if err := c.requestFullFramebuffer(); err != nil {
		return fmt.Errorf("FramebufferUpdateRequest: %w", err)
	}

	img, err := c.readFramebufferUpdate()
	if err != nil {
		return fmt.Errorf("reading framebuffer: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encoding PNG: %w", err)
	}
	return nil
}

// CaptureFramebuffer grabs the current screen as an in-memory RGBA image
// without writing to disk. Used for screen-content analysis (brightness polling).
func (c *VNCClient) CaptureFramebuffer() (*image.RGBA, error) {
	c.drainPending()

	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer c.conn.SetDeadline(time.Time{})

	if err := c.requestFullFramebuffer(); err != nil {
		return nil, fmt.Errorf("FramebufferUpdateRequest: %w", err)
	}
	return c.readFramebufferUpdate()
}

// ScreenBrightness returns the mean brightness (0.0–1.0) of an RGBA image.
// 0.0 = fully black, 1.0 = fully white.
func ScreenBrightness(img *image.RGBA) float64 {
	bounds := img.Bounds()
	totalPixels := bounds.Dx() * bounds.Dy()
	if totalPixels == 0 {
		return 0
	}

	// Sample every 8th pixel for speed (1920×1200 = 288K pixels → ~36K samples).
	var sum uint64
	var count int
	for i := 0; i < len(img.Pix); i += 4 * 8 {
		r := uint64(img.Pix[i])
		g := uint64(img.Pix[i+1])
		b := uint64(img.Pix[i+2])
		sum += (r + g + b)
		count++
	}
	if count == 0 {
		return 0
	}
	// max possible per sample = 255*3 = 765
	return float64(sum) / float64(count) / 765.0
}

// drainPending reads and discards any queued server messages on the connection.
// Unlike raw byte draining, this parses complete RFB messages so the protocol
// stream stays aligned. Returns silently once the buffer is empty.
func (c *VNCClient) drainPending() {
	bpp := int(c.pixFmt.BitsPerPixel) / 8
	if bpp < 1 {
		bpp = 4
	}

	drainDeadline := time.Now().Add(1 * time.Second)

	for {
		if time.Now().After(drainDeadline) {
			return
		}
		// Short deadline to detect an empty buffer — if no message type byte
		// arrives within 100ms, there's nothing queued.
		c.conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
		var msgType uint8
		if err := binary.Read(c.conn, binary.BigEndian, &msgType); err != nil {
			c.conn.SetDeadline(time.Time{})
			return
		}

		// Got a message — give enough time to read the full body.
		c.conn.SetDeadline(time.Now().Add(5 * time.Second))

		switch msgType {
		case rfbServerFramebufferUpdate:
			var header struct {
				Padding uint8
				NumRect uint16
			}
			if err := binary.Read(c.conn, binary.BigEndian, &header); err != nil {
				c.conn.SetDeadline(time.Time{})
				return
			}
			for i := 0; i < int(header.NumRect); i++ {
				var rect struct {
					X, Y, W, H   uint16
					EncodingType int32
				}
				if err := binary.Read(c.conn, binary.BigEndian, &rect); err != nil {
					c.conn.SetDeadline(time.Time{})
					return
				}
				pixelBytes := int64(rect.W) * int64(rect.H) * int64(bpp)
				if pixelBytes > 0 {
					if _, err := io.CopyN(io.Discard, c.conn, pixelBytes); err != nil {
						c.conn.SetDeadline(time.Time{})
						return
					}
				}
			}
		default:
			if err := c.skipServerMessage(msgType); err != nil {
				c.conn.SetDeadline(time.Time{})
				return
			}
		}
	}
}

// requestFullFramebuffer asks the server for a complete framebuffer update.
func (c *VNCClient) requestFullFramebuffer() error {
	msg := make([]byte, 10)
	msg[0] = rfbFramebufferUpdateRequest
	msg[1] = 0 // incremental = false (full update)
	// x-position = 0, y-position = 0
	binary.BigEndian.PutUint16(msg[6:], c.width)
	binary.BigEndian.PutUint16(msg[8:], c.height)
	_, err := c.conn.Write(msg)
	return err
}

// readFramebufferUpdate reads the server's FramebufferUpdate response and
// assembles it into an RGBA image using the server's pixel format.
func (c *VNCClient) readFramebufferUpdate() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, int(c.width), int(c.height)))
	bpp := int(c.pixFmt.BitsPerPixel) / 8
	if bpp < 1 {
		bpp = 4
	}

	for {
		var msgType uint8
		if err := binary.Read(c.conn, binary.BigEndian, &msgType); err != nil {
			return nil, fmt.Errorf("reading message type: %w", err)
		}

		if msgType != rfbServerFramebufferUpdate {
			if err := c.skipServerMessage(msgType); err != nil {
				return nil, err
			}
			continue
		}

		// FramebufferUpdate: padding(1) + number-of-rectangles(2)
		var header struct {
			Padding uint8
			NumRect uint16
		}
		if err := binary.Read(c.conn, binary.BigEndian, &header); err != nil {
			return nil, fmt.Errorf("reading update header: %w", err)
		}

		for i := 0; i < int(header.NumRect); i++ {
			var rect struct {
				X, Y, W, H   uint16
				EncodingType int32
			}
			if err := binary.Read(c.conn, binary.BigEndian, &rect); err != nil {
				return nil, fmt.Errorf("reading rect %d header: %w", i, err)
			}

			if rect.EncodingType != rfbEncodingRaw {
				// Skip non-RAW rectangles (pseudo-encodings have W*H=0 pixel data)
				pixelBytes := int(rect.W) * int(rect.H) * bpp
				if pixelBytes > 0 {
					if _, err := io.CopyN(io.Discard, c.conn, int64(pixelBytes)); err != nil {
						return nil, fmt.Errorf("skipping rect %d (encoding %d): %w", i, rect.EncodingType, err)
					}
				}
				continue
			}

			pixelData := make([]byte, int(rect.W)*int(rect.H)*bpp)
			if _, err := io.ReadFull(c.conn, pixelData); err != nil {
				return nil, fmt.Errorf("reading rect %d pixels: %w", i, err)
			}

			c.blitRect(img, pixelData, int(rect.X), int(rect.Y), int(rect.W), int(rect.H), bpp)
		}
		return img, nil
	}
}

// blitRect copies pixels from the server's format into the Go RGBA image.
func (c *VNCClient) blitRect(img *image.RGBA, data []byte, rx, ry, rw, rh, bpp int) {
	pf := &c.pixFmt
	for y := 0; y < rh; y++ {
		for x := 0; x < rw; x++ {
			srcOff := (y*rw + x) * bpp
			if srcOff+bpp > len(data) {
				return
			}

			// Read raw pixel value (little-endian or big-endian based on server format)
			var pixel uint32
			switch bpp {
			case 4:
				if pf.BigEndian {
					pixel = binary.BigEndian.Uint32(data[srcOff:])
				} else {
					pixel = binary.LittleEndian.Uint32(data[srcOff:])
				}
			case 2:
				if pf.BigEndian {
					pixel = uint32(binary.BigEndian.Uint16(data[srcOff:]))
				} else {
					pixel = uint32(binary.LittleEndian.Uint16(data[srcOff:]))
				}
			case 1:
				pixel = uint32(data[srcOff])
			}

			// Extract channels using server's shift/max values
			r := uint8(((pixel >> pf.RedShift) & uint32(pf.RedMax)) * 255 / uint32(pf.RedMax))
			g := uint8(((pixel >> pf.GreenShift) & uint32(pf.GreenMax)) * 255 / uint32(pf.GreenMax))
			b := uint8(((pixel >> pf.BlueShift) & uint32(pf.BlueMax)) * 255 / uint32(pf.BlueMax))

			imgX := rx + x
			imgY := ry + y
			if imgX < int(c.width) && imgY < int(c.height) {
				off := img.PixOffset(imgX, imgY)
				img.Pix[off+0] = r
				img.Pix[off+1] = g
				img.Pix[off+2] = b
				img.Pix[off+3] = 255
			}
		}
	}
}

// skipServerMessage skips an unexpected server message by reading its payload.
func (c *VNCClient) skipServerMessage(msgType uint8) error {
	switch msgType {
	case 1: // SetColourMapEntries
		skip := make([]byte, 5) // padding(1) + firstColour(2) + numColours(2)
		if _, err := io.ReadFull(c.conn, skip); err != nil {
			return err
		}
		numColours := binary.BigEndian.Uint16(skip[3:5])
		colours := make([]byte, int(numColours)*6)
		_, err := io.ReadFull(c.conn, colours)
		return err
	case 2: // Bell — no payload
		return nil
	case 3: // ServerCutText
		skip := make([]byte, 7) // padding(3) + length(4)
		if _, err := io.ReadFull(c.conn, skip); err != nil {
			return err
		}
		textLen := binary.BigEndian.Uint32(skip[3:7])
		text := make([]byte, textLen)
		_, err := io.ReadFull(c.conn, text)
		return err
	default:
		return fmt.Errorf("unknown server message type %d", msgType)
	}
}
