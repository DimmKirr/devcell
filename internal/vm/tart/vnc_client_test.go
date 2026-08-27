package tart

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockVNCServer implements a minimal RFB 3.8 server for testing.
type mockVNCServer struct {
	listener   net.Listener
	received   []VNCKeyEvent
	mu         sync.Mutex
	acceptErr  chan error
	serverConn net.Conn
}

func newMockVNCServer(t *testing.T) *mockVNCServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockVNCServer{
		listener:  ln,
		acceptErr: make(chan error, 1),
	}
	go s.serve()
	return s
}

func (s *mockVNCServer) addr() string {
	return s.listener.Addr().String()
}

func (s *mockVNCServer) close() {
	s.listener.Close()
	if s.serverConn != nil {
		s.serverConn.Close()
	}
}

func (s *mockVNCServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		s.acceptErr <- err
		return
	}
	s.serverConn = conn
	s.acceptErr <- nil

	// 1. Send server version
	conn.Write([]byte("RFB 003.008\n"))

	// 2. Read client version
	clientVer := make([]byte, 12)
	io.ReadFull(conn, clientVer)

	// 3. Send security types (1 type: None)
	conn.Write([]byte{1, rfbSecTypeNone})

	// 4. Read selected security type
	secType := make([]byte, 1)
	io.ReadFull(conn, secType)

	// 5. Send security result (OK)
	binary.Write(conn, binary.BigEndian, uint32(0))

	// 6. Read ClientInit
	clientInit := make([]byte, 1)
	io.ReadFull(conn, clientInit)

	// 7. Send ServerInit (1920x1200, pixel format, desktop name)
	binary.Write(conn, binary.BigEndian, uint16(1920)) // width
	binary.Write(conn, binary.BigEndian, uint16(1200)) // height

	// Pixel format (16 bytes)
	pixelFormat := []byte{
		32,                     // bpp
		24,                     // depth
		0,                      // big-endian
		1,                      // true-color
		0, 255, 0, 255, 0, 255, // RGB max
		16, 8, 0, // RGB shift
		0, 0, 0, // padding
	}
	conn.Write(pixelFormat)

	// Desktop name
	name := "mock-vm"
	binary.Write(conn, binary.BigEndian, uint32(len(name)))
	conn.Write([]byte(name))

	// 8. Read key events until connection closes
	for {
		buf := make([]byte, 8)
		_, err := io.ReadFull(conn, buf)
		if err != nil {
			return
		}
		if buf[0] != rfbKeyEvent {
			continue
		}
		ev := VNCKeyEvent{
			DownFlag: buf[1] != 0,
			Key:      binary.BigEndian.Uint32(buf[4:]),
		}
		s.mu.Lock()
		s.received = append(s.received, ev)
		s.mu.Unlock()
	}
}

func (s *mockVNCServer) events() []VNCKeyEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]VNCKeyEvent, len(s.received))
	copy(result, s.received)
	return result
}

func (s *mockVNCServer) waitForEvents(n int, timeout time.Duration) []VNCKeyEvent {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		evts := s.events()
		if len(evts) >= n {
			return evts
		}
		time.Sleep(5 * time.Millisecond)
	}
	return s.events()
}

// --- Tests ---

func TestVNCClient_Connect(t *testing.T) {
	server := newMockVNCServer(t)
	defer server.close()

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	if client.Width() != 1920 {
		t.Errorf("width = %d, want 1920", client.Width())
	}
	if client.Height() != 1200 {
		t.Errorf("height = %d, want 1200", client.Height())
	}
}

func TestVNCClient_SendKey(t *testing.T) {
	server := newMockVNCServer(t)
	defer server.close()

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	if err := client.SendKey(KeySpace); err != nil {
		t.Fatalf("SendKey: %v", err)
	}

	events := server.waitForEvents(2, time.Second)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if !events[0].DownFlag || events[0].Key != 0x0020 {
		t.Errorf("event[0] = %+v, want down+space", events[0])
	}
	if events[1].DownFlag || events[1].Key != 0x0020 {
		t.Errorf("event[1] = %+v, want up+space", events[1])
	}
}

