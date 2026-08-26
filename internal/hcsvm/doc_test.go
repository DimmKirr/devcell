package hcsvm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVMDocJSON_IsValidHCSv2(t *testing.T) {
	doc := VMDocJSON(512, 1)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(doc), &parsed), "doc must be valid JSON")

	schema := parsed["SchemaVersion"].(map[string]any)
	assert.Equal(t, float64(2), schema["Major"], "HCS v2 schema")

	vm := parsed["VirtualMachine"].(map[string]any)
	chipset := vm["Chipset"].(map[string]any)
	_, hasUefi := chipset["Uefi"]
	assert.True(t, hasUefi, "diskless Gen2 boots straight into UEFI — that IS the boot screen")

	topo := vm["ComputeTopology"].(map[string]any)
	mem := topo["Memory"].(map[string]any)
	assert.Equal(t, float64(512), mem["SizeInMB"])
	cpu := topo["Processor"].(map[string]any)
	assert.Equal(t, float64(1), cpu["Count"])

	// The test VM must die with its creator, not leak into the WinPE session.
	assert.Equal(t, true, parsed["ShouldTerminateOnLastHandleClosed"])
}

func TestVMDocJSON_ParamsFlowThrough(t *testing.T) {
	doc := VMDocJSON(1024, 2)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(doc), &parsed))
	topo := parsed["VirtualMachine"].(map[string]any)["ComputeTopology"].(map[string]any)
	assert.Equal(t, float64(1024), topo["Memory"].(map[string]any)["SizeInMB"])
	assert.Equal(t, float64(2), topo["Processor"].(map[string]any)["Count"])
}
