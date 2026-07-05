// Package ux's PhaseRunner is the sequential phase checklist primitive that
// sits below the cell-open banner (Banner) and above the structured debug
// info table (KV). CELL-262.
//
// Design separation between the three ux primitives:
//
//   - Banner — header. One row. Identity (cell · project #bunk).
//   - KV     — structured info table. N rows of key/value pairs, debug-gated.
//   - PhaseRunner — sequential step list. N rows of `✓ name [— detail] <elapsed>`.
//
// Each does ONE job; callers compose them top-to-bottom.
//
// # Cooked-mode constraint
//
// PhaseRunner intentionally uses the same ProgressSpinner model as everywhere
// else in this package: a goroutine ticker that writes `\r\033[K` for
// line-clearing, never a Bubble Tea program. The reason: cell-open hands the
// TTY off to `docker exec -it ... claude` at the end. Anything that puts the
// terminal in raw mode or alt screen (i.e. tea.NewProgram) risks leaving
// claude with a broken stdio if the lifecycle isn't perfectly drained. Cooked
// mode is the boring, proven shape.
//
// # Sequencing
//
// Phases run synchronously, one at a time. If a phase needs to fan out
// internally that's fine; PhaseRunner itself never renders concurrent rows
// (cursor-positioning gymnastics break under interleaved stderr writes).
//
// # Wire format
//
// The success row format is `✓ <name>[ — <detail>] <elapsed>` — same as
// ProgressSpinner.Success because PhaseRunner is a thin convenience wrapper
// around it. CELL-261's FormatSecretsPhase produces strings that drop into
// the `<detail>` slot verbatim.
//
package ux

// PhaseRunner owns the sequential phase list above the resumed parent
// spinner. Zero-value is usable; no constructor needed.
type PhaseRunner struct {
	cur *ProgressSpinner
}

// Phase runs fn under a per-phase ProgressSpinner. On nil-error, lands a
// permanent `✓ <name> <elapsed>` row and returns nil. On error, lands a
// permanent `✗ <name> — <err>` row and returns the error verbatim so the
// caller can propagate or recover with errors.Is.
func (p *PhaseRunner) Phase(name string, fn func() error) error {
	p.cur = NewProgressSpinner(name)
	if err := fn(); err != nil {
		p.cur.Fail(name + " — " + err.Error())
		p.cur = nil
		return err
	}
	p.cur.Success(name)
	p.cur = nil
	return nil
}

// PhaseDetailed is the variant for phases that compute a permanent-line
// suffix on success (e.g. "Loading secrets" returning "7 resolved", "Image
// pin" returning the short SHA). Empty detail string omits the trailing
// " — " so single-value rows stay clean.
//
// On error, behaves like Phase: lands `✗ <name> — <err>` and returns the
// error. The detail value is discarded in the error path because the row
// already carries the error message.
func (p *PhaseRunner) PhaseDetailed(name string, fn func() (detail string, err error)) error {
	p.cur = NewProgressSpinner(name)
	detail, err := fn()
	if err != nil {
		p.cur.Fail(name + " — " + err.Error())
		p.cur = nil
		return err
	}
	if detail == "" {
		p.cur.Success(name)
	} else {
		p.cur.Success(name + " — " + detail)
	}
	p.cur = nil
	return nil
}

// PhaseDetailedRunning is like PhaseDetailed but renders a different label
// while the spinner is active vs the permanent ✓/✗ row. Use for phases whose
// in-progress text carries a user prompt that no longer applies once the
// phase completes (e.g. "Loading secrets (please authorize 1Password)" while
// running, "Loading secrets — 7 resolved" once done).
//
// Identical behavior to PhaseDetailed on the final row: `✓ <finalName>` /
// `✓ <finalName> — <detail>` / `✗ <finalName> — <err>`.
func (p *PhaseRunner) PhaseDetailedRunning(running, finalName string, fn func() (detail string, err error)) error {
	p.cur = NewProgressSpinner(running)
	detail, err := fn()
	if err != nil {
		p.cur.Fail(finalName + " — " + err.Error())
		p.cur = nil
		return err
	}
	if detail == "" {
		p.cur.Success(finalName)
	} else {
		p.cur.Success(finalName + " — " + detail)
	}
	p.cur = nil
	return nil
}

// Seal lands the final ✓ row before the host→container handoff. Explicit
// boundary marker — the call site that follows Seal is the docker exec that
// takes the TTY. Distinct from Phase because there's no work to wrap; the
// runner just emits the row.
//
// In practice the caller writes:
//
//	pr.Seal("Cell ready")
//	return execClaude(ctx, ...)   // takes the TTY from the row below
func (p *PhaseRunner) Seal(name string) {
	sp := NewProgressSpinner(name)
	sp.Success(name)
}