func TestVNCClient_TypeString(t *testing.T) {
	server := newMockVNCServer(t)
	defer server.close()

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	if err := client.TypeString("hi"); err != nil {
		t.Fatalf("TypeString: %v", err)
	}

	events := server.waitForEvents(4, time.Second)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (h↓ h↑ i↓ i↑)", len(events))
	}
	expected := []VNCKeyEvent{
		{DownFlag: true, Key: uint32('h')},
		{DownFlag: false, Key: uint32('h')},
		{DownFlag: true, Key: uint32('i')},
		{DownFlag: false, Key: uint32('i')},
	}
	for i, want := range expected {
		if events[i] != want {
			t.Errorf("event[%d] = %+v, want %+v", i, events[i], want)
		}
	}
}

func TestVNCClient_TypeUpperCase(t *testing.T) {
	server := newMockVNCServer(t)
	defer server.close()

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	if err := client.TypeString("A"); err != nil {
		t.Fatalf("TypeString: %v", err)
	}

	events := server.waitForEvents(4, time.Second)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (shift↓ A↓ A↑ shift↑)", len(events))
	}
	if events[0].Key != KeyLeftShift.VNCKeyCode() || !events[0].DownFlag {
		t.Errorf("event[0] = %+v, want shift↓", events[0])
	}
	if events[1].Key != uint32('A') || !events[1].DownFlag {
		t.Errorf("event[1] = %+v, want A↓", events[1])
	}
}

func TestExecuteBootCommands_MockVNC(t *testing.T) {
	server := newMockVNCServer(t)
	defer server.close()

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	commands := []BootDirective{
		KeyDirective{Key: KeySpace},
		TypeDirective{Text: "ab"},
		KeyDirective{Key: KeyReturn},
	}

	if err := ExecuteBootCommands(client, commands); err != nil {
		t.Fatalf("ExecuteBootCommands: %v", err)
	}

	// space(2) + "ab"(4) + enter(2) = 8 events
	events := server.waitForEvents(8, time.Second)
	if len(events) != 8 {
		t.Fatalf("got %d events, want 8", len(events))
	}
}

func TestExecuteBootCommands_WaitsRespected(t *testing.T) {
	server := newMockVNCServer(t)
	defer server.close()

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	commands := []BootDirective{
		KeyDirective{Key: KeySpace},
		WaitDirective{Duration: 100 * time.Millisecond},
		KeyDirective{Key: KeyTab},
	}

	start := time.Now()
	if err := ExecuteBootCommands(client, commands); err != nil {
		t.Fatalf("ExecuteBootCommands: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < 90*time.Millisecond {
		t.Errorf("executed too fast (%v), expected at least 100ms wait", elapsed)
	}

	events := server.waitForEvents(4, time.Second)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}
}

func TestExecuteBootCommands_ErrorMidSequence(t *testing.T) {
	server := newMockVNCServer(t)
	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}

	commands := []BootDirective{
		KeyDirective{Key: KeySpace},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeyReturn},
	}

	// Close the server to force an error mid-sequence
	server.close()
	client.Close()

	err = ExecuteBootCommands(client, commands)
	if err == nil {
		t.Fatal("expected error after connection close, got nil")
	}
	// Error should mention the directive index
	errStr := fmt.Sprintf("%v", err)
	if !strings.Contains(errStr, "directive") {
		t.Errorf("error %q should mention directive index", errStr)
	}
}

func TestVNCClient_ConnectRefused(t *testing.T) {
	// Use a port that's not listening
	_, err := DialVNC("127.0.0.1:1", 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected error connecting to closed port")
	}
}

// --- VNC Auth (RFB security type 2) tests ---

