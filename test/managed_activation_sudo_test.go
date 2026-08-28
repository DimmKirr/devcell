// managed_activation_sudo_test.go — L1 wiring checks that the LLM modules'
// `home.activation.setupManaged<Agent>` scripts survive hosts where sudo is
// not on the legacy PATH, or not present at all.
//
// Failure mode this test pins (run 20260804, NixOS-WSL stage 13):
//
//	Each activation script exported PATH="/usr/bin:/bin:$PATH" and then ran
//	`sudo cp ... /etc/<agent>/...`. On NixOS the sudo wrapper lives in
//	/run/wrappers/bin — nothing installs it into /usr/bin — so the very
//	first sudo line aborted the whole home-manager activation with
//	`sudo: command not found` (exit 127) AFTER the profile was built,
//	leaving the generation half-activated inside the WSL2 distro.
//	Staging /etc files is a convenience; killing activation over it is not.

package container_test

import (
	"strings"
	"testing"
)

var managedActivationModules = []string{
	"modules/llm/claude.nix",
	"modules/llm/codex.nix",
	"modules/llm/opencode.nix",
	"modules/llm/gemini.nix",
}

// TestLlmActivation_SudoReachableOnNixOS asserts the activation PATH
// includes /run/wrappers/bin, where NixOS (and NixOS-WSL) put the sudo
// setuid wrapper.
func TestLlmActivation_SudoReachableOnNixOS(t *testing.T) {
	for _, mod := range managedActivationModules {
		content := readNixhomeFile(t, mod)
		if !strings.Contains(content, "home.activation.setupManaged") {
			continue
		}
		if !strings.Contains(content, "/run/wrappers/bin") {
			t.Errorf("%s: activation PATH lacks /run/wrappers/bin — on NixOS-WSL sudo is only there, so `sudo cp ... /etc/...` dies with exit 127 and aborts home-manager activation", mod)
		}
	}
}

// TestLlmActivation_ToleratesMissingSudo asserts the /etc staging is
// guarded: with no sudo anywhere, activation must skip the staging rather
// than abort the generation.
func TestLlmActivation_ToleratesMissingSudo(t *testing.T) {
	for _, mod := range managedActivationModules {
		content := readNixhomeFile(t, mod)
		if !strings.Contains(content, "home.activation.setupManaged") {
			continue
		}
		if !strings.Contains(content, "command -v sudo") {
			t.Errorf("%s: /etc staging is unguarded — a host without sudo fails the whole home-manager switch instead of skipping the optional /etc/<agent> staging", mod)
		}
	}
}
