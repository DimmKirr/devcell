package tart

import (
	"strings"
	"testing"
	"time"
)

func TestAcquireInputs_Defaults(t *testing.T) {
	a := AcquireInputs{}
	a.ApplyDefaults()

	if a.SSHHost != "localhost" {
		t.Errorf("SSHHost = %q, want localhost", a.SSHHost)
	}
	if a.SSHPort != 22 {
		t.Errorf("SSHPort = %d, want 22", a.SSHPort)
	}
	if a.SSHTimeout != 120*time.Second {
		t.Errorf("SSHTimeout = %v, want 120s", a.SSHTimeout)
	}
}

func TestAcquireInputs_Validate_External(t *testing.T) {
	a := AcquireInputs{ExternalVM: true}
	if err := a.Validate(); err != nil {
		t.Errorf("external VM should not require VMName: %v", err)
	}
}

func TestAcquireInputs_Validate_MissingVMName(t *testing.T) {
	a := AcquireInputs{}
	err := a.Validate()
	if err == nil {
		t.Fatal("expected error for missing VMName")
	}
	if !strings.Contains(err.Error(), "VMName") {
		t.Errorf("error = %q, want mention of VMName", err)
	}
}

func TestAcquireInputs_Validate_OK(t *testing.T) {
	a := AcquireInputs{VMName: "test-vm"}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecideAcquire_Table(t *testing.T) {
	tests := []struct {
		name           string
		external       bool
		alreadyRunning bool
		want           AcquireDecision
	}{
		{"external VM", true, false, DecisionExternal},
		{"external VM + running", true, true, DecisionExternal},
		{"managed VM already running", false, true, DecisionAlreadyRunning},
		{"managed VM needs start", false, false, DecisionStartVM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideAcquire(tt.external, tt.alreadyRunning)
			if got != tt.want {
				t.Errorf("DecideAcquire(%v, %v) = %v, want %v",
					tt.external, tt.alreadyRunning, got, tt.want)
			}
		})
	}
}

func TestAcquireDecision_String(t *testing.T) {
	tests := []struct {
		d    AcquireDecision
		want string
	}{
		{DecisionExternal, "external"},
		{DecisionAlreadyRunning, "already-running"},
		{DecisionStartVM, "start-vm"},
		{AcquireDecision(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", int(tt.d), got, tt.want)
		}
	}
}

func TestAcquireInputs_Validate_OK_WithTemplate(t *testing.T) {
	a := AcquireInputs{VMName: "test-vm", TemplateName: "devcell-tart-ultimate"}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecideAcquire_WithTemplate(t *testing.T) {
	tests := []struct {
		name           string
		external       bool
		alreadyRunning bool
		hasTemplate    bool
		want           AcquireDecision
	}{
		{"template available, no instance", false, false, true, DecisionCloneTemplate},
		{"instance running, template irrelevant", false, true, true, DecisionAlreadyRunning},
		{"no template, no instance", false, false, false, DecisionStartVM},
		{"external, template irrelevant", true, false, true, DecisionExternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideAcquireEx(tt.external, tt.alreadyRunning, tt.hasTemplate)
			if got != tt.want {
				t.Errorf("DecideAcquireEx(%v, %v, %v) = %v, want %v",
					tt.external, tt.alreadyRunning, tt.hasTemplate, got, tt.want)
			}
		})
	}
}

func TestAcquireDecision_CloneTemplate_String(t *testing.T) {
	if got := DecisionCloneTemplate.String(); got != "clone-template" {
		t.Errorf("DecisionCloneTemplate.String() = %q, want \"clone-template\"", got)
	}
}

func TestAcquireResult_ManagedVsExternal(t *testing.T) {
	managed := AcquireResult{Managed: true, SSHHost: "localhost", SSHPort: 22}
	if !managed.Managed {
		t.Error("expected Managed=true")
	}

	external := AcquireResult{Managed: false, SSHHost: "10.0.0.5", SSHPort: 2222}
	if external.Managed {
		t.Error("expected Managed=false")
	}
	if external.SSHHost != "10.0.0.5" {
		t.Errorf("SSHHost = %q, want 10.0.0.5", external.SSHHost)
	}
}
