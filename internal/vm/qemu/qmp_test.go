package qemu

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startMockQMPServer(t *testing.T, statusValue string) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "qmp.sock")

	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		greeting := map[string]any{
			"QMP": map[string]any{
				"version": map[string]any{
					"qemu": map[string]any{"micro": 0, "minor": 2, "major": 9},
				},
				"capabilities": []any{},
			},
		}
		json.NewEncoder(conn).Encode(greeting)

		dec := json.NewDecoder(conn)

		var cmd1 map[string]any
		if err := dec.Decode(&cmd1); err != nil {
			return
		}
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})

		var cmd2 map[string]any
		if err := dec.Decode(&cmd2); err != nil {
			return
		}
		json.NewEncoder(conn).Encode(map[string]any{
			"return": map[string]any{"status": statusValue, "running": statusValue == "running"},
		})
	}()

	return sockPath
}

func TestQueryVMState_ParsesRunning(t *testing.T) {
	sockPath := startMockQMPServer(t, "running")

	state, err := QueryVMState(sockPath)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, state)
}

func TestQueryVMState_ParsesPaused(t *testing.T) {
	sockPath := startMockQMPServer(t, "paused")

	state, err := QueryVMState(sockPath)
	require.NoError(t, err)
	assert.Equal(t, StateStopped, state)
}

func TestQueryVMState_NoSocket(t *testing.T) {
	_, err := QueryVMState("/tmp/nonexistent-qmp-socket-12345.sock")
	assert.Error(t, err)
}

func startMockQMPServerScreendump(t *testing.T) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		greeting := map[string]any{
			"QMP": map[string]any{
				"version":      map[string]any{"qemu": map[string]any{"micro": 0, "minor": 2, "major": 9}},
				"capabilities": []any{},
			},
		}
		json.NewEncoder(conn).Encode(greeting)
		dec := json.NewDecoder(conn)

		// capabilities
		var cmd1 map[string]any
		dec.Decode(&cmd1)
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})

		// screendump
		var cmd2 map[string]any
		dec.Decode(&cmd2)
		// Verify the command is screendump with a filename arg
		assert.Equal(t, "screendump", cmd2["execute"])
		args, _ := cmd2["arguments"].(map[string]any)
		assert.NotEmpty(t, args["filename"])
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})
	}()
	return sockPath
}

func TestQMPScreendump(t *testing.T) {
	sockPath := startMockQMPServerScreendump(t)
	outFile := filepath.Join(t.TempDir(), "screen.ppm")
	err := QMPScreendump(sockPath, outFile)
	require.NoError(t, err)
}

func TestQMPScreendump_NoSocket(t *testing.T) {
	err := QMPScreendump("/tmp/nonexistent-qmp-socket-12345.sock", "/tmp/test.ppm")
	assert.Error(t, err)
}

func startMockQMPServerBlockStats(t *testing.T) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		greeting := map[string]any{
			"QMP": map[string]any{
				"version":      map[string]any{"qemu": map[string]any{"micro": 0, "minor": 2, "major": 9}},
				"capabilities": []any{},
			},
		}
		json.NewEncoder(conn).Encode(greeting)
		dec := json.NewDecoder(conn)

		// capabilities
		var cmd1 map[string]any
		dec.Decode(&cmd1)
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})

		// query-blockstats
		var cmd2 map[string]any
		dec.Decode(&cmd2)
		assert.Equal(t, "query-blockstats", cmd2["execute"])
		json.NewEncoder(conn).Encode(map[string]any{
			"return": []any{
				map[string]any{
					"device": "cdrom0",
					"stats": map[string]any{
						"rd_bytes":      12345678,
						"rd_operations": 42,
						"wr_bytes":      0,
					},
				},
				map[string]any{
					"device": "virtio0",
					"stats": map[string]any{
						"rd_bytes":      1024,
						"rd_operations": 2,
						"wr_bytes":      512,
					},
				},
			},
		})
	}()
	return sockPath
}

func TestQMPBlockStats(t *testing.T) {
	sockPath := startMockQMPServerBlockStats(t)

	stats, err := QMPBlockStats(sockPath)
	require.NoError(t, err)
	require.Contains(t, stats, "cdrom0")
	require.Contains(t, stats, "virtio0")
	assert.Equal(t, int64(12345678), stats["cdrom0"].ReadBytes)
	assert.Equal(t, int64(42), stats["cdrom0"].ReadOps)
	assert.Equal(t, int64(512), stats["virtio0"].WriteBytes)
}

