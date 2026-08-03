package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Answer-volume consumption. Windows Setup mounting the FAT volume reads a
// few KB of filesystem metadata; actually consuming autounattend.xml reads
// far more. Measured 2026-07-29 (CELL-362): mounted-but-unread ≈ 3.5KB,
// consumed ≈ 191KB. The threshold sits between the clusters so either side
// has >10x margin.

func TestAnswerVolumeConsumed_MountOnlyIsNotConsumption(t *testing.T) {
	assert.False(t, AnswerVolumeConsumed(3500),
		"~3.5KB is FAT metadata probing — Setup did not read autounattend.xml")
}

func TestAnswerVolumeConsumed_MeasuredConsumption(t *testing.T) {
	assert.True(t, AnswerVolumeConsumed(191<<10),
		"~191KB is the measured signature of Setup consuming the answer file")
}

func TestAnswerVolumeConsumed_Boundary(t *testing.T) {
	assert.True(t, AnswerVolumeConsumed(64<<10))
	assert.False(t, AnswerVolumeConsumed(64<<10-1))
	assert.False(t, AnswerVolumeConsumed(0))
}
