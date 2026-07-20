package tart

import (
	"crypto/des"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// RFB protocol constants.
const (
	rfbVersion = "RFB 003.008\n"

	// Security types.
	rfbSecTypeNone    = 1
	rfbSecTypeVNCAuth = 2

	// Client message types.
	rfbKeyEvent     = 4
	rfbPointerEvent = 5
)

// VNCClient is a minimal VNC (RFB) client for sending keystrokes and pointer
// events. It implements just enough of the RFB protocol for Setup Assistant
// automation, including OCR-based screen detection.
type VNCClient struct {
	conn   net.Conn
	width  uint16
	height uint16
	pixFmt serverPixelFormat
}

// serverPixelFormat stores the pixel format from the ServerInit message.
type serverPixelFormat struct {
	BitsPerPixel uint8
	Depth        uint8
	BigEndian    bool
	TrueColour   bool
	RedMax       uint16
	GreenMax     uint16
	BlueMax      uint16
	RedShift     uint8
	GreenShift   uint8
	BlueShift    uint8
}

// DialVNC connects to a VNC server with no authentication and completes the RFB handshake.
func DialVNC(addr string, timeout time.Duration) (*VNCClient, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("VNC connect: %w", err)
	}

	c := &VNCClient{conn: conn}
	if err := c.handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("VNC handshake: %w", err)
	}
	return c, nil
}

// DialVNCAuth connects to a VNC server with VNC Authentication (RFB security type 2).
func DialVNCAuth(addr, password string, timeout time.Duration) (*VNCClient, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("VNC connect: %w", err)
	}

	c := &VNCClient{conn: conn}
	if err := c.handshakeAuth(password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("VNC handshake: %w", err)
	}
	return c, nil
}

// NewVNCClient wraps an existing connection (for testing with mock servers).
func NewVNCClient(conn net.Conn) (*VNCClient, error) {
	c := &VNCClient{conn: conn}
	if err := c.handshake(); err != nil {
		return nil, fmt.Errorf("VNC handshake: %w", err)
	}
	return c, nil
}

func (c *VNCClient) handshake() error {
	// 1. Read server version
	serverVersion := make([]byte, 12)
	if _, err := io.ReadFull(c.conn, serverVersion); err != nil {
		return fmt.Errorf("reading server version: %w", err)
	}

	// 2. Send client version
	if _, err := c.conn.Write([]byte(rfbVersion)); err != nil {
		return fmt.Errorf("sending client version: %w", err)
	}

	// 3. Read security types (RFB 3.8 format: count + types)
	var numTypes uint8
	if err := binary.Read(c.conn, binary.BigEndian, &numTypes); err != nil {
		return fmt.Errorf("reading security type count: %w", err)
	}
	if numTypes == 0 {
		return fmt.Errorf("server offered 0 security types (connection refused)")
	}

	types := make([]byte, numTypes)
	if _, err := io.ReadFull(c.conn, types); err != nil {
		return fmt.Errorf("reading security types: %w", err)
	}

	// 4. Select security type None
	hasNone := false
	for _, t := range types {
		if t == rfbSecTypeNone {
			hasNone = true
			break
		}
	}
	if !hasNone {
		return fmt.Errorf("server does not support security type None (offered: %v)", types)
	}

	if _, err := c.conn.Write([]byte{rfbSecTypeNone}); err != nil {
		return fmt.Errorf("sending security type: %w", err)
	}

	// 5. Read security result (0 = OK)
	var secResult uint32
	if err := binary.Read(c.conn, binary.BigEndian, &secResult); err != nil {
		return fmt.Errorf("reading security result: %w", err)
	}
	if secResult != 0 {
		return fmt.Errorf("security handshake failed (result=%d)", secResult)
	}

	// 6. Send ClientInit (shared flag = 1)
	if _, err := c.conn.Write([]byte{1}); err != nil {
		return fmt.Errorf("sending ClientInit: %w", err)
	}

	// 7. Read ServerInit
	return c.readServerInit()
}