func TestVNCClient_AuthConnect(t *testing.T) {
	server := newMockVNCAuthServer(t, "testpass")
	defer server.close()

	client, err := DialVNCAuth(server.addr(), "testpass", 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNCAuth: %v", err)
	}
	defer client.Close()

	if client.Width() != 1920 {
		t.Errorf("width = %d, want 1920", client.Width())
	}
	if client.Height() != 1200 {
		t.Errorf("height = %d, want 1200", client.Height())
	}
}

func TestVNCClient_AuthWrongPassword(t *testing.T) {
	server := newMockVNCAuthServer(t, "correct")
	defer server.close()

	_, err := DialVNCAuth(server.addr(), "wrong", 2*time.Second)
	if err == nil {
		t.Fatal("expected error with wrong password")
	}
}

func TestVNCClient_AuthSendKey(t *testing.T) {
	server := newMockVNCAuthServer(t, "secret")
	defer server.close()

	client, err := DialVNCAuth(server.addr(), "secret", 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNCAuth: %v", err)
	}
	defer client.Close()

	if err := client.SendKey(KeySpace); err != nil {
		t.Fatalf("SendKey: %v", err)
	}

	events := server.waitForEvents(2, time.Second)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if !events[0].DownFlag || events[0].Key != 0x0020 {
		t.Errorf("event[0] = %+v, want down+space", events[0])
	}
}

// mockVNCAuthServer implements RFB 3.8 with VNC Authentication (type 2).
type mockVNCAuthServer struct {
	listener   net.Listener
	password   string
	received   []VNCKeyEvent
	mu         sync.Mutex
	acceptErr  chan error
	serverConn net.Conn
}

func newMockVNCAuthServer(t *testing.T, password string) *mockVNCAuthServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockVNCAuthServer{
		listener:  ln,
		password:  password,
		acceptErr: make(chan error, 1),
	}
	go s.serve()
	return s
}

func (s *mockVNCAuthServer) addr() string {
	return s.listener.Addr().String()
}

func (s *mockVNCAuthServer) close() {
	s.listener.Close()
	if s.serverConn != nil {
		s.serverConn.Close()
	}
}

func (s *mockVNCAuthServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		s.acceptErr <- err
		return
	}
	s.serverConn = conn
	s.acceptErr <- nil

	// 1. Send server version
	conn.Write([]byte("RFB 003.008\n"))

	// 2. Read client version
	clientVer := make([]byte, 12)
	io.ReadFull(conn, clientVer)

	// 3. Send security types (1 type: VNC Auth = 2)
	conn.Write([]byte{1, rfbSecTypeVNCAuth})

	// 4. Read selected security type
	secType := make([]byte, 1)
	io.ReadFull(conn, secType)

	// 5. Send 16-byte challenge
	challenge := make([]byte, 16)
	for i := range challenge {
		challenge[i] = byte(i + 1) // deterministic for testing
	}
	conn.Write(challenge)

	// 6. Read 16-byte response
	response := make([]byte, 16)
	io.ReadFull(conn, response)

	// 7. Verify: compute expected response using the password
	expected := vncAuthEncrypt(challenge, s.password)
	match := true
	for i := range expected {
		if expected[i] != response[i] {
			match = false
			break
		}
	}

	if match {
		binary.Write(conn, binary.BigEndian, uint32(0)) // OK
	} else {
		binary.Write(conn, binary.BigEndian, uint32(1)) // fail
		conn.Close()
		return
	}

	// 8. Read ClientInit
	clientInit := make([]byte, 1)
	io.ReadFull(conn, clientInit)

	// 9. Send ServerInit
	binary.Write(conn, binary.BigEndian, uint16(1920))
	binary.Write(conn, binary.BigEndian, uint16(1200))
	pixelFormat := []byte{
		32, 24, 0, 1,
		0, 255, 0, 255, 0, 255,
		16, 8, 0,
		0, 0, 0,
	}
	conn.Write(pixelFormat)
	name := "mock-auth-vm"
	binary.Write(conn, binary.BigEndian, uint32(len(name)))
	conn.Write([]byte(name))

	// 10. Read key events
	for {
		buf := make([]byte, 8)
		_, err := io.ReadFull(conn, buf)
		if err != nil {
			return
		}
		if buf[0] != rfbKeyEvent {
			continue
		}
		ev := VNCKeyEvent{
			DownFlag: buf[1] != 0,
			Key:      binary.BigEndian.Uint32(buf[4:]),
		}
		s.mu.Lock()
		s.received = append(s.received, ev)
		s.mu.Unlock()
	}
}

