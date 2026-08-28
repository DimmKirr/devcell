package qemu

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

func QueryVMState(socketPath string) (VMState, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return StateUnknown, fmt.Errorf("QMP connect: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var greeting map[string]any
	if err := dec.Decode(&greeting); err != nil {
		return StateUnknown, fmt.Errorf("QMP greeting: %w", err)
	}

	if err := enc.Encode(map[string]string{"execute": "qmp_capabilities"}); err != nil {
		return StateUnknown, fmt.Errorf("QMP capabilities: %w", err)
	}
	var capResp map[string]any
	if err := dec.Decode(&capResp); err != nil {
		return StateUnknown, fmt.Errorf("QMP capabilities response: %w", err)
	}

	if err := enc.Encode(map[string]string{"execute": "query-status"}); err != nil {
		return StateUnknown, fmt.Errorf("QMP query-status: %w", err)
	}
	var statusResp struct {
		Return struct {
			Status  string `json:"status"`
			Running bool   `json:"running"`
		} `json:"return"`
	}
	if err := dec.Decode(&statusResp); err != nil {
		return StateUnknown, fmt.Errorf("QMP query-status response: %w", err)
	}

	switch statusResp.Return.Status {
	case "running":
		return StateRunning, nil
	case "paused", "suspended", "shutdown", "postmigrate", "prelaunch", "finish-migrate":
		return StateStopped, nil
	default:
		return StateUnknown, nil
	}
}

// QMPScreendump captures the QEMU display framebuffer to a PPM file via QMP.
func QMPScreendump(socketPath, outputFile string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("QMP connect: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var greeting map[string]any
	if err := dec.Decode(&greeting); err != nil {
		return fmt.Errorf("QMP greeting: %w", err)
	}

	if err := enc.Encode(map[string]string{"execute": "qmp_capabilities"}); err != nil {
		return fmt.Errorf("QMP capabilities: %w", err)
	}
	var capResp map[string]any
	if err := dec.Decode(&capResp); err != nil {
		return fmt.Errorf("QMP capabilities response: %w", err)
	}

	cmd := map[string]any{
		"execute":   "screendump",
		"arguments": map[string]string{"filename": outputFile},
	}
	if err := enc.Encode(cmd); err != nil {
		return fmt.Errorf("QMP screendump: %w", err)
	}
	var resp map[string]any
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("QMP screendump response: %w", err)
	}
	if errObj, ok := resp["error"]; ok {
		return fmt.Errorf("QMP screendump error: %v", errObj)
	}
	return nil
}

// qmpKeyHoldMS is how long each key is held. QMP defaults to 100ms, which
// under TCG is long enough for the guest to begin auto-repeating: a typed
// command came back as a wall of repeated characters with a stuck shift.
const qmpKeyHoldMS = 20

// qmpKeyGap paces keystrokes so a slow guest keyboard driver does not drop or
// reorder them.
const qmpKeyGap = 30 * time.Millisecond

// QMPDismissFirstLogonUI sends a single Esc to the guest. Windows 11 opens
// the Start menu on its own at the first sign-in after OOBE, and an
// unattended VM never produces the input event that would close it — it
// stays over every subsequent screenshot. One Esc at "SSH ready" (which is
// also the proof that first logon happened) dismisses it.
func QMPDismissFirstLogonUI(socketPath string) error {
	return QMPSendKeys(socketPath, [][]string{{"esc"}})
}

// QMPSendKeys sends a sequence of keystrokes to the VM via QMP.
// Each element of keystrokes is a set of QKeyCodes pressed simultaneously
// (e.g. []string{"shift", "f"} for uppercase F).
func QMPSendKeys(socketPath string, keystrokes [][]string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("QMP connect: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var greeting map[string]any
	if err := dec.Decode(&greeting); err != nil {
		return fmt.Errorf("QMP greeting: %w", err)
	}
	if err := enc.Encode(map[string]string{"execute": "qmp_capabilities"}); err != nil {
		return fmt.Errorf("QMP capabilities: %w", err)
	}
	var capResp map[string]any
	if err := dec.Decode(&capResp); err != nil {
		return fmt.Errorf("QMP capabilities response: %w", err)
	}

	for _, combo := range keystrokes {
		var qkeys []map[string]any
		for _, k := range combo {
			qkeys = append(qkeys, map[string]any{"type": "qcode", "data": k})
		}
		cmd := map[string]any{
			"execute": "send-key",
			"arguments": map[string]any{
				"keys":      qkeys,
				"hold-time": qmpKeyHoldMS,
			},
		}
		if err := enc.Encode(cmd); err != nil {
			return fmt.Errorf("QMP send-key %v: %w", combo, err)
		}
		var resp map[string]any
		if err := dec.Decode(&resp); err != nil {
			return fmt.Errorf("QMP send-key %v response: %w", combo, err)
		}
		if errObj, ok := resp["error"]; ok {
			return fmt.Errorf("QMP send-key %v error: %v", combo, errObj)
		}
		time.Sleep(qmpKeyGap)
	}
	return nil
}

// BlockDeviceStats holds I/O counters for one block device from query-blockstats.
type BlockDeviceStats struct {
	ReadBytes  int64
	ReadOps    int64
	WriteBytes int64
}

// qmpHandshake dials the QMP socket and negotiates capabilities, returning
// ready-to-use encoder/decoder pairs. Caller must close the returned conn.
func qmpHandshake(socketPath string, deadline time.Duration) (net.Conn, *json.Encoder, *json.Decoder, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("QMP connect: %w", err)
	}
	conn.SetDeadline(time.Now().Add(deadline))

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var greeting map[string]any
	if err := dec.Decode(&greeting); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("QMP greeting: %w", err)
	}
	if err := enc.Encode(map[string]string{"execute": "qmp_capabilities"}); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("QMP capabilities: %w", err)
	}
	var capResp map[string]any
	if err := dec.Decode(&capResp); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("QMP capabilities response: %w", err)
	}
	return conn, enc, dec, nil
}

