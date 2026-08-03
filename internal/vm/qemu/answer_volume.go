package qemu

// answerVolumeConsumedBytes separates "Windows mounted the FAT volume" from
// "Setup read autounattend.xml off it". Measured 2026-07-29 (CELL-362):
// mount-only probing reads ~3.5KB of filesystem metadata; consumption jumps
// to ~191KB. 64KB sits between the clusters with >10x margin each side.
const answerVolumeConsumedBytes = 64 << 10

// AnswerVolumeConsumed reports whether the guest's cumulative reads from the
// answer volume prove Setup consumed the answer file. This is the earliest
// host-visible signal that an install is unattended rather than sitting at
// the interactive language screen — the two are indistinguishable on screen
// ratios alone.
func AnswerVolumeConsumed(readBytes int64) bool {
	return readBytes >= answerVolumeConsumedBytes
}