func (s *mockVNCAuthServer) events() []VNCKeyEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]VNCKeyEvent, len(s.received))
	copy(result, s.received)
	return result
}

func (s *mockVNCAuthServer) waitForEvents(n int, timeout time.Duration) []VNCKeyEvent {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		evts := s.events()
		if len(evts) >= n {
			return evts
		}
		time.Sleep(5 * time.Millisecond)
	}
	return s.events()
}

// mockFramebufferServer is a VNC server that responds to FramebufferUpdateRequests
// with configurable solid-color frames. The color can be changed between requests
// to simulate screen transitions (e.g., black boot screen → bright Setup Assistant).
type mockFramebufferServer struct {
	listener   net.Listener
	serverConn net.Conn
	acceptErr  chan error
	mu         sync.Mutex
	frameR     uint8
	frameG     uint8
	frameB     uint8
	width      uint16
	height     uint16
	pushStop   chan struct{}
}

func newMockFramebufferServer(t *testing.T, w, h uint16) *mockFramebufferServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockFramebufferServer{
		listener:  ln,
		acceptErr: make(chan error, 1),
		width:     w,
		height:    h,
	}
	go s.serve()
	return s
}

func (s *mockFramebufferServer) addr() string { return s.listener.Addr().String() }

func (s *mockFramebufferServer) close() {
	if s.pushStop != nil {
		close(s.pushStop)
	}
	s.listener.Close()
	if s.serverConn != nil {
		s.serverConn.Close()
	}
}

func (s *mockFramebufferServer) setUnsolicitedPush(enabled bool, interval time.Duration) {
	if !enabled {
		if s.pushStop != nil {
			close(s.pushStop)
			s.pushStop = nil
		}
		return
	}
	s.pushStop = make(chan struct{})
	go func() {
		for {
			select {
			case <-s.pushStop:
				return
			case <-time.After(interval):
				s.mu.Lock()
				conn := s.serverConn
				s.mu.Unlock()
				if conn != nil {
					s.sendFrame(conn)
				}
			}
		}
	}()
}

func (s *mockFramebufferServer) setColor(r, g, b uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frameR, s.frameG, s.frameB = r, g, b
}

func (s *mockFramebufferServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		s.acceptErr <- err
		return
	}
	s.serverConn = conn
	s.acceptErr <- nil

	// RFB handshake (no auth)
	conn.Write([]byte("RFB 003.008\n"))
	clientVer := make([]byte, 12)
	io.ReadFull(conn, clientVer)
	conn.Write([]byte{1, rfbSecTypeNone})
	secType := make([]byte, 1)
	io.ReadFull(conn, secType)
	binary.Write(conn, binary.BigEndian, uint32(0))
	clientInit := make([]byte, 1)
	io.ReadFull(conn, clientInit)

	// ServerInit
	binary.Write(conn, binary.BigEndian, s.width)
	binary.Write(conn, binary.BigEndian, s.height)
	pixelFormat := []byte{
		32, 24, 0, 1,
		0, 255, 0, 255, 0, 255,
		16, 8, 0,
		0, 0, 0,
	}
	conn.Write(pixelFormat)
	name := "mock-fb"
	binary.Write(conn, binary.BigEndian, uint32(len(name)))
	conn.Write([]byte(name))

	// Message loop: read client messages, respond to FramebufferUpdateRequests
	for {
		var msgType uint8
		if err := binary.Read(conn, binary.BigEndian, &msgType); err != nil {
			return
		}
		switch msgType {
		case rfbFramebufferUpdateRequest:
			// Read rest of request (9 bytes: incremental + x + y + w + h)
			req := make([]byte, 9)
			if _, err := io.ReadFull(conn, req); err != nil {
				return
			}
			s.sendFrame(conn)
		case rfbKeyEvent:
			skip := make([]byte, 7)
			io.ReadFull(conn, skip)
		case rfbPointerEvent:
			skip := make([]byte, 5)
			io.ReadFull(conn, skip)
		default:
			return
		}
	}
}

