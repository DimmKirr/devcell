package qemu

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

// GDBConn is a minimal GDB Remote Serial Protocol client that can
// read and write guest virtual memory through QEMU's built-in GDB stub.
type GDBConn struct {
	conn net.Conn
}

// GDBDial connects to QEMU's GDB stub at the given address (e.g.
// "tcp:localhost:1234" or "unix:/path/to/sock"). It sends the initial
// handshake and returns a ready-to-use connection.
func GDBDial(addr string, timeout time.Duration) (*GDBConn, error) {
	network, address, _ := strings.Cut(addr, ":")
	if network == "unix" {
		// addr was "unix:/path"
	} else {
		// addr was "tcp:host:port" — recombine host:port
		network = "tcp"
		address = addr[len("tcp:"):]
	}

	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return nil, fmt.Errorf("gdb dial %s: %w", addr, err)
	}
	conn.SetDeadline(time.Now().Add(timeout))

	g := &GDBConn{conn: conn}

	// QEMU sends '+' and possibly a stop notification ($T05#b9) on
	// connect. Drain everything available within a short window.
	_ = g.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	drain := make([]byte, 256)
	for {
		n, err := g.conn.Read(drain)
		if err != nil || n == 0 {
			break
		}
		// ACK any stop notification the stub sent
		g.conn.Write([]byte("+"))
	}
	_ = g.conn.SetReadDeadline(time.Time{})

	return g, nil
}

func (g *GDBConn) Close() error {
	return g.conn.Close()
}

// gdbChecksum computes the GDB RSP checksum (sum of bytes mod 256).
func gdbChecksum(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	return sum
}

// sendPacket sends a GDB RSP packet and reads the ACK + reply.
func (g *GDBConn) sendPacket(payload string) (string, error) {
	csum := gdbChecksum([]byte(payload))
	pkt := fmt.Sprintf("$%s#%02x", payload, csum)

	g.conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := g.conn.Write([]byte(pkt)); err != nil {
		return "", fmt.Errorf("gdb write: %w", err)
	}

	// Read response: optional '+', then '$...#xx'
	buf := make([]byte, 4096)
	var resp []byte
	for {
		n, err := g.conn.Read(buf)
		if err != nil {
			return "", fmt.Errorf("gdb read: %w", err)
		}
		resp = append(resp, buf[:n]...)

		// Look for complete packet: $...#xx
		s := string(resp)
		dollarIdx := strings.Index(s, "$")
		if dollarIdx < 0 {
			continue
		}
		hashIdx := strings.Index(s[dollarIdx:], "#")
		if hashIdx < 0 {
			continue
		}
		hashIdx += dollarIdx
		if len(s) >= hashIdx+3 {
			body := s[dollarIdx+1 : hashIdx]
			// Send ACK
			g.conn.Write([]byte("+"))
			return body, nil
		}
	}
}

// Stop halts the guest (equivalent to Ctrl-C in GDB).
func (g *GDBConn) Stop() error {
	g.conn.SetDeadline(time.Now().Add(5 * time.Second))
	// Send interrupt (0x03)
	if _, err := g.conn.Write([]byte{0x03}); err != nil {
		return fmt.Errorf("gdb stop: %w", err)
	}
	// Read the stop reply
	buf := make([]byte, 256)
	var resp []byte
	for {
		n, err := g.conn.Read(buf)
		if err != nil {
			return fmt.Errorf("gdb stop read: %w", err)
		}
		resp = append(resp, buf[:n]...)
		if strings.Contains(string(resp), "#") {
			break
		}
	}
	return nil
}

// Continue resumes guest execution.
func (g *GDBConn) Continue() error {
	// 'c' resumes; the stub sends a stop reply only when it halts again,
	// so we just fire and don't wait for a reply.
	csum := gdbChecksum([]byte("c"))
	pkt := fmt.Sprintf("$c#%02x", csum)
	g.conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err := g.conn.Write([]byte(pkt))
	return err
}

