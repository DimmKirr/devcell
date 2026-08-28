package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewVM_InitialState(t *testing.T) {
	spec := testSpec()
	vm := NewVM(spec, NopObserver{}, "")
	assert.Equal(t, StateStopped, vm.State())
	assert.Equal(t, "stopped", vm.StateString())
}

func TestVMState_Constants(t *testing.T) {
	assert.Equal(t, VMState("unknown"), StateUnknown)
	assert.Equal(t, VMState("stopped"), StateStopped)
	assert.Equal(t, VMState("running"), StateRunning)
	assert.Equal(t, VMState("error"), StateError)
}