func (s *mockFramebufferServer) sendFrame(conn net.Conn) {
	s.mu.Lock()
	r, g, b := s.frameR, s.frameG, s.frameB
	w, h := s.width, s.height
	s.mu.Unlock()

	// FramebufferUpdate header: type(1) + padding(1) + numRects(2)
	binary.Write(conn, binary.BigEndian, uint8(rfbServerFramebufferUpdate))
	binary.Write(conn, binary.BigEndian, uint8(0))
	binary.Write(conn, binary.BigEndian, uint16(1)) // 1 rectangle

	// Rectangle header: x(2) + y(2) + w(2) + h(2) + encoding(4)
	binary.Write(conn, binary.BigEndian, uint16(0))
	binary.Write(conn, binary.BigEndian, uint16(0))
	binary.Write(conn, binary.BigEndian, w)
	binary.Write(conn, binary.BigEndian, h)
	binary.Write(conn, binary.BigEndian, int32(rfbEncodingRaw))

	// Pixel data: BGRX (little-endian 32bpp with shifts R=16, G=8, B=0)
	pixel := []byte{b, g, r, 0}
	for y := 0; y < int(h); y++ {
		for x := 0; x < int(w); x++ {
			conn.Write(pixel)
		}
	}
}

func TestCaptureFramebuffer_BrightnessTransition(t *testing.T) {
	server := newMockFramebufferServer(t, 64, 64)
	defer server.close()

	// Start with black screen (simulating boot)
	server.setColor(0, 0, 0)

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	// Poll 1: black screen — brightness should be ~0
	img, err := client.CaptureFramebuffer()
	if err != nil {
		t.Fatalf("CaptureFramebuffer (black): %v", err)
	}
	b1 := ScreenBrightness(img)
	if b1 > 0.01 {
		t.Errorf("black screen brightness = %.4f, want < 0.01", b1)
	}

	// Switch to white screen (simulating Setup Assistant appearing)
	server.setColor(255, 255, 255)

	// Poll 2: white screen — brightness should be ~1.0
	img, err = client.CaptureFramebuffer()
	if err != nil {
		t.Fatalf("CaptureFramebuffer (white): %v", err)
	}
	b2 := ScreenBrightness(img)
	if b2 < 0.99 {
		t.Errorf("white screen brightness = %.4f, want > 0.99", b2)
	}

	// Verify threshold crossing: b1 < 0.15 < b2
	const threshold = 0.15
	if b1 >= threshold {
		t.Errorf("black brightness %.4f should be below threshold %.2f", b1, threshold)
	}
	if b2 < threshold {
		t.Errorf("white brightness %.4f should be above threshold %.2f", b2, threshold)
	}
}

