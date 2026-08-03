package cfg

import (
	"strings"
	"testing"
)

// The home-manager module's option set is generated from CellConfig so it
// can never drift from the Go TOML schema. `task hm:generate` (a dep of
// cell:build) writes nix/home-manager/options.nix from HMOptionsNix.

func TestHMOptionsNix_HeaderMarksGenerated(t *testing.T) {
	out := HMOptionsNix()
	for _, want := range []string{"Code generated", "DO NOT EDIT", "task hm:generate"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q", want)
		}
	}
}

func TestHMOptionsNix_EmitsAllTopLevelSections(t *testing.T) {
	out := HMOptionsNix()
	for _, section := range []string{
		"cell = {", "build = {", "nix = {", "llm = {", "git = {",
		"ports = {", "op = {", "aws = {", "stealth = {", "gui = {",
		"packages = {",
	} {
		if !strings.Contains(out, section) {
			t.Errorf("missing section %q", section)
		}
	}
	// Map- and list-typed top levels are leaves, not sections.
	for _, leaf := range []string{
		"env = opt (types.attrsOf types.str);",
		"mise = opt (types.attrsOf types.str);",
	} {
		if !strings.Contains(out, leaf) {
			t.Errorf("missing leaf %q", leaf)
		}
	}
}

func TestHMOptionsNix_MapsGoTypesToNixTypes(t *testing.T) {
	out := HMOptionsNix()
	cases := map[string]string{
		"string":            "image_tag = opt types.str;",
		"*bool":             "thin = opt types.bool;",
		"bool":              "docker_privileged = opt types.bool;",
		"int":               "qemu_cpus = opt types.int;",
		"[]string":          "modules = opt (types.listOf types.str);",
		"map[string]string": "libvirt_path_map = opt (types.attrsOf types.str);",
	}
	for goType, want := range cases {
		if !strings.Contains(out, want) {
			t.Errorf("%s mapping: missing %q", goType, want)
		}
	}
}

func TestHMOptionsNix_NestedStructsBecomeSubmodules(t *testing.T) {
	out := HMOptionsNix()
	// map[string]LLMProvider → attrsOf submodule
	if !strings.Contains(out, "providers = opt (types.attrsOf (types.submodule") {
		t.Error("llm.models.providers should be attrsOf submodule")
	}
	if !strings.Contains(out, "base_url = opt types.str;") {
		t.Error("LLMProvider.base_url leaf missing")
	}
	// []VolumeMount → listOf submodule
	if !strings.Contains(out, "volumes = opt (types.listOf (types.submodule") {
		t.Error("volumes should be listOf submodule")
	}
	if !strings.Contains(out, "mount = opt types.str;") {
		t.Error("VolumeMount.mount leaf missing")
	}
	// LLMSection.Models is a plain nested section, not a submodule
	if !strings.Contains(out, "models = {") {
		t.Error("llm.models should be a plain nested section")
	}
}

func TestHMOptionsNix_Deterministic(t *testing.T) {
	first := HMOptionsNix()
	second := HMOptionsNix()
	if first != second {
		t.Error("output must be deterministic")
	}
}

func TestHMOptionsNix_BalancedBracesAndParens(t *testing.T) {
	out := HMOptionsNix()
	if n := strings.Count(out, "{") - strings.Count(out, "}"); n != 0 {
		t.Errorf("unbalanced braces: %+d", n)
	}
	if n := strings.Count(out, "(") - strings.Count(out, ")"); n != 0 {
		t.Errorf("unbalanced parens: %+d", n)
	}
}
