package tart

import (
	"fmt"
	"time"
)

// AcquireResult holds the outcome of AcquireDarwinVM.
type AcquireResult struct {
	VM      *VM    // non-nil only if we started a managed VM (caller must shut it down)
	SSHHost string // resolved SSH host (guest IP or external)
	SSHPort uint16 // resolved SSH port
	Managed bool   // true if we started the VM (vs. external/already-running)
}

// AcquireInputs are the parameters for AcquireDarwinVM.
type AcquireInputs struct {
	VMName       string            // tart VM name (instance, e.g. "DIMM-tart")
	TemplateName string            // template VM to clone from if instance doesn't exist
	SharedDirs   map[string]string // tag -> host path (VirtioFS via --dir)
	Disks        []string          // raw disk image paths (VirtIO block devices via --disk)
	SSHHost      string
	SSHPort      uint16
	ExternalVM   bool // user explicitly configured SSH target — skip lifecycle
	SSHTimeout   time.Duration
	InitFunc     func() error // called to auto-init when VM doesn't exist; nil = error instead
}

// ApplyDefaults fills zero values.
func (a *AcquireInputs) ApplyDefaults() {
	if a.SSHHost == "" {
		a.SSHHost = "localhost"
	}
	if a.SSHPort == 0 {
		a.SSHPort = 22
	}
	if a.SSHTimeout == 0 {
		a.SSHTimeout = 120 * time.Second
	}
}

// Validate checks required fields.
func (a *AcquireInputs) Validate() error {
	if a.ExternalVM {
		return nil
	}
	if a.VMName == "" {
		return fmt.Errorf("VMName is required for managed VM")
	}
	return nil
}

// AcquireDecision describes what AcquireDarwinVM will do.
type AcquireDecision int

const (
	DecisionExternal       AcquireDecision = iota // external VM — just wait for SSH
	DecisionAlreadyRunning                        // managed VM already accepting connections
	DecisionStartVM                               // need to start managed VM
	DecisionCloneTemplate                         // clone from template, then start
)

// DecideAcquire is the pure-function decision: what should AcquireDarwinVM do?
func DecideAcquire(external bool, alreadyRunning bool) AcquireDecision {
	return DecideAcquireEx(external, alreadyRunning, false)
}

// DecideAcquireEx extends DecideAcquire with template awareness.
func DecideAcquireEx(external, alreadyRunning, hasTemplate bool) AcquireDecision {
	if external {
		return DecisionExternal
	}
	if alreadyRunning {
		return DecisionAlreadyRunning
	}
	if hasTemplate {
		return DecisionCloneTemplate
	}
	return DecisionStartVM
}

func (d AcquireDecision) String() string {
	switch d {
	case DecisionExternal:
		return "external"
	case DecisionAlreadyRunning:
		return "already-running"
	case DecisionStartVM:
		return "start-vm"
	case DecisionCloneTemplate:
		return "clone-template"
	default:
		return fmt.Sprintf("unknown(%d)", int(d))
	}
}
