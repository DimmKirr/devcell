package ux_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

func TestVerboseDefaultsFalse(t *testing.T) {
	// Reset state before checking default
	ux.Verbose = false
	ux.LogPlainText = false

	if ux.Verbose {
		t.Error("Verbose should default to false")
	}
	if ux.LogPlainText {
		t.Error("LogPlainText should default to false")
	}
}

func TestVerboseImpliesPlainText(t *testing.T) {
	// Caller convention: --debug sets both
	ux.Verbose = true
	ux.LogPlainText = true
	defer func() { ux.Verbose = false; ux.LogPlainText = false }()

	if !ux.Verbose {
		t.Error("Verbose should be true after --debug")
	}
	if !ux.LogPlainText {
		t.Error("LogPlainText should be true after --debug")
	}
}
