package qemu

// LaunchAction is one VM-acquisition step.
type LaunchAction int

const (
	ActionUseLocal LaunchAction = iota // instance disk exists locally
	ActionBuild                        // build from Windows ISO (full install + provision)
	ActionDryRun                       // dry-run mode, no VM work
	ActionAttach                       // attach to already-running VM
	ActionClone                        // clone existing template to instance
)

// LaunchInputs are the inputs to DecideLaunchActions.
type LaunchInputs struct {
	DryRun         bool // --dry-run set
	ExplicitBuild  bool // --force set, force rebuild
	DiskExists     bool // instance disk image exists at expected path
	TemplateExists bool // template disk image exists (ready to clone)
	VMRunning      bool // existing QEMU process detected via PID file + QMP
}

// DecideLaunchActions returns the ordered fallback sequence.
//
//	DryRun          → [DryRun]
//	ExplicitBuild   → [Build]
//	VMRunning       → [Attach]
//	DiskExists      → [UseLocal]
//	TemplateExists  → [Clone]
//	cold start      → [Build]
func DecideLaunchActions(in LaunchInputs) []LaunchAction {
	switch {
	case in.DryRun:
		return []LaunchAction{ActionDryRun}
	case in.ExplicitBuild:
		return []LaunchAction{ActionBuild}
	case in.VMRunning:
		return []LaunchAction{ActionAttach}
	case in.DiskExists:
		return []LaunchAction{ActionUseLocal}
	case in.TemplateExists:
		return []LaunchAction{ActionClone}
	default:
		return []LaunchAction{ActionBuild}
	}
}
