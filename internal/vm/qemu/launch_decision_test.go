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
	actions := DecideLaunchActions(LaunchInputs{ExplicitBuild: true, DiskExists: true})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DiskExists(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{DiskExists: true})
	assert.Equal(t, []LaunchAction{ActionUseLocal}, actions)
}

func TestDecideLaunchActions_ColdStart(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DryRunOverridesAll(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{DryRun: true, ExplicitBuild: true, DiskExists: true})
	assert.Equal(t, []LaunchAction{ActionDryRun}, actions)
}

func TestDecideLaunchActions_ForceOverridesDiskExists(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{ExplicitBuild: true, DiskExists: true})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_VMRunning(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{VMRunning: true, DiskExists: true})
	assert.Equal(t, []LaunchAction{ActionAttach}, actions)
}

func TestDecideLaunchActions_VMRunningButForce(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{VMRunning: true, ExplicitBuild: true, DiskExists: true})
	assert.Equal(t, []LaunchAction{ActionBuild}, actions)
}

func TestDecideLaunchActions_DryRunOverridesVMRunning(t *testing.T) {
	actions := DecideLaunchActions(LaunchInputs{DryRun: true, VMRunning: true, DiskExists: true})
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
