package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveEngine_FlagWins(t *testing.T) {
	got := resolveEngine("tart", "docker", false)
	assert.Equal(t, "tart", got)
}

func TestResolveEngine_TOMLFallback(t *testing.T) {
	got := resolveEngine("", "qemu", false)
	assert.Equal(t, "qemu", got)
}

func TestResolveEngine_DefaultDocker(t *testing.T) {
	got := resolveEngine("", "", false)
	assert.Equal(t, "docker", got)
}

func TestResolveEngine_MacOSAlias(t *testing.T) {
	got := resolveEngine("", "", true)
	assert.Equal(t, "vagrant", got)
}

func TestResolveEngine_MacOSOverridesFlag(t *testing.T) {
	got := resolveEngine("qemu", "", true)
	assert.Equal(t, "vagrant", got)
}

func TestResolveEngine_ExplicitDocker(t *testing.T) {
	got := resolveEngine("docker", "", false)
	assert.Equal(t, "docker", got)
}

func TestResolveEngine_TOMLVagrant(t *testing.T) {
	got := resolveEngine("", "vagrant", false)
	assert.Equal(t, "vagrant", got)
}
