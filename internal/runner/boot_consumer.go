package runner

import (
	"github.com/DimmKirr/devcell/internal/ux"
)

// ConsumeBootEvents reads BootDirWatcher events and renders each as a
// permanent ✓ row directly via ux.SuccessMsg. CELL-264.
//
// Wire format matches the host-side PhaseRunner (CELL-262) so the rendered
// checklist reads as one continuous boot story:
//
//	✓ Cell ready                           ← from PhaseRunner.Seal on the host
//	✓ Loading mise tools                   ← from sentinel mise.starting
//	✓ Mise ready                           ← from sentinel mise.ready
//	✓ GUI ready                            ← from sentinel gui.ready
//	                                        ← boot.ready stops the consumer; TTY handoff
//
// # Why no spinner
//
// The CELL-262 PhaseRunner uses ProgressSpinner for host-side phases
// because the host is actively *doing* work while the spinner ticks.
// Here, the events represent completed milestones from inside the
// container — by the time the host sees the sentinel file, the work is
// already done. A spinner adds no information AND introduces a race: the
// spinner's clear-on-stop can land on the wrong terminal row when the
// container's stdout writes concurrently to the same TTY (visible as
// a stuck `⠋ GUI ready 80ms` ghost row preceding the sealed ✓).
//
// Direct permanent-line emission avoids the race entirely. No spinner
// goroutine, no clear, no chance of fighting the container TTY.
//
// # Title lookup precedence
//
//  1. event.Title — set by BootDirWatcher from the host-side titles map
//  2. event.Component when Title is empty AND component != "boot" —
//     falls back so a fragment introducing a new component still produces
//     a visible row (just with the raw name) instead of being dropped.
//
// # boot.ready
//
// The terminal `boot.ready` event (sentinel emitted by entrypoint.sh
// after all fragments) carries Title="" by design — the consumer treats
// it as the seal trigger, NOT as a row to render. Returns immediately
// when seen.
func ConsumeBootEvents(events <-chan BootEvent) {
	// Per-row elapsed comes from ProgressSpinner itself: its clock starts
	// in NewProgressSpinner (i.e. when the `.starting` sentinel arrives)
	// and Success() prints the delta to `.ready`. That's the right number
	// — each row shows how long that specific phase took.
	//
	// Paired-event merge: `<X>.starting` opens the spinner with the
	// in-progress title ("Configuring nix"); the matching `<X>.ready`
	// seals THAT SAME spinner with the completed-state title ("Nix ready")
	// — one row, one ✓, elapsed = starting→ready. Without this, every
	// `.ready` event opened its own redundant spinner that the next event
	// sealed at 0s, producing noise like `✓ Nix ready 0s` directly after
	// `✓ Configuring nix 324ms`.
	var sp *ux.ProgressSpinner
	var currentComponent string
	var currentTitle string

	sealCurrent := func(finalTitle string) {
		if sp == nil {
			return
		}
		sp.Success(finalTitle)
		sp = nil
		currentComponent = ""
		currentTitle = ""
	}

	for ev := range events {
		// Terminal seal — boot.ready means entrypoint finished sourcing
		// all fragments and is about to exec the child binary. Stop
		// rendering immediately so we don't fight claude/zsh for the TTY.
		if ev.Component == "boot" && ev.State == "ready" {
			sealCurrent(currentTitle)
			return
		}

		title := ev.Title
		if title == "" {
			// Unknown component: fall back to the raw name so the user
			// still sees that *something* happened in the container.
			title = ev.Component + " " + ev.State
		}

		// Paired .ready: an in-flight spinner already exists for THIS
		// component (opened by its .starting). Seal it in place with the
		// ready-state title — no new row.
		if ev.State == "ready" && sp != nil && ev.Component == currentComponent {
			sealCurrent(title)
			continue
		}

		// New phase (either a .starting or a solo .ready like
		// container.ready / entrypoint.ready). Seal whatever was in flight
		// with its own current title, then open a fresh spinner. For solo
		// .ready events the next event seals this spinner — preserving the
		// existing visible-timing behavior for the container/entrypoint
		// boot rows.
		sealCurrent(currentTitle)
		currentComponent = ev.Component
		currentTitle = title
		// Animated spinner while we wait for the next event. The user
		// sees `⠋ Configuring nix 1.2s` ticking until nix.ready arrives,
		// then a permanent `✓ Nix ready 1.4s` — same row, transitioned.
		sp = ux.NewProgressSpinner(title)
	}

	// Channel closed without boot.ready (BootDirWatcher.Close, typically
	// because the container exited). The agent process has the TTY by
	// now; do NOT seal — writing more rows after the shell prompt would
	// be visually confusing. Just abandon the in-flight spinner.
	if sp != nil {
		sp.Stop()
	}
}