func TestQMPBlockStats_NoSocket(t *testing.T) {
	_, err := QMPBlockStats("/tmp/nonexistent-qmp-socket-12345.sock")
	assert.Error(t, err)
}

func startMockQMPServerHumanMonitor(t *testing.T, reply string) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		greeting := map[string]any{
			"QMP": map[string]any{
				"version":      map[string]any{"qemu": map[string]any{"micro": 0, "minor": 2, "major": 9}},
				"capabilities": []any{},
			},
		}
		json.NewEncoder(conn).Encode(greeting)
		dec := json.NewDecoder(conn)

		// capabilities
		var cmd1 map[string]any
		dec.Decode(&cmd1)
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})

		// human-monitor-command
		var cmd2 map[string]any
		dec.Decode(&cmd2)
		assert.Equal(t, "human-monitor-command", cmd2["execute"])
		args, _ := cmd2["arguments"].(map[string]any)
		assert.NotEmpty(t, args["command-line"])
		json.NewEncoder(conn).Encode(map[string]any{"return": reply})
	}()
	return sockPath
}

func TestQMPHumanMonitor(t *testing.T) {
	sockPath := startMockQMPServerHumanMonitor(t, " PC=000000013fa60124 X00=0000000000000000\n")

	out, err := QMPHumanMonitor(sockPath, "info registers")
	require.NoError(t, err)
	assert.Contains(t, out, "PC=000000013fa60124")
}

func TestQMPHumanMonitor_NoSocket(t *testing.T) {
	_, err := QMPHumanMonitor("/tmp/nonexistent-qmp-socket-12345.sock", "info registers")
	assert.Error(t, err)
}

func startMockQMPServerEject(t *testing.T) (string, chan map[string]any) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	got := make(chan map[string]any, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		json.NewEncoder(conn).Encode(map[string]any{
			"QMP": map[string]any{
				"version":      map[string]any{"qemu": map[string]any{"micro": 0, "minor": 2, "major": 9}},
				"capabilities": []any{},
			},
		})
		dec := json.NewDecoder(conn)

		var cmd1 map[string]any
		dec.Decode(&cmd1)
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})

		var cmd2 map[string]any
		dec.Decode(&cmd2)
		got <- cmd2
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})
	}()
	return sockPath, got
}

func TestQMPEjectMedium(t *testing.T) {
	// After Windows applies its image it reboots; with the installer CD still
	// present and holding bootindex=0, the VM boots the installer again and
	// Setup stops on "you started an upgrade and booted from installation
	// media". Ejecting sends the next boot to the disk.
	sockPath, got := startMockQMPServerEject(t)

	require.NoError(t, QMPEjectMedium(sockPath, "cdrom0"))

	cmd := <-got
	assert.Equal(t, "eject", cmd["execute"])
	args, _ := cmd["arguments"].(map[string]any)
	assert.Equal(t, "cdrom0", args["id"])
	assert.Equal(t, true, args["force"], "tray may be locked by the guest")
}

func TestQMPEjectMedium_NoSocket(t *testing.T) {
	assert.Error(t, QMPEjectMedium("/tmp/nonexistent-qmp-socket-12345.sock", "cdrom0"))
}

func TestStringToQKeyStrokes_ShellPunctuation(t *testing.T) {
	// Driving a guest shell over QMP needs redirection and grouping, not just
	// alphanumerics — without these, any diagnostic command is untypeable.
	for _, tc := range []struct {
		in    string
		combo []string
	}{
		{">", []string{"shift", "dot"}},
		{"<", []string{"shift", "comma"}},
		{"&", []string{"shift", "7"}},
		{"(", []string{"shift", "9"}},
		{")", []string{"shift", "0"}},
		{"%", []string{"shift", "5"}},
		{"*", []string{"shift", "8"}},
		{"_", []string{"shift", "minus"}},
		{"\"", []string{"shift", "apostrophe"}},
		{"|", []string{"shift", "backslash"}},
		{"^", []string{"shift", "6"}},
		{",", []string{"comma"}},
		{"=", []string{"equal"}},
		{"'", []string{"apostrophe"}},
	} {
		got := StringToQKeyStrokes(tc.in)
		require.Len(t, got, 1, "%q must map to one keystroke", tc.in)
		assert.Equal(t, tc.combo, got[0], "wrong keys for %q", tc.in)
	}
}

