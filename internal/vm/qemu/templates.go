package qemu

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

// Guest scripts live as files under templates/, never as Go raw strings.
//
// PowerShell's escape character is the backtick — the same character that
// delimits a Go raw string — so a script needing `n, `t or a line continuation
// cannot be written inline at all. That is not hypothetical: writing `Staged`
// inside a comment terminated a raw string and broke the build on 2026-07-31.
// Splicing Go constants also forced scripts to close and reopen their string
// mid-line, which is how a `"$env:USERNAME:(R)"` interpolation bug survived
// review. Template fields remove both hazards, and the scripts become
// syntax-highlightable, greppable files.
//
// Layout: templates/<domain>/<name>.ps1.tmpl — provision/ for build-time
// provisioning, devenv/ for dev-environment setup.
//
//go:embed templates
var guestTemplates embed.FS

// renderTemplate renders a guest script by path under templates/.
//
// A template that fails to parse or execute is a programming error, not a
// runtime condition: the data comes from our own config structs, so there is
// no input a user could supply to trigger it. Panicking keeps every caller's
// signature free of an error that cannot happen in practice.
func renderTemplate(path string, data any) string {
	raw, err := guestTemplates.ReadFile("templates/" + path)
	if err != nil {
		panic(fmt.Sprintf("guest template %s: %v", path, err))
	}
	// Partials are parsed alongside every script, so a shared fragment (the
	// virtio CD probe, say) is written once and pulled in with
	// {{template "name"}} rather than spliced from a Go constant.
	tmpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		panic(fmt.Sprintf("parsing guest template %s: %v", path, err))
	}
	if _, err := tmpl.ParseFS(guestTemplates, "templates/partials/*.tmpl"); err != nil {
		panic(fmt.Sprintf("parsing guest template partials: %v", err))
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("rendering guest template %s: %v", path, err))
	}
	return buf.String()
}
