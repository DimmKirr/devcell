package qemu

import (
	"fmt"
	"time"
)

// Guest-stall detection.
//
// A hung guest and a booting one look identical on screen: Windows blanks the
// display after ~8 idle minutes, and the display also stops updating at
// ExitBootServices. So blackness — or any display-only rule — cannot decide it,
// and a detector built on frame content will eventually fail a healthy boot.
//
// What separates them is the conjunction of three signals: the frame is
// unchanged, no bytes were read, AND the vCPU program counter has not moved.
// The last term is the strongest: a live guest cannot hold one PC across polls
// seconds apart, idle or not. Observed in the real KVM failure — rd frozen at
// 1081344 and PC pinned at 000000013c347200 for 40 consecutive polls.

// StallSignal is one poll's worth of liveness evidence.
type StallSignal struct {
	ScreenHash uint64 // hash of the raw screendump
	ReadBytes  int64  // cumulative bytes read across block devices
	PC         string // vCPU program counter, as reported by "info registers"
}

// StallTracker counts consecutive polls in which nothing observable changed.
type StallTracker struct {
	prev     *StallSignal
	consec   int
	everRead bool
}

// Observe records a poll and returns the number of consecutive unchanged polls
// seen so far.
//
// It returns 0 until the guest has read at least one byte: before boot has
// touched the media, identical polls are expected and say nothing about
// liveness. That guard is what keeps a slow start from being called a hang.
func (s *StallTracker) Observe(sig StallSignal) int {
	if sig.ReadBytes > 0 {
		s.everRead = true
	}
	prev := s.prev
	cur := sig
	s.prev = &cur

	if !s.everRead {
		s.consec = 0
		return 0
	}
	if prev == nil {
		s.consec = 0
		return 0
	}
	unchanged := prev.ScreenHash == sig.ScreenHash &&
		prev.ReadBytes == sig.ReadBytes &&
		prev.PC == sig.PC
	if unchanged {
		s.consec++
	} else {
		s.consec = 0
	}
	return s.consec
}

// Stalled reports whether at least threshold consecutive unchanged polls have
// been observed.
func (s *StallTracker) Stalled(threshold int) bool {
	return s.consec >= threshold
}

// Consecutive returns the current unchanged-poll count.
func (s *StallTracker) Consecutive() int { return s.consec }

// StallPollsFor converts a stall budget in seconds into a poll count, given the
// poll interval. Never returns fewer than 2: detecting "unchanged" needs two
// samples to compare, so a single poll can never establish a stall.
func StallPollsFor(budgetSeconds, intervalSeconds int) int {
	if intervalSeconds <= 0 {
		return 2
	}
	n := budgetSeconds / intervalSeconds
	if n < 2 {
		return 2
	}
	return n
}

// WriteProgressTracker watches cumulative bytes written to the guest's disk.
//
// It answers a narrower question than StallTracker — "is the install making
// progress?" rather than "is the guest alive?" — and needs only one signal,
// which makes it usable from the CLI without QMP register dumps. Windows Setup
// writes continuously, so a full window with no new bytes means the guest never
// got as far as applying the image: wrong boot device, dead firmware, or Setup
// exiting before it started.
type WriteProgressTracker struct {
	// Window is how long a guest may write nothing before it counts as stalled.
	// It must exceed the longest legitimate pause: Windows goes quiet for a few
	// minutes at a time under TCG, so minutes, not seconds.
	Window time.Duration

	started  bool
	lastData int64
	lastMove time.Duration
	elapsed  time.Duration
}

// Observe records cumulative bytes written at elapsed time since start, and
// reports whether the guest has now been silent for longer than Window.
func (w *WriteProgressTracker) Observe(written int64, elapsed time.Duration) bool {
	w.elapsed = elapsed
	if !w.started {
		w.started = true
		w.lastData = written
		w.lastMove = elapsed
		return false
	}
	if written != w.lastData {
		w.lastData = written
		w.lastMove = elapsed
	}
	return elapsed-w.lastMove >= w.Window
}

// Reason describes the stall in the terms it was measured in.
func (w *WriteProgressTracker) Reason() string {
	return fmt.Sprintf("guest wrote %d MB and nothing further for %s (total elapsed %s) — "+
		"Windows Setup writes continuously, so it never started applying the image",
		w.lastData>>20, (w.elapsed - w.lastMove).Round(time.Second), w.elapsed.Round(time.Second))
}
