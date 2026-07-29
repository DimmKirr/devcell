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
				"version": map[string]any{"qemu": map[string]any{"micro": 0, "minor": 2, "major": 9}},
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