func (c *VNCClient) handshakeAuth(password string) error {
	// 1. Read server version
	serverVersion := make([]byte, 12)
	if _, err := io.ReadFull(c.conn, serverVersion); err != nil {
		return fmt.Errorf("reading server version: %w", err)
	}

	// 2. Send client version
	if _, err := c.conn.Write([]byte(rfbVersion)); err != nil {
		return fmt.Errorf("sending client version: %w", err)
	}

	// 3. Read security types
	var numTypes uint8
	if err := binary.Read(c.conn, binary.BigEndian, &numTypes); err != nil {
		return fmt.Errorf("reading security type count: %w", err)
	}
	if numTypes == 0 {
		return fmt.Errorf("server offered 0 security types (connection refused)")
	}

	types := make([]byte, numTypes)
	if _, err := io.ReadFull(c.conn, types); err != nil {
		return fmt.Errorf("reading security types: %w", err)
	}

	// 4. Select VNC Auth
	hasAuth := false
	for _, t := range types {
		if t == rfbSecTypeVNCAuth {
			hasAuth = true
			break
		}
	}
	if !hasAuth {
		return fmt.Errorf("server does not support VNC Authentication (offered: %v)", types)
	}

	if _, err := c.conn.Write([]byte{rfbSecTypeVNCAuth}); err != nil {
		return fmt.Errorf("sending security type: %w", err)
	}

	// 5. Read 16-byte challenge
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(c.conn, challenge); err != nil {
		return fmt.Errorf("reading VNC auth challenge: %w", err)
	}

	// 6. Encrypt challenge with password and send response
	response := vncAuthEncrypt(challenge, password)
	if _, err := c.conn.Write(response); err != nil {
		return fmt.Errorf("sending VNC auth response: %w", err)
	}

	// 7. Read security result
	var secResult uint32
	if err := binary.Read(c.conn, binary.BigEndian, &secResult); err != nil {
		return fmt.Errorf("reading security result: %w", err)
	}
	if secResult != 0 {
		return fmt.Errorf("VNC authentication failed (result=%d)", secResult)
	}

	// 8. Send ClientInit (shared flag = 1)
	if _, err := c.conn.Write([]byte{1}); err != nil {
		return fmt.Errorf("sending ClientInit: %w", err)
	}

	// 9. Read ServerInit
	return c.readServerInit()
}

// readServerInit reads the ServerInit message (shared by both handshake paths).
func (c *VNCClient) readServerInit() error {
	var serverInit struct {
		Width  uint16
		Height uint16
	}
	if err := binary.Read(c.conn, binary.BigEndian, &serverInit); err != nil {
		return fmt.Errorf("reading ServerInit dimensions: %w", err)
	}
	c.width = serverInit.Width
	c.height = serverInit.Height

	// Read and store pixel format (16 bytes)
	var pf [16]byte
	if _, err := io.ReadFull(c.conn, pf[:]); err != nil {
		return fmt.Errorf("reading pixel format: %w", err)
	}
	c.pixFmt = serverPixelFormat{
		BitsPerPixel: pf[0],
		Depth:        pf[1],
		BigEndian:    pf[2] != 0,
		TrueColour:   pf[3] != 0,
		RedMax:       binary.BigEndian.Uint16(pf[4:6]),
		GreenMax:     binary.BigEndian.Uint16(pf[6:8]),
		BlueMax:      binary.BigEndian.Uint16(pf[8:10]),
		RedShift:     pf[10],
		GreenShift:   pf[11],
		BlueShift:    pf[12],
	}

	// Read desktop name
	var nameLen uint32
	if err := binary.Read(c.conn, binary.BigEndian, &nameLen); err != nil {
		return fmt.Errorf("reading desktop name length: %w", err)
	}
	if nameLen > 0 {
		nameBytes := make([]byte, nameLen)
		if _, err := io.ReadFull(c.conn, nameBytes); err != nil {
			return fmt.Errorf("reading desktop name: %w", err)
		}
	}

	return nil
}

// vncAuthEncrypt encrypts a 16-byte challenge with a password using VNC's
// DES-based authentication. The password is truncated or padded to 8 bytes,
// each byte's bits are reversed, then used as the DES key to encrypt
// two 8-byte blocks of the challenge.
func vncAuthEncrypt(challenge []byte, password string) []byte {
	// Pad/truncate password to 8 bytes
	key := make([]byte, 8)
	copy(key, []byte(password))

	// VNC reverses bits in each byte of the key
	for i := range key {
		key[i] = reverseBits(key[i])
	}

	cipher, _ := des.NewCipher(key)

	result := make([]byte, 16)
	cipher.Encrypt(result[0:8], challenge[0:8])
	cipher.Encrypt(result[8:16], challenge[8:16])
	return result
}

