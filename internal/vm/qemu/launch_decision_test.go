package qemu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecideLaunchActions_DryRun(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{DryRun: true})
	assert.Equal(t, []LaunchAction{ActionDryRun}, actions)
}

func TestDecideLaunchActions_ExplicitBuild(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{ExplicitBuild: true, DiskExists: true, Provisioned: true})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DiskExists(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{DiskExists: true, Provisioned: true})
	assert.Equal(t, []LaunchAction{ActionUseLocal}, actions)
}

func TestDecideLaunchActions_ColdStart(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DryRunOverridesAll(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{DryRun: true, ExplicitBuild: true, DiskExists: true, Provisioned: true})
	assert.Equal(t, []LaunchAction{ActionDryRun}, actions)
}

func TestDecideLaunchActions_ForceOverridesDiskExists(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{ExplicitBuild: true, DiskExists: true, Provisioned: true})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_VMRunning(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{VMRunning: true, DiskExists: true, Provisioned: true})
	assert.Equal(t, []LaunchAction{ActionAttach}, actions)
}

func TestDecideLaunchActions_VMRunningButForce(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{VMRunning: true, ExplicitBuild: true, DiskExists: true, Provisioned: true})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DryRunOverridesVMRunning(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{DryRun: true, VMRunning: true, DiskExists: true, Provisioned: true})
	assert.Equal(t, []LaunchAction{ActionDryRun}, actions)
}

func TestDecideLaunchActions_TemplateExistsNoInstance(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{TemplateExists: true})
	assert.Equal(t, []LaunchAction{ActionClone}, actions)
}

func TestDecideLaunchActions_ColdStartNoTemplate(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_ForceOverridesTemplateExists(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{ExplicitBuild: true, TemplateExists: true})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_TemplateExistsTooSmall(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{
		TemplateExists:    true,
		TemplateSizeBytes: 193 * 1024,
	})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_TemplateExistsLargeEnough(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{
		TemplateExists:    true,
		TemplateSizeBytes: 5 * 1024 * 1024 * 1024,
	})
	assert.Equal(t, []LaunchAction{ActionClone}, actions)
}

func TestDecideLaunchActions_TemplateSizeZeroMeansUnchecked(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{TemplateExists: true, TemplateSizeBytes: 0})
	assert.Equal(t, []LaunchAction{ActionClone}, actions)
}

func TestDecideLaunchActions_DiskExistsButNotProvisioned(t *testing.T) {
	// Instance disk exists but no .provisioned marker — rebuild from scratch.
	actions := DecideLaunchActions(LaunchInputs{DiskExists: true, Provisioned: false})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DiskExistsButTooSmall(t *testing.T) {
	// Instance disk is a corrupt stub from a failed clone — rebuild.
	actions := DecideLaunchActions(LaunchInputs{
		DiskExists:    true,
		DiskSizeBytes: 193 * 1024,
		Provisioned:   true,
	})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DiskExistsUnprovisionedAndTooSmall(t *testing.T) {
	// Both invalid size and no marker — rebuild.
	actions := DecideLaunchActions(LaunchInputs{
		DiskExists:    true,
		DiskSizeBytes: 100,
		Provisioned:   false,
	})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}