// TestCaptureFramebuffer_DrainUnsolicited verifies that CaptureFramebuffer works
// even when the server pushes unsolicited FramebufferUpdate messages between
// capture requests — the exact behavior of Apple's _VZVNCServer.
func TestCaptureFramebuffer_DrainUnsolicited(t *testing.T) {
	server := newMockFramebufferServer(t, 32, 32)
	defer server.close()

	// Enable unsolicited frame pushing
	server.setColor(0, 0, 0)
	server.setUnsolicitedPush(true, 5*time.Millisecond)

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	// Wait for unsolicited frames to accumulate
	time.Sleep(50 * time.Millisecond)

	// CaptureFramebuffer must drain stale frames and return a fresh one
	server.setColor(255, 0, 0) // switch to red
	img, err := client.CaptureFramebuffer()
	if err != nil {
		t.Fatalf("CaptureFramebuffer after unsolicited: %v", err)
	}

	// The returned frame should reflect the color at time of capture, not stale data
	b := ScreenBrightness(img)
	// Red (255,0,0) has brightness = 255/(3*255) ≈ 0.333
	if b < 0.30 || b > 0.36 {
		t.Errorf("red screen brightness = %.4f, want ~0.333", b)
	}

	// Second capture should also work (verifying protocol stream is still aligned)
	server.setColor(0, 255, 0) // switch to green
	time.Sleep(30 * time.Millisecond)
	img, err = client.CaptureFramebuffer()
	if err != nil {
		t.Fatalf("CaptureFramebuffer second: %v", err)
	}
	b = ScreenBrightness(img)
	if b < 0.30 || b > 0.36 {
		t.Errorf("green screen brightness = %.4f, want ~0.333", b)
	}
}

func TestCaptureFramebuffer_MultiPollTransition(t *testing.T) {
	server := newMockFramebufferServer(t, 32, 32)
	defer server.close()

	client, err := DialVNC(server.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer client.Close()

	// Simulate gradual boot: dark → dark → dark → bright
	colors := []struct {
		r, g, b   uint8
		wantBelow float64
	}{
		{0, 0, 0, 0.01},
		{5, 5, 5, 0.03},
		{10, 10, 10, 0.05},
	}

	for i, c := range colors {
		server.setColor(c.r, c.g, c.b)
		img, err := client.CaptureFramebuffer()
		if err != nil {
			t.Fatalf("poll %d: CaptureFramebuffer: %v", i, err)
		}
		b := ScreenBrightness(img)
		if b >= c.wantBelow {
			t.Errorf("poll %d: brightness %.4f, want < %.2f", i, b, c.wantBelow)
		}
		if b >= 0.15 {
			t.Errorf("poll %d: brightness %.4f should be below SA threshold 0.15", i, b)
		}
	}

	// Setup Assistant appears
	server.setColor(240, 240, 240)
	img, err := client.CaptureFramebuffer()
	if err != nil {
		t.Fatalf("poll SA: CaptureFramebuffer: %v", err)
	}
	b := ScreenBrightness(img)
	if b < 0.15 {
		t.Errorf("SA screen brightness %.4f should be above threshold 0.15", b)
	}
	if b < 0.90 {
		t.Errorf("SA screen brightness %.4f should be close to 1.0", b)
	}
}

func TestScreenBrightness(t *testing.T) {
	tests := []struct {
		name     string
		r, g, b  uint8
		wantLow  float64
		wantHigh float64
	}{
		{"black", 0, 0, 0, 0.0, 0.01},
		{"white", 255, 255, 255, 0.99, 1.0},
		{"mid-gray", 128, 128, 128, 0.49, 0.51},
		{"dark boot screen", 10, 10, 10, 0.03, 0.05},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 64, 64))
			for i := 0; i < len(img.Pix); i += 4 {
				img.Pix[i] = tt.r
				img.Pix[i+1] = tt.g
				img.Pix[i+2] = tt.b
				img.Pix[i+3] = 255
			}
			got := ScreenBrightness(img)
			if got < tt.wantLow || got > tt.wantHigh {
				t.Errorf("ScreenBrightness = %.4f, want [%.2f, %.2f]", got, tt.wantLow, tt.wantHigh)
			}
		})
	}
}