// reverseBits reverses the bit order in a byte.
func reverseBits(b byte) byte {
	var r byte
	for i := 0; i < 8; i++ {
		r = (r << 1) | (b & 1)
		b >>= 1
	}
	return r
}

// SendKeyEvent sends a single VNC key event.
func (c *VNCClient) SendKeyEvent(event VNCKeyEvent) error {
	buf := make([]byte, 8)
	buf[0] = rfbKeyEvent
	if event.DownFlag {
		buf[1] = 1
	}
	// buf[2], buf[3] = padding
	binary.BigEndian.PutUint32(buf[4:], event.Key)
	_, err := c.conn.Write(buf)
	return err
}

// SendKey sends a key-down then key-up event for a special key.
func (c *VNCClient) SendKey(key SpecialKey) error {
	for _, ev := range EncodeSpecialKey(key) {
		if err := c.SendKeyEvent(ev); err != nil {
			return err
		}
	}
	return nil
}

// SendKeyDown sends a key-down event without a corresponding key-up.
func (c *VNCClient) SendKeyDown(key SpecialKey) error {
	return c.SendKeyEvent(VNCKeyEvent{DownFlag: true, Key: key.VNCKeyCode()})
}

// SendKeyUp sends a key-up event. Pairs with a prior SendKeyDown.
func (c *VNCClient) SendKeyUp(key SpecialKey) error {
	return c.SendKeyEvent(VNCKeyEvent{DownFlag: false, Key: key.VNCKeyCode()})
}

// SendPointerEvent sends a VNC pointer event (mouse movement / button press).
// buttonMask bit 0 = left button, bit 1 = middle, bit 2 = right.
func (c *VNCClient) SendPointerEvent(buttonMask uint8, x, y uint16) error {
	buf := make([]byte, 6)
	buf[0] = rfbPointerEvent
	buf[1] = buttonMask
	binary.BigEndian.PutUint16(buf[2:], x)
	binary.BigEndian.PutUint16(buf[4:], y)
	_, err := c.conn.Write(buf)
	return err
}

// Click sends a left mouse button press and release at the given coordinates.
func (c *VNCClient) Click(x, y uint16) error {
	if err := c.SendPointerEvent(1, x, y); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.SendPointerEvent(0, x, y)
}

// TypeString sends key events for each character in the string.
func (c *VNCClient) TypeString(s string) error {
	for _, ev := range EncodeString(s) {
		if err := c.SendKeyEvent(ev); err != nil {
			return err
		}
	}
	return nil
}

// Width returns the framebuffer width from ServerInit.
func (c *VNCClient) Width() uint16 { return c.width }

// Height returns the framebuffer height from ServerInit.
func (c *VNCClient) Height() uint16 { return c.height }

// Close closes the VNC connection.
func (c *VNCClient) Close() error {
	return c.conn.Close()
}

// ExecuteBootCommands runs a sequence of boot directives against a VNC server.
// Returns an error annotated with the directive index if sending fails.
func ExecuteBootCommands(client *VNCClient, commands []BootDirective) error {
	return ExecuteBootCommandsObserved(client, commands, NopObserver{})
}

// ExecuteBootCommandsObserved is like ExecuteBootCommands but reports progress
// to obs, including which directive is currently executing.
func ExecuteBootCommandsObserved(client *VNCClient, commands []BootDirective, obs Observer) error {
	return ExecuteBootCommandsWithScreenshots(client, commands, obs, nil)
}

// ocrPollInterval is the time between OCR framebuffer polls.
const ocrPollInterval = 2 * time.Second