func TestStringToQKeyStrokes_DropsUnmappableRunes(t *testing.T) {
	// Silently emitting nothing for an unmappable rune would corrupt the
	// command; callers need the count to match what they asked for.
	assert.Empty(t, StringToQKeyStrokes("€"))
}

func TestQMPSendKeys_UsesShortHoldTime(t *testing.T) {
	// send-key defaults to a 100ms hold. Under TCG that is long enough for the
	// guest to start auto-repeating, which turned a typed command into a wall
	// of repeated characters with a stuck shift. Hold briefly instead.
	sockPath, got := startMockQMPServerEject(t)

	require.NoError(t, QMPSendKeys(sockPath, [][]string{{"shift", "a"}}))

	cmd := <-got
	assert.Equal(t, "send-key", cmd["execute"])
	args, _ := cmd["arguments"].(map[string]any)
	hold, ok := args["hold-time"]
	require.True(t, ok, "hold-time must be set explicitly")
	assert.EqualValues(t, qmpKeyHoldMS, hold)
	assert.Less(t, qmpKeyHoldMS, 100, "must be shorter than the 100ms default")
}

// Run 20260731T205544 (run 7): Windows 11 auto-opens the Start menu at the
// first sign-in after OOBE, and an unattended VM never sends the input event
// that would close it — it sat over every screenshot from first logon to
// teardown. SSH-ready doubles as the "first logon reached" signal (sshd only
// starts from bootstrap's first-logon run), so one Esc there both confirms
// OOBE is over and clears the screen for the provisioning screenshots.
func TestQMPDismissFirstLogonUI_SendsEsc(t *testing.T) {
	sockPath, got := startMockQMPServerEject(t)

	require.NoError(t, QMPDismissFirstLogonUI(sockPath))

	cmd := <-got
	assert.Equal(t, "send-key", cmd["execute"])
	args, _ := cmd["arguments"].(map[string]any)
	keys, _ := args["keys"].([]any)
	require.Len(t, keys, 1, "one keystroke: a bare Esc, nothing else")
	key, _ := keys[0].(map[string]any)
	assert.EqualValues(t, "esc", key["data"])
}

func TestQMPDismissFirstLogonUI_NoSocket(t *testing.T) {
	assert.Error(t, QMPDismissFirstLogonUI("/tmp/nonexistent-qmp-socket-99999.sock"))
}

// QMPQueryKVM — the only way for a run to prove, from inside itself, that
// hardware virtualization was actually engaged. Without it, attributing a run
// dir to KVM vs TCG relies on the code state at launch.
func startMockQMPServerQueryKVM(t *testing.T, enabled, present bool) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		json.NewEncoder(conn).Encode(map[string]any{
			"QMP": map[string]any{
				"version":      map[string]any{"qemu": map[string]any{"micro": 0, "minor": 2, "major": 9}},
				"capabilities": []any{},
			},
		})
		dec := json.NewDecoder(conn)
		var caps map[string]any
		dec.Decode(&caps)
		json.NewEncoder(conn).Encode(map[string]any{"return": map[string]any{}})
		var cmd map[string]any
		dec.Decode(&cmd)
		assert.Equal(t, "query-kvm", cmd["execute"])
		json.NewEncoder(conn).Encode(map[string]any{
			"return": map[string]any{"enabled": enabled, "present": present},
		})
	}()
	return sockPath
}

func TestQMPQueryKVM_Enabled(t *testing.T) {
	enabled, present, err := QMPQueryKVM(startMockQMPServerQueryKVM(t, true, true))
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.True(t, present)
}

// The case that matters: KVM compiled in but not in use. A run that reports
// this while claiming to be a KVM run is lying about its accelerator.
func TestQMPQueryKVM_PresentButDisabled(t *testing.T) {
	enabled, present, err := QMPQueryKVM(startMockQMPServerQueryKVM(t, false, true))
	require.NoError(t, err)
	assert.False(t, enabled)
	assert.True(t, present)
}

func TestQMPQueryKVM_NoSocket(t *testing.T) {
	_, _, err := QMPQueryKVM("/tmp/nonexistent-qmp-socket-99999.sock")
	assert.Error(t, err)
}