// ReadMemory reads len bytes from virtual address addr.
func (g *GDBConn) ReadMemory(addr uint64, length int) ([]byte, error) {
	cmd := fmt.Sprintf("m%x,%x", addr, length)
	reply, err := g.sendPacket(cmd)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(reply, "E") {
		return nil, fmt.Errorf("gdb read memory error: %s", reply)
	}
	return hex.DecodeString(reply)
}

// WriteMemory writes data to virtual address addr.
func (g *GDBConn) WriteMemory(addr uint64, data []byte) error {
	cmd := fmt.Sprintf("M%x,%x:%s", addr, len(data), hex.EncodeToString(data))
	reply, err := g.sendPacket(cmd)
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("gdb write memory: %s", reply)
	}
	return nil
}

// WriteUint16LE writes a little-endian uint16 to the given virtual address.
func (g *GDBConn) WriteUint16LE(addr uint64, val uint16) error {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, val)
	return g.WriteMemory(addr, buf)
}

// ReadUint16LE reads a little-endian uint16 from the given virtual address.
func (g *GDBConn) ReadUint16LE(addr uint64) (uint16, error) {
	data, err := g.ReadMemory(addr, 2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(data), nil
}

// SetBreakpoint inserts a software breakpoint (Z0) at addr.
func (g *GDBConn) SetBreakpoint(addr uint64) error {
	cmd := fmt.Sprintf("Z0,%x,4", addr)
	reply, err := g.sendPacket(cmd)
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("gdb set breakpoint: %s", reply)
	}
	return nil
}

// RemoveBreakpoint removes a software breakpoint (z0) at addr.
func (g *GDBConn) RemoveBreakpoint(addr uint64) error {
	cmd := fmt.Sprintf("z0,%x,4", addr)
	reply, err := g.sendPacket(cmd)
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("gdb remove breakpoint: %s", reply)
	}
	return nil
}

// ReadRegisters reads all general-purpose registers via the 'g' packet.
// Returns the raw hex-encoded register dump.
func (g *GDBConn) ReadRegisters() (string, error) {
	return g.sendPacket("g")
}

// ReadRegister reads a single register by index via the 'p' packet.
// AArch64 QEMU register indices: x0-x30 = 0-30, SP = 31, PC = 32,
// CPSR = 33, V0-V31 = 34-65, FPSR = 66, FPCR = 67,
// ELR_EL1 = 68 (0x44), ... system regs vary by QEMU version.
func (g *GDBConn) ReadRegister(index int) ([]byte, error) {
	reply, err := g.sendPacket(fmt.Sprintf("p%x", index))
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(reply, "E") {
		return nil, fmt.Errorf("gdb read register %d: %s", index, reply)
	}
	return hex.DecodeString(reply)
}

// WriteRegister writes a single register by index via the 'P' packet.
func (g *GDBConn) WriteRegister(index int, data []byte) error {
	reply, err := g.sendPacket(fmt.Sprintf("P%x=%s", index, hex.EncodeToString(data)))
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("gdb write register %d: %s", index, reply)
	}
	return nil
}

// WaitBreak waits for the stub to report a stop event (breakpoint hit,
// signal, etc). Returns the raw stop-reply packet body.
func (g *GDBConn) WaitBreak(timeout time.Duration) (string, error) {
	g.conn.SetDeadline(time.Now().Add(timeout))
	buf := make([]byte, 4096)
	var resp []byte
	for {
		n, err := g.conn.Read(buf)
		if err != nil {
			return "", fmt.Errorf("gdb wait: %w", err)
		}
		resp = append(resp, buf[:n]...)
		s := string(resp)
		if idx := strings.Index(s, "$"); idx >= 0 {
			if end := strings.Index(s[idx:], "#"); end >= 0 && len(s) >= idx+end+3 {
				body := s[idx+1 : idx+end]
				g.conn.Write([]byte("+"))
				return body, nil
			}
		}
	}
}