// ExecuteBootCommandsWithScreenshots runs boot directives with an optional
// pre-action screenshot callback. If screenshotBefore is non-nil, it is called
// before each KeyDirective, KeyDownDirective, and TypeDirective with the
// directive index and a short label describing the action.
func ExecuteBootCommandsWithScreenshots(client *VNCClient, commands []BootDirective, obs Observer, screenshotBefore func(idx int, label string)) error {
	total := len(commands)
	for i, cmd := range commands {
		frac := float64(i) / float64(total)
		switch d := cmd.(type) {
		case WaitDirective:
			obs.Logf("[%d/%d] wait %s", i+1, total, d.Duration)
			obs.Progress(frac, fmt.Sprintf("waiting %s", d.Duration))
			time.Sleep(d.Duration)

		case WaitTextDirective:
			obs.Logf("[%d/%d] wait for text %q (timeout %s)", i+1, total, d.Text, d.Timeout)
			obs.Progress(frac, fmt.Sprintf("waiting for %q", d.Text))
			deadline := time.Now().Add(d.Timeout)
			found := false
			for time.Now().Before(deadline) {
				img, err := client.CaptureFramebuffer()
				if err != nil {
					obs.Logf("  OCR poll: framebuffer error: %v", err)
					time.Sleep(ocrPollInterval)
					continue
				}
				if _, ok := FindTextOnScreen(img, d.Text); ok {
					obs.Logf("  found %q on screen", d.Text)
					found = true
					break
				}
				obs.Logf("  %q not found, retrying in %s…", d.Text, ocrPollInterval)
				time.Sleep(ocrPollInterval)
			}
			if !found {
				if screenshotBefore != nil {
					screenshotBefore(i, fmt.Sprintf("timeout-waittext-%s", d.Text))
				}
				return fmt.Errorf("directive %d: timed out waiting for text %q after %s", i, d.Text, d.Timeout)
			}

		case ClickTextDirective:
			obs.Logf("[%d/%d] click text %q (timeout %s)", i+1, total, d.Text, d.Timeout)
			obs.Progress(frac, fmt.Sprintf("clicking %q", d.Text))
			if screenshotBefore != nil {
				label := d.Text
				if len(label) > 20 {
					label = label[:20]
				}
				screenshotBefore(i, fmt.Sprintf("click-%s", label))
			}
			deadline := time.Now().Add(d.Timeout)
			clicked := false
			for time.Now().Before(deadline) {
				img, err := client.CaptureFramebuffer()
				if err != nil {
					time.Sleep(ocrPollInterval)
					continue
				}
				rect, ok := FindTextOnScreen(img, d.Text)
				if ok {
					cx := uint16(rect.Min.X + rect.Dx()/2)
					cy := uint16(rect.Min.Y + rect.Dy()/2)
					obs.Logf("  clicking %q at (%d, %d)", d.Text, cx, cy)
					if err := client.Click(cx, cy); err != nil {
						return fmt.Errorf("directive %d (click %q): %w", i, d.Text, err)
					}
					clicked = true
					break
				}
				time.Sleep(ocrPollInterval)
			}
			if !clicked {
				return fmt.Errorf("directive %d: timed out waiting to click %q after %s", i, d.Text, d.Timeout)
			}

		case KeyDirective:
			obs.Logf("[%d/%d] key %d", i+1, total, d.Key)
			if screenshotBefore != nil {
				screenshotBefore(i, fmt.Sprintf("key-%d", d.Key))
			}
			if err := client.SendKey(d.Key); err != nil {
				return fmt.Errorf("directive %d (key %d): %w", i, d.Key, err)
			}

		case KeyDownDirective:
			obs.Logf("[%d/%d] key-down %d", i+1, total, d.Key)
			if screenshotBefore != nil {
				screenshotBefore(i, fmt.Sprintf("keydown-%d", d.Key))
			}
			if err := client.SendKeyDown(d.Key); err != nil {
				return fmt.Errorf("directive %d (keydown %d): %w", i, d.Key, err)
			}

		case KeyUpDirective:
			obs.Logf("[%d/%d] key-up %d", i+1, total, d.Key)
			if err := client.SendKeyUp(d.Key); err != nil {
				return fmt.Errorf("directive %d (keyup %d): %w", i, d.Key, err)
			}

		case TypeDirective:
			obs.Logf("[%d/%d] type %q", i+1, total, d.Text)
			if screenshotBefore != nil {
				label := d.Text
				if len(label) > 20 {
					label = label[:20]
				}
				screenshotBefore(i, fmt.Sprintf("type-%s", label))
			}
			if err := client.TypeString(d.Text); err != nil {
				return fmt.Errorf("directive %d (type %q): %w", i, d.Text, err)
			}

		default:
			return fmt.Errorf("directive %d: unknown type %T", i, cmd)
		}
	}
	obs.Progress(1.0, "done")
	return nil
}
