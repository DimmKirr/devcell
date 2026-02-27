package main

// White-box tests for stripCellFlags — package main for access to unexported symbols.

import (
	"reflect"
	"testing"
)

func TestStripCellFlags_BoolFlagStripped(t *testing.T) {
	got := stripCellFlags([]string{"--build", "claude", "--resume"})
	want := []string{"claude", "--resume"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestStripCellFlags_MacosBoolFlagStripped(t *testing.T) {
	got := stripCellFlags([]string{"--macos", "claude", "--resume"})
	want := []string{"claude", "--resume"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestStripCellFlags_StringFlagSpaceFormStripped(t *testing.T) {
	got := stripCellFlags([]string{"--engine", "docker", "claude"})
	want := []string{"claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestStripCellFlags_StringFlagEqualsFormStripped(t *testing.T) {
	got := stripCellFlags([]string{"--engine=vagrant", "claude"})
	want := []string{"claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestStripCellFlags_VagrantProviderSpaceForm(t *testing.T) {
	got := stripCellFlags([]string{"--vagrant-provider", "utm", "opencode"})
	want := []string{"opencode"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestStripCellFlags_VagrantBoxEqualsForm(t *testing.T) {
	got := stripCellFlags([]string{"--vagrant-box=mybox", "claude"})
	want := []string{"claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestStripCellFlags_MultipleMixed(t *testing.T) {
	got := stripCellFlags([]string{
		"--engine", "vagrant",
		"--macos",
		"--plain-text",
		"--vagrant-provider=utm",
		"--resume",
		"abc",
	})
	want := []string{"--resume", "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestStripCellFlags_UnknownFlagsPassThrough(t *testing.T) {
	got := stripCellFlags([]string{"--conversation", "xyz", "--model", "gpt4"})
	want := []string{"--conversation", "xyz", "--model", "gpt4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unknown flags should pass through unchanged: want %v, got %v", want, got)
	}
}

func TestStripCellFlags_EmptyInput(t *testing.T) {
	got := stripCellFlags([]string{})
	if len(got) != 0 {
		t.Errorf("empty input should return empty: got %v", got)
	}
}

func TestScanStringFlag_SpaceForm(t *testing.T) {
	old := osArgs
	osArgs = []string{"cell", "--engine", "vagrant", "claude"}
	defer func() { osArgs = old }()

	got := scanStringFlag("--engine")
	if got != "vagrant" {
		t.Errorf("want vagrant, got %q", got)
	}
}

func TestScanStringFlag_EqualsForm(t *testing.T) {
	old := osArgs
	osArgs = []string{"cell", "--engine=docker", "claude"}
	defer func() { osArgs = old }()

	got := scanStringFlag("--engine")
	if got != "docker" {
		t.Errorf("want docker, got %q", got)
	}
}

func TestScanStringFlag_Missing(t *testing.T) {
	old := osArgs
	osArgs = []string{"cell", "claude"}
	defer func() { osArgs = old }()

	got := scanStringFlag("--engine")
	if got != "" {
		t.Errorf("want empty string, got %q", got)
	}
}
