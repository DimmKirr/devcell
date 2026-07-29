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
				"keys": qkeys,
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
	}
	return nil
}

// StringToQKeyStrokes converts a string to QMP keystroke sequences.
// Each returned element is a slice of simultaneously-pressed QKeyCodes.
func StringToQKeyStrokes(s string) [][]string {
	var strokes [][]string
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z':
			strokes = append(strokes, []string{string(ch)})
		case ch >= 'A' && ch <= 'Z':
			strokes = append(strokes, []string{"shift", string(ch - 'A' + 'a')})
		case ch >= '0' && ch <= '9':
			strokes = append(strokes, []string{string(ch)})
		case ch == '\\':
			strokes = append(strokes, []string{"backslash"})
		case ch == ':':
			strokes = append(strokes, []string{"shift", "semicolon"})
		case ch == '.':
			strokes = append(strokes, []string{"dot"})
		case ch == '/':
			strokes = append(strokes, []string{"slash"})
		case ch == '-':
			strokes = append(strokes, []string{"minus"})
		case ch == ' ':
			strokes = append(strokes, []string{"spc"})
		case ch == '\n':
			strokes = append(strokes, []string{"ret"})
		}
	}
	return strokes
}