// QMPBlockStats returns per-device I/O counters via query-blockstats.
// Growing read counters on a frozen display prove the guest is still booting.
func QMPBlockStats(socketPath string) (map[string]BlockDeviceStats, error) {
	conn, enc, dec, err := qmpHandshake(socketPath, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := enc.Encode(map[string]string{"execute": "query-blockstats"}); err != nil {
		return nil, fmt.Errorf("QMP query-blockstats: %w", err)
	}
	var resp struct {
		Return []struct {
			Device string `json:"device"`
			Stats  struct {
				RdBytes int64 `json:"rd_bytes"`
				RdOps   int64 `json:"rd_operations"`
				WrBytes int64 `json:"wr_bytes"`
			} `json:"stats"`
		} `json:"return"`
		Error map[string]any `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("QMP query-blockstats response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("QMP query-blockstats error: %v", resp.Error)
	}

	stats := make(map[string]BlockDeviceStats, len(resp.Return))
	for _, d := range resp.Return {
		if d.Device == "" {
			continue
		}
		stats[d.Device] = BlockDeviceStats{
			ReadBytes:  d.Stats.RdBytes,
			ReadOps:    d.Stats.RdOps,
			WriteBytes: d.Stats.WrBytes,
		}
	}
	return stats, nil
}

// QMPQueryKVM reports whether the running VM has hardware virtualization
// enabled, and whether KVM is present on the host at all.
//
// This is how a run proves its own accelerator. `-accel kvm` cannot silently
// degrade to TCG — QEMU exits with an error instead — but that is an argument
// from flag semantics, not evidence in the artifact. Asking the live VM turns
// "this run should have used KVM" into "this run did".
func QMPQueryKVM(socketPath string) (enabled, present bool, err error) {
	conn, enc, dec, err := qmpHandshake(socketPath, 5*time.Second)
	if err != nil {
		return false, false, err
	}
	defer conn.Close()

	if err := enc.Encode(map[string]string{"execute": "query-kvm"}); err != nil {
		return false, false, fmt.Errorf("QMP query-kvm: %w", err)
	}
	var resp struct {
		Return struct {
			Enabled bool `json:"enabled"`
			Present bool `json:"present"`
		} `json:"return"`
		Error map[string]any `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		return false, false, fmt.Errorf("QMP query-kvm response: %w", err)
	}
	if resp.Error != nil {
		return false, false, fmt.Errorf("QMP query-kvm error: %v", resp.Error)
	}
	return resp.Return.Enabled, resp.Return.Present, nil
}

// QMPHumanMonitor runs an HMP command (e.g. "info registers") via QMP and
// returns its text output. A changing PC across calls proves the vCPU is alive.
func QMPHumanMonitor(socketPath, command string) (string, error) {
	conn, enc, dec, err := qmpHandshake(socketPath, 10*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	cmd := map[string]any{
		"execute":   "human-monitor-command",
		"arguments": map[string]any{"command-line": command},
	}
	if err := enc.Encode(cmd); err != nil {
		return "", fmt.Errorf("QMP human-monitor-command: %w", err)
	}
	var resp struct {
		Return string         `json:"return"`
		Error  map[string]any `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		return "", fmt.Errorf("QMP human-monitor-command response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("QMP human-monitor-command error: %v", resp.Error)
	}
	return resp.Return, nil
}

// StringToQKeyStrokes converts a string to QMP keystroke sequences.
// Each returned element is a slice of simultaneously-pressed QKeyCodes.
func StringToQKeyStrokes(s string) [][]string {
	var strokes [][]string
	for _, ch := range s {
		if combo, ok := qkeyForRune(ch); ok {
			strokes = append(strokes, combo)
		}
	}
	return strokes
}

// shiftedQKeys maps characters produced with Shift on a US layout.
var shiftedQKeys = map[rune]string{
	'!': "1", '@': "2", '#': "3", '$': "4", '%': "5",
	'^': "6", '&': "7", '*': "8", '(': "9", ')': "0",
	'_': "minus", '+': "equal", '{': "bracket_left", '}': "bracket_right",
	'|': "backslash", ':': "semicolon", '"': "apostrophe",
	'<': "comma", '>': "dot", '?': "slash", '~': "grave_accent",
}

// plainQKeys maps characters typed without a modifier.
var plainQKeys = map[rune]string{
	'\\': "backslash", '.': "dot", '/': "slash", '-': "minus", ' ': "spc",
	'\n': "ret", '\t': "tab", ',': "comma", ';': "semicolon", '=': "equal",
	'\'': "apostrophe", '[': "bracket_left", ']': "bracket_right", '`': "grave_accent",
}

// qkeyForRune returns the QKeyCode combination for a character, reporting
// false when the character has no mapping — callers must not assume every
// rune produces a keystroke, or a typed command would silently lose bytes.
func qkeyForRune(ch rune) ([]string, bool) {
	switch {
	case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
		return []string{string(ch)}, true
	case ch >= 'A' && ch <= 'Z':
		return []string{"shift", string(ch - 'A' + 'a')}, true
	}
	if key, ok := shiftedQKeys[ch]; ok {
		return []string{"shift", key}, true
	}
	if key, ok := plainQKeys[ch]; ok {
		return []string{key}, true
	}
	return nil, false
}

// QMPEjectMedium ejects removable media (id is the drive id, e.g. "cdrom0").
//
// Windows Setup reboots after applying its image. If the installer CD is still
// attached and holds bootindex=0, the firmware boots the installer again
// instead of the freshly installed OS, and Setup stops on "It looks like you
// started an upgrade and booted from installation media". Ejecting once the
// image is applied sends the next boot to disk. force=true because the guest
// may hold the tray locked.
func QMPEjectMedium(socketPath, id string) error {
	conn, enc, dec, err := qmpHandshake(socketPath, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	cmd := map[string]any{
		"execute":   "eject",
		"arguments": map[string]any{"id": id, "force": true},
	}
	if err := enc.Encode(cmd); err != nil {
		return fmt.Errorf("QMP eject %s: %w", id, err)
	}
	var resp map[string]any
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("QMP eject %s response: %w", id, err)
	}
	if errObj, ok := resp["error"]; ok {
		return fmt.Errorf("QMP eject %s error: %v", id, errObj)
	}
	return nil
}

// QMPQuit sends the "quit" command which flushes all block device caches
// and exits the QEMU process cleanly. Unlike Process.Kill(), this ensures
// writeback-cached qcow2 images are consistent on disk.
func QMPQuit(socketPath string) error {
	conn, enc, _, err := qmpHandshake(socketPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	return enc.Encode(map[string]string{"execute": "quit"})
}
